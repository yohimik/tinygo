//go:build (darwin || (linux && !baremetal && !wasip1 && !wasm_unknown && !wasip2 && !nintendoswitch)) && scheduler.threads

package runtime

import (
	"internal/futex"
)

// With threads there is no scheduler loop, so nothing ever calls waitForEvents
// and the task.Pause the cooperative scheduler uses would never be resumed.
// The goroutine inside os/signal is a real thread instead, so it can block in
// the kernel until the signal handler wakes it.
//
// It cannot wait on signalFutex: sleepTicks waits on that same futex and swaps
// it back to zero, which would swallow the wakeup meant for the receiver. So
// the receiver gets a futex of its own, with the same 0/1 protocol: the value
// is 1 when the handler has stored signals the receiver has not looked at yet.
var signalRecvFutex futex.Futex

// Futex signalWaitUntilIdle waits on. Its value is always zero; it exists only
// as a wakeup address, so that draining a signal can wake a waiter without it
// having to poll. The wait has a timeout as well, because a wake sent just
// before the wait starts is not remembered.
var signalIdleFutex futex.Futex

// How long signalWaitUntilIdle blocks before rechecking on its own.
const signalIdlePoll = 1e6 // 1ms, in nanoseconds

//go:linkname signal_recv os/signal.signal_recv
func signal_recv() uint32 {
	// Function called from os/signal to get the next received signal.
	for {
		if num, ok := nextReceivedSignal(); ok {
			if receivedSignals.Load() == 0 {
				// That was the last pending signal, so signalWaitUntilIdle can
				// return now.
				signalIdleFutex.WakeAll()
			}
			return num
		}

		// There are no signals to receive, so block until the handler reports
		// one. Clear the flag first and then check receivedSignals again: the
		// handler stores the signal before it sets the flag, so either that
		// recheck sees the signal, or the handler sets the flag to 1 and the
		// wait below returns immediately because the value is no longer 0.
		// Either way the wakeup cannot be lost.
		signalRecvFutex.Store(0)
		if receivedSignals.Load() != 0 {
			continue
		}
		signalRecvFutex.Wait(0)
	}
}

//go:linkname signal_waitUntilIdle os/signal.signalWaitUntilIdle
func signal_waitUntilIdle() {
	// Wait until signal_recv has processed all signals. Gosched is a no-op
	// with threads, so this has to actually block: signal_recv wakes this
	// futex once it drains the last pending signal.
	for receivedSignals.Load() != 0 {
		signalIdleFutex.WaitUntil(0, signalIdlePoll)
	}
}

// Called from the signal handler to wake signal_recv. Only an atomic store and
// a futex wake syscall, both of which are safe to call from a signal handler
// on an arbitrary thread.
func signalRecvWake() {
	if signalRecvFutex.Swap(1) == 0 {
		// Changed from 0 to 1, so signal_recv may be waiting on it.
		signalRecvFutex.WakeAll()
	}
}

// Reactivate the goroutine waiting for signals, if there are any.
// There is no such goroutine with threads: the receiver waits on
// signalRecvFutex and is woken by the signal handler directly. sleepTicks
// still calls this after its own wait on signalFutex.
func checkSignals() bool {
	return false
}
