package motion

import "github.com/eko/qc/media"

// Defaults, tuned on synthetic moves of known speed and on real content
// (docs/validation.md). Speeds are relative to the picture width, so they
// hold at any resolution and frame rate.
const (
	// defaultSmooth is the low-pass window of the camera move: short
	// enough to follow a pan starting or stopping, long enough to average
	// out the jitter of a handheld camera.
	defaultSmooth = 0.5 // seconds
	// defaultShakeWindow is the window the camera path is detrended over:
	// what moves faster than ~2 Hz is shake (handheld tremor and walking
	// bounce are 2-10 Hz), what moves slower is the intended move.
	defaultShakeWindow = 0.5 // seconds
	// defaultMinSpeed (% of the width per second): a pan crossing the
	// frame in 40 s is the slowest a viewer notices as a move.
	defaultMinSpeed = 2.5
	// defaultMinZoom (% of scale per second): a 2%/s push-in is the
	// slowest noticeable.
	defaultMinZoom = 2
	// defaultMaxShake (% of the width, median over the shot): above, the
	// camera path visibly jitters (5 pixels at 1080p).
	defaultMaxShake = 0.25
	// defaultMinParallax is the misfit of a single similarity, relative
	// to the motion, above which a moving shot is a tracking or dolly
	// shot: depth layers move at different speeds.
	defaultMinParallax = 0.3
	// defaultMinConfidence is the frame confidence under which a frame's
	// estimate is ignored (flat picture, fade, competing motions).
	defaultMinConfidence = 0.25
)

// Options tunes the classification. The zero value uses the defaults.
type Options struct {
	// Smooth is the low-pass window of the camera move. Default 0.5s.
	Smooth media.Duration
	// ShakeWindow is the detrending window of the camera path: faster
	// motion is shake. Default 0.5s.
	ShakeWindow media.Duration
	// MinSpeed is the pan or tilt speed of a moving camera, in % of the
	// picture width per second. Default 2.5.
	MinSpeed float64
	// MinZoom is the zoom speed of a moving camera, in % of scale per
	// second. Default 2.
	MinZoom float64
	// MaxShake is the median jitter of the camera path, in % of the
	// picture width, above which a shot is shaky. Default 0.25.
	MaxShake float64
	// MinParallax is the relative misfit of the global model above which a
	// moving shot is a tracking shot. Default 0.3.
	MinParallax float64
	// MinConfidence is the confidence (0-1) under which a frame's estimate
	// is ignored. Default 0.25.
	MinConfidence float64
}

func (o Options) withDefaults() Options {
	defaults := []struct {
		value *float64
		def   float64
	}{
		{&o.MinSpeed, defaultMinSpeed},
		{&o.MinZoom, defaultMinZoom},
		{&o.MaxShake, defaultMaxShake},
		{&o.MinParallax, defaultMinParallax},
		{&o.MinConfidence, defaultMinConfidence},
	}

	for _, d := range defaults {
		if *d.value <= 0 {
			*d.value = d.def
		}
	}

	if o.Smooth <= 0 {
		o.Smooth = media.Seconds(defaultSmooth)
	}

	if o.ShakeWindow <= 0 {
		o.ShakeWindow = media.Seconds(defaultShakeWindow)
	}

	return o
}
