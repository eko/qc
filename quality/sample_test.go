package quality

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

// uniformStrata splits n frames into strata of size frames.
func uniformStrata(
	n, size int,
) []*stratum {
	var strata []*stratum
	for first := 0; first < n; first += size {
		strata = append(strata, newStratum(first, min(first+size, n), defaultClipFrames))
	}

	return strata
}

// scoresBy returns a scorer giving every frame of a clip the score of its
// first frame, computed by f.
func scoresBy(
	f func(frame int) float64,
) clipScorer {
	return func(_ context.Context, clips []clip, _ int) ([]clipResult, error) {
		results := make([]clipResult, len(clips))

		for i, c := range clips {
			scores := make([]float64, c.to-c.from)
			for j := range scores {
				scores[j] = f(c.from)
			}

			results[i] = clipResult{clip: c, scores: scores}
		}

		return results, nil
	}
}

// noisy scores frames with a deterministic spread of about ±spread.
func noisy(
	spread float64,
) func(int) float64 {
	return func(frame int) float64 {
		return 80 + spread*math.Sin(float64(frame)*12.9898)
	}
}

func TestSample(
	t *testing.T,
) {
	errScore := errors.New("scoring failed")

	testCases := []struct {
		name  string
		n     int
		size  int
		opts  Options
		score clipScorer
		check func(t *testing.T, out sampleOutcome, rounds []estimate)
		err   error
	}{
		{
			name:  "constant scores stop after the first round",
			n:     4000,
			size:  400,
			score: scoresBy(func(int) float64 { return 90 }),
			check: func(t *testing.T, out sampleOutcome, rounds []estimate) {
				assert.Equal(t, 1, out.rounds)
				assert.InDelta(t, 90, out.est.mean, 1e-9)
				assert.Zero(t, out.est.halfWidth)
				assert.False(t, out.fallback)
				// Pilot (2 per stratum) doubled on a sparse first pass.
				assert.Equal(t, 2*2*10*defaultClipFrames, out.scored)
				assert.Len(t, rounds, 1)
			},
		},
		{
			name:  "noisy scores need more rounds",
			n:     40000,
			size:  400,
			opts:  Options{Precision: 0.15},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, out sampleOutcome, rounds []estimate) {
				assert.Greater(t, out.rounds, 1)
				assert.LessOrEqual(t, out.rounds, maxRounds)
				assert.False(t, out.fallback)
				assert.Len(t, rounds, out.rounds)
				assert.Len(t, out.results, out.scored/defaultClipFrames)
			},
		},
		{
			name: "rounds are capped",
			n:    40000,
			size: 400,
			opts: Options{Precision: 0.15, MaxShare: 0.5, Budget: true},
			// Scores spread far more after the pilot: the second stage,
			// sized on the pilot's variance, misses the target.
			score: func(ctx context.Context, clips []clip, round int) ([]clipResult, error) {
				if round == 1 {
					return scoresBy(noisy(4))(ctx, clips, round)
				}

				return scoresBy(noisy(40))(ctx, clips, round)
			},
			check: func(t *testing.T, out sampleOutcome, _ []estimate) {
				assert.Equal(t, maxRounds, out.rounds)
				assert.Greater(t, out.est.halfWidth, 0.15)
				assert.False(t, out.fallback)
			},
		},
		{
			name:  "fallback when the precision needs too many frames",
			n:     4000,
			size:  400,
			opts:  Options{Precision: 0.01},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, out sampleOutcome, _ []estimate) {
				assert.True(t, out.fallback)
				assert.Greater(t, out.projected, defaultMaxShare)
			},
		},
		{
			name:  "fallback when the variance is unknown, even in budget mode",
			n:     6,
			size:  6,
			opts:  Options{Budget: true},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, out sampleOutcome, rounds []estimate) {
				assert.True(t, out.fallback)
				assert.True(t, math.IsInf(out.projected, 1))
				assert.True(t, math.IsInf(rounds[0].halfWidth, 1))
			},
		},
		{
			name:  "budget mode spends MaxShare",
			n:     4000,
			size:  400,
			opts:  Options{Precision: 0.01, Budget: true, MaxShare: 0.3},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, out sampleOutcome, _ []estimate) {
				assert.False(t, out.fallback)
				assert.Greater(t, out.rounds, 1)
				assert.InDelta(t, 0.3, float64(out.scored)/4000, 0.02)
			},
		},
		{
			name:  "budget already spent",
			n:     4000,
			size:  400,
			opts:  Options{Precision: 0.01, Budget: true, MaxShare: 0.03},
			score: scoresBy(noisy(4)),
			check: func(t *testing.T, out sampleOutcome, _ []estimate) {
				assert.False(t, out.fallback)
				assert.Equal(t, 1, out.rounds)
			},
		},
		{
			name: "stops when no slot is left",
			n:    40,
			size: 8,
			opts: Options{MaxShare: 1000},
			// Dropping results leaves sampled slots without a clip mean: the
			// estimate keeps a sampling error while every slot is taken.
			score: func(ctx context.Context, clips []clip, round int) ([]clipResult, error) {
				results, err := scoresBy(noisy(4))(ctx, clips, round)
				if round > 1 {
					return nil, err
				}

				return results[:len(results)/2], err
			},
			check: func(t *testing.T, out sampleOutcome, _ []estimate) {
				assert.False(t, out.fallback)
				assert.Equal(t, 1, out.rounds)
				assert.Greater(t, out.est.halfWidth, defaultPrecision)
			},
		},
		{
			name: "scoring error",
			n:    4000,
			size: 400,
			score: func(context.Context, []clip, int) ([]clipResult, error) {
				return nil, errScore
			},
			err: errScore,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var rounds []estimate

			strata := uniformStrata(testCase.n, testCase.size)
			out, err := sample(t.Context(), strata, testCase.n, testCase.opts.withDefaults(), testCase.score, func(est estimate, round int) {
				rounds = append(rounds, est)
				assert.Len(t, rounds, round)
			})

			if testCase.err != nil {
				require.ErrorIs(t, err, testCase.err)

				return
			}

			require.NoError(t, err)
			testCase.check(t, out, rounds)
		})
	}
}

func TestFirstGrowth(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		pilot int
		n     int
		want  float64
	}{
		{name: "pilot covers 10% or more", pilot: 25, n: 1000, want: minGrowth},
		{name: "sparse pilot", pilot: 24, n: 1000, want: sparseGrowth},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, firstGrowth(testCase.pilot, testCase.n, 4), 0)
		})
	}
}

func TestDrawPilot(
	t *testing.T,
) {
	strata := []*stratum{newStratum(0, 4, 4), newStratum(4, 44, 4)}

	assert.Equal(t, 3, drawPilot(strata, newSlotPicker(1)))
	assert.Len(t, strata[0].sampled, 1, "a single slot")
	assert.Len(t, strata[1].sampled, pilotClips)
	assert.NotEqual(t, strata[1].sampled[0], strata[1].sampled[1], "without replacement")
}

func TestSlotPicker(
	t *testing.T,
) {
	draw := func(seed uint64) []int {
		s := newStratum(0, 400, 4)
		picker := newSlotPicker(seed)

		for range len(s.slots) {
			s.sampled = append(s.sampled, picker.next(s))
		}

		return s.sampled
	}

	testCases := []struct {
		name string
		seed uint64
		same bool
	}{
		{name: "same seed, same order", seed: 1, same: true},
		{name: "other seed, other order", seed: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			order := draw(testCase.seed)
			assert.ElementsMatch(t, draw(1), order, "a permutation of the slots")
			assert.Equal(t, testCase.same, assert.ObjectsAreEqual(draw(1), order))
		})
	}
}

func TestAllocate(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		strata    []*stratum
		batch     int
		wantAdded int
		check     func(t *testing.T, strata []*stratum)
	}{
		{
			name:      "larger strata get more clips",
			strata:    []*stratum{newStratum(0, 400, 4), newStratum(400, 440, 4)},
			batch:     20,
			wantAdded: 20,
			check: func(t *testing.T, strata []*stratum) {
				assert.Greater(t, len(strata[0].sampled), 5*len(strata[1].sampled))
			},
		},
		{
			name:      "full strata are skipped",
			strata:    []*stratum{newStratum(0, 8, 4), newStratum(8, 16, 4)},
			batch:     10,
			wantAdded: 2,
			check: func(t *testing.T, strata []*stratum) {
				for _, s := range strata {
					assert.Len(t, s.sampled, len(s.slots))
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			picker := newSlotPicker(1)
			for _, s := range testCase.strata {
				s.sampled = append(s.sampled, picker.next(s))
			}

			assert.Equal(t, testCase.wantAdded, allocate(testCase.strata, testCase.batch, picker.next))
			testCase.check(t, testCase.strata)
		})
	}
}

func TestPendingClips(
	t *testing.T,
) {
	s := newStratum(0, 16, 4)
	s.sampled = []int{2, 0, 3}
	withClips(s, 80)

	assert.Equal(t, []clip{{stratum: s, from: 0, to: 4}, {stratum: s, from: 12, to: 16}}, pendingClips([]*stratum{s}))
}

func TestStratumFrames(
	t *testing.T,
) {
	testCases := []struct {
		name string
		n    int
		want int
	}{
		{name: "short video", n: 30, want: 1},
		{name: "one clip per stratum of 60", n: 6000, want: 100},
		{name: "rounded up", n: 6001, want: 101},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, stratumFrames(testCase.n, Options{}.withDefaults()))
		})
	}
}

func TestFramesAt(
	t *testing.T,
) {
	pts := []media.Duration{0, 40, 80, 120}

	testCases := []struct {
		name  string
		times []media.Duration
		want  []int
	}{
		{name: "exact times", times: []media.Duration{0, 80}, want: []int{0, 2}},
		{name: "between frames", times: []media.Duration{50}, want: []int{2}},
		{name: "after the last frame", times: []media.Duration{121}, want: []int{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, framesAt(pts, testCase.times))
		})
	}
}

func TestSimulateEdgeCases(
	t *testing.T,
) {
	scores := []float64{80, 81, 82, 83, 84, 85, 86, 87}
	pts := make([]media.Duration, len(scores))

	testCases := []struct {
		name   string
		scores []float64
		runs   int
		check  func(t *testing.T, sim Simulation)
	}{
		{
			name: "no run", scores: scores, runs: 0,
			check: func(t *testing.T, sim Simulation) {
				assert.Zero(t, sim.Coverage)
				assert.InDelta(t, 83.5, sim.True, 1e-9)
			},
		},
		{
			name: "no score", runs: 3,
			check: func(t *testing.T, sim Simulation) {
				assert.Zero(t, sim.Coverage)
			},
		},
		{
			name: "every run falls back", scores: scores[:6], runs: 3,
			check: func(t *testing.T, sim Simulation) {
				assert.Equal(t, 3, sim.Fallbacks)
				assert.InDelta(t, 1, sim.Coverage, 0)
				assert.Zero(t, sim.SampledCoverage)
				assert.InDelta(t, 1, sim.MeanShare, 0)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			sim := Simulate(testCase.scores, pts[:len(testCase.scores)], nil, Options{}, testCase.runs)
			testCase.check(t, sim)
		})
	}
}
