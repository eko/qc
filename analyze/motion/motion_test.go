package motion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analyze/analyzetest"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

// feed consumes frames rendered by draw (full-resolution luma, thumbnails
// built by the pool) at 25 fps.
func feed(
	t *testing.T,
	a *Analyzer,
	pool *frame.Pool,
	frames int,
	draw func(i int) []byte,
) {
	t.Helper()

	for i := range frames {
		f := pool.Get()
		f.Index, f.PTS = i, media.Seconds(float64(i)/25)
		copy(f.Luma.Pix, draw(i))
		pool.BuildThumb(f)
		require.NoError(t, a.Consume(f))
		f.Release()
	}

	require.NoError(t, a.Close())
}

func shot(
	first, last int,
) scene.Shot {
	return scene.Shot{
		Interval:   media.Interval{Start: media.Seconds(float64(first) / 25), End: media.Seconds(float64(last+1) / 25)},
		FirstFrame: first, LastFrame: last,
	}
}

func TestAnalyzer(
	t *testing.T,
) {
	tex := newTexture(1400, 4)
	pool := frame.NewPool(480, 270, frame.PoolOptions{ThumbMaxWidth: 240})
	a := New(Options{})

	// 30 frames panning right (camera) by 2 thumbnail pixels a frame (40%
	// of the width per second), then a cut to 30 static frames elsewhere.
	feed(t, a, pool, 60, func(i int) []byte {
		if i < 30 {
			return tex.render(480, 270, 300+4*float64(i), 400, 1, 0)
		}

		return tex.render(480, 270, 900, 900, 1, 0)
	})

	res := a.Result([]scene.Shot{shot(0, 29), shot(30, 59), shot(70, 80)})

	require.Len(t, res.Shots, 3)
	assert.Equal(t, ClassPan, res.Shots[0].Class)
	assert.Equal(t, DirectionRight, res.Shots[0].Direction)
	assert.InDelta(t, 2.0/240*100*25, res.Shots[0].Pan, 0.5)
	assert.Equal(t, ClassStatic, res.Shots[1].Class)
	assert.Equal(t, ClassUnknown, res.Shots[2].Class, "a shot beyond the frames")

	assert.Len(t, res.Frames.Pan, 60)
	assert.Zero(t, res.Frames.Confidence[0])
	assert.Zero(t, res.Frames.Confidence[30], "the cut frame is not trusted")
	assert.InDelta(t, res.Frames.Pan[1], res.Frames.Pan[0], 1e-9, "untrusted frames are interpolated")
	assert.Greater(t, res.Frames.Confidence[31], 0.5)

	wantShares := []ClassShare{
		{Class: ClassPan, Shots: 1, Duration: media.Seconds(1.2), Share: 1.2 / 2.84},
		{Class: ClassStatic, Shots: 1, Duration: media.Seconds(1.2), Share: 1.2 / 2.84},
		{Class: ClassUnknown, Shots: 1, Duration: media.Seconds(0.44), Share: 0.44 / 2.84},
	}
	require.Len(t, res.Summary.Classes, len(wantShares))

	for i, want := range wantShares {
		got := res.Summary.Classes[i]
		assert.Equal(t, want.Class, got.Class)
		assert.Equal(t, want.Shots, got.Shots)
		assert.Equal(t, want.Duration, got.Duration)
		assert.InDelta(t, want.Share, got.Share, 1e-9)
	}

	assert.InDelta(t, 58.0/60, res.Summary.Reliable, 1e-9)
}

// panClip renders frame i of a 40-frame clip panning right, then left.
func panClip(
	tex texture,
) func(f *frame.Frame, i int) {
	return func(f *frame.Frame, i int) {
		x := 300 + 4*float64(min(i, 20)) - 3*float64(max(i-20, 0))
		copy(f.Luma.Pix, tex.render(480, 270, x, 400, 1, 0))
	}
}

func TestAnalyzerForks(
	t *testing.T,
) {
	pool := frame.NewPool(480, 270, frame.PoolOptions{ThumbMaxWidth: 240})
	frames := analyzetest.Frames(pool, 40, 25, panClip(newTexture(1200, 5)))

	defer analyzetest.Release(frames)

	shots := []scene.Shot{shot(0, 20), shot(21, 39)}
	seq, split := New(Options{}), New(Options{})

	// Runs overlapping by one frame, one of them starting inside a shot.
	analyzetest.Split(t, seq, split, frames, 17, 30)

	assert.Equal(t, seq.Result(shots), split.Result(shots))
}

func TestAnalyzerWithoutFrames(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		setup func(a *Analyzer)
	}{
		{name: "no frame"},
		{name: "16-bit frames are not analysed", setup: func(a *Analyzer) {
			pool := frame.NewPool(64, 64, frame.PoolOptions{HighBitDepth: true})
			f := pool.Get()
			_ = a.Consume(f)
			f.Release()
		}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := New(Options{})
			if testCase.setup != nil {
				testCase.setup(a)
			}

			res := a.Result([]scene.Shot{shot(0, 0)})
			assert.Equal(t, []Shot{{Class: ClassUnknown}}, res.Shots)
			assert.Equal(t, ClassUnknown, res.Summary.Classes[0].Class)
		})
	}
}

func TestOptionsDefaults(
	t *testing.T,
) {
	testCases := []struct {
		name string
		opts Options
		want Options
	}{
		{
			name: "zero value",
			want: Options{
				Smooth: media.Seconds(defaultSmooth), ShakeWindow: media.Seconds(defaultShakeWindow),
				MinSpeed: defaultMinSpeed, MinZoom: defaultMinZoom, MaxShake: defaultMaxShake,
				MinParallax: defaultMinParallax, MinConfidence: defaultMinConfidence,
			},
		},
		{
			name: "set values are kept",
			opts: Options{Smooth: media.Seconds(1), ShakeWindow: media.Seconds(1), MinSpeed: 1, MinZoom: 1, MaxShake: 1, MinParallax: 1, MinConfidence: 1},
			want: Options{Smooth: media.Seconds(1), ShakeWindow: media.Seconds(1), MinSpeed: 1, MinZoom: 1, MaxShake: 1, MinParallax: 1, MinConfidence: 1},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.opts.withDefaults())
		})
	}
}

func TestSummarize(
	t *testing.T,
) {
	classes := []Shot{{Class: ClassPan, Shaky: true}, {Class: ClassStatic}, {Class: ClassPan}}
	shots := []scene.Shot{shot(0, 24), shot(25, 49), shot(50, 99)}

	got := summarize(classes, shots, 0.9)

	assert.Equal(t, 1, got.ShakyShots)
	assert.InDelta(t, 0.9, got.Reliable, 1e-9)
	require.Len(t, got.Classes, 2)
	assert.Equal(t, ClassPan, got.Classes[0].Class)
	assert.Equal(t, 2, got.Classes[0].Shots)
	assert.InDelta(t, 0.75, got.Classes[0].Share, 1e-9)
}
