//go:build !unix

package ffexec

import (
	"fmt"
	"os"
)

// outputPipe returns the read and write ends of a process output.
func outputPipe() (r, w *os.File, err error) {
	r, w, err = os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("pipe: %w", err)
	}

	return r, w, nil
}
