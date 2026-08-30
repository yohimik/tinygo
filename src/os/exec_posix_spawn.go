// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build (linux || darwin) && !baremetal && !tinygo.wasm && !nintendoswitch

package os

import (
	"errors"
	"internal/itoa"
	"runtime"
	"sync/atomic"
	"syscall"
	_ "unsafe" // for go:linkname
)

// Process creation on a hosted OS goes through posix_spawn(3) rather than
// fork(2) plus execve(2).
//
// TinyGo runs these targets with the "threads" scheduler, so the process is
// genuinely multi-threaded, and it collects with Boehm, which stops the world
// by signalling every thread. A raw fork() from Go would give the child a
// single thread holding whatever locks the other threads happened to own —
// malloc's among them — and a GC signal could land in the window between the
// fork and the exec. posix_spawn hands that whole problem to libc, which
// performs the clone and the exec itself, with no Go code running in between.
//
// The child's environment is set up through the two POSIX objects that
// posix_spawn takes: a file-actions list (dup2/close/chdir) and an attribute
// block (signal mask). Their representations differ per OS — musl defines both
// as by-value structs, Darwin as opaque pointers — so the types live in
// exec_posix_spawn_linux.go and exec_posix_spawn_darwin.go and only the
// bindings and the logic are shared here.

// The only signal values guaranteed to be present in the os package on all
// systems are os.Interrupt (send the process an interrupt) and os.Kill (force
// the process to exit). On Windows, sending os.Interrupt to a process with
// os.Process.Signal is not implemented; it will return an error instead of
// sending a signal.
var (
	Interrupt Signal = syscall.SIGINT
	Kill      Signal = syscall.SIGKILL
)

// Give the child an empty signal mask. The spawning thread may well have
// signals blocked — the Boehm collector blocks its stop-the-world signal
// around critical sections — and unlike handler dispositions a blocked mask
// survives exec, so the child would inherit it.
//
// POSIX_SPAWN_SETSIGDEF is deliberately not set: exec already resets every
// handled signal to its default, and the only dispositions that survive it are
// the ones explicitly set to SIG_IGN, which the TinyGo runtime never does.
const _POSIX_SPAWN_SETSIGMASK = 0x08

// POSIX_SPAWN_SETPGROUP, which makes posix_spawn put the child in the process
// group named by posix_spawnattr_setpgroup. The value is 2 in musl
// (lib/musl/include/spawn.h) and in Darwin's <sys/spawn.h> alike.
const _POSIX_SPAWN_SETPGROUP = 0x02

// Keep compatible with golang and always succeed and return new proc with pid.
func findProcess(pid int) (*Process, error) {
	return &Process{Pid: pid}, nil
}

func (p *Process) release() error {
	// NOOP for unix.
	p.Pid = -1
	// no need for a finalizer anymore
	runtime.SetFinalizer(p, nil)
	return nil
}

// ProcessState stores information about a process, as reported by Wait.
type ProcessState struct {
	pid    int                // The process's id.
	status syscall.WaitStatus // System-dependent status info.
	rusage *syscall.Rusage
}

// Pid returns the process id of the exited process.
func (p *ProcessState) Pid() int {
	return p.pid
}

func (p *ProcessState) String() string {
	if p == nil {
		return "<nil>"
	}
	status := p.status
	res := ""
	switch {
	case status.Exited():
		res = "exit status " + itoa.Itoa(status.ExitStatus())
	case status.Signaled():
		res = "signal: " + status.Signal().String()
	case status.Stopped():
		res = "stop signal: " + status.StopSignal().String()
		if status.StopSignal() == syscall.SIGTRAP && status.TrapCause() != 0 {
			res += " (trap " + itoa.Itoa(status.TrapCause()) + ")"
		}
	case status.Continued():
		res = "continued"
	}
	if status.CoreDump() {
		res += " (core dumped)"
	}
	return res
}

func (p *ProcessState) Success() bool {
	return p.status.ExitStatus() == 0
}

// Sys returns system-dependent exit information about
// the process. Convert it to the appropriate underlying
// type, such as syscall.WaitStatus on Unix, to access its contents.
func (p *ProcessState) Sys() interface{} {
	return p.status
}

// SysUsage returns system-dependent resource usage information about
// the exited process. Convert it to the appropriate underlying
// type, such as *syscall.Rusage on Unix, to access its contents.
func (p *ProcessState) SysUsage() interface{} {
	return p.rusage
}

func (p *ProcessState) Exited() bool {
	return p.status.Exited()
}

// ExitCode returns the exit code of the exited process, or -1
// if the process hasn't exited or was terminated by a signal.
func (p *ProcessState) ExitCode() int {
	// return -1 if the process hasn't started.
	if p == nil || !p.status.Exited() {
		return -1
	}
	return p.status.ExitStatus()
}

// Wait waits for the Process to exit, and then returns a ProcessState
// describing its status and an error, if any.
func (p *Process) Wait() (*ProcessState, error) {
	if p.Pid == -1 {
		return nil, syscall.EINVAL
	}
	var status syscall.WaitStatus
	var rusage syscall.Rusage
	var wpid int
	var err error
	for {
		wpid, err = syscall.Wait4(p.Pid, &status, 0, &rusage)
		// The Boehm collector stops the world with a signal, and a thread
		// parked in wait4 is exactly the kind of thread it has to interrupt,
		// so EINTR here is routine rather than exceptional.
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return nil, NewSyscallError("wait", err)
	}
	atomic.StoreInt32(&p.done, 1)
	return &ProcessState{pid: wpid, status: status, rusage: &rusage}, nil
}

// Signal sends a signal to the Process. Sending Interrupt on Windows is not
// implemented.
func (p *Process) Signal(sig Signal) error {
	if p.Pid == -1 {
		return errors.New("os: process already released")
	}
	if p.Pid == 0 {
		return errors.New("os: process not initialized")
	}
	if atomic.LoadInt32(&p.done) != 0 {
		return ErrProcessDone
	}
	s, ok := sig.(syscall.Signal)
	if !ok {
		return errors.New("os: unsupported signal type")
	}
	if err := syscall.Kill(p.Pid, s); err != nil {
		// The process may have exited and been reaped by another goroutine
		// between the check above and the kill; report that as ErrProcessDone,
		// which is what exec.CommandContext expects when its context fires at
		// the same moment the command finishes.
		if err == syscall.ESRCH {
			return ErrProcessDone
		}
		return err
	}
	return nil
}

// Kill causes the Process to exit immediately. Kill does not wait until the
// Process has actually exited. This only kills the Process itself, not any
// other processes it may have started.
func (p *Process) Kill() error {
	return p.Signal(Kill)
}

// In Golang, the idiomatic way to create a new process is to use the
// StartProcess function. Since the model of operating system processes in
// TinyGo differs from the one in Golang, we implement the StartProcess
// function differently: the child is created with posix_spawn instead of a
// fork/exec pair.
func startProcess(name string, argv []string, attr *ProcAttr) (p *Process, err error) {
	if attr == nil {
		attr = new(ProcAttr)
	}
	if attr.Sys != nil {
		// Everything posix_spawn cannot express (setsid, credentials, a
		// controlling terminal, ptrace, ...) is rejected by name rather than
		// silently ignored. Only Setpgid/Pgid are honoured; see
		// checkSysProcAttr in the per-OS files.
		if err := checkSysProcAttr(attr.Sys); err != nil {
			return nil, err
		}
	}

	pid, err := forkExec(name, argv, attr)
	if err != nil {
		return nil, err
	}

	return &Process{Pid: pid}, nil
}

// forkExec spawns the program at argv0 and returns its pid. Despite the name
// it does not fork: posix_spawn does the cloning and the exec in one call, and
// reports a failure to exec back to us as its return value, so the usual
// parent/child status pipe is not needed either.
func forkExec(argv0 string, argv []string, attr *ProcAttr) (pid int, err error) {
	if len(argv) == 0 {
		return 0, errors.New("exec: no argv")
	}
	if attr == nil {
		attr = new(ProcAttr)
	}

	// BytePtrFromString rejects strings containing a NUL byte, which is
	// exactly the check the C API needs.
	argv0p, err := syscall.BytePtrFromString(argv0)
	if err != nil {
		return 0, err
	}
	argvp, err := syscall.SlicePtrFromStrings(argv)
	if err != nil {
		return 0, err
	}
	env := attr.Env
	if env == nil {
		// Go's documented behaviour: a nil Env means the parent's environment.
		env = Environ()
	}
	envp, err := syscall.SlicePtrFromStrings(env)
	if err != nil {
		return 0, err
	}

	var fa spawnFileActions
	if errno := posix_spawn_file_actions_init(&fa); errno != 0 {
		return 0, syscall.Errno(errno)
	}
	defer posix_spawn_file_actions_destroy(&fa)

	var sa spawnAttr
	if errno := posix_spawnattr_init(&sa); errno != 0 {
		return 0, syscall.Errno(errno)
	}
	defer posix_spawnattr_destroy(&sa)

	var mask sigset
	if errno := posix_spawnattr_setsigmask(&sa, &mask); errno != 0 {
		return 0, syscall.Errno(errno)
	}

	flags := int16(_POSIX_SPAWN_SETSIGMASK)

	// Setpgid is the one SysProcAttr field posix_spawn can express, and the one
	// process-shaped programs actually reach for: a shell script runner puts
	// each child in its own process group so that it can signal the whole tree
	// with kill(-pgid). Pgid == 0 means "a new group whose id is the child's
	// pid", which is exactly what posix_spawnattr_setpgroup(0) does.
	if attr.Sys != nil && attr.Sys.Setpgid {
		if errno := posix_spawnattr_setpgroup(&sa, int32(attr.Sys.Pgid)); errno != 0 {
			return 0, syscall.Errno(errno)
		}
		flags |= _POSIX_SPAWN_SETPGROUP
	}

	if errno := posix_spawnattr_setflags(&sa, flags); errno != 0 {
		return 0, syscall.Errno(errno)
	}

	if attr.Dir != "" {
		dirp, err := syscall.BytePtrFromString(attr.Dir)
		if err != nil {
			return 0, err
		}
		// The file-actions list keeps a malloc'd copy of the path on musl, but
		// Darwin's implementation stores the pointer we hand it, and neither
		// allocation is visible to the garbage collector. Keep the Go bytes
		// alive until posix_spawn has run.
		defer runtime.KeepAlive(dirp)
		if errno := posix_spawn_file_actions_addchdir_np(&fa, dirp); errno != 0 {
			return 0, syscall.Errno(errno)
		}
	}

	// Lay the child's file descriptors out the way ProcAttr describes: entry i
	// becomes the child's descriptor i, and a missing entry means the
	// descriptor is closed. os/exec always passes three of these (stdin,
	// stdout, stderr) plus one per ExtraFiles entry.
	for i, f := range attr.Files {
		fd := ^uintptr(0)
		if f != nil {
			fd = f.Fd()
		}
		if fd == ^uintptr(0) {
			if errno := posix_spawn_file_actions_addclose(&fa, int32(i)); errno != 0 {
				return 0, syscall.Errno(errno)
			}
			continue
		}
		// dup2 onto the same descriptor is defined to clear FD_CLOEXEC rather
		// than being a no-op, which is what makes an inherited os.Stdin work.
		if errno := posix_spawn_file_actions_adddup2(&fa, int32(fd), int32(i)); errno != 0 {
			return 0, syscall.Errno(errno)
		}
	}

	var childPid int32
	// Hold ForkLock for the same reason the standard library does: it excludes
	// descriptors created without O_CLOEXEC (see pipe on Darwin) from leaking
	// into a child spawned concurrently.
	syscall.ForkLock.Lock()
	errno := posix_spawn(&childPid, argv0p, &fa, &sa, &argvp[0], &envp[0])
	syscall.ForkLock.Unlock()
	runtime.KeepAlive(argv0p)
	runtime.KeepAlive(argvp)
	runtime.KeepAlive(envp)
	if errno != 0 {
		// posix_spawn reports failure as an errno, including the errno of a
		// failed exec in the child, which it learns over an internal pipe.
		return 0, syscall.Errno(errno)
	}

	return int(childPid), nil
}

// Bindings for the posix_spawn family. These are declared with //go:linkname
// rather than //export because //export additionally promises the compiler
// that pointer arguments do not escape, and these do: the file-actions list
// and the attribute block outlive each individual call, and the paths handed
// to addchdir_np may be stored inside them.

//go:linkname posix_spawn posix_spawn
func posix_spawn(pid *int32, path *byte, fa *spawnFileActions, sa *spawnAttr, argv **byte, envp **byte) int32

//go:linkname posix_spawn_file_actions_init posix_spawn_file_actions_init
func posix_spawn_file_actions_init(fa *spawnFileActions) int32

//go:linkname posix_spawn_file_actions_destroy posix_spawn_file_actions_destroy
func posix_spawn_file_actions_destroy(fa *spawnFileActions) int32

//go:linkname posix_spawn_file_actions_adddup2 posix_spawn_file_actions_adddup2
func posix_spawn_file_actions_adddup2(fa *spawnFileActions, fildes, newfildes int32) int32

//go:linkname posix_spawn_file_actions_addclose posix_spawn_file_actions_addclose
func posix_spawn_file_actions_addclose(fa *spawnFileActions, fildes int32) int32

// Present in musl since 1.1.24 and in macOS since 10.15.
//
//go:linkname posix_spawn_file_actions_addchdir_np posix_spawn_file_actions_addchdir_np
func posix_spawn_file_actions_addchdir_np(fa *spawnFileActions, path *byte) int32

//go:linkname posix_spawnattr_init posix_spawnattr_init
func posix_spawnattr_init(sa *spawnAttr) int32

//go:linkname posix_spawnattr_destroy posix_spawnattr_destroy
func posix_spawnattr_destroy(sa *spawnAttr) int32

//go:linkname posix_spawnattr_setflags posix_spawnattr_setflags
func posix_spawnattr_setflags(sa *spawnAttr, flags int16) int32

//go:linkname posix_spawnattr_setsigmask posix_spawnattr_setsigmask
func posix_spawnattr_setsigmask(sa *spawnAttr, mask *sigset) int32

//go:linkname posix_spawnattr_setpgroup posix_spawnattr_setpgroup
func posix_spawnattr_setpgroup(sa *spawnAttr, pgroup int32) int32

// unsupportedSysFieldError names the SysProcAttr field a caller set that this
// implementation cannot honour. It unwraps to ErrNotImplementedSys so that code
// written against the older, all-or-nothing behaviour keeps working.
type unsupportedSysFieldError struct {
	field string
}

func (e *unsupportedSysFieldError) Error() string {
	return "os: SysProcAttr." + e.field + ": " + ErrNotImplementedSys.Error()
}

func (e *unsupportedSysFieldError) Unwrap() error {
	return ErrNotImplementedSys
}

// errUnsupportedSysField is the constructor the per-OS checkSysProcAttr uses.
func errUnsupportedSysField(field string) error {
	return &unsupportedSysFieldError{field: field}
}

// checkSysProcAttrCommon rejects every field that both Linux and Darwin declare
// and that posix_spawn cannot express. Setpgid and Pgid are deliberately absent
// — they are the two fields forkExec honours. Pgid is ignored when Setpgid is
// false, which is what the syscall package documents and what os/exec on other
// platforms does.
func checkSysProcAttrCommon(sys *syscall.SysProcAttr) error {
	switch {
	case sys.Chroot != "":
		return errUnsupportedSysField("Chroot")
	case sys.Credential != nil:
		return errUnsupportedSysField("Credential")
	case sys.Ptrace:
		return errUnsupportedSysField("Ptrace")
	case sys.Setsid:
		return errUnsupportedSysField("Setsid")
	case sys.Setctty:
		return errUnsupportedSysField("Setctty")
	case sys.Noctty:
		return errUnsupportedSysField("Noctty")
	case sys.Ctty != 0:
		return errUnsupportedSysField("Ctty")
	case sys.Foreground:
		return errUnsupportedSysField("Foreground")
	}
	return nil
}
