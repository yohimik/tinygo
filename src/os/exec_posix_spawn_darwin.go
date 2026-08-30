//go:build darwin

package os

import "syscall"

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
