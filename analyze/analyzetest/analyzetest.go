// Package analyzetest checks that analyzers split into concurrent runs
// (analyze.Forker) give the results of a sequential pass.
package analyzetest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

// Frames returns n frames of pool, frame i filled by fill (after which its
// thumbnail is built), numbered from 0 and timestamped at fps frames per
// second. The caller releases them.
func Frames(
	pool *frame.Pool,
	n int,
	fps float64,
	fill func(f *frame.Frame, i int),
) []*frame.Frame {
	frames := make([]*frame.Frame, n)

	for i := range frames {
		f := pool.Get()
		f.Index, f.PTS = i, media.Seconds(float64(i)/fps)
		fill(f, i)
		pool.BuildThumb(f)
		frames[i] = f
	}

	return frames
}

// Release releases frames.
func Release(
	frames []*frame.Frame,
) {
	for _, f := range frames {
		f.Release()
	}
}

// Split feeds frames to seq in one sequential pass and to forks of split,
// one per run starting at each of starts (and at 0), consecutive runs
// sharing a frame as analyze.RunSegments has them, the runs fed from the
// last to the first; both analyzers are then closed. Their results must
// then be equal.
func Split(
	t testing.TB,
	seq, split analyze.Forker,
	frames []*frame.Frame,
	starts ...int,
) {
	t.Helper()

	for _, f := range frames {
		require.NoError(t, seq.Consume(f))
	}

	require.NoError(t, seq.Close())

	bounds := append([]int{0}, starts...)

	for k := len(bounds) - 1; k >= 0; k-- {
		end := len(frames)
		if k+1 < len(bounds) {
			end = bounds[k+1] + 1
		}

		fork := split.Fork()
		for _, f := range frames[bounds[k]:end] {
			require.NoError(t, fork.Consume(f))
		}

		require.NoError(t, fork.Close())
	}

	require.NoError(t, split.Close())
}
