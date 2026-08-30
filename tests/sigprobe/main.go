// sigprobe asks the signal layer of a hosted TinyGo binary for one thing at a
// time — deliver a notified signal, deliver a second one, stop delivering,
// restore the default action, ignore a signal, terminate on an unregistered
// one — so a failure names the piece that broke rather than the program that
// reached it.
//
// Every check runs the probe binary again as a child, waits for the child to
// print READY, sends it a signal, and reads what the child made of it. That is
// the same shape a supervisor uses on a TinyGo-built CLI: the parent signals a
// child by pid and waits for it to shut down.
//
// Run it as: sigprobe [name ...], or with no arguments to run every check.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// How long a check waits for a child to say something before giving up.
const readTimeout = 20 * time.Second

// ---------------------------------------------------------------------------
// Child side: one role per check. Each role prints READY once it is listening,
// then prints what it observed.
// ---------------------------------------------------------------------------

var roles = map[string]func(){
	// Plain signal.Notify delivery.
	"notify": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		ready()
		select {
		case s := <-c:
			fmt.Println("GOT", s)
		case <-time.After(8 * time.Second):
			fmt.Println("NO SIGNAL")
		}
	},

	// signal.NotifyContext cancels its context on the signal.
	"notifycontext": func() {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		ready()
		select {
		case <-ctx.Done():
			fmt.Println("CTX DONE")
		case <-time.After(8 * time.Second):
			fmt.Println("NO SIGNAL")
		}
	},

	// Two signals in a row both reach the channel.
	"repeat": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		ready()
		for i := 0; i < 2; i++ {
			select {
			case s := <-c:
				fmt.Println("GOT", s)
			case <-time.After(8 * time.Second):
				fmt.Println("NO SIGNAL")
				return
			}
		}
	},

	// signal.Stop returns, and afterwards the signal is no longer delivered to
	// the channel: the default action (terminate) is back.
	"stop": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT)
		ready()
		select {
		case s := <-c:
			fmt.Println("GOT", s)
		case <-time.After(8 * time.Second):
			fmt.Println("NO SIGNAL")
			return
		}
		signal.Stop(c)
		fmt.Println("STOPPED")
		select {
		case s := <-c:
			fmt.Println("LEAKED", s)
		case <-time.After(8 * time.Second):
			fmt.Println("STILL ALIVE")
		}
	},

	// signal.Reset restores the default action for the signal.
	"reset": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT)
		ready()
		select {
		case s := <-c:
			fmt.Println("GOT", s)
		case <-time.After(8 * time.Second):
			fmt.Println("NO SIGNAL")
			return
		}
		signal.Reset(syscall.SIGINT)
		fmt.Println("RESET")
		select {
		case s := <-c:
			fmt.Println("LEAKED", s)
		case <-time.After(8 * time.Second):
			fmt.Println("STILL ALIVE")
		}
	},

	// signal.Ignore keeps the process alive without a reader.
	"ignore": func() {
		signal.Ignore(syscall.SIGINT)
		fmt.Println("IGNORING")
		ready()
		time.Sleep(3 * time.Second)
		fmt.Println("SURVIVED")
	},

	// A signal that was never notified keeps its default action.
	"unregistered": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT)
		ready()
		select {
		case s := <-c:
			fmt.Println("GOT", s)
		case <-time.After(8 * time.Second):
			fmt.Println("STILL ALIVE")
		}
	},

	// Delivery while every thread is busy taking locks and starting goroutines.
	"busy": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		done := make(chan struct{})
		var mu sync.Mutex
		shared := 0
		// Four threads hammering a mutex...
		for i := 0; i < 4; i++ {
			go func() {
				for {
					select {
					case <-done:
						return
					default:
					}
					mu.Lock()
					shared++
					mu.Unlock()
				}
			}()
		}
		// ...and a steady trickle of new goroutines starting and finishing.
		// Kept to a fixed rate: with the threads scheduler each one is an OS
		// thread, so an unbounded loop here would measure thread creation
		// rather than signal delivery.
		go func() {
			for {
				select {
				case <-done:
					return
				case <-time.After(2 * time.Millisecond):
				}
				var wg sync.WaitGroup
				for j := 0; j < 4; j++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						mu.Lock()
						shared++
						mu.Unlock()
					}()
				}
				wg.Wait()
			}
		}()
		ready()
		select {
		case s := <-c:
			fmt.Println("GOT", s)
		case <-time.After(8 * time.Second):
			fmt.Println("NO SIGNAL")
		}
		close(done)
	},

	// Delivery while the collector is under allocation pressure.
	"gc": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		done := make(chan struct{})
		for i := 0; i < 4; i++ {
			go func() {
				var keep [][]byte
				for {
					select {
					case <-done:
						return
					default:
					}
					for j := 0; j < 64; j++ {
						keep = append(keep, make([]byte, 4096))
					}
					if len(keep) > 512 {
						keep = nil
					}
				}
			}()
		}
		ready()
		select {
		case s := <-c:
			fmt.Println("GOT", s)
		case <-time.After(8 * time.Second):
			fmt.Println("NO SIGNAL")
		}
		close(done)
	},

	// The shape a supervisor uses: shut down cleanly on the signal and exit 0.
	"graceful": func() {
		c := make(chan os.Signal, 4)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		ready()
		select {
		case s := <-c:
			fmt.Println("GOT", s)
			fmt.Println("SHUTTING DOWN")
			time.Sleep(100 * time.Millisecond)
			fmt.Println("CLEAN EXIT")
			os.Exit(0)
		case <-time.After(8 * time.Second):
			fmt.Println("NO SIGNAL")
			os.Exit(1)
		}
	},
}

func ready() {
	fmt.Println("READY")
}

// ---------------------------------------------------------------------------
// Parent side.
// ---------------------------------------------------------------------------

type check struct {
	name string
	run  func() (string, error)
}

// signalled reports the signal a finished child died from, or 0 if it exited
// normally.
func signalled(err error) syscall.Signal {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return 0
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		return 0
	}
	if !ws.Signaled() {
		return 0
	}
	return ws.Signal()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// child runs the probe again in the given role. The returned handle lets the
// caller wait for output lines, send signals, and wait for the exit.
type child struct {
	cmd   *exec.Cmd
	lines chan string
	rest  []string
	mu    sync.Mutex
	all   []string
}

func startChild(role string) (*child, error) {
	cmd := exec.Command(self, "-child", role)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := &child{cmd: cmd, lines: make(chan string, 32)}
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			ch.mu.Lock()
			ch.all = append(ch.all, line)
			ch.mu.Unlock()
			ch.lines <- line
		}
		close(ch.lines)
	}()
	return ch, nil
}

// await waits for a line equal to want, discarding earlier lines.
func (c *child) await(want string) error {
	deadline := time.After(readTimeout)
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				return fmt.Errorf("child exited before printing %q (saw %v)", want, c.output())
			}
			if line == want {
				return nil
			}
		case <-deadline:
			return fmt.Errorf("timed out waiting for %q (saw %v)", want, c.output())
		}
	}
}

// line returns the next line the child prints.
func (c *child) line() (string, error) {
	select {
	case line, ok := <-c.lines:
		if !ok {
			return "", fmt.Errorf("child exited (saw %v)", c.output())
		}
		return line, nil
	case <-time.After(readTimeout):
		return "", fmt.Errorf("timed out reading a line (saw %v)", c.output())
	}
}

func (c *child) signal(sig syscall.Signal) error {
	return c.cmd.Process.Signal(sig)
}

func (c *child) wait() error {
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(readTimeout):
		c.cmd.Process.Kill()
		<-done
		return errors.New("child did not exit in time")
	}
}

func (c *child) output() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.all))
	copy(out, c.all)
	return out
}

func (c *child) kill() {
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	c.cmd.Wait()
}

// deliver starts a role, waits for READY, sends sig, and returns the next line.
func deliver(role string, sig syscall.Signal, want string) (string, error) {
	ch, err := startChild(role)
	if err != nil {
		return "", err
	}
	if err := ch.await("READY"); err != nil {
		ch.kill()
		return "", err
	}
	start := time.Now()
	if err := ch.signal(sig); err != nil {
		ch.kill()
		return "", err
	}
	line, err := ch.line()
	if err != nil {
		ch.kill()
		return "", err
	}
	took := time.Since(start).Round(time.Millisecond)
	detail := fmt.Sprintf("%q after %v", line, took)
	if line != want {
		ch.kill()
		return detail, fmt.Errorf("want %q", want)
	}
	if err := ch.wait(); err != nil {
		return detail, fmt.Errorf("child exit: %v", err)
	}
	return detail, nil
}

var checks = []check{
	{"notify", func() (string, error) {
		return deliver("notify", syscall.SIGINT, "GOT interrupt")
	}},

	{"notify-term", func() (string, error) {
		return deliver("notify", syscall.SIGTERM, "GOT terminated")
	}},

	{"notifycontext", func() (string, error) {
		return deliver("notifycontext", syscall.SIGINT, "CTX DONE")
	}},

	{"repeat", func() (string, error) {
		ch, err := startChild("repeat")
		if err != nil {
			return "", err
		}
		defer ch.kill()
		if err := ch.await("READY"); err != nil {
			return "", err
		}
		if err := ch.signal(syscall.SIGINT); err != nil {
			return "", err
		}
		first, err := ch.line()
		if err != nil {
			return "", err
		}
		if err := ch.signal(syscall.SIGTERM); err != nil {
			return first, err
		}
		second, err := ch.line()
		if err != nil {
			return first, err
		}
		detail := fmt.Sprintf("%q then %q", first, second)
		if first != "GOT interrupt" || second != "GOT terminated" {
			return detail, errors.New(`want "GOT interrupt" then "GOT terminated"`)
		}
		return detail, nil
	}},

	{"stop", func() (string, error) {
		ch, err := startChild("stop")
		if err != nil {
			return "", err
		}
		if err := ch.await("READY"); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.signal(syscall.SIGINT); err != nil {
			ch.kill()
			return "", err
		}
		// The signal reaches the channel, then Stop() must return.
		if err := ch.await("GOT interrupt"); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.await("STOPPED"); err != nil {
			ch.kill()
			return "", errors.New("signal.Stop did not return: " + err.Error())
		}
		// After Stop the default action is back, so a second SIGINT kills it.
		if err := ch.signal(syscall.SIGINT); err != nil {
			ch.kill()
			return "", err
		}
		werr := ch.wait()
		detail := fmt.Sprintf("%v, exit %v", ch.output(), werr)
		for _, l := range ch.output() {
			if strings.HasPrefix(l, "LEAKED") {
				return detail, errors.New("signal was still delivered after Stop")
			}
		}
		if signalled(werr) != syscall.SIGINT {
			return detail, errors.New("child was not terminated by the second SIGINT")
		}
		return detail, nil
	}},

	{"reset", func() (string, error) {
		ch, err := startChild("reset")
		if err != nil {
			return "", err
		}
		if err := ch.await("READY"); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.signal(syscall.SIGINT); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.await("GOT interrupt"); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.await("RESET"); err != nil {
			ch.kill()
			return "", errors.New("signal.Reset did not return: " + err.Error())
		}
		if err := ch.signal(syscall.SIGINT); err != nil {
			ch.kill()
			return "", err
		}
		werr := ch.wait()
		detail := fmt.Sprintf("%v, exit %v", ch.output(), werr)
		if signalled(werr) != syscall.SIGINT {
			return detail, errors.New("default action was not restored by Reset")
		}
		return detail, nil
	}},

	{"ignore", func() (string, error) {
		ch, err := startChild("ignore")
		if err != nil {
			return "", err
		}
		if err := ch.await("READY"); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.signal(syscall.SIGINT); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.await("SURVIVED"); err != nil {
			ch.kill()
			return "", err
		}
		werr := ch.wait()
		detail := fmt.Sprintf("%v, exit %v", ch.output(), exitCode(werr))
		if werr != nil {
			return detail, errors.New("child did not exit cleanly")
		}
		return detail, nil
	}},

	{"unregistered", func() (string, error) {
		ch, err := startChild("unregistered")
		if err != nil {
			return "", err
		}
		if err := ch.await("READY"); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.signal(syscall.SIGHUP); err != nil {
			ch.kill()
			return "", err
		}
		werr := ch.wait()
		detail := fmt.Sprintf("%v, signal %v, exit %v", ch.output(), signalled(werr), exitCode(werr))
		if signalled(werr) != syscall.SIGHUP {
			return detail, errors.New("a signal that was never notified should keep its default action")
		}
		return detail, nil
	}},

	{"busy", func() (string, error) {
		return deliver("busy", syscall.SIGINT, "GOT interrupt")
	}},

	{"gc", func() (string, error) {
		return deliver("gc", syscall.SIGTERM, "GOT terminated")
	}},

	{"graceful", func() (string, error) {
		ch, err := startChild("graceful")
		if err != nil {
			return "", err
		}
		if err := ch.await("READY"); err != nil {
			ch.kill()
			return "", err
		}
		start := time.Now()
		if err := ch.signal(syscall.SIGINT); err != nil {
			ch.kill()
			return "", err
		}
		if err := ch.await("CLEAN EXIT"); err != nil {
			ch.kill()
			return "", err
		}
		werr := ch.wait()
		took := time.Since(start).Round(time.Millisecond)
		detail := fmt.Sprintf("%v after %v, exit %v", ch.output(), took, exitCode(werr))
		if werr != nil {
			return detail, errors.New("child did not exit 0")
		}
		return detail, nil
	}},
}

var self string

func main() {
	if len(os.Args) > 2 && os.Args[1] == "-child" {
		role, ok := roles[os.Args[2]]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown role %q\n", os.Args[2])
			os.Exit(2)
		}
		role()
		return
	}

	self = os.Args[0]
	if !strings.Contains(self, "/") {
		if p, err := exec.LookPath(self); err == nil {
			self = p
		}
	}

	want := os.Args[1:]
	selected := func(name string) bool {
		if len(want) == 0 {
			return true
		}
		for _, w := range want {
			if w == name {
				return true
			}
		}
		return false
	}

	failures := 0
	ran := 0
	for _, c := range checks {
		if !selected(c.name) {
			continue
		}
		ran++
		detail, err := c.run()
		if err != nil {
			failures++
			fmt.Printf("FAIL  %-14s %s: %v\n", c.name, detail, err)
			continue
		}
		fmt.Printf("ok    %-14s %s\n", c.name, detail)
	}

	if ran == 0 {
		fmt.Fprintln(os.Stderr, "usage: sigprobe [name ...]")
		os.Exit(2)
	}
	if failures != 0 {
		fmt.Printf("\n%d of %d checks failed\n", failures, ran)
		os.Exit(1)
	}
	fmt.Printf("\nall %d checks passed\n", ran)
}
