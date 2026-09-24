package quality

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestParseSample(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    Sample
		wantErr bool
	}{
		{name: "empty", input: " "},
		{name: "share", input: "5%", want: Sample{Share: 0.05}},
		{name: "fractional share with spaces", input: " 0.5 % ", want: Sample{Share: 0.005}},
		{name: "every frame", input: "100%", want: Sample{Share: 1}},
		{name: "clips per scene", input: "2/scene", want: Sample{PerScene: 2}},
		{name: "clips per scene, long form", input: "1-Per-Scene", want: Sample{PerScene: 1}},
		{name: "share above 100%", input: "150%", wantErr: true},
		{name: "zero share", input: "0%", wantErr: true},
		{name: "share not a number", input: "abc%", wantErr: true},
		{name: "zero clips per scene", input: "0/scene", wantErr: true},
		{name: "fractional clips per scene", input: "1.5/scene", wantErr: true},
		{name: "bare number", input: "5", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseSample(testCase.input)

			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidSample)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestSampleValidate(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		sample  Sample
		wantErr bool
	}{
		{name: "zero"},
		{name: "share", sample: Sample{Share: 1}},
		{name: "clips per scene", sample: Sample{PerScene: 3}},
		{name: "share above 1", sample: Sample{Share: 1.5}, wantErr: true},
		{name: "negative share", sample: Sample{Share: -0.1}, wantErr: true},
		{name: "NaN share", sample: Sample{Share: math.NaN()}, wantErr: true},
		{name: "negative clips", sample: Sample{PerScene: -1}, wantErr: true},
		{name: "both budgets", sample: Sample{Share: 0.1, PerScene: 1}, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.sample.Validate()

			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidSample)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestSampleString(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		sample Sample
		want   string
	}{
		{name: "zero", want: ""},
		{name: "share", sample: Sample{Share: 0.05}, want: "5%"},
		{name: "fractional share", sample: Sample{Share: 0.025}, want: "2.5%"},
		{name: "clips per scene", sample: Sample{PerScene: 2}, want: "2/scene"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.sample.String())

			if testCase.want != "" {
				parsed, err := ParseSample(testCase.want)
				require.NoError(t, err)
				assert.InDelta(t, testCase.sample.Share, parsed.Share, 1e-12, "String round-trips")
			}
		})
	}
}

func TestSampleReportSummary(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		report SampleReport
		want   string
	}{
		{name: "share", report: SampleReport{Sample: Sample{Share: 0.05}, Clips: 199}, want: "5% of frames · 199 clips"},
		{
			name:   "clips per scene",
			report: SampleReport{Sample: Sample{PerScene: 2}, Clips: 482, Boundaries: BoundariesKeyframes},
			want:   "2/scene (keyframes) · 482 clips",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.report.Summary())
		})
	}
}

// framesPTS returns the timestamps of n frames at 25 fps.
func framesPTS(
	n int,
) []media.Duration {
	pts := make([]media.Duration, n)
	for i := range pts {
		pts[i] = media.Seconds(float64(i) / 25)
	}

	return pts
}

// ranges returns the frame ranges of strata.
func ranges(
	strata []*stratum,
) [][2]int {
	out := make([][2]int, len(strata))
	for i, s := range strata {
		out[i] = [2]int{s.first, s.last}
	}

	return out
}

func TestSceneStrata(
	t *testing.T,
) {
	pts := framesPTS(100)

	testCases := []struct {
		name   string
		bounds []media.Duration
		want   [][2]int
	}{
		{name: "no boundary: one scene", want: [][2]int{{0, 100}}},
		{
			name:   "one stratum per scene, never grouped, unsorted and duplicated boundaries",
			bounds: []media.Duration{pts[60], pts[20], pts[20], pts[0]},
			want:   [][2]int{{0, 20}, {20, 60}, {60, 100}},
		},
		{
			name:   "a scene shorter than a clip joins its predecessor",
			bounds: []media.Duration{pts[50], pts[52]},
			want:   [][2]int{{0, 52}, {52, 100}},
		},
		{
			name:   "a first scene shorter than a clip joins its successor",
			bounds: []media.Duration{pts[2], pts[50]},
			want:   [][2]int{{0, 50}, {50, 100}},
		},
		{
			name:   "boundaries past the end are ignored",
			bounds: []media.Duration{pts[50], media.Seconds(60)},
			want:   [][2]int{{0, 50}, {50, 100}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, ranges(sceneStrata(100, pts, testCase.bounds, 4)))
		})
	}

	t.Run("boundaries in frames of the longer video past n", func(t *testing.T) {
		longer := framesPTS(120)
		got := sceneStrata(100, longer, []media.Duration{longer[50], longer[110]}, 4)

		assert.Equal(t, [][2]int{{0, 50}, {50, 100}}, ranges(got))
	})
}

// sampledClips counts the drawn clips of strata.
func sampledClips(
	strata []*stratum,
) int {
	clips := 0
	for _, s := range strata {
		clips += len(s.sampled)
	}

	return clips
}

func TestPlanBudget(
	t *testing.T,
) {
	const n = 4000

	pts := framesPTS(n)

	var keyframes []media.Duration
	for i := 0; i < n; i += 200 {
		keyframes = append(keyframes, pts[i])
	}

	testCases := []struct {
		name  string
		n     int
		keys  []media.Duration
		opts  Options
		check func(t *testing.T, strata []*stratum, report SampleReport)
	}{
		{
			name: "share: two clips per stratum, then proportional",
			n:    n,
			keys: keyframes,
			opts: Options{Sample: Sample{Share: 0.1}},
			check: func(t *testing.T, strata []*stratum, report SampleReport) {
				assert.Equal(t, 100, sampledClips(strata), "10% of 4000 frames in clips of 4")
				assert.LessOrEqual(t, 2*len(strata), 100)
				assert.Equal(t, BoundariesKeyframes, report.Boundaries)
				assert.Empty(t, report.Clamped)

				for _, s := range strata {
					assert.GreaterOrEqual(t, len(s.sampled), pilotClips)
				}
			},
		},
		{
			name: "share: raised to the fewest clips estimating a variance",
			n:    n,
			keys: keyframes,
			opts: Options{Sample: Sample{Share: 0.0001}},
			check: func(t *testing.T, strata []*stratum, report SampleReport) {
				assert.Equal(t, minBudgetClips, sampledClips(strata))
				assert.Len(t, strata, 2)
				assert.Contains(t, report.Clamped, "raised to 4 clips")
			},
		},
		{
			name: "share: the whole video",
			n:    n,
			keys: keyframes,
			opts: Options{Sample: Sample{Share: 1}},
			check: func(t *testing.T, strata []*stratum, report SampleReport) {
				assert.Equal(t, n/defaultClipFrames, sampledClips(strata))
				assert.True(t, allScheduled(strata))
				assert.Equal(t, "the budget covers the whole video: all 1000 clips scored", report.Clamped)
			},
		},
		{
			name: "share: long GOPs split evenly still fit the budget",
			n:    n,
			opts: Options{Sample: Sample{Share: 0.03}},
			check: func(t *testing.T, strata []*stratum, _ SampleReport) {
				assert.Equal(t, 30, sampledClips(strata))
			},
		},
		{
			name: "per scene: shot cuts and keyframes cut the scenes",
			n:    n,
			keys: keyframes,
			opts: Options{Sample: Sample{PerScene: 3}, Cuts: []media.Duration{pts[100], pts[3000]}},
			check: func(t *testing.T, strata []*stratum, report SampleReport) {
				assert.Len(t, strata, 21, "20 GOPs, one split by a cut")
				assert.Equal(t, 3*21, sampledClips(strata))
				assert.Equal(t, BoundariesShots, report.Boundaries)
				assert.Empty(t, report.Clamped)
			},
		},
		{
			name: "per scene: short scenes give all their clips",
			n:    n,
			keys: []media.Duration{pts[8], pts[2000]},
			opts: Options{Sample: Sample{PerScene: 3}},
			check: func(t *testing.T, strata []*stratum, report SampleReport) {
				assert.Equal(t, 2+3+3, sampledClips(strata))
				assert.Equal(t, "1 of 3 scenes hold fewer than 3 clips: all their frames scored", report.Clamped)
			},
		},
		{
			name: "per scene: one clip in a single scene cannot estimate a variance",
			n:    n,
			opts: Options{Sample: Sample{PerScene: 1}},
			check: func(t *testing.T, strata []*stratum, report SampleReport) {
				require.Len(t, strata, 1)
				assert.Len(t, strata[0].sampled, 2)
				assert.Contains(t, report.Clamped, "2 clips per scene scored")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			strata, report := planBudget(testCase.n, framesPTS(testCase.n), testCase.keys, testCase.opts.withDefaults())

			assert.Equal(t, testCase.opts.Sample, report.Sample)
			testCase.check(t, strata, report)
		})
	}
}

// allScheduled reports whether every slot of strata is drawn.
func allScheduled(
	strata []*stratum,
) bool {
	for _, s := range strata {
		if len(s.sampled) != len(s.slots) {
			return false
		}
	}

	return true
}

func TestPlanBudgetReproducible(
	t *testing.T,
) {
	pts := framesPTS(2000)
	opts := Options{Sample: Sample{Share: 0.05}, Seed: 3}.withDefaults()

	first, _ := planBudget(2000, pts, nil, opts)
	second, _ := planBudget(2000, pts, nil, opts)

	for i := range first {
		assert.Equal(t, first[i].sampled, second[i].sampled)
	}
}

func TestCollapsedVariance(
	t *testing.T,
) {
	single := func(first, last int, mean float64) *stratum {
		s := newStratum(first, last, 4)
		withClips(s, mean)

		return s
	}

	exhausted := newStratum(0, 4, 4)
	withClips(exhausted, 10)

	testCases := []struct {
		name   string
		strata []*stratum
		want   float64
		wantDF int
	}{
		{
			// 4 (W₁W₂/(W₁+W₂))² (ȳ₁ − ȳ₂)² (1 − 1/10) with W = 1/2.
			name:   "pair",
			strata: []*stratum{single(0, 40, 80), single(40, 80, 90)},
			want:   22.5,
			wantDF: 1,
		},
		{
			// 3/2 · (1 − 1/10) · (1/3)² · (10² + 0 + 10²).
			name:   "an odd count ends with a triple",
			strata: []*stratum{single(0, 40, 80), single(40, 80, 90), single(80, 120, 100)},
			want:   30,
			wantDF: 2,
		},
		{
			name:   "fully sampled strata are left out",
			strata: []*stratum{exhausted, single(4, 44, 80), single(44, 84, 90)},
			// Two strata of W = 40/84: W² (ȳ₁ − ȳ₂)² (1 − 1/10).
			want:   (40.0 / 84) * (40.0 / 84) * 100 * 0.9,
			wantDF: 1,
		},
		{
			name:   "a single stratum cannot be paired",
			strata: []*stratum{single(0, 40, 80)},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, df := collapsedVariance(testCase.strata, totalFrames(testCase.strata))

			assert.InDelta(t, testCase.want, got, 1e-9)
			assert.Equal(t, testCase.wantDF, df)
		})
	}
}

func TestEstimateSeparate(
	t *testing.T,
) {
	twoClips := func() []*stratum {
		a, b := newStratum(0, 40, 4), newStratum(40, 80, 4)
		withClips(a, 79, 81)
		withClips(b, 89, 91)

		return []*stratum{a, b}
	}

	oneClip := func() []*stratum {
		a, b := newStratum(0, 40, 4), newStratum(40, 80, 4)
		withClips(a, 80)
		withClips(b, 90)

		return []*stratum{a, b}
	}

	constant := twoClips()
	for _, s := range constant {
		s.clipMeans = []float64{85, 85}
	}

	testCases := []struct {
		name         string
		strata       []*stratum
		wantMean     float64
		wantHalf     float64
		wantDF       float64
		wantVariance string
	}{
		{
			// Each stratum: W² (1 − 2/10) s² / 2 = 0.2; Satterthwaite df 2.
			name:         "own variance per stratum",
			strata:       twoClips(),
			wantMean:     85,
			wantHalf:     tQuantile(0.975, 2) * math.Sqrt(0.4),
			wantDF:       2,
			wantVariance: VarianceSeparate,
		},
		{
			name:         "one clip per stratum: collapsed strata",
			strata:       oneClip(),
			wantMean:     85,
			wantHalf:     tQuantile(0.975, 1) * math.Sqrt(22.5),
			wantDF:       1,
			wantVariance: VarianceCollapsed,
		},
		{
			name:         "no spread: no sampling error",
			strata:       constant,
			wantMean:     85,
			wantVariance: VarianceSeparate,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			est := estimateSeparate(testCase.strata, 0.95)

			assert.InDelta(t, testCase.wantMean, est.mean, 1e-9)
			assert.InDelta(t, testCase.wantHalf, est.halfWidth, 1e-9)
			assert.InDelta(t, testCase.wantDF, est.df, 1e-9)
			assert.Equal(t, testCase.wantVariance, est.variance)
		})
	}
}

func TestEstimatorFor(
	t *testing.T,
) {
	strata := func() []*stratum {
		a, b := newStratum(0, 40, 4), newStratum(40, 120, 4)
		withClips(a, 79, 81)
		withClips(b, 80, 90)

		return []*stratum{a, b}
	}

	testCases := []struct {
		name   string
		sample Sample
		want   string
	}{
		{name: "precision loop: pooled", want: VariancePooled},
		{name: "share: pooled", sample: Sample{Share: 0.05}, want: VariancePooled},
		{name: "per scene: separate", sample: Sample{PerScene: 2}, want: VarianceSeparate},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, estimatorFor(testCase.sample)(strata(), 0.95).variance)
		})
	}
}

func TestEstimateCollapsedUnknown(
	t *testing.T,
) {
	a := newStratum(0, 40, 4)
	withClips(a, 80)

	assert.True(t, math.IsInf(estimateMean([]*stratum{a}, 0.95).halfWidth, 1))
}

func TestSampling(
	t *testing.T,
) {
	pts := framesPTS(4000)
	errScore := errors.New("scoring failed")

	testCases := []struct {
		name  string
		opts  Options
		score clipScorer
		check func(t *testing.T, strata []*stratum, out sampleOutcome, rounds []estimate)
		err   error
	}{
		{
			name:  "fixed budget: one round, no fallback",
			opts:  Options{Sample: Sample{Share: 0.05}, Precision: 1e-9},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, strata []*stratum, out sampleOutcome, rounds []estimate) {
				assert.Equal(t, 1, out.rounds)
				assert.Len(t, rounds, 1)
				assert.False(t, out.fallback)
				assert.Equal(t, 200, out.scored, "5% of 4000 frames")
				require.NotNil(t, out.budget)
				assert.Equal(t, 50, out.budget.Clips)
				assert.Equal(t, VariancePooled, out.budget.Variance)
				assert.Greater(t, out.est.halfWidth, 1e-9, "the interval is the one reached")
				assert.Equal(t, sampledClips(strata), out.budget.Clips)
			},
		},
		{
			name:  "one clip per scene: collapsed strata",
			opts:  Options{Sample: Sample{PerScene: 1}},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, strata []*stratum, out sampleOutcome, _ []estimate) {
				require.NotNil(t, out.budget)
				assert.Equal(t, VarianceCollapsed, out.budget.Variance)
				assert.Equal(t, len(strata), out.budget.Clips)
				assert.False(t, math.IsInf(out.est.halfWidth, 1))
			},
		},
		{
			name:  "precision-driven: no budget",
			opts:  Options{Precision: 5},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, _ []*stratum, out sampleOutcome, _ []estimate) {
				assert.Nil(t, out.budget)
			},
		},
		{
			name: "scoring error",
			opts: Options{Sample: Sample{PerScene: 2}},
			score: func(context.Context, []clip, int) ([]clipResult, error) {
				return nil, errScore
			},
			err: errScore,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var rounds []estimate

			var keys []media.Duration
			for i := 0; i < 4000; i += 100 {
				keys = append(keys, pts[i])
			}

			strata, out, err := sampling(t.Context(), 4000, pts, keys, testCase.opts.withDefaults(), testCase.score,
				func(est estimate, _ int) { rounds = append(rounds, est) })

			if testCase.err != nil {
				require.ErrorIs(t, err, testCase.err)

				return
			}

			require.NoError(t, err)
			testCase.check(t, strata, out, rounds)
		})
	}
}

// TestSimulateBudgetCoverage replays the fixed budgets on synthetic
// shot-structured scores: the pooled and separate intervals must cover the
// truth about 95% of the time, the collapsed ones at least as often.
func TestSimulateBudgetCoverage(
	t *testing.T,
) {
	rng := rand.New(rand.NewPCG(11, 11))

	const n = 6000

	scores := make([]float64, n)
	pts := framesPTS(n)

	var keys []media.Duration

	level := 85.0

	for i := range scores {
		if i%(40+rng.IntN(60)) == 0 {
			keys = append(keys, pts[i])
			level = 70 + 25*rng.Float64()
		}

		scores[i] = level + math.Sin(float64(i)/15) + 0.7*rng.NormFloat64()
	}

	testCases := []struct {
		name         string
		sample       Sample
		lo, hi       float64
		wantMaxShare float64
	}{
		{name: "5% of the frames", sample: Sample{Share: 0.05}, lo: 0.915, hi: 0.985, wantMaxShare: 0.051},
		{name: "2 clips per scene", sample: Sample{PerScene: 2}, lo: 0.915, hi: 0.985, wantMaxShare: 0.2},
		{name: "1 clip per scene", sample: Sample{PerScene: 1}, lo: 0.93, hi: 1, wantMaxShare: 0.1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sim := Simulate(scores, pts, keys, Options{Sample: testCase.sample}, 300)
			t.Logf("%+v", sim)

			assert.Zero(t, sim.Fallbacks, "a budget never falls back")
			assert.GreaterOrEqual(t, sim.Coverage, testCase.lo)
			assert.LessOrEqual(t, sim.Coverage, testCase.hi)
			assert.LessOrEqual(t, sim.MeanShare, testCase.wantMaxShare)
			assert.Positive(t, sim.MeanAbsErr)
			assert.LessOrEqual(t, sim.MeanAbsErr, sim.RMSE)
		})
	}
}

func TestMeasureBudget(
	t *testing.T,
) {
	// 4 s with a keyframe every second: four scenes of 25 frames.
	ref, dist := clips(t, testutil.Clip{Seconds: 4, GOP: 25})
	n := ref.Bitstream.PacketCount

	exact, err := measureWithin(t, decodedMeter(), ref, dist, Options{Model: testModel, Exact: true})
	require.NoError(t, err)

	testCases := []struct {
		name  string
		opts  Options
		check func(t *testing.T, res *Result)
	}{
		{
			name: "share of the frames",
			opts: Options{Model: testModel, Sample: Sample{Share: 0.2}, Metrics: []string{MetricPSNR}},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, ModeSampled, res.Mode)
				assert.Equal(t, 1, res.Rounds)
				require.NotNil(t, res.Sample)
				assert.Equal(t, 20, res.FramesScored, "20% of 100 frames")
				assert.Equal(t, VariancePooled, res.Sample.Variance)
				assert.InDelta(t, exact.Mean, res.Mean, res.HalfWidth)
				require.NotEmpty(t, res.Metrics)
				assert.Positive(t, res.Metrics[0].HalfWidth, "metrics get the interval of the same clips")
			},
		},
		{
			name: "one clip per scene, with shot cuts",
			opts: Options{Model: testModel, Sample: Sample{PerScene: 1}, Cuts: []media.Duration{media.Seconds(2.4)}},
			check: func(t *testing.T, res *Result) {
				require.NotNil(t, res.Sample)
				assert.Equal(t, BoundariesShots, res.Sample.Boundaries)
				assert.Equal(t, VarianceCollapsed, res.Sample.Variance)
				assert.Len(t, res.Strata, 5)
				assert.Equal(t, 5, res.Sample.Clips)
				assert.Positive(t, res.HalfWidth)

				data, err := json.Marshal(res)
				require.NoError(t, err, "a budget result is valid JSON")
				assert.Contains(t, string(data), `"sample":{"perScene":1,"clips":5`)
			},
		},
		{
			name: "the whole video",
			opts: Options{Model: testModel, Sample: Sample{Share: 1}, Metrics: []string{MetricPSNR}},
			check: func(t *testing.T, res *Result) {
				require.NotNil(t, res.Sample)
				assert.Contains(t, res.Sample.Clamped, "the budget covers the whole video")
				assert.Equal(t, n, res.FramesScored)
				assert.Zero(t, res.HalfWidth)
				assert.InDelta(t, exact.Mean, res.Mean, 1e-9)
				require.NotEmpty(t, res.Metrics)
				assert.Zero(t, res.Metrics[0].HalfWidth)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var budgets []Sample

			opts := testCase.opts
			opts.Progress = func(p Progress) { budgets = append(budgets, p.Sample) }

			res, err := measureWithin(t, decodedMeter(), ref, dist, opts)
			require.NoError(t, err)

			assert.Equal(t, n, res.FramesTotal)
			assert.Empty(t, res.Fallback)
			assert.Equal(t, testCase.opts.Sample, res.Sample.Sample)
			require.NotEmpty(t, budgets)
			assert.Equal(t, testCase.opts.Sample, budgets[len(budgets)-1], "progress carries the budget")
			testCase.check(t, res)
		})
	}
}

// TestMeasureBudgetTinyClip covers a video too short for two clips: a budget
// scores its only clip and reports an exact interval instead of falling back.
func TestMeasureBudgetTinyClip(
	t *testing.T,
) {
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.28}))

	res, err := measureWithin(t, decodedMeter(), ref, ref, Options{Model: testModel, Sample: Sample{PerScene: 1}, Metrics: []string{MetricPSNR}})
	require.NoError(t, err)

	assert.Equal(t, ModeSampled, res.Mode)
	assert.Equal(t, ref.Bitstream.PacketCount, res.FramesScored)
	assert.Zero(t, res.HalfWidth)

	_, err = json.Marshal(res)
	require.NoError(t, err)
}

func TestMeasureInvalidSample(
	t *testing.T,
) {
	_, err := NewMeter(nil, nil).Measure(t.Context(), Input{}, Input{}, Options{Sample: Sample{Share: 2}})

	require.ErrorIs(t, err, ErrInvalidSample)
}
