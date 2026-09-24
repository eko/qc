package quality

import (
	"context"
	"math"

	"github.com/eko/qc/internal/stats"
)

// Two-stage design (after Stein): a pilot round only estimates the variance,
// a second round sized from it is always run, and the interval comes from all
// clips. Stopping as soon as the pilot looks precise enough is what makes
// adaptive intervals lie, since it favours pilots with a low variance draw.
const (
	// pilotClips is the number of pilot clips per stratum, the minimum to
	// measure the within-stratum variance everywhere.
	pilotClips = 2
	// minGrowth is the minimum size of the second stage, relative to the pilot.
	minGrowth = 0.5
	// sparseGrowth replaces minGrowth when the pilot covers less than
	// sparsePilot of the frames.
	sparseGrowth = 1.0
	sparsePilot  = 0.1
	// maxRounds bounds the loop: a third round only runs when the second
	// stage missed the target.
	maxRounds = 3
	// planMargin oversizes the planned stages to absorb variance noise.
	planMargin = 1.2
)

// clipScorer scores clips and returns one result per clip, in any order.
type clipScorer func(ctx context.Context, clips []clip, round int) ([]clipResult, error)

// sampleOutcome is the result of the sampling loop.
type sampleOutcome struct {
	est     estimate
	results []clipResult
	rounds  int
	scored  int
	// fallback is set when reaching the precision would cost more than
	// MaxShare of the frames: every frame should be scored instead.
	fallback  bool
	projected float64
	// budget describes the fixed budget spent, nil when sampling was
	// driven by a precision.
	budget *SampleReport
}

// sample runs the adaptive stratified sampling loop over strata of an n-frame
// video. It is independent of how clips are scored, which lets the exact same
// logic be replayed on precomputed scores (see Simulate). onRound, when set,
// receives the estimate after each round.
func sample(
	ctx context.Context,
	strata []*stratum,
	n int,
	opts Options,
	score clipScorer,
	onRound func(estimate, int),
) (sampleOutcome, error) {
	picker := newSlotPicker(opts.Seed)

	// The second stage is allocated in proportion to stratum sizes (the
	// variance is pooled), so its minimum part does not depend on the pilot
	// results: it joins the first sweep, and a video is decoded once unless
	// the precision needs more.
	pilot := drawPilot(strata, picker)
	growth := firstGrowth(pilot, n, opts.ClipFrames)
	allocate(strata, int(math.Ceil(float64(pilot)*growth)), picker.next)

	var out sampleOutcome

	for {
		out.rounds++

		results, err := score(ctx, pendingClips(strata), out.rounds)
		if err != nil {
			return out, err
		}

		out.record(results)
		out.est = estimateMean(strata, opts.Confidence)

		if onRound != nil {
			onRound(out.est, out.rounds)
		}

		if out.est.halfWidth <= opts.Precision || out.rounds == maxRounds {
			return out, nil
		}

		extra, ok := out.nextStage(strata, n, opts)
		if !ok || allocate(strata, extra, picker.next) == 0 {
			return out, nil
		}
	}
}

// drawPilot samples up to pilotClips slots in every stratum and returns the
// number of clips drawn.
func drawPilot(
	strata []*stratum,
	picker *slotPicker,
) int {
	pilot := 0

	for _, s := range strata {
		for range min(pilotClips, len(s.slots)) {
			s.sampled = append(s.sampled, picker.next(s))
			pilot++
		}
	}

	return pilot
}

// firstGrowth is the size of the second stage drawn with the pilot, relative
// to it. On long videos a sweep (a full decode) costs far more than scoring a
// few more clips: the first sweep is made likely to be the only one.
func firstGrowth(
	pilot, n, clipFrames int,
) float64 {
	if pilotFrames := pilot * clipFrames; float64(pilotFrames) < sparsePilot*float64(n) {
		return sparseGrowth
	}

	return minGrowth
}

// record adds the clip means of a round to their strata.
func (o *sampleOutcome) record(
	results []clipResult,
) {
	for _, cr := range results {
		cr.clip.stratum.addClip(stats.Mean(cr.scores), len(cr.scores))
		o.scored += len(cr.scores)
	}

	o.results = append(o.results, results...)
}

// nextStage sizes the next round from the current estimate: the half-width
// shrinks like 1/√clips, so reaching the precision needs about
// (half-width / precision)² times the clips sampled so far. It returns false
// when sampling must stop, with fallback set when every frame should be
// scored instead: the variance is unknown, or the projection exceeds MaxShare
// outside budget mode. In budget mode the stage spends what is left of
// MaxShare.
func (o *sampleOutcome) nextStage(
	strata []*stratum,
	n int,
	opts Options,
) (int, bool) {
	sampled := 0
	for _, s := range strata {
		sampled += len(s.sampled)
	}

	ratio := o.est.halfWidth / opts.Precision
	factor := ratio * ratio * planMargin

	o.projected = float64(o.scored) * factor / float64(n)

	unknown := math.IsInf(factor, 1) || math.IsNaN(factor)
	if unknown || o.projected > opts.MaxShare {
		if unknown || !opts.Budget {
			o.fallback = true

			return 0, false
		}

		factor = opts.MaxShare * float64(n) / float64(o.scored)
		if factor <= 1 {
			return 0, false
		}
	}

	return int(math.Ceil(float64(sampled) * (factor - 1))), true
}
