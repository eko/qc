package encode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/eko/qc/internal/ffexec"
)

// ErrStalled is returned for an encode whose output stopped growing for
// longer than the stall timeout (see WithStallTimeout), twice in a row.
var ErrStalled = errors.New("encoder stalled: its output stopped growing")

// defaultStallTimeout is how long an encode may go without its output
// growing. Encoders write their output as they go, at most a lookahead and
// a GOP late; SVT-AV1 at its fastest presets was seen to freeze with no CPU
// use, once in a few dozen ladder builds, which would otherwise hang the
// build forever.
const defaultStallTimeout = 5 * time.Minute

// stallPolls is how many times per stall timeout the output is checked.
const stallPolls = 20

// WithStallTimeout sets how long an encode may go without its output
// growing before it is stopped and tried once more (default 5 minutes).
func WithStallTimeout(
	timeout time.Duration,
) Option {
	return func(f *FFmpeg) {
		f.stall = timeout
	}
}

// encodeWatched runs ffmpeg with args writing dst, stops it when dst stops
// growing for the stall timeout, and tries once more: a stall is an
// encoder freezing, which does not recur on a new run. progress, when set,
// follows the frames written (args then ask ffmpeg for -progress on
// stdout, see progressArgs).
func (f *FFmpeg) encodeWatched(
	ctx context.Context,
	args []string,
	dst string,
	progress func(frames int),
) error {
	err := f.runWatched(ctx, args, dst, progress)
	if !errors.Is(err, ErrStalled) {
		return err
	}

	f.logger.WarnContext(ctx, "encoder stalled, trying again", "output", dst, "timeout", f.stall)

	return f.runWatched(ctx, args, dst, progress)
}

// runWatched runs ffmpeg with args once, stopped with ErrStalled when dst
// does not grow for the stall timeout.
func (f *FFmpeg) runWatched(
	ctx context.Context,
	args []string,
	dst string,
	progress func(frames int),
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var stalled atomic.Bool

	done := make(chan struct{})
	defer close(done)

	go watchOutput(dst, f.stall, done, func() {
		stalled.Store(true)
		cancel()
	})

	consume := discard
	if progress != nil {
		var last lastFrames

		consume = ffexec.LineReader(last.reader(progress))
	}

	err := ffexec.Stream(ctx, f.bin, args, consume)
	if stalled.Load() {
		return fmt.Errorf("%s: %w", dst, ErrStalled)
	}

	return err
}

// watchOutput calls stall once when the size of path does not change for
// timeout, until done is closed.
func watchOutput(
	path string,
	timeout time.Duration,
	done <-chan struct{},
	stall func(),
) {
	ticker := time.NewTicker(max(timeout/stallPolls, time.Millisecond))
	defer ticker.Stop()

	size, changed := int64(-1), time.Now()

	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			if s := fileSize(path); s != size {
				size, changed = s, now

				continue
			}

			if now.Sub(changed) >= timeout {
				stall()

				return
			}
		}
	}
}

// fileSize is the size of path, -1 when it does not exist yet.
func fileSize(
	path string,
) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}

	return info.Size()
}
