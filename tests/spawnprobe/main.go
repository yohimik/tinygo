// spawnprobe asks the process layer of a hosted TinyGo binary for one thing at
// a time — start a program, collect its output, read its exit status, feed it a
// stdin, give it a working directory, kill it — so a failure names the piece
// that broke rather than the command that reached it.
//
// Run it as: spawnprobe [name ...], or with no arguments to run every check.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type check struct {
	name string
	run  func() (string, error)
}

var checks = []check{
	{"output", func() (string, error) {
		out, err := exec.Command("/bin/echo", "hello", "world").CombinedOutput()
		if err != nil {
			return fmt.Sprintf("%q", out), err
		}
		if string(out) != "hello world\n" {
			return fmt.Sprintf("%q", out), errors.New("unexpected output")
		}
		return fmt.Sprintf("%q", out), nil
	}},

	{"stderr", func() (string, error) {
		out, err := exec.Command("/bin/sh", "-c", "echo out; echo err 1>&2").CombinedOutput()
		if err != nil {
			return fmt.Sprintf("%q", out), err
		}
		if !strings.Contains(string(out), "out") || !strings.Contains(string(out), "err") {
			return fmt.Sprintf("%q", out), errors.New("stderr was not combined into the output")
		}
		return fmt.Sprintf("%q", out), nil
	}},

	{"exitstatus", func() (string, error) {
		err := exec.Command("/bin/sh", "-c", "exit 7").Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return fmt.Sprintf("%v", err), errors.New("wanted an *exec.ExitError")
		}
		ws, ok := ee.Sys().(syscall.WaitStatus)
		detail := fmt.Sprintf("code=%d exited=%v success=%v string=%q sys=%T",
			ee.ExitCode(), ee.Exited(), ee.Success(), ee.String(), ee.Sys())
		if !ok || ws.ExitStatus() != 7 {
			return detail, errors.New("Sys() is not a syscall.WaitStatus reporting 7")
		}
		if ee.ExitCode() != 7 || !ee.Exited() || ee.Success() || ee.String() != "exit status 7" {
			return detail, errors.New("wrong ProcessState")
		}
		return detail, nil
	}},

	{"env", func() (string, error) {
		cmd := exec.Command("/bin/sh", "-c", "echo $SPAWNPROBE")
		cmd.Env = []string{"SPAWNPROBE=explicit"}
		out, err := cmd.Output()
		if err != nil {
			return fmt.Sprintf("%q", out), err
		}
		if string(out) != "explicit\n" {
			return fmt.Sprintf("%q", out), errors.New("cmd.Env did not reach the child")
		}
		return fmt.Sprintf("%q", out), nil
	}},

	{"inheritedenv", func() (string, error) {
		os.Setenv("SPAWNPROBE_INHERIT", "inherited")
		out, err := exec.Command("/bin/sh", "-c", "echo $SPAWNPROBE_INHERIT").Output()
		if err != nil {
			return fmt.Sprintf("%q", out), err
		}
		if string(out) != "inherited\n" {
			return fmt.Sprintf("%q", out), errors.New("a nil cmd.Env did not inherit the environment")
		}
		return fmt.Sprintf("%q", out), nil
	}},

	{"dir", func() (string, error) {
		cmd := exec.Command("/bin/sh", "-c", "pwd -P")
		cmd.Dir = "/"
		out, err := cmd.Output()
		if err != nil {
			return fmt.Sprintf("%q", out), err
		}
		if strings.TrimSpace(string(out)) != "/" {
			return fmt.Sprintf("%q", out), errors.New("cmd.Dir did not become the child's working directory")
		}
		return fmt.Sprintf("%q", out), nil
	}},

	{"stdin", func() (string, error) {
		cmd := exec.Command("/bin/sh", "-c", "cat")
		cmd.Stdin = strings.NewReader("payload\n")
		out, err := cmd.Output()
		if err != nil {
			return fmt.Sprintf("%q", out), err
		}
		if string(out) != "payload\n" {
			return fmt.Sprintf("%q", out), errors.New("stdin did not reach the child")
		}
		return fmt.Sprintf("%q", out), nil
	}},

	{"extrafiles", func() (string, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return "", err
		}
		defer r.Close()
		cmd := exec.Command("/bin/sh", "-c", "echo extra 1>&3")
		cmd.ExtraFiles = []*os.File{w}
		err = cmd.Run()
		w.Close()
		if err != nil {
			return "", err
		}
		buf := make([]byte, 32)
		n, err := r.Read(buf)
		if err != nil {
			return "", err
		}
		if string(buf[:n]) != "extra\n" {
			return fmt.Sprintf("%q", buf[:n]), errors.New("ExtraFiles did not become descriptor 3")
		}
		return fmt.Sprintf("%q", buf[:n]), nil
	}},

	{"noleak", func() (string, error) {
		// A pipe held by the parent must not be inherited by an unrelated
		// child, or it never reports end of file.
		r, w, err := os.Pipe()
		if err != nil {
			return "", err
		}
		defer r.Close()
		if err := exec.Command("/bin/sh", "-c", "sleep 3").Start(); err != nil {
			return "", err
		}
		w.Close()
		done := make(chan error, 1)
		go func() {
			b := make([]byte, 1)
			_, err := r.Read(b)
			done <- err
		}()
		select {
		case err := <-done:
			return fmt.Sprintf("read returned %v", err), nil
		case <-time.After(2 * time.Second):
			return "read blocked", errors.New("the write end leaked into the child")
		}
	}},

	{"kill", func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := exec.CommandContext(ctx, "/bin/sleep", "30").Run()
		elapsed := time.Since(start)
		detail := fmt.Sprintf("elapsed=%v err=%v", elapsed.Round(time.Millisecond), err)
		if err == nil {
			return detail, errors.New("sleep was not killed")
		}
		if elapsed > 5*time.Second {
			return detail, errors.New("the context did not kill the child promptly")
		}
		return detail, nil
	}},

	{"notexist", func() (string, error) {
		err := exec.Command("/nonexistent/binary").Run()
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Sprintf("%v", err), errors.New("wanted os.ErrNotExist")
		}
		return fmt.Sprintf("%v", err), nil
	}},

	{"concurrent", func() (string, error) {
		const n = 16
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			go func(i int) {
				out, err := exec.Command("/bin/echo", fmt.Sprint(i)).Output()
				if err == nil && strings.TrimSpace(string(out)) != fmt.Sprint(i) {
					err = fmt.Errorf("got %q, wanted %d", out, i)
				}
				errs <- err
			}(i)
		}
		var first error
		for i := 0; i < n; i++ {
			if err := <-errs; err != nil && first == nil {
				first = err
			}
		}
		return fmt.Sprintf("%d spawns", n), first
	}},

	// A script runner puts each child in its own process group so that it can
	// signal the whole tree with kill(-pgid). This is the shape that matters.
	{"setpgid", func() (string, error) {
		cmd := exec.Command("/bin/sleep", "30")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return "", err
		}
		defer func() {
			cmd.Process.Kill()
			cmd.Wait()
		}()
		pid := cmd.Process.Pid
		pgid, err := syscall.Getpgid(pid)
		if err != nil {
			return fmt.Sprintf("pid=%d", pid), err
		}
		detail := fmt.Sprintf("pid=%d pgid=%d parent=%d", pid, pgid, os.Getpid())
		if pgid != pid {
			return detail, errors.New("the child did not lead its own process group")
		}
		// The whole point: one signal to the negated group id reaches it.
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			return detail, fmt.Errorf("kill(-%d): %w", pgid, err)
		}
		return detail, nil
	}},

	{"setpgid-join", func() (string, error) {
		leader := exec.Command("/bin/sleep", "30")
		leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := leader.Start(); err != nil {
			return "", err
		}
		defer func() {
			leader.Process.Kill()
			leader.Wait()
		}()

		joiner := exec.Command("/bin/sleep", "30")
		joiner.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Process.Pid}
		if err := joiner.Start(); err != nil {
			return "", err
		}
		defer func() {
			joiner.Process.Kill()
			joiner.Wait()
		}()

		pgid, err := syscall.Getpgid(joiner.Process.Pid)
		if err != nil {
			return "", err
		}
		detail := fmt.Sprintf("leader=%d joiner=%d pgid=%d", leader.Process.Pid, joiner.Process.Pid, pgid)
		if pgid != leader.Process.Pid {
			return detail, errors.New("the second child did not join the first one's group")
		}
		return detail, nil
	}},

	{"pgroup-inherit", func() (string, error) {
		cmd := exec.Command("/bin/sleep", "30")
		if err := cmd.Start(); err != nil {
			return "", err
		}
		defer func() {
			cmd.Process.Kill()
			cmd.Wait()
		}()
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			return "", err
		}
		parent, err := syscall.Getpgid(os.Getpid())
		if err != nil {
			return "", err
		}
		detail := fmt.Sprintf("child=%d parent=%d", pgid, parent)
		if pgid != parent {
			return detail, errors.New("a plain spawn did not inherit the parent's process group")
		}
		return detail, nil
	}},

	{"sysunsupported", func() (string, error) {
		cmd := exec.Command("/bin/echo", "hello")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		err := cmd.Start()
		if err == nil {
			cmd.Process.Kill()
			cmd.Wait()
			return "", errors.New("Setsid was silently ignored")
		}
		if !strings.Contains(err.Error(), "Setsid") {
			return err.Error(), errors.New("the error does not name the field that was refused")
		}
		return err.Error(), nil
	}},
}

func main() {
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
		fmt.Fprintln(os.Stderr, "usage: spawnprobe [name ...]")
		os.Exit(2)
	}
	if failures != 0 {
		fmt.Printf("\n%d of %d checks failed\n", failures, ran)
		os.Exit(1)
	}
	fmt.Printf("\nall %d checks passed\n", ran)
}
