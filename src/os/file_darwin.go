package os

import "syscall"

func pipe(p []int) error {
	// Darwin has no pipe2, so the descriptors have to be marked close-on-exec
	// afterwards, under ForkLock so that no process is spawned in between.
	// Without the flag both ends leak into every child started by
	// StartProcess, and a child holding a duplicate of the write end keeps the
	// pipe from ever reporting EOF — which is precisely how os/exec collects a
	// command's output.
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	if err := syscall.Pipe(p); err != nil {
		return err
	}
	syscall.CloseOnExec(p[0])
	syscall.CloseOnExec(p[1])
	return nil
}
