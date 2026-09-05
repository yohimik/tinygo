//go:build darwin

package os

import (
	"syscall"
	_ "unsafe" // Required by go:linkname. See https://pkg.go.dev/cmd/compile#hdr-Compiler_Directives.
)

var spawnDevNull = [...]byte{'/', 'd', 'e', 'v', '/', 'n', 'u', 'l', 'l', 0}

func addSpawnClose(fa *spawnFileActions, fd int32) error {
	// Darwin rejects close actions on unopened descriptors.
	// See posix_spawn(2), ERRORS, EBADF.
	if errno := posix_spawn_file_actions_addopen(fa, fd, &spawnDevNull[0], syscall.O_RDONLY, 0); errno != 0 {
		return syscall.Errno(errno)
	}
	if errno := posix_spawn_file_actions_addclose(fa, fd); errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}

//go:linkname posix_spawn_file_actions_addopen posix_spawn_file_actions_addopen
func posix_spawn_file_actions_addopen(fa *spawnFileActions, fd int32, path *byte, flags int32, mode uint16) int32

// Darwin declares both POSIX objects as opaque pointers in <spawn.h>:
//
//	typedef void *posix_spawnattr_t;
//	typedef void *posix_spawn_file_actions_t;
//
// so the "struct" a caller allocates is one pointer wide and libc mallocs the
// real thing behind it. uintptr rather than unsafe.Pointer, because what libc
// stores there is not Go memory and the collector should not follow it.
type spawnFileActions uintptr

type spawnAttr uintptr

// Darwin's sigset_t is a 32-bit mask. The zero value is the empty set, which
// is the only mask this package installs.
type sigset uint32

// checkSysProcAttr reports whether the SysProcAttr a caller passed asks for
// anything posix_spawn cannot do. Darwin's syscall.SysProcAttr declares exactly
// the fields checkSysProcAttrCommon covers, plus Setpgid and Pgid, which
// forkExec honours through POSIX_SPAWN_SETPGROUP.
func checkSysProcAttr(sys *syscall.SysProcAttr) error {
	return checkSysProcAttrCommon(sys)
}
