// Package black detects black segments: runs of frames where nearly every
// thumbnail pixel is close to the nominal black level.
package black

import (
	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/segments"
	"github.com/eko/qc/media"
)

// Defaults, those of ffmpeg's blackdetect filter.
const (
	defaultPixelThreshold = 0.1
	defaultPictureRatio   = 0.98
	defaultMinDuration    = 0.5 // seconds
)

// Options tunes the detector. The zero value uses the defaults.
type Options struct {
	// PixelThreshold is the luma tolerance above black, as a share of the
	// black-to-white range. Default 0.1 (as ffmpeg blackdetect).
	PixelThreshold float64
	// PictureRatio is the share of dark pixels for a black frame. Default 0.98.
	PictureRatio float64
	// MinDuration is the minimum length of a reported segment. Default 0.5s.
	MinDuration media.Duration
}

func (o Options) withDefaults() Options {
	if o.PixelThreshold <= 0 {
		o.PixelThreshold = defaultPixelThreshold
	}

	if o.PictureRatio <= 0 {
		o.PictureRatio = defaultPictureRatio
	}

	if o.MinDuration <= 0 {
		o.MinDuration = media.Seconds(defaultMinDuration)
	}

	return o
}

// Result lists black segments.
type Result struct {
	Segments []media.Interval `json:"segments"`
}

// Analyzer implements analyze.Analyzer and analyze.Forker.
type Analyzer struct {
	opts Options
	// threshold is the brightest code value still counted as dark.
	threshold byte
	series    analyze.Series[flagged]
	// seq is the run of a sequential pass (Consume).
	seq *run
}

// flagged is the measure of one frame.
type flagged struct {
	black bool
	pts   media.Duration
}

// New returns an Analyzer for a signal with the given nominal levels.
func New(
	levels media.Levels,
	opts Options,
) *Analyzer {
	opts = opts.withDefaults()
	threshold := float64(levels.Black) + opts.PixelThreshold*float64(levels.White-levels.Black)

	return &Analyzer{opts: opts, threshold: byte(min(threshold, 255))}
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	if a.seq == nil {
		a.seq = a.fork()
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
	return a.fork()
}

func (a *Analyzer) fork() *run {
	return &run{parent: a, first: -1}
}

// Result returns the black segments. end is the end time of the last frame.
func (a *Analyzer) Result(
	end media.Duration,
) Result {
	frames := a.series.Merge()
	flags, pts := make([]bool, len(frames)), make([]media.Duration, len(frames))

	for i, fr := range frames {
		flags[i], pts[i] = fr.black, fr.pts
	}

	return Result{Segments: segments.Detect(flags, pts, end, a.opts.MinDuration)}
}

// run flags the frames of one run.
type run struct {
	parent *Analyzer
	first  int
	frames []flagged
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	if r.first < 0 {
		r.first = f.Index
	}

	pix := analyze.Thumbnail(f)

	dark := 0
	for _, v := range pix {
		if v <= r.parent.threshold {
			dark++
		}
	}

	black := len(pix) > 0 && float64(dark) >= r.parent.opts.PictureRatio*float64(len(pix))
	r.frames = append(r.frames, flagged{black: black, pts: f.PTS})

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	r.parent.series.Add(r.first, r.frames)

	return nil
}
