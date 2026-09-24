package quality

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/eko/qc/media"
)

// ErrInvalidSample is returned for a malformed fixed sampling budget.
var ErrInvalidSample = errors.New("invalid sample budget")

// Sample is a fixed sampling budget: a share of the frames, or a number of
// clips in every scene, scored in a single round whatever precision it
// reaches. The zero value selects precision-driven sampling.
type Sample struct {
	// Share is the share of the frames scored, in (0, 1].
	Share float64 `json:"share,omitempty"`
	// PerScene is the number of clips scored in every scene.
	PerScene int `json:"perScene,omitempty"`
}

// Spellings of a budget (see ParseSample).
const (
	percentSuffix    = "%"
	sceneSuffix      = "/scene"
	sceneAliasSuffix = "-per-scene"
	percent          = 100
	// shareDigits are the significant digits a share is printed with.
	shareDigits = 6
)

// ParseSample reads a budget: a share of the frames ("5%", "0.5%") or a
// number of clips per scene ("2/scene", or "2-per-scene"). An empty string
// is the zero Sample.
func ParseSample(
	s string,
) (Sample, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	switch {
	case s == "":
		return Sample{}, nil
	case strings.HasSuffix(s, percentSuffix):
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, percentSuffix)), 64)
		if err != nil || !(v > 0 && v <= percent) {
			return Sample{}, fmt.Errorf("%w %q: want a share of frames in (0%%, 100%%]", ErrInvalidSample, s)
		}

		return Sample{Share: v / percent}, nil
	case strings.HasSuffix(s, sceneSuffix), strings.HasSuffix(s, sceneAliasSuffix):
		count := strings.TrimSuffix(strings.TrimSuffix(s, sceneSuffix), sceneAliasSuffix)

		v, err := strconv.Atoi(strings.TrimSpace(count))
		if err != nil || v < 1 {
			return Sample{}, fmt.Errorf("%w %q: want a positive number of clips per scene", ErrInvalidSample, s)
		}

		return Sample{PerScene: v}, nil
	}

	return Sample{}, fmt.Errorf("%w %q: want a share of frames (5%%) or clips per scene (2/scene)", ErrInvalidSample, s)
}

// IsZero reports whether no budget is set.
func (s Sample) IsZero() bool {
	return s == Sample{}
}

// Validate rejects a share outside (0, 1], a negative clip count, or both
// kinds of budget at once.
func (s Sample) Validate() error {
	switch {
	case s.Share < 0 || s.Share > 1 || math.IsNaN(s.Share):
		return fmt.Errorf("%w: share %g outside (0, 1]", ErrInvalidSample, s.Share)
	case s.PerScene < 0:
		return fmt.Errorf("%w: %d clips per scene", ErrInvalidSample, s.PerScene)
	case s.Share > 0 && s.PerScene > 0:
		return fmt.Errorf("%w: a share of frames and clips per scene are exclusive", ErrInvalidSample)
	}

	return nil
}

// String spells the budget as ParseSample reads it ("5%", "2/scene").
func (s Sample) String() string {
	switch {
	case s.PerScene > 0:
		return strconv.Itoa(s.PerScene) + sceneSuffix
	case s.Share > 0:
		return strconv.FormatFloat(s.Share*percent, 'g', shareDigits, 64) + percentSuffix
	}

	return ""
}

// Stratum boundaries of a fixed budget (SampleReport.Boundaries).
const (
	BoundariesKeyframes = "keyframes"
	BoundariesShots     = "shots+keyframes"
)

// Variance estimators of a sampled measurement (SampleReport.Variance).
const (
	VariancePooled    = "pooled"
	VarianceSeparate  = "separate"
	VarianceCollapsed = "collapsed"
)

// SampleReport describes the fixed budget a measurement spent.
type SampleReport struct {
	Sample
	// Clips is the number of clips scored.
	Clips int `json:"clips"`
	// Boundaries tells what strata follow: the keyframes of the distorted
	// stream, or those and the detected shot cuts (Options.Cuts).
	Boundaries string `json:"boundaries"`
	// Variance is the estimator of the interval: the within-stratum
	// variance pooled over strata (share budgets), each scene's own variance
	// (per-scene budgets), or collapsed strata when each holds a single clip.
	Variance string `json:"variance"`
	// Clamped explains how the budget was adapted to the video.
	Clamped string `json:"clamped,omitempty"`
}

// minBudgetClips is the smallest share budget: two strata of pilotClips,
// the fewest clips giving the pooled variance two degrees of freedom.
const minBudgetClips = 2 * pilotClips

// strataGrowth widens the strata of a share budget until the pilot fits in
// it: GOPs split evenly can make strata slightly shorter than their target.
const strataGrowth = 8

// planBudget stratifies an n-frame video for a fixed budget and draws its
// clips. Strata never straddle a keyframe of the distorted stream nor, when
// set, a shot cut of opts.Cuts. Both are kept: a shot detector misses
// gradual transitions an encoder often marks with a keyframe, and a forced
// keyframe (maximum GOP length, fixed-GOP streaming encodes) splits a long
// shot into strata closer to proportional allocation. Replays measured the
// union below the error of either alone.
//
//   - A share budget groups GOPs into strata sized so that the budget holds
//     pilotClips clips per stratum, then gives what is left in proportion to
//     stratum sizes: stratification as fine as the pooled variance allows.
//   - A per-scene budget makes one stratum per scene, never grouped, and draws
//     PerScene clips in each.
func planBudget(
	n int,
	pts, keyframes []media.Duration,
	opts Options,
) ([]*stratum, SampleReport) {
	report := SampleReport{Sample: opts.Sample, Boundaries: BoundariesKeyframes}

	bounds := keyframes
	if len(opts.Cuts) > 0 {
		bounds, report.Boundaries = append(slices.Clone(keyframes), opts.Cuts...), BoundariesShots
	}

	picker := newSlotPicker(opts.Seed)

	var strata []*stratum
	if opts.Sample.PerScene > 0 {
		strata = sceneStrata(n, pts, bounds, opts.ClipFrames)
		report.Clamped = drawPerScene(strata, opts.Sample.PerScene, picker)
	} else {
		strata, report.Clamped = drawShare(n, pts, bounds, opts, picker)
	}

	return strata, report
}

// drawShare stratifies for a share budget and draws its clips. It returns
// why the budget was clamped, if it was.
func drawShare(
	n int,
	pts, bounds []media.Duration,
	opts Options,
	picker *slotPicker,
) ([]*stratum, string) {
	requested := int(math.Ceil(opts.Sample.Share * float64(n) / float64(opts.ClipFrames)))
	budget := max(requested, minBudgetClips)

	target := int(math.Ceil(float64(n) / float64(budget/pilotClips)))
	strata := buildStrata(n, pts, bounds, target, opts.ClipFrames)

	for len(strata)*pilotClips > budget && target < n {
		target += target/strataGrowth + 1
		strata = buildStrata(n, pts, bounds, target, opts.ClipFrames)
	}

	pilot := drawPilot(strata, picker)
	allocate(strata, budget-pilot, picker.next)

	slots := 0
	for _, s := range strata {
		slots += len(s.slots)
	}

	switch {
	case budget >= slots:
		return strata, fmt.Sprintf("the budget covers the whole video: all %d clips scored", slots)
	case requested < minBudgetClips:
		return strata, fmt.Sprintf("raised to %d clips, the fewest that estimate the variance", minBudgetClips)
	}

	return strata, ""
}

// drawPerScene draws perScene clips in every stratum (every clip of a scene
// too short for them). A single clip per scene needs at least two scenes
// with a sampling error to pair them: otherwise two clips per scene are
// drawn. It returns why the budget was clamped, if it was.
func drawPerScene(
	strata []*stratum,
	perScene int,
	picker *slotPicker,
) string {
	var notes []string

	if perScene == 1 && varyingStrata(strata) < 2 {
		perScene = pilotClips
		notes = append(notes, "fewer than two scenes to pair: 2 clips per scene scored to estimate the variance")
	}

	short := 0

	for _, s := range strata {
		if len(s.slots) < perScene {
			short++
		}

		for range min(perScene, len(s.slots)) {
			s.sampled = append(s.sampled, picker.next(s))
		}
	}

	if short > 0 {
		notes = append(notes, fmt.Sprintf("%d of %d scenes hold fewer than %d clips: all their frames scored", short, len(strata), perScene))
	}

	return strings.Join(notes, "; ")
}

// varyingStrata counts the strata holding more than one clip slot: the ones
// a single clip leaves a sampling error in.
func varyingStrata(
	strata []*stratum,
) int {
	count := 0

	for _, s := range strata {
		if len(s.slots) > 1 {
			count++
		}
	}

	return count
}

// sceneStrata makes one stratum per scene: frames [0, n) are cut at every
// boundary, and a scene too short for a clip joins its predecessor (or its
// successor at the start).
func sceneStrata(
	n int,
	pts, bounds []media.Duration,
	clipFrames int,
) []*stratum {
	cuts := append([]int{0}, framesAt(pts, bounds)...)
	cuts = append(cuts, n)
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)

	var ranges [][2]int

	for i := 1; i < len(cuts); i++ {
		// pts may cover the frames the longer video has past n.
		a, b := cuts[i-1], min(cuts[i], n)
		if a >= b {
			continue
		}

		switch last := len(ranges) - 1; {
		case last >= 0 && b-a < clipFrames:
			ranges[last][1] = b
		case last >= 0 && ranges[last][1]-ranges[last][0] < clipFrames:
			ranges[last][1] = b
		default:
			ranges = append(ranges, [2]int{a, b})
		}
	}

	strata := make([]*stratum, len(ranges))
	for i, r := range ranges {
		strata[i] = newStratum(r[0], r[1], clipFrames)
	}

	return strata
}

// sampleBudget scores the clips drawn by planBudget in a single round and
// estimates the mean. There is no stopping rule and no fallback to exact
// scoring: the interval is the one the budget reaches.
func sampleBudget(
	ctx context.Context,
	strata []*stratum,
	opts Options,
	score clipScorer,
	onRound func(estimate, int),
) (sampleOutcome, error) {
	out := sampleOutcome{rounds: 1}

	results, err := score(ctx, pendingClips(strata), out.rounds)
	if err != nil {
		return out, err
	}

	out.record(results)
	out.est = budgetEstimate(strata, opts)

	if onRound != nil {
		onRound(out.est, out.rounds)
	}

	return out, nil
}

// budgetEstimate is the estimator of the design (estimatorFor), except that
// a budget scoring every clip slot is exact even when no variance could be
// estimated (a video too short for two clips): its interval is empty instead
// of unknown.
func budgetEstimate(
	strata []*stratum,
	opts Options,
) estimate {
	est := estimatorFor(opts.Sample)(strata, opts.Confidence)
	if allSampled(strata) {
		est.halfWidth = 0
	}

	return est
}

// allSampled reports whether every clip slot of every stratum is scored.
func allSampled(
	strata []*stratum,
) bool {
	for _, s := range strata {
		if len(s.clipMeans) < len(s.slots) {
			return false
		}
	}

	return true
}

// sampling plans and runs a sampled measurement of an n-frame video: the
// precision-driven loop by default, a single round with a fixed budget. It
// is the entry point shared by Measure and the replays of Simulate.
func sampling(
	ctx context.Context,
	n int,
	pts, keyframes []media.Duration,
	opts Options,
	score clipScorer,
	onRound func(estimate, int),
) ([]*stratum, sampleOutcome, error) {
	if opts.Sample.IsZero() {
		strata := buildStrata(n, pts, keyframes, stratumFrames(n, opts), opts.ClipFrames)
		out, err := sample(ctx, strata, n, opts, score, onRound)

		return strata, out, err
	}

	strata, report := planBudget(n, pts, keyframes, opts)

	out, err := sampleBudget(ctx, strata, opts, score, onRound)
	if err != nil {
		return strata, out, err
	}

	report.Clips = len(out.results)
	report.Variance = out.est.variance
	out.budget = &report

	return strata, out, nil
}

// Summary describes the budget in a few words for reports:
// "5% of frames · 199 clips", "2/scene (keyframes) · 482 clips".
func (r SampleReport) Summary() string {
	if r.PerScene > 0 {
		return fmt.Sprintf("%s (%s) · %d clips", r.Sample, r.Boundaries, r.Clips)
	}

	return fmt.Sprintf("%s of frames · %d clips", r.Sample, r.Clips)
}
