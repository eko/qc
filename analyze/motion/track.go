package motion

import (
	"math"
	"slices"
)

// Classification parameters.
const (
	// minReliableShare and minReliableFrames: a shot with fewer reliable
	// frames is unknown.
	minReliableShare  = 0.5
	minReliableFrames = 3
	// minMovingShare is the share of moving frames making a shot a move
	// rather than static: a camera settling for a moment is still static.
	minMovingShare = 0.3
	// dominantShare is the share of the moving frames one move must cover
	// to name the shot; otherwise it is mixed.
	dominantShare = 0.6
	// mixedRatio: a frame moving along two components, the weaker at least
	// this share of the stronger (both above their threshold), is mixed.
	mixedRatio = 0.5
	// minMagnitude (working pixels per frame) floors the motion parallax
	// is relative to, so that the misfit of slow moves is not magnified.
	minMagnitude = 1.0
)

// track is the camera motion over one shot, one value per frame.
type track struct {
	pan, tilt, zoom, roll []float64
	shake, confidence     []float64
	parallax              []float64
	valid                 []bool
	reliable              int
}

// newTrack builds the camera motion of the frames of one shot (samples, dt
// seconds from each frame's predecessor), estimated on working images
// width pixels wide. The first frame of a shot is compared with the
// previous shot: it is not reliable.
func newTrack(
	samples []sample,
	dt []float64,
	width float64,
	o Options,
) track {
	n := len(samples)
	t := track{
		pan: make([]float64, n), tilt: make([]float64, n), zoom: make([]float64, n), roll: make([]float64, n),
		confidence: make([]float64, n), parallax: make([]float64, n), valid: make([]bool, n),
	}
	dx, dy := make([]float64, n), make([]float64, n)

	for k, s := range samples {
		if k == 0 || s.confidence < o.MinConfidence {
			continue
		}

		t.valid[k], t.confidence[k] = true, s.confidence
		t.pan[k], t.tilt[k], t.zoom[k], t.roll[k] = cameraRates(s.model, dt[k], width)
		dx[k], dy[k] = -real(s.model.t)/width*100, imag(s.model.t)/width*100
		t.parallax[k] = s.residual / max(s.magnitude, minMagnitude)
		t.reliable++
	}

	step := median(dt)
	smooth := window(o.Smooth.Seconds(), step)

	for _, series := range [][]float64{t.pan, t.tilt, t.zoom, t.roll, dx, dy} {
		interpolate(series, t.valid)
	}

	for _, series := range [][]float64{t.pan, t.tilt, t.zoom, t.roll} {
		movingAverage(series, smooth)
	}

	t.shake = jitter(dx, dy, window(o.ShakeWindow.Seconds(), step))

	return t
}

// store copies the track into the title's series from frame first.
func (t track) store(
	s *Series,
	first int,
) {
	copy(s.Pan[first:], t.pan)
	copy(s.Tilt[first:], t.tilt)
	copy(s.Zoom[first:], t.zoom)
	copy(s.Roll[first:], t.roll)
	copy(s.Shake[first:], t.shake)
	copy(s.Confidence[first:], t.confidence)
}

// window is the odd number of frames of step seconds covering seconds: 1
// (no smoothing) when seconds is shorter than two frames.
func window(
	seconds, step float64,
) int {
	return max(0, int(math.Round(seconds/step/2)))*2 + 1
}

// interpolate replaces the values of the invalid frames by a linear
// interpolation of the valid ones around them (the nearest one at the
// ends). Without any valid frame, values are left as they are.
func interpolate(
	values []float64,
	valid []bool,
) {
	prev := -1

	for i := range values {
		if !valid[i] {
			continue
		}

		switch {
		case prev < 0:
			for j := range i {
				values[j] = values[i]
			}
		default:
			for j := prev + 1; j < i; j++ {
				f := float64(j-prev) / float64(i-prev)
				values[j] = values[prev] + f*(values[i]-values[prev])
			}
		}

		prev = i
	}

	if prev >= 0 {
		for j := prev + 1; j < len(values); j++ {
			values[j] = values[prev]
		}
	}
}

// movingAverage replaces values by their centred moving average over size
// frames (odd), truncated at the ends.
func movingAverage(
	values []float64,
	size int,
) {
	n := len(values)
	prefix := make([]float64, n+1)

	for i, v := range values {
		prefix[i+1] = prefix[i] + v
	}

	half := size / 2
	for i := range values {
		from, to := max(0, i-half), min(n, i+half+1)
		values[i] = (prefix[to] - prefix[from]) / float64(to-from)
	}
}

// jitter is the distance, per frame, between the camera path (the running
// sum of the displacements dx, dy) and its local linear trend over size
// frames: the high-frequency part of the path, as video stabilisers
// separate it (Grundmann et al., "Auto-directed video stabilization with
// robust L1 optimal camera paths", CVPR 2011). A local line rather than a
// moving average keeps a steady pan jitter-free up to the shot's edges,
// where the window is truncated.
func jitter(
	dx, dy []float64,
	size int,
) []float64 {
	px, py := make([]float64, len(dx)), make([]float64, len(dy))

	var x, y float64

	for i := range dx {
		x, y = x+dx[i], y+dy[i]
		px[i], py[i] = x, y
	}

	sx, sy := slices.Clone(px), slices.Clone(py)
	localTrend(sx, size)
	localTrend(sy, size)

	out := make([]float64, len(dx))
	for i := range out {
		out[i] = math.Hypot(px[i]-sx[i], py[i]-sy[i])
	}

	return out
}

// localTrend replaces values by the least-squares line through the size
// values (odd) centred on each, evaluated there; the window is truncated
// at the ends. Prefix sums make it O(n).
func localTrend(
	values []float64,
	size int,
) {
	n := len(values)
	sumV, sumKV := make([]float64, n+1), make([]float64, n+1)

	for k, v := range values {
		sumV[k+1], sumKV[k+1] = sumV[k]+v, sumKV[k]+float64(k)*v
	}

	half := size / 2
	for i := range values {
		from, to := max(0, i-half), min(n, i+half+1)
		count := float64(to - from)
		// Frame numbers relative to i: their mean and variance.
		meanK := float64(from+to-1)/2 - float64(i)
		varK := (count*count - 1) / 12
		meanV := (sumV[to] - sumV[from]) / count
		covKV := (sumKV[to]-sumKV[from])/count - (meanK+float64(i))*meanV

		values[i] = meanV
		if varK > 0 {
			values[i] -= covKV / varK * meanK
		}
	}
}

// median of values (0 when empty), without modifying them.
func median(
	values []float64,
) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := slices.Clone(values)
	slices.Sort(sorted)

	return sorted[len(sorted)/2]
}
