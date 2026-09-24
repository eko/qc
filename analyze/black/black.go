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

// Analyzer implements analyze.Analyzer.
type Analyzer struct {
	opts Options
	// threshold is the brightest code value still counted as dark.
	threshold byte
	flags     []bool
	pts       []media.Duration
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
	pix := analyze.Thumbnail(f)

	dark := 0
	for _, v := range pix {
		if v <= a.threshold {
			dark++
		}
	}

	a.flags = append(a.flags, len(pix) > 0 && float64(dark) >= a.opts.PictureRatio*float64(len(pix)))
	a.pts = append(a.pts, f.PTS)

	return nil
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	return nil
}

// Result returns the black segments. end is the end time of the last frame.
func (a *Analyzer) Result(
	end media.Duration,
) Result {
	return Result{Segments: segments.Detect(a.flags, a.pts, end, a.opts.MinDuration)}
}
