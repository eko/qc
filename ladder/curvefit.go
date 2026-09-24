package ladder

import (
	"cmp"
	"math"
	"slices"

	"github.com/eko/qc/internal/linalg"
)

// Prior of the curve model, in VMAF against u = ln(bitrate) − centre. Real
// rate-quality curves are smooth and concave in log bitrate: a cubic fits a
// dense 7-CRF grid of every resolution of a real title within 0.1 VMAF (a
// quadratic within 0.4), where piecewise-linear interpolation between probes
// 7 CRF apart errs by up to 1 VMAF, and by 4 to 6 between probes 14 CRF
// apart. Quadratic coefficients fitted on 27 curves of real ladders (H.264,
// HEVC, AV1, drama and cartoon, 270p to 1080p) ranged from −2.3 (flat top of
// a 1080p curve) to −9.4 (a 270p cartoon curve), −4.5 on average; the cubic
// coefficients of the dense grid stayed within ±1.2.
const (
	priorQuadratic   = -4.5
	priorQuadraticSD = 2.5
	priorCubicSD     = 1.0
	// vaguePrecision leaves the level and slope of a curve to the data.
	vaguePrecision = 1e-8
	// shapeNoise is the probe noise (VMAF standard deviation) assumed when
	// computing the shape uncertainty: what the probes' positions leave
	// unknown, independent of measurement noise, which more encodes cannot
	// reduce as cheaply as a tighter --precision would.
	shapeNoise = 0.05
	// minNoise floors the standard deviation of a probe (an exact
	// measurement still differs from the title by the digest's choice).
	minNoise = 0.1
	// z95 turns a standard deviation into a 95% half-width.
	z95 = 1.96
	// densifyStep is the spacing, in ln(bitrate), of the points a fitted
	// curve is sampled on for the envelope (≈5% bitrate).
	densifyStep = 0.05
)

// curvaturePrior is the Gaussian prior of the quadratic coefficient.
type curvaturePrior struct {
	mean, sd float64
}

// defaultPrior is the curvature prior before the title's own curves tell.
var defaultPrior = curvaturePrior{mean: priorQuadratic, sd: priorQuadraticSD}

// pooledSD is the spread of the quadratic coefficient between resolutions
// of one title (±1 on the real ladders above, against ±2.5 across titles).
const pooledSD = 1.5

// minPooled is the number of resolutions with three probes or more needed
// to pool their curvature.
const minPooled = 2

// pooledPrior averages the quadratic coefficient of the fits with three
// probes or more, when at least minPooled resolutions have them.
func pooledPrior(
	fits []curveFit,
) (curvaturePrior, bool) {
	sum, n := 0.0, 0

	for _, f := range fits {
		if len(f.probes) >= 3 {
			sum += f.mean[2]
			n++
		}
	}

	if n < minPooled {
		return curvaturePrior{}, false
	}

	return curvaturePrior{mean: sum / float64(n), sd: pooledSD}, true
}

// terms is the number of coefficients of the cubic model.
const terms = 4

type (
	vector [terms]float64
	matrix [terms][terms]float64
)

// curveFit is the Bayesian cubic regression of one resolution's VMAF on
// ln(bitrate): a Gaussian prior on the coefficients (vague level and slope,
// informative curvature and cubic terms) updated by probes whose noise is
// their measurement half-width. Its covariance does not depend on measured
// values, so the benefit of a probe can be computed before encoding it.
type curveFit struct {
	width, height int
	centre        float64
	// probes of the resolution, sorted by bitrate, with a positive bitrate.
	probes []Probe
	mean   vector
	// precision and shape are the inverse covariances of the coefficients
	// with the probes' real noise and with shapeNoise.
	precision matrix
	shape     matrix
	// reach is the lowest ln(bitrate) probed, or to be probed (see with).
	reach float64
}

// fitCurve fits the probes of one resolution.
func fitCurve(
	probes []Probe,
	prior curvaturePrior,
) curveFit {
	sorted := slices.Clone(probes)
	slices.SortFunc(sorted, func(a, b Probe) int { return cmp.Compare(a.Bitrate, b.Bitrate) })
	sorted = slices.DeleteFunc(sorted, func(p Probe) bool { return p.Bitrate <= 0 })

	f := curveFit{probes: sorted}
	if len(probes) > 0 {
		f.width, f.height = probes[0].Width, probes[0].Height
	}

	if len(sorted) > 0 {
		f.reach = math.Log(float64(sorted[0].Bitrate))
		f.centre = (f.reach + math.Log(float64(sorted[len(sorted)-1].Bitrate))) / 2
	}

	priorMean := vector{0, 0, prior.mean, 0}
	priorPrecision := vector{vaguePrecision, vaguePrecision, 1 / (prior.sd * prior.sd), 1 / (priorCubicSD * priorCubicSD)}

	var rhs vector

	for i := range terms {
		f.precision[i][i] = priorPrecision[i]
		f.shape[i][i] = priorPrecision[i]
		rhs[i] = priorPrecision[i] * priorMean[i]
	}

	for _, p := range sorted {
		phi := f.basis(math.Log(float64(p.Bitrate)))
		noise := probeNoise(p)
		f.precision = f.precision.addOuter(phi, 1/(noise*noise))
		f.shape = f.shape.addOuter(phi, 1/(shapeNoise*shapeNoise))

		for i := range terms {
			rhs[i] += phi[i] * p.VMAF / (noise * noise)
		}
	}

	f.mean = f.precision.inverse().mul(rhs)

	return f
}

// probeNoise is the standard deviation of a probe's VMAF.
func probeNoise(
	p Probe,
) float64 {
	return math.Max(p.HalfWidth/z95, minNoise)
}

// basis returns the regressors of ln(bitrate) x.
func (f curveFit) basis(
	x float64,
) vector {
	u := x - f.centre

	return vector{1, u, u * u, u * u * u}
}

// predict returns the fitted VMAF at ln(bitrate) x and its 95% half-width,
// measurement noise included.
func (f curveFit) predict(
	x float64,
) (float64, float64) {
	phi := f.basis(x)

	return dot(f.mean, phi), z95 * math.Sqrt(math.Max(phi.quadratic(f.precision.inverse()), 0))
}

// shapeError is the 95% half-width of the fitted VMAF at x that the probe
// positions leave unknown (see shapeNoise).
func (f curveFit) shapeError(
	x float64,
) float64 {
	phi := f.basis(x)

	return z95 * math.Sqrt(math.Max(phi.quadratic(f.shape.inverse()), 0))
}

// with returns the fit's shape once a probe at x is added: the benefit of a
// probe is known before encoding it.
func (f curveFit) with(
	x float64,
) curveFit {
	f.shape = f.shape.addOuter(f.basis(x), 1/(shapeNoise*shapeNoise))
	f.reach = math.Min(f.reach, x)

	return f
}

// rangeLog returns the probed range of ln(bitrate), ok false without probes.
func (f curveFit) rangeLog() (float64, float64, bool) {
	if len(f.probes) == 0 {
		return 0, 0, false
	}

	return math.Log(float64(f.probes[0].Bitrate)), math.Log(float64(f.probes[len(f.probes)-1].Bitrate)), true
}

// crfAt interpolates the CRF of ln(bitrate) x between the probes, linearly
// extrapolated outside (log bitrate is close to linear in CRF).
func (f curveFit) crfAt(
	x float64,
) float64 {
	xs := make([]float64, len(f.probes))
	crfs := make([]float64, len(f.probes))

	for i, p := range f.probes {
		xs[i], crfs[i] = math.Log(float64(p.Bitrate)), p.CRF
	}

	v, _ := interpolate(xs, crfs, x, true)

	return v
}

// curve samples the fitted curve every densifyStep over the probed range
// (and at every probe) into a Curve, so that the envelope and the rung
// settings work unchanged. The sampled VMAF is made non-decreasing, CRFs
// are interpolated between the probes, and each point carries its 95%
// half-width.
func (f curveFit) curve() Curve {
	c := Curve{Width: f.width, Height: f.height}

	lo, hi, ok := f.rangeLog()
	if !ok {
		return c
	}

	var xs []float64
	for x := lo; x < hi; x += densifyStep {
		xs = append(xs, x)
	}

	for _, p := range f.probes {
		xs = append(xs, math.Log(float64(p.Bitrate)))
	}

	slices.Sort(xs)
	xs = slices.CompactFunc(xs, func(a, b float64) bool { return math.Abs(b-a) < 1e-9 })

	for _, x := range xs {
		v, hw := f.predict(x)
		c.logR = append(c.logR, x)
		c.vmaf = append(c.vmaf, v)
		c.crf = append(c.crf, f.crfAt(x))
		c.halfWidth = append(c.halfWidth, hw)
	}

	c.vmaf = isotonic(c.vmaf)

	return c
}

// fitsOf fits the probes of every resolution, highest first.
func fitsOf(
	probes []Probe,
) []curveFit {
	byHeight := map[int][]Probe{}
	for _, p := range probes {
		byHeight[p.Height] = append(byHeight[p.Height], p)
	}

	fits := make([]curveFit, 0, len(byHeight))
	for _, ps := range byHeight {
		fits = append(fits, fitCurve(ps, defaultPrior))
	}

	// Curves of one title share their shape: the curvature measured on the
	// resolutions with enough probes is the better prior for the others.
	if prior, ok := pooledPrior(fits); ok {
		for i, f := range fits {
			fits[i] = fitCurve(f.probes, prior)
		}
	}

	slices.SortFunc(fits, func(a, b curveFit) int { return cmp.Compare(b.height, a.height) })

	return fits
}

// smoothCurves are the fitted curves of fits.
func smoothCurves(
	fits []curveFit,
) []Curve {
	curves := make([]Curve, len(fits))
	for i, f := range fits {
		curves[i] = f.curve()
	}

	return curves
}

func dot(
	a, b vector,
) float64 {
	return linalg.Dot(a[:], b[:])
}

// quadratic returns vᵀ m v.
func (v vector) quadratic(
	m matrix,
) float64 {
	return dot(v, m.mul(v))
}

// mul returns m v.
func (m matrix) mul(
	v vector,
) vector {
	var out vector
	for i := range terms {
		out[i] = dot(m[i], v)
	}

	return out
}

// addOuter returns m + w v vᵀ.
func (m matrix) addOuter(
	v vector,
	w float64,
) matrix {
	for i := range terms {
		for j := range terms {
			m[i][j] += w * v[i] * v[j]
		}
	}

	return m
}

// inverse inverts a symmetric positive-definite matrix. Every matrix
// inverted here has the prior's positive diagonal, so pivots never vanish.
func (m matrix) inverse() matrix {
	rows := make([][]float64, terms)
	for i := range m {
		rows[i] = m[i][:]
	}

	var inv matrix
	for i, row := range linalg.Invert(rows) {
		copy(inv[i][:], row)
	}

	return inv
}
