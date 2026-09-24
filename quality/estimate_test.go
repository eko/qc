package quality

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

func TestTQuantile(
	t *testing.T,
) {
	testCases := []struct {
		name string
		df   float64
		want float64
	}{
		{name: "df 1", df: 1, want: 12.7062},
		{name: "df 2", df: 2, want: 4.3027},
		{name: "df 5", df: 5, want: 2.5706},
		{name: "df 10", df: 10, want: 2.2281},
		{name: "df 30", df: 30, want: 2.0423},
		{name: "normal", df: math.Inf(1), want: 1.9600},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, tQuantile(0.975, testCase.df), 5e-3)
		})
	}
}

func TestEstimateMean(
	t *testing.T,
) {
	// Two equal strata of 10 slots; stratum means 80 and 90.
	a := newStratum(0, 40, 4)
	b := newStratum(40, 80, 4)
	withClips(a, 79, 81)
	withClips(b, 89, 91)

	est := estimateMean([]*stratum{a, b}, 0.95)

	assert.InDelta(t, 85.0, est.mean, 1e-9)
	assert.Greater(t, est.halfWidth, 0.0)

	// Scoring every slot of every stratum leaves no sampling error.
	for _, s := range []*stratum{a, b} {
		s.clipMeans, s.clipFrames = nil, nil
		for _, slot := range s.slots {
			s.addClip(85, slot[1]-slot[0])
		}
	}

	assert.Zero(t, estimateMean([]*stratum{a, b}, 0.95).halfWidth)
}

func TestEstimateMeanWithoutVariance(
	t *testing.T,
) {
	a := newStratum(0, 40, 4)
	withClips(a, 80)

	assert.True(t, math.IsInf(estimateMean([]*stratum{a}, 0.95).halfWidth, 1))
}

func TestBuildStrata(
	t *testing.T,
) {
	pts := make([]media.Duration, 300)
	for i := range pts {
		pts[i] = media.Seconds(float64(i) / 25)
	}

	keys := []media.Duration{0, pts[40], pts[45], pts[120], pts[300-1]}

	testCases := []struct {
		name   string
		target int
		check  func(t *testing.T, strata []*stratum)
	}{
		{
			name:   "GOP boundaries, tiny GOPs merged",
			target: 30,
			check: func(t *testing.T, strata []*stratum) {
				require.NotEmpty(t, strata)
				assert.Equal(t, 45, strata[0].last, "GOP 40-45 is too short to stand alone")
				assert.Equal(t, 45, strata[1].first)
			},
		},
		{
			name:   "long GOPs are split, short ones grouped",
			target: 60,
			check: func(t *testing.T, strata []*stratum) {
				for _, s := range strata {
					assert.GreaterOrEqual(t, s.frames(), 8)
					assert.LessOrEqual(t, s.frames(), 2*60)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			strata := buildStrata(300, pts, keys, testCase.target, 4)

			covered := 0
			for i, s := range strata {
				if i > 0 {
					assert.Equal(t, strata[i-1].last, s.first, "strata must tile the video")
				}

				covered += s.frames()
				assert.Equal(t, s.first, s.slots[0][0])
				assert.Equal(t, s.last, s.slots[len(s.slots)-1][1])
			}

			assert.Equal(t, 300, covered)
			testCase.check(t, strata)
		})
	}
}

// TestBuildStrataPastEnd builds strata over fewer frames than the timestamps
// cover (the other video is shorter): later keyframes are ignored.
func TestBuildStrataPastEnd(
	t *testing.T,
) {
	pts := make([]media.Duration, 100)
	for i := range pts {
		pts[i] = media.Seconds(float64(i) / 25)
	}

	testCases := []struct {
		name string
		keys []media.Duration
		want [][2]int
	}{
		{name: "keyframes after the end", keys: []media.Duration{0, pts[20], pts[60], pts[80]}, want: [][2]int{{0, 20}, {20, 40}}},
		{name: "no keyframe", want: [][2]int{{0, 20}, {20, 40}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got [][2]int
			for _, s := range buildStrata(40, pts, testCase.keys, 20, 4) {
				got = append(got, [2]int{s.first, s.last})
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestNormalQuantile(
	t *testing.T,
) {
	testCases := []struct {
		name string
		p    float64
		want float64
	}{
		{name: "median", p: 0.5, want: 0},
		{name: "central region", p: 0.975, want: 1.959964},
		{name: "lower tail", p: 0.001, want: -3.090232},
		{name: "upper tail", p: 0.999, want: 3.090232},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, normalQuantile(testCase.p), 1e-6)
		})
	}
}

func TestPooledVariance(
	t *testing.T,
) {
	a := newStratum(0, 40, 4)
	b := newStratum(40, 80, 4)
	single := newStratum(80, 120, 4)
	withClips(a, 1, 3)
	withClips(b, 10, 12, 14)
	withClips(single, 50)

	testCases := []struct {
		name     string
		strata   []*stratum
		want     float64
		wantDF   int
		wantMean float64
	}{
		{name: "pooled over strata", strata: []*stratum{a, b, single}, want: (2.0 + 2*4.0) / 3, wantDF: 3},
		{name: "no stratum with two clips", strata: []*stratum{single}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, df := pooledVariance(testCase.strata)
			assert.InDelta(t, testCase.want, got, 1e-12)
			assert.Equal(t, testCase.wantDF, df)
		})
	}
}

// TestSimulateCoverage checks the end-to-end sampling loop on synthetic
// shot-structured scores: the 95% intervals must contain the truth about 95%
// of the time.
func TestSimulateCoverage(
	t *testing.T,
) {
	rng := rand.New(rand.NewPCG(7, 7))

	const n = 6000

	scores := make([]float64, n)
	pts := make([]media.Duration, n)

	var keys []media.Duration

	level := 85.0

	for i := range scores {
		pts[i] = media.Seconds(float64(i) / 25)

		if i%(40+rng.IntN(60)) == 0 {
			keys = append(keys, pts[i])
			level = 70 + 25*rng.Float64()
		}

		scores[i] = level + math.Sin(float64(i)/15) + 0.7*rng.NormFloat64()
	}

	sim := Simulate(scores, pts, keys, Options{Precision: 0.5}, 400)
	t.Logf("%+v", sim)

	assert.LessOrEqual(t, sim.Fallbacks, 400/50, "fallbacks to exact scoring stay rare")
	assert.InDelta(t, 0.95, sim.SampledCoverage, 0.035)
	assert.Less(t, sim.MeanShare, 0.25)
	assert.LessOrEqual(t, sim.MeanHalf, 0.5)
}

// withClips records clips of 4 frames with the given means.
func withClips(
	s *stratum,
	means ...float64,
) {
	for _, m := range means {
		s.addClip(m, 4)
	}
}

// TestStratumMeanWeightsClipLength checks that a fully sampled stratum whose
// last slot is longer gives exactly its frame mean.
func TestStratumMeanWeightsClipLength(
	t *testing.T,
) {
	s := newStratum(0, 11, 4) // slots [0,4) and [4,11)
	s.addClip(10, 4)
	s.addClip(20, 7)

	assert.InDelta(t, (10*4+20*7)/11.0, s.mean(), 1e-12)
	assert.Zero(t, estimateMean([]*stratum{s}, 0.95).halfWidth, "every slot scored: no sampling error")
	assert.InDelta(t, (10*4+20*7)/11.0, estimateMean([]*stratum{s}, 0.95).mean, 1e-12)
}
