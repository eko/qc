package ladder

import (
	"cmp"
	"math"
	"slices"
)

// Probe is one measured encode of the digest (a probe encode: not to be
// confused with package probe, which reads container metadata).
type Probe struct {
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	CRF       float64 `json:"crf"`
	Bitrate   int64   `json:"bitrate"`
	VMAF      float64 `json:"vmaf"`
	HalfWidth float64 `json:"vmafHalfWidth"`
	// Extra marks a probe added, in fixed probing, to extend a curve that
	// stops short of Constraints.TopVMAF: the top resolution's, and the
	// resolution just below the top rung's when it promises to reach the
	// top quality at least 10% cheaper (see the package documentation).
	Extra bool `json:"extra,omitempty"`
}

// Curve is the rate-quality curve of one resolution: VMAF and CRF as
// piecewise-linear functions of log bitrate between probes. VMAF is made
// non-decreasing (measurement noise can invert close probes).
type Curve struct {
	Width  int
	Height int
	logR   []float64
	vmaf   []float64
	crf    []float64
	// halfWidth is the measurement half-width of each probe.
	halfWidth []float64
}

// NewCurve builds the curve of the probes of one resolution.
func NewCurve(
	probes []Probe,
) Curve {
	sorted := slices.Clone(probes)
	slices.SortFunc(sorted, func(a, b Probe) int { return cmp.Compare(a.Bitrate, b.Bitrate) })

	c := Curve{}
	if len(sorted) > 0 {
		c.Width, c.Height = sorted[0].Width, sorted[0].Height
	}

	for _, p := range sorted {
		if p.Bitrate <= 0 {
			continue
		}

		c.logR = append(c.logR, math.Log(float64(p.Bitrate)))
		c.vmaf = append(c.vmaf, p.VMAF)
		c.crf = append(c.crf, p.CRF)
		c.halfWidth = append(c.halfWidth, p.HalfWidth)
	}

	c.vmaf = isotonic(c.vmaf)

	return c
}

// Range returns the bitrates spanned by the probes.
func (c Curve) Range() (lo, hi float64) {
	if len(c.logR) == 0 {
		return 0, 0
	}

	return math.Exp(c.logR[0]), math.Exp(c.logR[len(c.logR)-1])
}

// VMAFAt interpolates the VMAF at bitrate; ok is false outside the probed
// range (no extrapolation: the envelope only trusts measurements).
func (c Curve) VMAFAt(
	bitrate float64,
) (float64, bool) {
	return interpolate(c.logR, c.vmaf, math.Log(bitrate), false)
}

// extendedVMAF is VMAFAt, extended past the probes along the edge
// segments.
func (c Curve) extendedVMAF(
	bitrate float64,
) float64 {
	if v, ok := c.VMAFAt(bitrate); ok || len(c.vmaf) < 2 || c.vmaf[0] == c.vmaf[len(c.vmaf)-1] {
		return v
	}

	a, b := c.edgeSegment(math.Log(bitrate) < c.logR[0])
	x := math.Log(bitrate)

	return c.vmaf[a] + (x-c.logR[a])*(c.vmaf[b]-c.vmaf[a])/(c.logR[b]-c.logR[a])
}

// QualityRange returns the VMAF spanned by the probes.
func (c Curve) QualityRange() (lo, hi float64) {
	if len(c.vmaf) == 0 {
		return 0, 0
	}

	return c.vmaf[0], c.vmaf[len(c.vmaf)-1]
}

// BitrateFor returns the bitrate at which the curve reaches vmaf, by
// interpolation in log bitrate. Outside the probed quality range it
// extrapolates along the nearest segment and ok is false. A curve without
// any slope returns its only bitrate.
func (c Curve) BitrateFor(
	vmaf float64,
) (float64, bool) {
	n := len(c.vmaf)

	switch {
	case n == 0:
		return 0, false
	case c.vmaf[n-1] == c.vmaf[0]:
		return math.Exp(c.logR[n-1]), vmaf == c.vmaf[0]
	case vmaf < c.vmaf[0]:
		return c.extrapolate(vmaf, true), false
	case vmaf > c.vmaf[n-1]:
		return c.extrapolate(vmaf, false), false
	}

	// The cheapest bitrate reaching vmaf: flat runs (pooled by the isotonic
	// fit) resolve to their first point.
	i := slices.IndexFunc(c.vmaf, func(v float64) bool { return v >= vmaf })
	if i == 0 {
		return math.Exp(c.logR[0]), true
	}

	a, b := i-1, i
	t := (vmaf - c.vmaf[a]) / (c.vmaf[b] - c.vmaf[a])

	return math.Exp(c.logR[a] + t*(c.logR[b]-c.logR[a])), true
}

// extrapolate continues the first (low) or last segment whose quality
// changes beyond the probed range.
func (c Curve) extrapolate(
	vmaf float64,
	low bool,
) float64 {
	a, b := c.edgeSegment(low)

	return math.Exp(c.logR[a] + (vmaf-c.vmaf[a])*(c.logR[b]-c.logR[a])/(c.vmaf[b]-c.vmaf[a]))
}

// edgeSegment returns the first (low) or last segment whose quality
// changes, to extrapolate along. The curve is not flat (BitrateFor checks).
func (c Curve) edgeSegment(
	low bool,
) (int, int) {
	n := len(c.vmaf)

	if low {
		return 0, slices.IndexFunc(c.vmaf, func(v float64) bool { return v > c.vmaf[0] })
	}

	a := n - 2
	for c.vmaf[a] == c.vmaf[n-1] {
		a--
	}

	return a, n - 1
}

// halfWidthAt interpolates the half-width of the curve's points at bitrate,
// the nearest point's outside the probed range.
func (c Curve) halfWidthAt(
	bitrate float64,
) float64 {
	x := math.Log(math.Min(math.Max(bitrate, math.Exp(c.logR[0])), math.Exp(c.logR[len(c.logR)-1])))
	v, _ := interpolate(c.logR, c.halfWidth, x, true)

	return v
}

// CRFAt returns the CRF expected to produce bitrate. log(bitrate) is close to
// linear in CRF, so it extrapolates linearly outside the probes.
func (c Curve) CRFAt(
	bitrate float64,
) float64 {
	v, _ := interpolate(c.logR, c.crf, math.Log(bitrate), true)

	return v
}

// interpolate is piecewise-linear interpolation of ys over sorted xs.
func interpolate(
	xs, ys []float64,
	x float64,
	extrapolate bool,
) (float64, bool) {
	switch {
	case len(xs) == 0:
		return 0, false
	case len(xs) == 1:
		return ys[0], x == xs[0] || extrapolate
	}

	i, _ := slices.BinarySearch(xs, x)

	switch {
	case i == 0 && x < xs[0]:
		if !extrapolate {
			return 0, false
		}

		i = 1
	case i == len(xs):
		if !extrapolate {
			return 0, false
		}

		i = len(xs) - 1
	case i == 0:
		return ys[0], true
	}

	x0, x1, y0, y1 := xs[i-1], xs[i], ys[i-1], ys[i]
	if x1 == x0 {
		return y1, true
	}

	return y0 + (y1-y0)*(x-x0)/(x1-x0), true
}

// isotonic returns the closest non-decreasing sequence (pool adjacent
// violators).
func isotonic(
	ys []float64,
) []float64 {
	type block struct {
		sum   float64
		count int
	}

	var blocks []block

	for _, y := range ys {
		blocks = append(blocks, block{sum: y, count: 1})

		for len(blocks) > 1 {
			last, prev := blocks[len(blocks)-1], blocks[len(blocks)-2]
			if prev.sum/float64(prev.count) <= last.sum/float64(last.count) {
				break
			}

			blocks = blocks[:len(blocks)-2]
			blocks = append(blocks, block{sum: prev.sum + last.sum, count: prev.count + last.count})
		}
	}

	out := make([]float64, 0, len(ys))
	for _, b := range blocks {
		for range b.count {
			out = append(out, b.sum/float64(b.count))
		}
	}

	return out
}

// HullPoint is a point of the upper envelope of all curves.
type HullPoint struct {
	Bitrate int64   `json:"bitrate"`
	VMAF    float64 `json:"vmaf"`
	Height  int     `json:"height"`
}

// Envelope samples the upper envelope of curves on a log-spaced bitrate grid
// of the given number of points: at each bitrate, the resolution that gives
// the highest VMAF. Points with no curve defined are skipped.
func Envelope(
	curves []Curve,
	points int,
) []HullPoint {
	lo, hi := math.Inf(1), 0.0

	for _, c := range curves {
		a, b := c.Range()
		if b > 0 {
			lo, hi = min(lo, a), max(hi, b)
		}
	}

	if hi == 0 || points < 2 {
		return nil
	}

	var hull []HullPoint

	for i := range points {
		r := math.Exp(math.Log(lo) + (math.Log(hi)-math.Log(lo))*float64(i)/float64(points-1))

		best := HullPoint{VMAF: -1}

		for _, c := range curves {
			if v, ok := c.VMAFAt(r); ok && v > best.VMAF {
				best = HullPoint{Bitrate: int64(math.Round(r)), VMAF: v, Height: c.Height}
			}
		}

		if best.VMAF >= 0 {
			hull = append(hull, best)
		}
	}

	return hull
}
