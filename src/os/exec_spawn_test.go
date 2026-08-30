//go:build (linux || darwin) && !baremetal && !tinygo.wasm

package os_test

import (
	"errors"
	. "os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Test the functionality of StartProcess, which spawns a new process and
// reports its exit status through Wait.
func TestForkExec(t *testing.T) {
	proc, err := StartProcess("/bin/echo", []string{"echo", "hello", "world"}, &ProcAttr{})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}

	if proc == nil {
		t.Fatalf("proc is nil")
	}

	if proc.Pid == 0 {
		t.Fatalf("StartProcess failed: new process has pid 0")
	}

	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if !state.Exited() {
		t.Errorf("wanted the process to have exited, got %v", state)
	}
	if !state.Success() {
		t.Errorf("wanted a successful exit, got %v", state)
	}
	if state.ExitCode() != 0 {
		t.Errorf("wanted exit code 0, got %d", state.ExitCode())
	}
	if _, ok := state.Sys().(syscall.WaitStatus); !ok {
		t.Errorf("wanted Sys() to be a syscall.WaitStatus, got %T", state.Sys())
	}
}

// A process that exits non-zero must report that status rather than an error.
func TestForkExecExitStatus(t *testing.T) {
	proc, err := StartProcess("/bin/sh", []string{"sh", "-c", "exit 3"}, &ProcAttr{})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}

	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if state.Success() {
		t.Errorf("wanted an unsuccessful exit, got %v", state)
	}
	if state.ExitCode() != 3 {
		t.Errorf("wanted exit code 3, got %d", state.ExitCode())
	}
	if state.String() != "exit status 3" {
		t.Errorf("wanted %q, got %q", "exit status 3", state.String())
	}
}

// Killing a process must be reported as a signalled, not an exited, status.
func TestForkExecKill(t *testing.T) {
	proc, err := StartProcess("/bin/sh", []string{"sh", "-c", "sleep 30"}, &ProcAttr{})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}

	if err := proc.Kill(); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if state.Exited() {
		t.Errorf("wanted the process to have been signalled, got %v", state)
	}

	// Once the process has been reaped, signalling it again reports that it is
	// done rather than reaching an unrelated process that reused the pid.
	if err := proc.Kill(); !errors.Is(err, ErrProcessDone) {
		t.Errorf("wanted ErrProcessDone, got %v", err)
	}
}

func TestForkExecErrNotExist(t *testing.T) {
	proc, err := StartProcess("invalid", []string{"invalid"}, &ProcAttr{})
	if !errors.Is(err, ErrNotExist) {
		t.Fatalf("wanted ErrNotExist, got %s\n", err)
	}

	if proc != nil {
		t.Fatalf("wanted nil, got %v\n", proc)
	}
}

// Dir is honoured through a chdir file action.
func TestForkExecProcDir(t *testing.T) {
	proc, err := StartProcess("/bin/sh", []string{"sh", "-c", "test \"$(pwd -P)\" = /"}, &ProcAttr{Dir: "/"})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}

	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if !state.Success() {
		t.Errorf("the child did not start in /, got %v", state)
	}
}

// A SysProcAttr whose fields are all zero asks for nothing, so it is accepted
// and the child starts as it would have without one.
func TestForkExecProcSysEmpty(t *testing.T) {
	proc, err := StartProcess("/bin/echo", []string{"echo", "hello", "world"}, &ProcAttr{Sys: &syscall.SysProcAttr{}})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}
	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if !state.Success() {
		t.Errorf("wanted a successful exit, got %v", state)
	}
}

// Everything posix_spawn cannot express is refused by name rather than
// silently ignored, and the error still unwraps to ErrNotImplementedSys.
func TestForkExecProcSysUnsupported(t *testing.T) {
	for _, test := range []struct {
		field string
		sys   *syscall.SysProcAttr
	}{
		{"Chroot", &syscall.SysProcAttr{Chroot: "/"}},
		{"Ptrace", &syscall.SysProcAttr{Ptrace: true}},
		{"Setsid", &syscall.SysProcAttr{Setsid: true}},
		{"Setctty", &syscall.SysProcAttr{Setctty: true}},
		{"Noctty", &syscall.SysProcAttr{Noctty: true}},
		{"Ctty", &syscall.SysProcAttr{Ctty: 1}},
		{"Foreground", &syscall.SysProcAttr{Foreground: true}},
	} {
		proc, err := StartProcess("/bin/echo", []string{"echo", "hello"}, &ProcAttr{Sys: test.sys})
		if !errors.Is(err, ErrNotImplementedSys) {
			t.Errorf("%s: wanted an error wrapping ErrNotImplementedSys, got %v", test.field, err)
		}
		if err != nil && !strings.Contains(err.Error(), test.field) {
			t.Errorf("%s: wanted the error to name the field, got %q", test.field, err.Error())
		}
		if proc != nil {
			t.Errorf("%s: wanted nil, got %v", test.field, proc)
		}
	}
}

// startSleeper spawns a process that stays alive long enough to be inspected.
func startSleeper(t *testing.T, sys *syscall.SysProcAttr) *Process {
	t.Helper()
	proc, err := StartProcess("/bin/sleep", []string{"sleep", "30"}, &ProcAttr{Sys: sys})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}
	return proc
}

// Setpgid with a zero Pgid puts the child in a brand new process group whose id
// is the child's own pid. That is what a program that wants to signal a whole
// script tree with kill(-pgid) depends on.
func TestForkExecSetpgid(t *testing.T) {
	proc := startSleeper(t, &syscall.SysProcAttr{Setpgid: true})
	defer func() {
		proc.Kill()
		proc.Wait()
	}()

	pgid, err := syscall.Getpgid(proc.Pid)
	if err != nil {
		t.Fatalf("Getpgid(%d) failed: %v", proc.Pid, err)
	}
	if pgid != proc.Pid {
		t.Errorf("wanted the child to lead its own group %d, got group %d", proc.Pid, pgid)
	}
	if pgid == Getpid() {
		t.Errorf("the child stayed in the parent's group %d", pgid)
	}
}

// A non-zero Pgid joins an existing group instead of creating one.
func TestForkExecSetpgidJoin(t *testing.T) {
	leader := startSleeper(t, &syscall.SysProcAttr{Setpgid: true})
	defer func() {
		leader.Kill()
		leader.Wait()
	}()

	joiner := startSleeper(t, &syscall.SysProcAttr{Setpgid: true, Pgid: leader.Pid})
	defer func() {
		joiner.Kill()
		joiner.Wait()
	}()

	pgid, err := syscall.Getpgid(joiner.Pid)
	if err != nil {
		t.Fatalf("Getpgid(%d) failed: %v", joiner.Pid, err)
	}
	if pgid != leader.Pid {
		t.Errorf("wanted the second child in group %d, got group %d", leader.Pid, pgid)
	}
}

// Without Setpgid the child stays in the group it inherited, which is how a
// terminal's ^C reaches it along with its parent.
func TestForkExecInheritsProcessGroup(t *testing.T) {
	proc := startSleeper(t, nil)
	defer func() {
		proc.Kill()
		proc.Wait()
	}()

	pgid, err := syscall.Getpgid(proc.Pid)
	if err != nil {
		t.Fatalf("Getpgid(%d) failed: %v", proc.Pid, err)
	}
	parent, err := syscall.Getpgid(Getpid())
	if err != nil {
		t.Fatalf("Getpgid(self) failed: %v", err)
	}
	if pgid != parent {
		t.Errorf("wanted the child in the parent's group %d, got group %d", parent, pgid)
	}
}

// A descriptor the parent never handed to the child must not survive the exec.
// This is not a tidiness question: a child holding a duplicate of a pipe's
// write end keeps that pipe from ever reporting EOF, and os/exec both collects
// a command's output and feeds it its stdin through exactly such pipes, so a
// leak here hangs the next command the program runs.
func TestForkExecDescriptorsDoNotLeak(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe failed: %v", err)
	}
	defer r.Close()
	defer w.Close()

	// The child gets no descriptors of its own, so if it can still write to
	// the write end that descriptor reached it by leaking.
	script := "echo leaked >&" + strconv.Itoa(int(w.Fd()))
	proc, err := StartProcess("/bin/sh", []string{"sh", "-c", script}, &ProcAttr{
		Files: []*File{nil, nil, nil},
	})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}
	state, err := proc.Wait()
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if state.Success() {
		t.Errorf("the child inherited the parent's pipe write end (fd %d)", w.Fd())
	}
}

// Files are handed to the child as its descriptors 0, 1 and 2.
func TestForkExecProcFiles(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatalf("Pipe failed: %v", err)
	}
	defer r.Close()

	proc, err := StartProcess("/bin/echo", []string{"echo", "piped"}, &ProcAttr{
		Files: []*File{nil, w, nil},
	})
	if err != nil {
		w.Close()
		t.Fatalf("StartProcess failed: %v", err)
	}
	// Drop the parent's copy of the write end, or the read below never sees
	// the end of the file.
	w.Close()

	buf := make([]byte, 32)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got := string(buf[:n]); got != "piped\n" {
		t.Errorf("wanted %q, got %q", "piped\n", got)
	}

	if _, err := proc.Wait(); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
}
