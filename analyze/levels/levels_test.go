package levels

import (
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
