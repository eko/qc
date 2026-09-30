package ladder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
)

// Probing is how probe encodes are placed.
type Probing string

// Probing modes.
const (
	// ProbingFixed encodes every candidate resolution at the codec's probe
	// CRFs (plus one extra probe when the top curve stops short), and
	// interpolates each curve linearly between its probes.
	ProbingFixed Probing = "fixed"
	// ProbingAdaptive starts from two CRFs per resolution, fits each curve
	// with a Bayesian cubic model, and adds probes where they reduce the
	// uncertainty of the rungs and of the crossovers between resolutions
	// most (see curveFit).
	ProbingAdaptive Probing = "adaptive"
)

// ErrInvalidProbing is returned for an unknown probing mode.
var ErrInvalidProbing = errors.New("invalid probing mode")

// ParseProbing reads a probing mode ("" is the default mode).
func ParseProbing(
	s string,
) (Probing, error) {
	switch p := Probing(s); p {
	case "", ProbingFixed, ProbingAdaptive:
		return p, nil
	}

	return "", fmt.Errorf("%w %q (supported: fixed, adaptive)", ErrInvalidProbing, s)
}

// Defaults of adaptive probing.
const (
	// defaultTolerance is the 95% half-width (VMAF) of the shape
	// uncertainty under which a rung is known: a twelfth of a rung step,
	// half the sampler's precision on each probe.
	defaultTolerance = 0.5
	// defaultBitrateTolerance is the same criterion expressed on the
	// bitrate of the rung at its quality (3%): on steep parts of a curve a
	// VMAF error is a small bitrate error.
	defaultBitrateTolerance = 0.03
)

// minExtension is how far below its lowest probe, in ln(bitrate), a probe
// extending a resolution's range goes at least (≈20% bitrate).
const minExtension = 0.2

// minGain is the smallest reduction of excess uncertainty (VMAF, summed over
// the rungs and crossovers) worth an encode.
const minGain = 0.05

// ProbingReport describes how the probes were placed.
type ProbingReport struct {
	Mode Probing `json:"mode"`
	// Rounds is the number of batches of probes.
	Rounds int `json:"rounds"`
	// Budget is the maximum number of probes (adaptive mode).
	Budget int `json:"budget,omitempty"`
	// Converged reports whether every rung and crossover was known within
	// the tolerance before the budget ran out (adaptive mode).
	Converged bool `json:"converged,omitempty"`
	// Challengers counts the probes of resolutions the rungs could not be
	// compared with (see Result.Probes, and docs/ladder.md).
	Challengers int `json:"challengers,omitempty"`
	// ProbePreset is the preset of the probes when it differs from the
	// rungs' (Options.ProbePreset); Anchors then moved the probes onto the
	// rungs' preset, and Probes carry the moved values.
	ProbePreset string   `json:"probePreset,omitempty"`
	Anchors     []Anchor `json:"anchors,omitempty"`
	// Level corrected the quality level of the probes, and of the sampled
	// rung measurements.
	Level *Level `json:"level,omitempty"`
}

// probeBudget is the maximum number of adaptive probes: two fewer than the
// fixed design (every resolution at every probe CRF, plus the extra probe),
// so adaptive is cheaper by construction; in replays on a real title's
// dense grid it was also more accurate at that budget (docs/validation.md).
// A single resolution still gets one probe beyond the initial two.
func (b *build) probeBudget() int {
	if b.opts.MaxProbes > 0 {
		return b.opts.MaxProbes
	}

	n := len(b.heights())

	return max(n*len(b.codec.ProbeCRFs)-1, 2*n+1)
}

// initialJobs is the starting design of adaptive probing: the lowest and
// highest probe CRFs at every resolution, which span the quality range and
// give each curve its level and slope (its curvature comes from the prior).
func (b *build) initialJobs() []Probe {
	crfs := []float64{b.codec.ProbeCRFs[0], b.codec.ProbeCRFs[len(b.codec.ProbeCRFs)-1]}

	var jobs []Probe

	for _, height := range b.heights() {
		w, h := b.geometry(height)
		for _, crf := range crfs {
			jobs = append(jobs, Probe{Width: w, Height: h, CRF: crf})
		}
	}

	return jobs
}

// probeAdaptive measures the initial design, then batches of up to Parallel
// probes placed where the ladder is least certain, until every rung and
// crossover is known within the tolerance or the budget is spent.
func (b *build) probeAdaptive(
	ctx context.Context,
) ([]Probe, error) {
	budget := b.probeBudget()
	jobs := b.initialJobs()
	b.resetProgress()

	var probes []Probe

	report := ProbingReport{Mode: ProbingAdaptive, Budget: budget}

	for len(jobs) > 0 {
		measured, err := b.measureAll(ctx, jobs, StageProbe, budget, b.opts.ProbePreset)
		if err != nil {
			return nil, err
		}

		probes = append(probes, measured...)
		report.Rounds++

		room := budget - len(probes)
		jobs = b.nextProbes(probes, min(room, b.opts.Parallel))
		report.Converged = len(jobs) == 0 && room > 0
	}

	b.probing = report

	return probes, nil
}

// quantity is an uncertain property of the planned ladder: the quality of a
// rung on its resolution's curve, or, when other is set, whether other beats
// the rung's resolution at its bitrate.
type quantity struct {
	height int
	other  int
	x      float64
	// tolerance of the half-width (rung) or of the regret (crossover).
	tolerance float64
	// gap is the fitted VMAF of other minus that of height (crossovers).
	gap float64
	// below marks a crossover with a higher resolution below its lowest
	// probe: only a probe there, extending its range, can settle it.
	below bool
}

// excess is how far q's uncertainty exceeds its tolerance under fits.
func (q quantity) excess(
	fits map[int]curveFit,
) float64 {
	if q.below && fits[q.other].reach <= q.x {
		return 0
	}

	hw := fits[q.height].shapeError(q.x)
	if q.other == 0 {
		return math.Max(hw-q.tolerance, 0)
	}

	other := fits[q.other].shapeError(q.x)

	return math.Max(q.gap+math.Hypot(hw, other)-q.tolerance, 0)
}

// nextProbes returns up to n probes reducing the uncertainty of the ladder
// planned on probes, chosen greedily: each pick is the probe whose addition
// reduces the summed excess uncertainty most. The fits' covariances do not
// depend on measured values, so later picks account for earlier ones before
// anything is encoded. None is returned once the ladder is known within the
// tolerance, or when no probe would help by minGain.
func (b *build) nextProbes(
	probes []Probe,
	n int,
) []Probe {
	if n <= 0 {
		return nil
	}

	extra := b.extraProbes(probes)
	jobs := extra[:min(len(extra), n)]

	fits := map[int]curveFit{}
	for _, f := range fitsOf(probes) {
		fits[f.height] = f
	}

	quantities := b.quantities(probes, fits)

	for len(jobs) < n {
		job, fit, ok := b.bestProbe(fits, quantities, slices.Concat(probes, jobs))
		if !ok {
			break
		}

		fits[job.Height] = fit
		jobs = append(jobs, job)
	}

	return jobs
}

// bestProbe returns the new probe, at the position of one of the
// quantities, that reduces their summed excess most, with the fit updated
// by it. ok is false when no probe reduces it by minGain.
func (b *build) bestProbe(
	fits map[int]curveFit,
	quantities []quantity,
	taken []Probe,
) (Probe, curveFit, bool) {
	before := totalExcess(quantities, fits)
	best, bestFit, bestGain := Probe{}, curveFit{}, minGain

	for _, q := range quantities {
		if q.excess(fits) == 0 {
			continue
		}

		for _, height := range []int{q.height, q.other} {
			f, ok := fits[height]
			if !ok {
				continue
			}

			x := q.x
			if q.below && height == q.other {
				// Extend well past the rung, which moves as curves change.
				x = math.Min(x, f.reach-minExtension)
			}

			job := Probe{Width: f.width, Height: f.height, CRF: b.roundCRF(f.crfAt(x))}
			if probed(taken, job) {
				continue
			}

			updated := f.with(x)
			fits[height] = updated
			gain := before - totalExcess(quantities, fits)
			fits[height] = f

			if gain > bestGain {
				best, bestFit, bestGain = job, updated, gain
			}
		}
	}

	return best, bestFit, bestGain > minGain
}

// totalExcess sums the excess uncertainty of quantities.
func totalExcess(
	quantities []quantity,
	fits map[int]curveFit,
) float64 {
	total := 0.0
	for _, q := range quantities {
		total += q.excess(fits)
	}

	return total
}

// sameCRF is the distance under which two CRFs are the same setting: half
// the finest encoder granularity (x264 and x265 half steps).
const sameCRF = 0.25

// probed reports whether taken already holds job's resolution and CRF.
func probed(
	taken []Probe,
	job Probe,
) bool {
	return slices.ContainsFunc(taken, func(p Probe) bool {
		return p.Height == job.Height && math.Abs(p.CRF-job.CRF) < sameCRF
	})
}

// quantities plans the ladder on the fitted curves and lists what must be
// known: every rung's quality, and every crossover where another resolution
// (not above the previous rung's) could beat the rung's resolution at its
// bitrate. An unplannable ladder yields none: more probes would not change
// the verdict Build reports.
func (b *build) quantities(
	probes []Probe,
	fits map[int]curveFit,
) []quantity {
	curves := b.curves(probes)

	targets, err := PlanRungs(Envelope(curves, envelopePoints), curves, b.opts.Constraints)
	if err != nil {
		return nil
	}

	var out []quantity

	limit := math.MaxInt

	for _, t := range targets {
		height := rungCurve(curves, t).Height
		x := math.Log(float64(t.Bitrate))
		f := fits[height]

		out = append(out, quantity{height: height, x: x, tolerance: b.rungTolerance(f, x)})

		if !t.Fixed {
			out = append(out, b.crossovers(fits, f, x, limit)...)
		}

		limit = t.Height
	}

	return out
}

// crossovers lists the resolutions (not above limit) competing with fit's
// at ln(bitrate) x: those whose probed range covers x, and higher ones whose
// lowest probe lies within one maximum rung ratio above x. The envelope never
// extrapolates, so a higher resolution is out of the running below its
// lowest probe even where it would win: only a probe there can tell.
func (b *build) crossovers(
	fits map[int]curveFit,
	fit curveFit,
	x float64,
	limit int,
) []quantity {
	var out []quantity

	own, _ := fit.predict(x)

	for height, f := range fits {
		lo, hi, ok := f.rangeLog()
		if height == fit.height || height > limit || !ok {
			continue
		}

		below := height > fit.height && x < lo && x >= lo-math.Log(b.opts.Constraints.MaxRatio)
		if !below && (x < lo || x > hi) {
			continue
		}

		v, _ := f.predict(x)

		// A covering resolution already better by more than the tolerance
		// was excluded on purpose (resolutions never increase down the
		// ladder).
		if gap := v - own; below || gap <= b.opts.Tolerance {
			out = append(out, quantity{height: fit.height, other: height, x: x, tolerance: b.opts.Tolerance, gap: gap, below: below})
		}
	}

	// Map iteration order is random: keep the plan reproducible.
	slices.SortFunc(out, func(a, c quantity) int { return c.other - a.other })

	return out
}

// rungTolerance is the half-width under which a rung at ln(bitrate) x on
// fit is known: the VMAF tolerance, or the bitrate tolerance turned into
// VMAF by the curve's slope, whichever is looser.
func (b *build) rungTolerance(
	fit curveFit,
	x float64,
) float64 {
	u := x - fit.centre
	slope := fit.mean[1] + 2*fit.mean[2]*u + 3*fit.mean[3]*u*u

	return math.Max(b.opts.Tolerance, math.Log1p(b.opts.BitrateTolerance)*slope)
}

// curves returns the rate-quality curves of probes: linear interpolation
// between probes in fixed mode, the fitted curves in adaptive mode.
func (b *build) curves(
	probes []Probe,
) []Curve {
	if b.opts.Probing == ProbingAdaptive {
		return smoothCurves(fitsOf(probes))
	}

	return curvesOf(probes)
}

// rungCurve is the curve a target's settings are read on: its imposed
// resolution, or the best curve covering its bitrate (see curveFor).
func rungCurve(
	curves []Curve,
	t Target,
) Curve {
	if t.Fixed {
		if c, ok := curveOf(curves, t.Height); ok {
			return c
		}
	}

	return curveFor(curves, t)
}
