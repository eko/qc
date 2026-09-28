// Package motion estimates the camera motion of every frame from the shared
// thumbnails, and classifies the camera work of every shot: static, pan,
// tilt, zoom, tracking (translation with parallax), handheld (shake) or
// mixed, with a shake measure.
//
// Per frame, a grid of blocks is matched against the previous thumbnail
// (integral-projection predictor, block matching on a two-level pyramid,
// Lucas–Kanade sub-pixel step), and a similarity transform (translation,
// zoom, roll) is fitted to the block vectors with MSAC, so moving objects
// are outliers rather than camera motion. Per shot, the motion series is
// split into a low-pass component (the intended camera move) and the
// high-frequency jitter of the camera path (shake), as video stabilisers
// do. It costs about 0.17 ms of one core per frame, whatever the source
// resolution.
package motion

import (
	"sync/atomic"

	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

// Analyzer implements analyze.Analyzer and analyze.Forker: runs of frames
// can be estimated concurrently, the classification waits for Result.
type Analyzer struct {
	opts   Options
	series analyze.Series[sample]
	// width is the width of the working images, in pixels: the unit of the
	// estimates (0 until a run has estimated a frame).
	width atomic.Int64
	// seq is the run of a sequential pass (Consume).
	seq *run
}

// sample is the measure of one frame: its motion against the previous
// frame of its run, and its timestamp.
type sample struct {
	estimate
	pts media.Duration
}

// New returns an Analyzer.
func New(
	opts Options,
) *Analyzer {
	return &Analyzer{opts: opts.withDefaults()}
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	if a.seq == nil {
		a.seq = &run{parent: a}
	}

	return a.seq.Consume(f)
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	if a.seq != nil {
		return a.seq.Close()
	}

	return nil
}

// Fork implements analyze.Forker.
func (a *Analyzer) Fork() analyze.Analyzer {
	return &run{parent: a}
}

// run estimates the motion of the frames of one run: the first frame of a
// run has none (its predecessor belongs to the previous run, which
// measures it).
type run struct {
	parent    *Analyzer
	estimator estimator
	first     int
	samples   []sample
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	if len(r.samples) == 0 {
		r.first = f.Index
	}

	plane := f.Thumb
	if len(f.Thumb.Pix) == 0 {
		plane = f.Luma
	}

	var e estimate
	if plane.BytesPerSample <= 1 {
		e = r.estimator.next(analyze.Thumbnail(f), plane.Width, plane.Height, plane.Stride)
	}

	r.samples = append(r.samples, sample{estimate: e, pts: f.PTS})

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	if r.estimator.w > 0 {
		r.parent.width.Store(int64(r.estimator.w))
	}

	r.parent.series.Add(r.first, r.samples)

	return nil
}
