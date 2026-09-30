// Package ffexec runs FFmpeg command-line tools and streams their output.
package ffexec

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const (
	// maxStderr bounds how much stderr is kept to enrich error messages:
	// ffmpeg reports the cause of a failure in its first lines, while a
	// misbehaving process could otherwise log without limit.
	maxStderr = 4096
	// initialLineBuffer and maxLineSize size the Lines scanner. ffprobe lines
	// are short; the upper bound only guards against a runaway line.
	initialLineBuffer = 64 * 1024
	maxLineSize       = 1024 * 1024
)

// ErrNotFound is returned when the requested binary is not in PATH.
var ErrNotFound = errors.New("ffexec: binary not found")

// Output runs bin with args and returns its whole stdout.
func Output(
	ctx context.Context,
	bin string,
	args []string,
) ([]byte, error) {
	var stdout bytes.Buffer

	err := Stream(ctx, bin, args, func(r io.Reader) error {
		if _, err := stdout.ReadFrom(r); err != nil {
			return fmt.Errorf("ffexec: read stdout: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return stdout.Bytes(), nil
}

// Lines runs bin with args and calls fn for every stdout line. The line slice
// is only valid during the call. Returning an error from fn stops the process.
func Lines(
	ctx context.Context,
	bin string,
	args []string,
	fn func(line []byte) error,
) error {
	return Stream(ctx, bin, args, LineReader(fn))
}

// LineReader is the consumer of Stream calling fn for every line, as Lines
// does.
func LineReader(
	fn func(line []byte) error,
) func(io.Reader) error {
	return func(r io.Reader) error {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, initialLineBuffer), maxLineSize)

		for scanner.Scan() {
			if err := fn(scanner.Bytes()); err != nil {
				return err
			}
		}

		if err := scanner.Err(); err != nil {
			return fmt.Errorf("ffexec: scan stdout: %w", err)
		}

		return nil
	}
}

// Stream runs bin with args and hands its stdout to consume. When consume
// returns an error the process is killed and that error is returned. A
// cancelled ctx kills the process and returns an error wrapping ctx.Err(); a
// non-zero exit returns an error carrying the beginning of stderr.
func Stream(
	ctx context.Context,
	bin string,
	args []string,
	consume func(io.Reader) error,
) error {
	return run(ctx, bin, args, 0, func(outputs []io.Reader) error {
		return consume(outputs[0])
	})
}

// ExtraOutput is the file descriptor of the extra output of StreamPair in
// the child process: ffmpeg writes to it as "pipe:3".
const ExtraOutput = "pipe:3"

// StreamPair runs bin with args like Stream, with a second output: a pipe
// the process sees as file descriptor 3 (ExtraOutput). consume receives
// stdout and that pipe. A process writing both outputs as it goes (ffmpeg
// with two outputs) blocks on whichever pipe is full: consume must read
// both as the process writes them, typically in turn.
func StreamPair(
	ctx context.Context,
	bin string,
	args []string,
	consume func(stdout, extra io.Reader) error,
) error {
	return run(ctx, bin, args, 1, func(outputs []io.Reader) error {
		return consume(outputs[0], outputs[1])
	})
}

// run starts bin with args, stdout and extra more output pipes (file
// descriptors 3 and up), hands their readers to consume, then drains them
// and waits for the process. See Stream for the error semantics.
func run(
	ctx context.Context,
	bin string,
	args []string,
	extra int,
	consume func([]io.Reader) error,
) error {
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNotFound, bin)
	}

	parent := ctx

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)

	stderr := &boundedBuffer{limit: maxStderr}
	cmd.Stderr = stderr

	outputs, closeOutputs, writers, err := pipes(cmd, extra)
	if err != nil {
		return err
	}
	defer closeOutputs()

	err = cmd.Start()

	// The child holds its own copies of the write ends.
	closeFiles(writers)

	if err != nil {
		return fmt.Errorf("ffexec: start %s: %w", bin, err)
	}

	consumeErr := consume(outputs)
	if consumeErr != nil {
		cancel()
	}

	// Wait must not run before the outputs are fully read: a consumer that
	// stops early would otherwise leave the process blocked on a full pipe
	// forever.
	drain(outputs)

	waitErr := cmd.Wait()

	switch {
	case parent.Err() != nil:
		// Cancellation kills the process and truncates its output: whatever
		// the consumer reported is a symptom, the cancellation is the cause.
		return fmt.Errorf("ffexec: %s: %w", bin, parent.Err())
	case consumeErr != nil:
		return consumeErr
	case waitErr != nil:
		return fmt.Errorf("ffexec: %s: %w: %s", bin, waitErr, strings.TrimSpace(stderr.String()))
	}

	return nil
}

// pipes connects stdout and extra more outputs to cmd. It returns their
// read ends, what closes them once done, and the write ends, which the
// caller closes once the process started (the child holds its copies).
func pipes(
	cmd *exec.Cmd,
	extra int,
) ([]io.Reader, func(), []*os.File, error) {
	var readers, writers []*os.File

	closeAll := func() {
		for _, r := range readers {
			_ = r.Close()
		}
	}

	for range extra + 1 {
		r, w, err := outputPipe()
		if err != nil {
			closeAll()
			closeFiles(writers)

			return nil, nil, nil, fmt.Errorf("ffexec: output pipe: %w", err)
		}

		readers, writers = append(readers, r), append(writers, w)
	}

	cmd.Stdout, cmd.ExtraFiles = writers[0], writers[1:]

	outputs := make([]io.Reader, len(readers))
	for i, r := range readers {
		outputs[i] = r
	}

	return outputs, closeAll, writers, nil
}

// closeFiles closes files, ignoring errors (write ends of pipes).
func closeFiles(
	files []*os.File,
) {
	for _, f := range files {
		_ = f.Close()
	}
}

// drain reads every output to its end, concurrently: the process may be
// blocked writing any of them.
func drain(
	outputs []io.Reader,
) {
	var wg sync.WaitGroup

	for _, r := range outputs {
		wg.Go(func() { _, _ = io.Copy(io.Discard, r) })
	}

	wg.Wait()
}

// boundedBuffer is an io.Writer keeping only the first limit bytes written;
// the rest is discarded without error so the process is never blocked.
type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		b.buf.Write(p[:min(len(p), remaining)])
	}

	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}
