//go:build unix

package ffexec

import (
	"fmt"
	"os"
	"syscall"
)

// socketBuffer is the send and receive buffer size of output sockets. A
// pipe moves at most 64 KiB per system call, so a 2 MB raw frame costs
// dozens of wake-ups on each side; with a large socket buffer both
// processes move it in a few calls (the kernel caps it at its limit).
const socketBuffer = 1 << 20

// socketpair and pipe create the descriptors of outputPipe (replaced by
// tests to simulate failures).
var (
	socketpair = defaultSocketpair
	pipe       = defaultPipe
)

// The system calls behind socketpair and pipe.
var (
	defaultSocketpair = syscall.Socketpair
	defaultPipe       = os.Pipe
)

// outputPipe returns the read and write ends of a process output: a Unix
// socket pair with large buffers, or a pipe if sockets are unavailable.
func outputPipe() (r, w *os.File, err error) {
	// Hold the fork lock until the descriptors are close-on-exec: a
	// process started meanwhile (concurrent decoders) would otherwise
	// inherit the write end, and the reader would wait for its exit.
	syscall.ForkLock.RLock()

	fds, err := socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fds[0])
		syscall.CloseOnExec(fds[1])
	}

	syscall.ForkLock.RUnlock()

	if err != nil {
		r, w, err = pipe()
		if err != nil {
			return nil, nil, fmt.Errorf("pipe: %w", err)
		}

		return r, w, nil
	}

	for _, fd := range fds {
		// Best effort: a smaller buffer only costs more system calls.
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_SNDBUF, socketBuffer)
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, socketBuffer)
	}

	return os.NewFile(uintptr(fds[0]), "output"), os.NewFile(uintptr(fds[1]), "output-writer"), nil
}
