// Package analyze fans decoded frames out to independent analyzers.
package analyze

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
)

// queueDepth bounds how many frames an analyzer may lag behind the decoder.
// It is the backpressure that keeps memory flat when an analyzer is slow.
const queueDepth = 8

// Analyzer consumes decoded frames. Consume is called from a single goroutine
// in presentation order; an analyzer that keeps a frame after Consume returns
// must Retain it. Close is called once after the last frame.
type Analyzer interface {
	Consume(f *frame.Frame) error
	Close() error
}

// Run decodes req once and feeds every frame to all analyzers concurrently.
// progress, when not nil, receives the number of frames decoded so far.
//
// The first error (decoder or analyzer) cancels the run; every analyzer is
// still closed, after its queue has been drained.
func Run(
	ctx context.Context,
	src decode.Source,
	req decode.Request,
	analyzers []Analyzer,
	progress func(frames int),
) error {
	group, ctx := errgroup.WithContext(ctx)

	queues := make([]chan *frame.Frame, len(analyzers))
	for i, a := range analyzers {
		queue := make(chan *frame.Frame, queueDepth)
		queues[i] = queue

		group.Go(func() error {
			return consume(queue, a)
		})
	}

	group.Go(func() error {
		defer func() {
			for _, queue := range queues {
				close(queue)
			}
		}()

		return src.Decode(ctx, req, func(f *frame.Frame) error {
			defer f.Release()

			if err := fanOut(ctx, f, queues); err != nil {
				return err
			}

			if progress != nil {
				progress(f.Index + 1)
			}

			return nil
		})
	})

	if err := group.Wait(); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	return nil
}

// fanOut hands one reference of f to every queue. It gives up when ctx is
// cancelled, which happens when an analyzer failed: its queue may then be
// full and the send would block forever.
func fanOut(
	ctx context.Context,
	f *frame.Frame,
	queues []chan *frame.Frame,
) error {
	for _, queue := range queues {
		f.Retain()

		select {
		case queue <- f:
		case <-ctx.Done():
			f.Release()

			return fmt.Errorf("frame %d: %w", f.Index, ctx.Err())
		}
	}

	return nil
}

// consume drains queue into a. After a failure it keeps draining (releasing
// frames) so the decoder never blocks on a dead consumer.
func consume(
	queue <-chan *frame.Frame,
	a Analyzer,
) error {
	var err error

	for f := range queue {
		if err == nil {
			err = a.Consume(f)
		}

		f.Release()
	}

	if closeErr := a.Close(); err == nil {
		err = closeErr
	}

	return err
}

// Thumbnail returns the samples analyzers working at low resolution should
// read: the frame's thumbnail, or its full luma when the pool builds no
// thumbnails (pooled 8-bit planes have no row padding, so Pix is the image).
// Without this fallback an empty thumbnail would read as a black, frozen and
// cut-free picture.
func Thumbnail(
	f *frame.Frame,
) []byte {
	if len(f.Thumb.Pix) > 0 {
		return f.Thumb.Pix
	}

	return f.Luma.Pix
}
