//go:build (linux || darwin) && !baremetal && !tinygo.wasm

package os_test

import (
	"errors"
	. "os"
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

func TestForkExecProcSys(t *testing.T) {
	proc, err := StartProcess("/bin/echo", []string{"echo", "hello", "world"}, &ProcAttr{Sys: &syscall.SysProcAttr{}})
	if !errors.Is(err, ErrNotImplementedSys) {
		t.Fatalf("wanted ErrNotImplementedSys, got %v\n", err)
	}

	if proc != nil {
		t.Fatalf("wanted nil, got %v\n", proc)
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
