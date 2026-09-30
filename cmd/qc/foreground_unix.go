//go:build unix

package main

import (
	"golang.org/x/sys/unix"
)

// inForeground reports whether the process is in the foreground process
// group of the terminal fd: a background job (qc run … &, brew test) must
// not draw on it, the kernel would stop it (SIGTTOU) at its first terminal
// setting.
func inForeground(
	fd int,
) bool {
	return foreground(func() (int, error) { return unix.IoctlGetInt(fd, unix.TIOCGPGRP) }, unix.Getpgrp)
}
