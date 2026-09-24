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

// Analyzer implements analyze.Analyzer.
type Analyzer struct {
	opts Options
	// prev is a copy of the previous thumbnail (frames are recycled).
	prev  []byte
	flags []bool
	pts   []media.Duration
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
	thumb := analyze.Thumbnail(f)

	frozen := false
	if a.prev != nil {
		frozen = scene.MeanAbsDiff(a.prev, thumb) <= a.opts.MaxDiff
	} else {
		a.prev = make([]byte, len(thumb))
	}

	// A frozen frame also freezes its predecessor, which starts the run.
	if frozen {
		a.flags[len(a.flags)-1] = true
	}

	copy(a.prev, thumb)
	a.flags = append(a.flags, frozen)
	a.pts = append(a.pts, f.PTS)

	return nil
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	return nil
}

// Result returns the frozen segments. end is the end time of the last frame.
func (a *Analyzer) Result(
	end media.Duration,
) Result {
	return Result{Segments: segments.Detect(a.flags, a.pts, end, a.opts.MinDuration)}
}
