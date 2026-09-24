package crop

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
	const w, h = 64, 48

	letterbox := func(x, y int) byte {
		if y < 6 || y >= 42 {
			return 16
		}

		return byte(60 + (x*y)%100)
	}

	testCases := []struct {
		name   string
		frames []func(x, y int) byte
		want   Result
	}{
		{
			name:   "letterbox",
			frames: []func(x, y int) byte{letterbox},
			want:   Result{Content: Rect{X: 0, Y: 6, Width: 64, Height: 36}, Letterbox: true, FramesUsed: 1},
		},
		{
			name: "pillarbox with a black frame ignored",
			frames: []func(x, y int) byte{
				func(int, int) byte { return 16 },
				func(x, _ int) byte {
					if x < 8 || x >= 56 {
						return 17
					}

					return 128
				},
			},
			want: Result{Content: Rect{X: 8, Y: 0, Width: 48, Height: 48}, Pillarbox: true, FramesUsed: 1},
		},
		{
			name: "bright subtitle in the bar keeps the bar",
			frames: []func(x, y int) byte{func(x, y int) byte {
				if y == 44 && x > 20 && x < 30 {
					return 235
				}

				return letterbox(x, y)
			}},
			want: Result{Content: Rect{X: 0, Y: 6, Width: 64, Height: 40}, Letterbox: true, FramesUsed: 1},
		},
		{
			name:   "no frame analysed",
			frames: nil,
			want:   Result{Content: Rect{Width: w, Height: h}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(w, h, frame.PoolOptions{})
			a := New(w, h, media.LevelsFor("tv"), Options{Every: 1})

			for i, fn := range testCase.frames {
				f := pool.Get()
				f.Index = i

				for y := range h {
					for x := range w {
						f.Luma.Pix[y*w+x] = fn(x, y)
					}
				}

				require.NoError(t, a.Consume(f))
				f.Release()
			}

			require.NoError(t, a.Close())
			assert.Equal(t, testCase.want, a.Result())
		})
	}
}

func TestAnalyzerSamplesFrames(
	t *testing.T,
) {
	const w, h = 16, 16

	pool := frame.NewPool(w, h, frame.PoolOptions{})
	a := New(w, h, media.LevelsFor("tv"), Options{})

	for i := range 25 {
		f := pool.Get()
		f.Index = i

		for p := range f.Luma.Pix {
			f.Luma.Pix[p] = 128
		}

		require.NoError(t, a.Consume(f))
		f.Release()
	}

	require.NoError(t, a.Close())
	assert.Equal(t, Result{Content: Rect{Width: w, Height: h}, FramesUsed: 3}, a.Result(), "frames 0, 10 and 20")
}
