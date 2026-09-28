// Package freeze detects frozen segments: runs of frames whose thumbnail does
// not change. Thumbnails are box-filtered, which averages out codec noise.
package freeze

import (
	"github.com/eko/qc/analyze"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/segments"
	"github.com/eko/qc/media"
)

const (
	// defaultMaxDiff is tight because box filtering already averaged out
	// codec noise: any real motion moves the thumbnail by more.
	defaultMaxDiff = 0.3
	// defaultMinDuration is ffmpeg freezedetect's default, in seconds.
	defaultMinDuration = 2
)

// Options tunes the detector. The zero value uses the defaults.
type Options struct {
	// MaxDiff is the maximum mean absolute difference (0-255) between frozen
	// frames. Default 0.3.
	MaxDiff float64
	// MinDuration is the minimum length of a reported segment. Default 2s (as
	// ffmpeg freezedetect).
	MinDuration media.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxDiff <= 0 {
		o.MaxDiff = defaultMaxDiff
	}

	if o.MinDuration <= 0 {
		o.MinDuration = media.Seconds(defaultMinDuration)
	}

	return o
}

// Result lists frozen segments.
type Result struct {
	Segments []media.Interval `json:"segments"`
}

// Analyzer implements analyze.Analyzer and analyze.Forker.
type Analyzer struct {
	opts   Options
	series analyze.Series[still]
	// seq is the run of a sequential pass (Consume).
	seq *run
}

// still is the measure of one frame: whether it repeats its predecessor.
type still struct {
	repeats bool
	pts     media.Duration
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

// Result returns the frozen segments. end is the end time of the last frame.
func (a *Analyzer) Result(
	end media.Duration,
) Result {
	frames := a.series.Merge()
	flags, pts := make([]bool, len(frames)), make([]media.Duration, len(frames))

	for i, fr := range frames {
		// A frame repeating its predecessor freezes both: the predecessor
		// starts the run.
		if fr.repeats {
			flags[i], flags[i-1] = true, true
		}

		pts[i] = fr.pts
	}

	return Result{Segments: segments.Detect(flags, pts, end, a.opts.MinDuration)}
}

// run compares the frames of one run with their predecessors.
type run struct {
	parent *Analyzer
	// prev is a copy of the previous thumbnail (frames are recycled).
	prev   []byte
	first  int
	frames []still
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	thumb := analyze.Thumbnail(f)

	repeats := false
	if r.prev != nil {
		repeats = scene.MeanAbsDiff(r.prev, thumb) <= r.parent.opts.MaxDiff
	} else {
		r.prev, r.first = make([]byte, len(thumb)), f.Index
	}

	copy(r.prev, thumb)
	r.frames = append(r.frames, still{repeats: repeats, pts: f.PTS})

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	r.parent.series.Add(r.first, r.frames)

	return nil
}
