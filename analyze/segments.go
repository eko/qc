package analyze

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
)

// Forker is implemented by analyzers whose work can be split into runs of
// consecutive frames analysed concurrently (RunSegments). Fork returns the
// analyzer of one run: it receives the frames of the run in order, from a
// single goroutine, while other forks analyse other runs, and it is closed
// after the last frame of its run. The Forker itself then receives no
// frame; it is closed after every fork, and its results must be those of a
// single sequential pass.
//
// Consecutive runs overlap by one frame: the last frame of a run is the
// first frame of the next one. A fork measures what compares a frame with
// its predecessor (motion, cuts) from the second frame of its run on, as a
// sequential pass does from the second frame of the video, so every such
// measure is taken once, by the run holding both frames. Measures of the
// shared frame alone are taken by both runs, identically. Series merges
// per-frame values that way.
type Forker interface {
	Analyzer
	Fork() Analyzer
}

// Splittable reports whether every analyzer is a Forker: only then can a
// video be analysed in concurrent segments (RunSegments).
func Splittable(
	analyzers []Analyzer,
) bool {
	for _, a := range analyzers {
		if _, ok := a.(Forker); !ok {
			return false
		}
	}

	return true
}

// ErrSegment is returned by RunSegments when a segment did not yield the
// frames planned: fewer than requested, or not the frame the previous
// segment ended with (a seek that did not land where expected). The
// segments are then unusable; a single sequential pass still is.
var ErrSegment = errors.New("segment does not match the plan")

// RunSegments analyses a video decoded in segments, up to workers of them
// at once, each fed to forks of every analyzer (all must be Forkers, see
// Splittable). reqs are consecutive runs of frames in order, overlapping by
// one frame (see Forker); every request but the last must set MaxFrames.
// progress, when not nil, receives the number of distinct frames analysed
// so far; calls are serialised.
//
// The first error (decoder, analyzer, ErrSegment) cancels the run; every
// fork and analyzer is still closed.
func RunSegments(
	ctx context.Context,
	src decode.Source,
	reqs []decode.Request,
	workers int,
	analyzers []Analyzer,
	progress func(frames int),
) error {
	s := &segmentRun{
		src: src, reqs: reqs, analyzers: analyzers, progress: progress,
		firsts: make([]uint32, len(reqs)), lasts: make([]uint32, len(reqs)),
	}

	err := s.run(ctx, max(workers, 1))

	for _, a := range analyzers {
		if closeErr := a.Close(); err == nil {
			err = closeErr
		}
	}

	if err == nil {
		err = s.checkOverlaps()
	}

	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	return nil
}

// segmentRun is the state of one RunSegments call.
type segmentRun struct {
	src       decode.Source
	reqs      []decode.Request
	analyzers []Analyzer
	progress  func(frames int)

	// firsts and lasts are checksums of the first and last frames of each
	// segment, compared once every segment is done.
	firsts, lasts []uint32

	mu   sync.Mutex
	done int
}

// run decodes every segment, workers at a time, in order of start.
func (s *segmentRun) run(
	ctx context.Context,
	workers int,
) error {
	parent := ctx
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(workers)

	for k := range s.reqs {
		if ctx.Err() != nil {
			break
		}

		group.Go(func() error {
			return s.segment(ctx, k)
		})
	}

	if err := group.Wait(); err != nil {
		return err
	}

	// Wait cancels the group's context: only the caller's says whether
	// segments were skipped.
	return parent.Err() //nolint:wrapcheck // the caller wraps
}

// segment decodes segment k into forks of every analyzer.
func (s *segmentRun) segment(
	ctx context.Context,
	k int,
) error {
	req := s.reqs[k]

	forks := make([]Analyzer, len(s.analyzers))
	for i, a := range s.analyzers {
		forks[i] = a.(Forker).Fork() //nolint:forcetypeassert // RunSegments requires Forkers
	}

	delivered := 0

	err := s.src.Decode(ctx, req, func(f *frame.Frame) error {
		defer f.Release()

		delivered++
		s.checksum(k, delivered, req.MaxFrames, f)

		for _, fork := range forks {
			if err := fork.Consume(f); err != nil {
				return err
			}
		}

		if delivered > 1 || k == 0 {
			s.advance()
		}

		return nil
	})

	for _, fork := range forks {
		if closeErr := fork.Close(); err == nil {
			err = closeErr
		}
	}

	if err == nil && req.MaxFrames > 0 && delivered != req.MaxFrames {
		err = fmt.Errorf("%w: segment %d yielded %d frames instead of %d", ErrSegment, k, delivered, req.MaxFrames)
	}

	return err
}

// checksumTable is hardware-accelerated on arm64 and amd64.
var checksumTable = crc32.MakeTable(crc32.Castagnoli)

// checksum records the checksum of the first frame of segment k and of its
// last one (the maxFrames-th), which the next segment must start with.
func (s *segmentRun) checksum(
	k, delivered, maxFrames int,
	f *frame.Frame,
) {
	if delivered == 1 {
		s.firsts[k] = crc32.Checksum(f.Luma.Pix, checksumTable)
	}

	if delivered == maxFrames {
		s.lasts[k] = crc32.Checksum(f.Luma.Pix, checksumTable)
	}
}

// checkOverlaps verifies that each segment started with the frame the
// previous one ended with.
func (s *segmentRun) checkOverlaps() error {
	for k := 1; k < len(s.reqs); k++ {
		if s.firsts[k] != s.lasts[k-1] {
			return fmt.Errorf("%w: segment %d does not start where segment %d ends", ErrSegment, k, k-1)
		}
	}

	return nil
}

// advance counts one more distinct frame and reports progress.
func (s *segmentRun) advance() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.done++
	if s.progress != nil {
		s.progress(s.done)
	}
}
