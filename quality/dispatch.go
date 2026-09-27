package quality

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
)

const (
	// pairBuffer bounds the frame pairs queued per clip. A small buffer
	// keeps backpressure on the decoders (an exact run is a single clip
	// spanning the whole video) while leaving room for the warm-up frames
	// shared by overlapping clips.
	pairBuffer = 8
	// decodeBuffer bounds the frames queued between a decoder and the
	// dispatcher.
	decodeBuffer = 8
	// minInFlight is the minimum number of clips queued or being scored.
	minInFlight = 2
)

// pair is a reference frame and its distorted counterpart.
type pair struct {
	ref, dist *frame.Frame
}

func (p pair) release() {
	p.ref.Release()
	p.dist.Release()
}

// sweep decodes w once on each side, piping only the frames of jobs, and
// scores the jobs on workers libvmaf contexts. A window starting after frame
// 0 seeks to it.
func (r *run) sweep(
	ctx context.Context,
	jobs []*clipJob,
	w window,
	workers, threads int,
	collect func(clipResult, int),
) error {
	// Clips in flight bound memory: frames of queued clips wait in memory.
	inFlight := max(minInFlight, 2*workers)
	queue := make(chan *clipJob, inFlight)
	slots := make(chan struct{}, inFlight)
	pool := frame.NewPool(r.spec.Width, r.spec.Height, frame.PoolOptions{Chroma: true, HighBitDepth: r.bitDepth > 8})
	selection := selectRanges(jobs, w)

	group, gctx := errgroup.WithContext(ctx)
	refFrames := make(chan *frame.Frame, decodeBuffer)
	distFrames := make(chan *frame.Frame, decodeBuffer)

	group.Go(r.decodeSide(gctx, r.ref, pool, w, selection, refFrames))
	group.Go(r.decodeSide(gctx, r.dist, pool, w, selection, distFrames))

	group.Go(func() error {
		defer close(queue)

		d := &dispatcher{jobs: jobs, queue: queue, slots: slots}
		err := d.run(gctx, refFrames, distFrames)

		// One side may end early (the videos differ in length, or a decoder
		// failed): let the other one finish instead of blocking on its
		// channel forever.
		drainFrames(refFrames)
		drainFrames(distFrames)

		return err
	})

	for range min(workers, len(jobs)) {
		group.Go(func() error {
			return r.work(queue, slots, threads, collect)
		})
	}

	if err := group.Wait(); err != nil {
		for _, job := range jobs {
			drainPairs(job.pairs)
		}

		return err
	}

	return nil
}

func (r *run) decodeSide(
	ctx context.Context,
	in Input,
	pool *frame.Pool,
	w window,
	selection [][2]int,
	out chan<- *frame.Frame,
) func() error {
	return func() error {
		defer close(out)

		req := decode.Request{
			Path:         in.Path,
			Pool:         pool,
			SourceWidth:  in.Video.Width,
			SourceHeight: in.Video.Height,
			Select:       selection,
			FrameRate:    in.Video.AvgFrameRate,
			Codec:        in.Video.Codec,
			FirstIndex:   w.seek,
			MaxFrames:    outputFrames(w, selection),
		}

		if w.seek > 0 {
			req.Start = r.ref.Bitstream.PTS[w.seek]
		}

		if r.toneMap {
			// Both sides are the reference's signal: the distorted file
			// may lack tags (raw digests, some encoders).
			req.ToneMap = &decode.ToneMap{Input: r.ref.Video.Color}
		}

		return r.meter.decoder.Decode(ctx, req, func(f *frame.Frame) error {
			select {
			case out <- f:
				return nil
			case <-ctx.Done():
				f.Release()

				return fmt.Errorf("decode %s: %w", in.Path, ctx.Err())
			}
		})
	}
}

// dispatcher zips both frame streams and hands each pair to every clip whose
// warm range contains it (consecutive clips may share warm-up frames). A clip
// is queued when its first frame arrives, once a slot is free: slots bound
// the clips queued or being scored, hence the frames held in memory.
type dispatcher struct {
	jobs  []*clipJob // sorted by warmFrom
	queue chan<- *clipJob
	slots chan<- struct{}
	// next is the first job not yet queued, active the queued jobs still
	// expecting frames.
	next   int
	active []*clipJob
}

// run dispatches frames until either stream ends, then closes the pair
// channel of every job so that workers finish. It fails when a stream ends
// before the first frame of a clip: that clip would never be scored.
func (d *dispatcher) run(
	ctx context.Context,
	refFrames, distFrames <-chan *frame.Frame,
) error {
	defer d.closeAll()

	for {
		ref, refOK := <-refFrames
		dist, distOK := <-distFrames

		if !refOK || !distOK {
			releaseFrames(ref, dist)

			if d.next < len(d.jobs) {
				missed := d.jobs[d.next]

				return fmt.Errorf("%w: streams ended before frame %d", errShortDecode, missed.warmFrom)
			}

			return nil
		}

		err := d.dispatch(ctx, ref, dist)
		releaseFrames(ref, dist)

		if err != nil {
			return err
		}
	}
}

// dispatch queues the clips starting at this pair and delivers it to every
// active clip containing it. The caller keeps its own references.
func (d *dispatcher) dispatch(
	ctx context.Context,
	ref, dist *frame.Frame,
) error {
	index := ref.Index

	for d.next < len(d.jobs) && d.jobs[d.next].warmFrom <= index {
		select {
		case d.slots <- struct{}{}:
		case <-ctx.Done():
			return fmt.Errorf("queue clip at frame %d: %w", d.jobs[d.next].warmFrom, ctx.Err())
		}

		d.queue <- d.jobs[d.next]
		d.active = append(d.active, d.jobs[d.next])
		d.next++
	}

	kept := d.active[:0]

	for _, job := range d.active {
		if index >= job.warmFrom && index < job.warmTo {
			if err := deliver(ctx, job, ref, dist); err != nil {
				return err
			}
		}

		if index+1 >= job.warmTo {
			close(job.pairs)

			continue
		}

		kept = append(kept, job)
	}

	d.active = kept

	return nil
}

// closeAll closes the pair channels of the jobs still expecting frames.
func (d *dispatcher) closeAll() {
	for _, job := range d.active {
		close(job.pairs)
	}

	for _, job := range d.jobs[d.next:] {
		close(job.pairs)
	}

	d.active, d.next = nil, len(d.jobs)
}

// deliver hands a new reference to the pair to job, giving up when ctx is
// cancelled (its worker may be gone).
func deliver(
	ctx context.Context,
	job *clipJob,
	ref, dist *frame.Frame,
) error {
	ref.Retain()
	dist.Retain()

	select {
	case job.pairs <- pair{ref: ref, dist: dist}:
		return nil
	case <-ctx.Done():
		releaseFrames(ref, dist)

		return fmt.Errorf("deliver frame %d: %w", ref.Index, ctx.Err())
	}
}

func releaseFrames(
	frames ...*frame.Frame,
) {
	for _, f := range frames {
		if f != nil {
			f.Release()
		}
	}
}

func drainFrames(
	frames <-chan *frame.Frame,
) {
	for f := range frames {
		f.Release()
	}
}

func drainPairs(
	pairs <-chan pair,
) {
	for p := range pairs {
		p.release()
	}
}
