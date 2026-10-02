// Package shotalloc is the pure math of per-shot encoding: the local
// rate-quality model of a shot, its prediction for shots the digest misses,
// and the equal-slope allocation of settings to shots (the per-shot convex
// hull of the Dynamic Optimizer). The ladder engine measures; this package
// only computes, so every step is table-testable.
package shotalloc

import "math"

// Model is the local rate-quality model of a shot at one resolution, from
// two probes: ln(bitrate) and VMAF are each a parabola in CRF through the
// shot's two measurements, bent as the title's own curves are at that
// resolution (Bend). Two probes give each shot its level and its slope; its
// curvature is the title's.
//
// VMAF was first a parabola in ln(bitrate), with one coefficient for the
// title. Its bump between the probes then grows as the square of the gap
// between the shot's two bitrates: over probes 15 CRF apart, shots were
// promised VMAF 100 where 94 was measured, and 10 to 25% more bitrate than
// they took, ln(bitrate) being taken as linear in CRF. In CRF, the bump is
// the one measured on the title, the same for every shot (see
// docs/validation.md).
type Model struct {
	// crfA and crfB are the CRFs of the probes; xA and xB the ln(bitrate)
	// and vA and vB the VMAF of the shot at them.
	crfA, crfB float64
	xA, xB     float64
	vA, vB     float64
	// bend is the curvature of both parabolas.
	bend Bend
}

// Bend is the curvature in CRF of the curves of a title at one resolution:
// the coefficient of CRF² of its ln(bitrate) and of its VMAF. The zero
// value is no curvature: both linear in CRF between the probes.
type Bend struct {
	Rate, VMAF float64
}

// NewModel fits the model of a shot measured at two CRFs a and b (with
// their bitrates and VMAFs): both parabolas go through both measurements.
func NewModel(
	crfA, crfB float64,
	rateA, rateB, vmafA, vmafB float64,
	bend Bend,
) Model {
	return Model{crfA: crfA, crfB: crfB, xA: math.Log(rateA), xB: math.Log(rateB), vA: vmafA, vB: vmafB, bend: bend}
}

// At returns the modelled bitrate (b/s) and VMAF at crf.
func (m Model) At(
	crf float64,
) (float64, float64) {
	x := m.parabola(crf, m.xA, m.xB, m.bend.Rate)
	v := m.parabola(crf, m.vA, m.vB, m.bend.VMAF)

	return math.Exp(x), math.Min(v, 100)
}

// parabola is the value at crf of the parabola through a at crfA and b at
// crfB with the quadratic coefficient bend. Where a bent curve would turn
// back (a higher CRF giving more bits or more quality, or a lower one
// less), it stays flat from its turning point on: neither ever grows with
// the CRF.
func (m Model) parabola(
	crf, a, b, bend float64,
) float64 {
	span := m.crfB - m.crfA
	if span == 0 {
		return a
	}

	slope := (b - a) / span

	if bend != 0 {
		// The turning point: a parabola opening upwards (bend > 0) falls
		// before it, one opening downwards falls after it.
		turn := (m.crfA+m.crfB)/2 - slope/(2*bend)
		if bend > 0 {
			crf = math.Min(crf, turn)
		} else {
			crf = math.Max(crf, turn)
		}
	}

	return a + slope*(crf-m.crfA) + bend*(crf-m.crfA)*(crf-m.crfB)
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
		m.crfA += w * s.crfA
		m.crfB += w * s.crfB
		m.xA += w * s.xA
		m.xB += w * s.xB
		m.vA += w * s.vA
		m.vB += w * s.vB
		m.bend.Rate += w * s.bend.Rate
		m.bend.VMAF += w * s.bend.VMAF
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
