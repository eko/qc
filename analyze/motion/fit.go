package motion

import (
	"math"
	"math/cmplx"
	"slices"
)

// Points and displacements are complex numbers x + iy (y down): a
// similarity transform (translation, uniform scale, rotation) is then
// p ↦ t + (1+z)·p, and the displacement field it induces is linear,
// v(p) = t + z·p, so both the minimal two-point solution and the
// least-squares fit are closed-form complex arithmetic.

// vector is the displacement of one block between two consecutive frames.
type vector struct {
	// at is the block centre relative to the image centre, in working
	// pixels.
	at complex128
	// d is the displacement of the content, in working pixels per frame.
	d complex128
}

// similarity is the displacement field t + z·p of a similarity transform.
type similarity struct {
	t, z complex128
}

func (m similarity) at(
	p complex128,
) complex128 {
	return m.t + m.z*p
}

// Robust fit parameters.
const (
	// inlierTolerance is the largest residual (working pixels) of a vector
	// agreeing with the model: 0.4% of the width, several times the
	// sub-pixel error of a textured block, well below the motion of a
	// moving object or of another depth plane.
	inlierTolerance = 0.5
	// minVectors is the fewest block vectors a model is fitted on.
	minVectors = 4
	// minPairSpan is the smallest distance (working pixels) between the
	// two blocks of a hypothesis: closer blocks make scale and rotation
	// ill-conditioned.
	minPairSpan = 20
	// refineRounds are the least-squares refits on the inliers.
	refineRounds = 2
)

// fit is the outcome of a robust fit.
type fit struct {
	model   similarity
	inliers int
	// residual is the 75th percentile of the residuals of every vector:
	// the misfit a single similarity leaves, high under parallax.
	residual float64
	// magnitude is the RMS displacement the model predicts at the blocks.
	magnitude float64
}

// fitter fits a similarity to block vectors with MSAC (Torr & Zisserman,
// "MLESAC: a new robust estimator with application to estimating image
// geometry", CVIU 2000): hypotheses from pairs of vectors are scored by
// their truncated squared residuals, and the best is refined by least
// squares on its inliers. Hypotheses are drawn deterministically (fixed
// pair offsets, plus the median translation) so that a run is
// reproducible; with a few dozen vectors that covers the plausible models.
type fitter struct {
	scratch []float64
}

func (f *fitter) fit(
	vs []vector,
) (fit, bool) {
	if len(vs) < minVectors {
		return fit{}, false
	}

	best := f.medianTranslation(vs)
	bestCost := cost(best, vs)

	n := len(vs)
	for i := range n {
		for _, j := range [2]int{(i + n/2) % n, (i + n/3) % n} {
			m, ok := fromPair(vs[i], vs[j])
			if !ok {
				continue
			}

			if c := cost(m, vs); c < bestCost {
				best, bestCost = m, c
			}
		}
	}

	return f.refine(best, vs)
}

// refine refits the model on its inliers, then measures the result.
func (f *fitter) refine(
	m similarity,
	vs []vector,
) (fit, bool) {
	for range refineRounds {
		refit, ok := leastSquares(m, vs)
		if !ok {
			break
		}

		m = refit
	}

	out := fit{model: m}
	f.scratch = f.scratch[:0]

	var energy float64

	for _, v := range vs {
		pred := m.at(v.at)
		r := cmplx.Abs(v.d - pred)
		f.scratch = append(f.scratch, r)
		energy += real(pred)*real(pred) + imag(pred)*imag(pred)

		if r < inlierTolerance {
			out.inliers++
		}
	}

	slices.Sort(f.scratch)
	out.residual = f.scratch[len(f.scratch)*3/4]
	out.magnitude = math.Sqrt(energy / float64(len(vs)))

	return out, out.inliers >= minVectors
}

// medianTranslation is the pure translation hypothesis: the component-wise
// median of the displacements, right whenever most blocks move together.
func (f *fitter) medianTranslation(
	vs []vector,
) similarity {
	f.scratch = f.scratch[:0]
	for _, v := range vs {
		f.scratch = append(f.scratch, real(v.d))
	}

	slices.Sort(f.scratch)
	x := f.scratch[len(vs)/2]

	f.scratch = f.scratch[:0]
	for _, v := range vs {
		f.scratch = append(f.scratch, imag(v.d))
	}

	slices.Sort(f.scratch)

	return similarity{t: complex(x, f.scratch[len(vs)/2])}
}

// fromPair is the similarity mapping two blocks exactly.
func fromPair(
	a, b vector,
) (similarity, bool) {
	span := a.at - b.at
	if cmplx.Abs(span) < minPairSpan {
		return similarity{}, false
	}

	z := (a.d - b.d) / span

	return similarity{t: a.d - z*a.at, z: z}, true
}

// cost is the MSAC cost: squared residuals truncated at the tolerance.
func cost(
	m similarity,
	vs []vector,
) float64 {
	const limit = inlierTolerance * inlierTolerance

	var sum float64

	for _, v := range vs {
		r := v.d - m.at(v.at)
		sum += min(real(r)*real(r)+imag(r)*imag(r), limit)
	}

	return sum
}

// leastSquares refits m on the vectors within the tolerance of it:
// z = Σ conj(p−p̄)(v−v̄) / Σ|p−p̄|², t = v̄ − z·p̄.
func leastSquares(
	m similarity,
	vs []vector,
) (similarity, bool) {
	var (
		sumP, sumV complex128
		n          float64
	)

	for _, v := range vs {
		if cmplx.Abs(v.d-m.at(v.at)) < inlierTolerance {
			sumP, sumV, n = sumP+v.at, sumV+v.d, n+1
		}
	}

	if n < minVectors {
		return m, false
	}

	meanP, meanV := sumP/complex(n, 0), sumV/complex(n, 0)

	var num complex128

	var den float64

	for _, v := range vs {
		if cmplx.Abs(v.d-m.at(v.at)) >= inlierTolerance {
			continue
		}

		p := v.at - meanP
		num += cmplx.Conj(p) * (v.d - meanV)
		den += real(p)*real(p) + imag(p)*imag(p)
	}

	z := num / complex(den, 0)

	return similarity{t: meanV - z*meanP, z: z}, true
}
