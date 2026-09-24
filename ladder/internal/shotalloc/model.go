// Package shotalloc is the pure math of per-shot encoding: the local
// rate-quality model of a shot, its prediction for shots the digest misses,
// and the equal-slope allocation of settings to shots (the per-shot convex
// hull of the Dynamic Optimizer). The ladder engine measures; this package
// only computes, so every step is table-testable.
package shotalloc

import "math"

// Model is the local rate-quality model of a shot at one resolution, from
// two probes: ln(bitrate) linear in CRF, and VMAF quadratic in ln(bitrate)
// around the probes' midpoint with the curvature of the title's curve at
// that resolution. Two probes give each shot its level, bitrate and slope;
// its curvature is the title's.
type Model struct {
	// x0 is ln(bitrate) at crf0 and beta its derivative in CRF.
	crf0, x0, beta float64
	// vMid is the VMAF at ln(bitrate) xMid, k its slope there and q the
	// quadratic coefficient.
	xMid, vMid, k, q float64
}

// NewModel fits the model of a shot measured at two CRFs a and b (with
// their bitrates and VMAFs), with quadratic coefficient q: the parabola goes
// through both measurements.
func NewModel(
	crfA, crfB float64,
	rateA, rateB, vmafA, vmafB float64,
	q float64,
) Model {
	xA, xB := math.Log(rateA), math.Log(rateB)
	half := (xB - xA) / 2
	m := Model{crf0: crfA, x0: xA, xMid: (xA + xB) / 2, vMid: (vmafA+vmafB)/2 - q*half*half, q: q}

	if crfB != crfA {
		m.beta = (xB - xA) / (crfB - crfA)
	}

	if xB != xA {
		m.k = (vmafB - vmafA) / (xB - xA)
	}

	return m
}

// At returns the modelled bitrate (b/s) and VMAF at crf.
func (m Model) At(
	crf float64,
) (float64, float64) {
	x := m.x0 + m.beta*(crf-m.crf0)
	d := x - m.xMid

	// Past the top of the parabola quality stays flat: more bits never
	// lower VMAF.
	if m.q < 0 && m.k > 0 {
		d = math.Min(d, -m.k/(2*m.q))
	}

	return math.Exp(x), math.Min(m.vMid+m.k*d+m.q*d*d, 100)
}

// Blend is the weighted average of models (weights summing to one): a shot
// covered by several digest pieces, or predicted from similar shots.
func Blend(
	models []Model,
	weights []float64,
) Model {
	var m Model

	for i, s := range models {
		w := weights[i]
		m.crf0 += w * s.crf0
		m.x0 += w * s.x0
		m.beta += w * s.beta
		m.xMid += w * s.xMid
		m.vMid += w * s.vMid
		m.k += w * s.k
		m.q += w * s.q
	}

	return m
}

// Normalise scales weights to sum to one.
func Normalise(
	weights []float64,
) []float64 {
	total := 0.0
	for _, w := range weights {
		total += w
	}

	out := make([]float64, len(weights))
	for i, w := range weights {
		out[i] = w / math.Max(total, 1e-12)
	}

	return out
}
