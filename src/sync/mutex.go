package sync

import (
	"internal/task"
)

type Mutex = task.Mutex

//go:linkname runtimeFatal runtime.runtimeFatal
func runtimeFatal(msg string)

// rwSem is a counting semaphore built on a futex. A release makes exactly n
// permits available and each acquire consumes exactly one, so a waiter that is
// woken has genuinely been handed the thing it was waiting for.
//
// That is the whole reason it exists. A futex on its own only supports "sleep
// until this word changes", which obliges a woken waiter to re-test a shared
// predicate — and by then a third party may have changed it back, sending the
// waiter to sleep again with nobody left who will ever wake it. RWMutex hands
// the lock from writer to readers and back, so both directions need a hand-off
// that survives that race. Unlike task.Semaphore this one supports any number
// of simultaneous waiters.
type rwSem struct {
	permits task.Futex
}

// release makes n more permits available and wakes everyone waiting for one.
func (s *rwSem) release(n uint32) {
	s.permits.Add(n)
	s.permits.WakeAll()
}

// acquire consumes one permit, waiting for one to appear if there are none.
func (s *rwSem) acquire() {
	for {
		v := s.permits.Load()
		if v == 0 {
			// A release landing between the load and the wait changes the
			// futex word, and the futex compares the word before sleeping, so
			// that wakeup cannot be lost.
			s.permits.Wait(0)
			continue
		}
		if s.permits.CompareAndSwap(v, v-1) {
			return
		}
	}
}

type RWMutex struct {
	// Reader count, counting every caller of RLock that has not yet returned
	// from RUnlock — readers that hold the lock and readers still queued for
	// it alike.
	// The value can be in two states: one where 0 means no readers and another
	// where -rwMutexMaxReaders means no readers. A base of 0 is normal
	// uncontended operation, a base of -rwMutexMaxReaders means a writer has
	// the lock or is trying to get the lock. In the second case, readers should
	// wait until the writer hands them the lock.
	readers task.Futex

	// The number of readers a waiting writer is still owed: those that already
	// held the lock when the writer arrived. Readers that arrive afterwards
	// queue on readerSem instead and are deliberately not counted here, which
	// is what stops a steady stream of them from starving the writer.
	readerWait task.Futex

	// Hand-offs. Unlock releases one permit per queued reader; the last reader
	// to leave releases one permit to the waiting writer.
	readerSem rwSem
	writerSem rwSem

	// Writer lock. Held between Lock() and Unlock().
	writerLock Mutex
}

const rwMutexMaxReaders = 1 << 30

// Lock locks rw for writing.
// If the lock is already locked for reading or writing,
// Lock blocks until the lock is available.
func (rw *RWMutex) Lock() {
	// Exclusive lock for writers.
	rw.writerLock.Lock()

	// Signal to readers that they can't lock this mutex anymore, and learn how
	// many of them hold it right now. Only these readers are worth waiting
	// for: the ones that arrive from here on will queue on readerSem, and
	// waiting for them too would mean never acquiring the lock at all under a
	// steady stream of readers.
	n := uint32(rwMutexMaxReaders)
	r := int32(rw.readers.Add(-n)) + rwMutexMaxReaders

	// Wait until those readers are unlocked. The last of them to leave hands
	// the lock over through writerSem. If they all left while we were counting,
	// readerWait is already back at zero and there is nothing to wait for.
	if r != 0 && int32(rw.readerWait.Add(uint32(r))) != 0 {
		rw.writerSem.acquire()
	}
}

// Unlock unlocks rw for writing. It is a run-time error if rw is
// not locked for writing on entry to Unlock.
//
// As with Mutexes, a locked [RWMutex] is not associated with a particular
// goroutine. One goroutine may [RWMutex.RLock] ([RWMutex.Lock]) a RWMutex and then
// arrange for another goroutine to [RWMutex.RUnlock] ([RWMutex.Unlock]) it.
func (rw *RWMutex) Unlock() {
	// Signal that new readers can lock this mutex.
	r := int32(rw.readers.Add(rwMutexMaxReaders))
	if r >= rwMutexMaxReaders {
		runtimeFatal("sync: Unlock of unlocked RWMutex")
	}

	// Hand the lock to every reader that queued up behind us — one permit
	// each, so that each of them acquires rather than re-reading a count that
	// the next writer may already have rebased. They are still counted in
	// rw.readers, so that next writer will wait for them in turn.
	if r > 0 {
		rw.readerSem.release(uint32(r))
	}

	// Done with this lock (next writer can try to get a lock).
	rw.writerLock.Unlock()
}

// TryLock tries to lock m and reports whether it succeeded.
//
// Note that while correct uses of TryLock do exist, they are rare,
// and use of TryLock is often a sign of a deeper problem
// in a particular use of mutexes.
func (rw *RWMutex) TryLock() bool {
	// Check for active writers
	if !rw.writerLock.TryLock() {
		return false
	}
	// Have write lock, now check for active readers
	n := uint32(rwMutexMaxReaders)
	if !rw.readers.CompareAndSwap(0, -n) {
		// Active readers, give up write lock
		rw.writerLock.Unlock()
		return false
	}
	return true
}

// RLock locks rw for reading.
//
// It should not be used for recursive read locking; a blocked Lock
// call excludes new readers from acquiring the lock. See the
// documentation on the [RWMutex] type.
func (rw *RWMutex) RLock() {
	// Add us as a reader.
	if int32(rw.readers.Add(1)) < 0 {
		// A writer holds the lock or is waiting for one, so queue up. Unlock
		// hands us a permit when it is our turn; we must not instead re-read
		// the reader count and decide for ourselves, because a writer that
		// arrives in between would rebase it and send us back to sleep after
		// our wakeup had already been spent — with that writer meanwhile
		// waiting for us, since it counted us when it arrived.
		rw.readerSem.acquire()
	}
}

// RUnlock undoes a single [RWMutex.RLock] call;
// it does not affect other simultaneous readers.
// It is a run-time error if rw is not locked for reading
// on entry to RUnlock.
func (rw *RWMutex) RUnlock() {
	// Remove us as a reader.
	one := uint32(1)
	if readers := int32(rw.readers.Add(-one)); readers < 0 {
		rw.rUnlockSlow(readers)
	}
}

// rUnlockSlow handles the RUnlock of a reader that a writer is waiting behind.
func (rw *RWMutex) rUnlockSlow(readers int32) {
	// Check whether RUnlock was called too often.
	if readers+1 == 0 || readers+1 == -rwMutexMaxReaders {
		runtimeFatal("sync: RUnlock of unlocked RWMutex")
	}

	// A writer is waiting for the readers it found when it arrived. If we are
	// the last of them, hand the lock over.
	if int32(rw.readerWait.Add(^uint32(0))) == 0 {
		rw.writerSem.release(1)
	}
}

// TryRLock tries to lock rw for reading and reports whether it succeeded.
//
// Note that while correct uses of TryRLock do exist, they are rare,
// and use of TryRLock is often a sign of a deeper problem
// in a particular use of mutexes.
func (rw *RWMutex) TryRLock() bool {
	for {
		c := rw.readers.Load()
		if c < 0 {
			// There is a writer waiting or writing.
			return false
		}
		if rw.readers.CompareAndSwap(c, c+1) {
			// Read lock obtained.
			return true
		}
	}
}

type Locker interface {
	Lock()
	Unlock()
}

// RLocker returns a Locker interface that implements
// the Lock and Unlock methods by calling rw.RLock and rw.RUnlock.
func (rw *RWMutex) RLocker() Locker {
	return (*rlocker)(rw)
}

type rlocker RWMutex

func (r *rlocker) Lock()   { (*RWMutex)(r).RLock() }
func (r *rlocker) Unlock() { (*RWMutex)(r).RUnlock() }
