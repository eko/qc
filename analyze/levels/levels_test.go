package levels

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

func TestAnalyzer(
	t *testing.T,
) {
	pool := frame.NewPool(4, 1, frame.PoolOptions{})
	f := pool.Get()
	copy(f.Luma.Pix, []byte{10, 16, 235, 240})

	a := New(media.LevelsFor("tv"))
	require.NoError(t, a.Consume(f))
	f.Release()
	require.NoError(t, a.Close())

	res := a.Result()
	assert.Equal(t, []float64{125.25}, res.Mean)
	assert.Equal(t, []float64{10}, res.Min)
	assert.Equal(t, []float64{240}, res.Max)
	assert.Equal(t, []float64{0.5}, res.OutOfRange)
	assert.InDelta(t, 10.0, res.GlobalMin, 1e-9)
}

func TestAnalyzerWithoutFrames(
	t *testing.T,
) {
	a := New(media.LevelsFor("pc"))
	require.NoError(t, a.Close())

	res := a.Result()
	assert.Empty(t, res.Mean)
	assert.Zero(t, res.GlobalMin)
	assert.Zero(t, res.GlobalMax)
	assert.Equal(t, media.LevelsFor("pc"), res.Levels)
}

// referenceStat is the histogram computation the vector loop replaced.
func referenceStat(
	p *frame.Plane,
	lv media.Levels,
) stat {
	var hist [256]int64

	for y := range p.Height {
		for _, v := range p.Row(y) {
			hist[v]++
		}
	}

	var sum, total, outside int64

	lo, hi := -1, 0

	for v, n := range hist {
		if n == 0 {
			continue
		}

		if lo < 0 {
			lo = v
		}

		hi = v
		sum += int64(v) * n
		total += n

		if v < lv.Black || v > lv.White {
			outside += n
		}
	}

	return stat{
		mean: float64(sum) / float64(total), lo: float64(lo), hi: float64(hi),
		outside: float64(outside) / float64(total),
	}
}

func TestStatsMatchHistogram(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		width, height int
		levels        media.Levels
		sample        func(rng *rand.Rand, x int) byte
	}{
		{
			name: "narrow range noise", width: 33, height: 7, levels: media.LevelsFor("tv"),
			sample: func(rng *rand.Rand, _ int) byte { return byte(rng.IntN(256)) },
		},
		{
			name: "full range", width: 1920, height: 4, levels: media.LevelsFor("pc"),
			sample: func(rng *rand.Rand, _ int) byte { return byte(rng.IntN(256)) },
		},
		{
			name: "rows longer than a vector run", width: 70001, height: 2, levels: media.LevelsFor("tv"),
			sample: func(rng *rand.Rand, x int) byte { return byte(x%200 + rng.IntN(56)) },
		},
		{
			name: "flat", width: 64, height: 3, levels: media.LevelsFor("tv"),
			sample: func(*rand.Rand, int) byte { return 16 },
		},
	}

	rng := rand.New(rand.NewPCG(3, 4))

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(testCase.width, testCase.height, frame.PoolOptions{})
			f := pool.Get()

			for i := range f.Luma.Pix {
				f.Luma.Pix[i] = testCase.sample(rng, i%testCase.width)
			}

			a := New(testCase.levels)
			require.NoError(t, a.Consume(f))
			require.NoError(t, a.Close())

			assert.Equal(t, []stat{referenceStat(&f.Luma, testCase.levels)}, a.series.Merge())
		})
	}
}
