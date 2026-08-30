//go:build linux && !baremetal && !tinygo.wasm && !nintendoswitch

package os

import "syscall"

// Storage for the two by-value POSIX objects posix_spawn takes. musl declares
// them in lib/musl/include/spawn.h as
//
//	typedef struct {
//		int __pad0[2];
//		void *__actions;
//		int __pad[16];
//	} posix_spawn_file_actions_t;
//
//	typedef struct {
//		int __flags;
//		pid_t __pgrp;
//		sigset_t __def, __mask;
//		int __prio, __pol;
//		void *__fn;
//		char __pad[64-sizeof(void *)];
//	} posix_spawnattr_t;
//
// with sigset_t being `struct { unsigned long __bits[128/sizeof(long)]; }`,
// which is 128 bytes on every architecture musl supports. That makes the
// file-actions struct 80 bytes on LP64 and 76 on 32-bit targets, and the
// attribute struct 336 bytes on both. The arrays below are sized past those
// numbers and are uint64 so that they are also correctly over-aligned; only
// libc ever looks inside them.
type spawnFileActions [16]uint64

type spawnAttr [48]uint64

// musl's sigset_t, 128 bytes on every architecture. The zero value is the
// empty set, which is the only mask this package installs.
type sigset [16]uint64

// checkSysProcAttr reports whether the SysProcAttr a caller passed asks for
// anything posix_spawn cannot do. Linux's syscall.SysProcAttr carries a good
// many more fields than the POSIX set, and every one of them needs the child to
// run Go code between the clone and the exec, which is precisely what this
// implementation does not do.
func checkSysProcAttr(sys *syscall.SysProcAttr) error {
	if err := checkSysProcAttrCommon(sys); err != nil {
		return err
	}
	switch {
	case sys.Pdeathsig != 0:
		return errUnsupportedSysField("Pdeathsig")
	case sys.Cloneflags != 0:
		return errUnsupportedSysField("Cloneflags")
	case sys.Unshareflags != 0:
		return errUnsupportedSysField("Unshareflags")
	case sys.UidMappings != nil:
		return errUnsupportedSysField("UidMappings")
	case sys.GidMappings != nil:
		return errUnsupportedSysField("GidMappings")
	case sys.GidMappingsEnableSetgroups:
		return errUnsupportedSysField("GidMappingsEnableSetgroups")
	case sys.AmbientCaps != nil:
		return errUnsupportedSysField("AmbientCaps")
	case sys.UseCgroupFD:
		return errUnsupportedSysField("UseCgroupFD")
	case sys.CgroupFD != 0:
		return errUnsupportedSysField("CgroupFD")
	case sys.PidFD != nil:
		return errUnsupportedSysField("PidFD")
	}
	return nil
}
