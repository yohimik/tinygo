//go:build linux && !baremetal && !tinygo.wasm && !nintendoswitch

package os

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
