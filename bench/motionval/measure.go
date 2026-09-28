package main

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
)

// shakeWindow is the detrending window of the true camera path: the
// analyzer's default, so both shakes measure the same thing.
const shakeWindow = 0.5

// outcome is the comparison of a clip's analysis with its ground truth.
type outcome struct {
	clip clip
	// frames counts the frames with a truth; reliable those the analyzer
	// estimated.
	frames, reliable int
	// pan, tilt and zoom are the per-frame errors (rates as the analysis
	// reports them, % of width/s and %/s) of the unsmoothed estimates.
	pan, tilt, zoom errorStats
	// shake is the measured and true shake of the first shot (% of width).
	shake, trueShake float64
	// got is the classification of each detected shot.
	got []motion.Shot
	ok  bool
}

// errorStats sums per-frame errors up.
type errorStats struct {
	bias, rmse float64
}

// analyzeFunc analyses a file with options.
type analyzeFunc func(ctx context.Context, path string, opts analysis.Options) (*analysis.Report, error)

// measure analyses a rendered clip twice, unsmoothed (per-frame accuracy)
// and with the defaults (classification), and compares both with the truth.
func measure(
	ctx context.Context,
	analyze analyzeFunc,
	path string,
	c clip,
) (outcome, error) {
	raw, err := analyze(ctx, path, analysis.Options{Video: analysis.VideoOptions{Motion: motion.Options{Smooth: 1}}})
	if err != nil {
		return outcome{}, err
	}

	def, err := analyze(ctx, path, analysis.Options{})
	if err != nil {
		return outcome{}, err
	}

	if raw.Frames == nil || len(raw.Frames.MotionPan) == 0 || def.Video == nil {
		return outcome{}, fmt.Errorf("%s: %w", c.name, errNoMotion)
	}

	out := frameErrors(c, raw.Frames)
	out.got = shotClasses(def.Video.Shots)
	out.ok = matches(c.want, out.got)
	out.trueShake = trueShake(c)

	if len(out.got) > 0 {
		out.shake = out.got[0].Shake
	}

	return out, nil
}

// frameErrors compares the unsmoothed per-frame estimates with the truth
// on the frames the analyzer trusted.
func frameErrors(
	c clip,
	f *analysis.FrameSeries,
) outcome {
	out := outcome{clip: c}
	cutFrame := int(math.Round(c.cut * float64(c.fps)))

	var pan, tilt, zoom []float64

	for n := 1; n < min(c.frames, len(f.MotionPan)); n++ {
		if c.cut > 0 && n == cutFrame {
			continue
		}

		out.frames++

		if f.MotionConfidence[n] <= 0 {
			continue
		}

		out.reliable++
		wantPan, wantTilt, wantZoom := c.rates(n)
		pan, tilt, zoom = append(pan, f.MotionPan[n]-wantPan), append(tilt, f.MotionTilt[n]-wantTilt), append(zoom, f.MotionZoom[n]-wantZoom)
	}

	out.pan, out.tilt, out.zoom = summarize(pan), summarize(tilt), summarize(zoom)

	return out
}

// rates is the true camera move from frame n-1 to n: pan and tilt in % of
// the width per second (positive right and up), zoom in % per second.
func (c clip) rates(
	n int,
) (pan, tilt, zoom float64) {
	x0, y0, m0 := c.cameraAt(n - 1)
	x1, y1, m1 := c.cameraAt(n)
	perSecond := float64(c.fps) * 100

	return (x1 - x0) * m1 / clipWidth * perSecond,
		-(y1 - y0) * m1 / clipWidth * perSecond,
		math.Log(m1/m0) * perSecond
}

func summarize(
	errs []float64,
) errorStats {
	if len(errs) == 0 {
		return errorStats{}
	}

	var sum, squares float64
	for _, e := range errs {
		sum, squares = sum+e, squares+e*e
	}

	n := float64(len(errs))

	return errorStats{bias: sum / n, rmse: math.Sqrt(squares / n)}
}

// trueShake is the median jitter of the true camera path of the first shot
// around its local linear trend, in % of the width, as the analyzer
// defines it: the path accumulates each frame's displacement at that
// frame's magnification.
func trueShake(
	c clip,
) float64 {
	last := c.frames
	if c.cut > 0 {
		last = int(math.Round(c.cut * float64(c.fps)))
	}

	xs, ys := make([]float64, last), make([]float64, last)
	for n := 1; n < last; n++ {
		x0, y0, _ := c.cameraAt(n - 1)
		x1, y1, m := c.cameraAt(n)
		xs[n], ys[n] = xs[n-1]+(x1-x0)*m, ys[n-1]+(y1-y0)*m
	}

	half := int(math.Round(shakeWindow * float64(c.fps) / 2))
	sx, sy := localTrend(xs, half), localTrend(ys, half)

	jitter := make([]float64, 0, last)
	for n := 1; n < last; n++ {
		jitter = append(jitter, math.Hypot(xs[n]-sx[n], ys[n]-sy[n]))
	}

	if len(jitter) == 0 {
		return 0
	}

	slices.Sort(jitter)

	return jitter[len(jitter)/2] / clipWidth * 100
}

// localTrend is the least-squares line through the 2·half+1 values
// centred on each (truncated at the ends), evaluated there.
func localTrend(
	values []float64,
	half int,
) []float64 {
	out := make([]float64, len(values))

	for i := range values {
		from, to := max(0, i-half), min(len(values), i+half+1)

		var sk, sv, skk, skv float64

		for k := from; k < to; k++ {
			d := float64(k - i)
			sk, sv, skk, skv = sk+d, sv+values[k], skk+d*d, skv+d*values[k]
		}

		n := float64(to - from)
		den := n*skk - sk*sk
		out[i] = sv / n

		if den != 0 {
			slope := (n*skv - sk*sv) / den
			out[i] = (sv - slope*sk) / n
		}
	}

	return out
}

func shotClasses(
	shots []analysis.ShotReport,
) []motion.Shot {
	out := make([]motion.Shot, 0, len(shots))
	for _, s := range shots {
		if s.Camera != nil {
			out = append(out, *s.Camera)
		}
	}

	return out
}

// matches reports whether every shot got its expected class, direction
// and shake flag.
func matches(
	want []expectation,
	got []motion.Shot,
) bool {
	if len(want) != len(got) {
		return false
	}

	for i, w := range want {
		g := got[i]
		if slices.Contains(w.accept, g.Class) {
			continue
		}

		if g.Class != w.class || g.Direction != w.direction || g.Shaky != w.shaky {
			return false
		}
	}

	return true
}
