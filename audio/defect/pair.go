package defect

import (
	"math"

	"github.com/eko/qc/audio/loudness"
)

// phaseSteps is the window of the correlation meter, in 100 ms steps:
// 400 ms, the integration time of the momentary loudness, long enough to
// average a few periods of the lowest frequencies that matter.
const phaseSteps = 4

// pair is the state of a pair of channels: their correlation (the
// normalised cross-product, as correlation meters show it: +1 for the same
// signal, 0 for unrelated ones, -1 for opposite polarities) and the energy
// of their difference.
type pair struct {
	left, right int
	// cross and diff are the step's sums of l × r and (l − r)², the totals
	// those of the whole signal.
	cross, diff           float64
	totalCross, totalDiff float64
	// recent holds the sums of the last phaseSteps steps, next the slot of
	// the coming one.
	recent      [phaseSteps]stepSums
	next        int
	correlation []float64
	outOfPhase  run
	segments    []span
}

// stepSums are a step's sums of l², r² and l × r.
type stepSums struct {
	left, right, cross float64
}

// measure adds the samples of both channels.
func (p *pair) measure(
	left, right []float32,
) {
	right = right[:len(left)]
	cross, diff := 0.0, 0.0

	for i, l := range left {
		a, b := float64(l), float64(right[i])
		cross += a * b
		d := a - b
		diff += d * d
	}

	p.cross += cross
	p.diff += diff
}

// phaseRule is when a pair is out of phase: a correlation at or below
// threshold while both channels are above silence (a mean square), for at
// least minLength samples.
type phaseRule struct {
	threshold float64
	silence   float64
	minLength int64
	stepSize  int
}

// phase returns the rule of the options.
func (d *Detector) phase() phaseRule {
	return phaseRule{
		threshold: d.opts.PhaseThreshold,
		silence:   float64(d.silence) * float64(d.silence),
		minLength: d.samples(d.opts.PhaseDuration.Seconds()),
		stepSize:  d.stepSize,
	}
}

// closeStep records the correlation over the window ending with the step,
// and flags the window when out of phase.
func (p *pair) closeStep(
	channels []channel,
	rule phaseRule,
) {
	p.recent[p.next] = stepSums{left: channels[p.left].stepSquares, right: channels[p.right].stepSquares, cross: p.cross}
	p.next = (p.next + 1) % phaseSteps
	p.totalCross += p.cross
	p.totalDiff += p.diff
	p.cross, p.diff = 0, 0

	var sums stepSums
	for _, s := range p.recent {
		sums.left, sums.right, sums.cross = sums.left+s.left, sums.right+s.right, sums.cross+s.cross
	}

	samples := float64(phaseSteps * rule.stepSize)
	audible := sums.left/samples > rule.silence && sums.right/samples > rule.silence

	r := 0.0
	if audible {
		r = correlation(sums.cross, sums.left, sums.right)
	}

	p.correlation = append(p.correlation, loudness.Round(r))

	index := int64(len(p.correlation))
	if audible && r <= rule.threshold {
		p.outOfPhase.extend(max(0, index-phaseSteps)*int64(rule.stepSize), index*int64(rule.stepSize))

		return
	}

	if s, ok := p.outOfPhase.close(rule.minLength); ok {
		p.segments = append(p.segments, s)
	}
}

// correlation is the normalised cross-product of two signals, 0 when
// either is silent.
func correlation(
	cross, left, right float64,
) float64 {
	if left <= 0 || right <= 0 {
		return 0
	}

	return max(-1, min(1, cross/math.Sqrt(left*right)))
}
