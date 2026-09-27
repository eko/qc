package frame

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoolThumb(
	t *testing.T,
) {
	testCases := []struct {
		name               string
		width, height      int
		maxWidth           int
		wantThumbW, wantTH int
	}{
		{name: "1080p", width: 1920, height: 1080, maxWidth: 240, wantThumbW: 240, wantTH: 135},
		{name: "odd factor", width: 1280, height: 720, maxWidth: 240, wantThumbW: 213, wantTH: 120},
		{name: "already small", width: 200, height: 100, maxWidth: 240, wantThumbW: 200, wantTH: 100},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := NewPool(testCase.width, testCase.height, PoolOptions{ThumbMaxWidth: testCase.maxWidth})
			f := pool.Get()

			for i := range f.Luma.Pix {
				f.Luma.Pix[i] = 100
			}

			pool.BuildThumb(f)

			assert.Equal(t, testCase.wantThumbW, f.Thumb.Width)
			assert.Equal(t, testCase.wantTH, f.Thumb.Height)
			assert.Equal(t, byte(100), f.Thumb.Pix[len(f.Thumb.Pix)-1])
			f.Release()
		})
	}
}

func TestBoxDownscaleAverages(
	t *testing.T,
) {
	src := Plane{Width: 4, Height: 2, Stride: 4, Pix: []byte{0, 10, 100, 200, 20, 30, 100, 200}}
	dst := Plane{Width: 2, Height: 1, Stride: 2, Pix: make([]byte, 2)}

	boxDownscale(&src, &dst, 2)

	assert.Equal(t, []byte{15, 150}, dst.Pix)
}

func TestNewPoolGeometry(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		width, height int
		opts          PoolOptions
		wantPlanes    int
		wantChroma    [2]int
		wantBPS       int
		wantThumb     bool
	}{
		{name: "luma only", width: 320, height: 180, wantPlanes: 1, wantBPS: 1},
		{
			name: "odd size chroma rounds up", width: 321, height: 181, opts: PoolOptions{Chroma: true},
			wantPlanes: 3, wantChroma: [2]int{161, 91}, wantBPS: 1,
		},
		{
			name: "high bit depth disables thumbnails", width: 320, height: 180,
			opts:       PoolOptions{Chroma: true, HighBitDepth: true, ThumbMaxWidth: 160},
			wantPlanes: 3, wantChroma: [2]int{160, 90}, wantBPS: 2,
		},
		{
			name: "thumbnails", width: 320, height: 180, opts: PoolOptions{ThumbMaxWidth: 160},
			wantPlanes: 1, wantBPS: 1, wantThumb: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := NewPool(testCase.width, testCase.height, testCase.opts)
			f := pool.Get()
			defer f.Release()

			assert.Equal(t, testCase.width, pool.Width())
			assert.Equal(t, testCase.height, pool.Height())
			assert.Equal(t, testCase.opts.Chroma, pool.Chroma())
			assert.Equal(t, testCase.opts.HighBitDepth, pool.HighBitDepth())

			planes := pool.Planes(f)
			require.Len(t, planes, testCase.wantPlanes)
			assert.Same(t, &f.Luma, planes[0])
			assert.Equal(t, testCase.width*testCase.wantBPS, f.Luma.Stride)
			assert.Len(t, f.Luma.Pix, testCase.width*testCase.height*testCase.wantBPS)

			if testCase.opts.Chroma {
				assert.Same(t, &f.Cb, planes[1])
				assert.Same(t, &f.Cr, planes[2])
				assert.Equal(t, testCase.wantChroma, [2]int{f.Cb.Width, f.Cb.Height})
				assert.Equal(t, testCase.wantChroma, [2]int{f.Cr.Width, f.Cr.Height})
				assert.Equal(t, testCase.wantBPS, f.Cb.BytesPerSample)
			}

			assert.Equal(t, testCase.wantThumb, f.Thumb.Pix != nil)
		})
	}
}

func TestBuildThumbWithoutThumbnails(
	t *testing.T,
) {
	pool := NewPool(4, 4, PoolOptions{})
	f := pool.Get()
	defer f.Release()

	pool.BuildThumb(f)

	assert.Nil(t, f.Thumb.Pix)
}

func TestBoxDownscaleFactorOneCopies(
	t *testing.T,
) {
	src := Plane{Width: 2, Height: 2, Stride: 2, Pix: []byte{1, 2, 3, 4}}
	dst := Plane{Width: 2, Height: 2, Stride: 2, Pix: make([]byte, 4)}

	boxDownscale(&src, &dst, 1)

	assert.Equal(t, src.Pix, dst.Pix)
}

func TestPlaneRow(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		plane Plane
		y     int
		want  []byte
	}{
		{
			name:  "8-bit with padding",
			plane: Plane{Width: 2, Height: 2, Stride: 3, Pix: []byte{1, 2, 0, 3, 4, 0}},
			y:     1,
			want:  []byte{3, 4},
		},
		{
			name:  "16-bit",
			plane: Plane{Width: 2, Height: 2, Stride: 4, BytesPerSample: 2, Pix: []byte{1, 0, 2, 0, 3, 0, 4, 0}},
			y:     1,
			want:  []byte{3, 0, 4, 0},
		},
		{
			name:  "unset sample size means 8-bit",
			plane: Plane{Width: 1, Height: 2, Stride: 1, Pix: []byte{5, 6}},
			y:     0,
			want:  []byte{5},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.plane.Row(testCase.y))
		})
	}
}

func TestFrameReferenceCounting(
	t *testing.T,
) {
	pool := NewPool(2, 2, PoolOptions{})
	f := pool.Get()
	assert.Equal(t, int32(1), f.refs.Load())

	f.Retain()
	assert.Equal(t, int32(2), f.refs.Load())

	f.Release()
	assert.Equal(t, int32(1), f.refs.Load())

	f.Release()
	assert.Equal(t, int32(0), f.refs.Load())

	again := pool.Get()
	assert.Equal(t, int32(1), again.refs.Load(), "a recycled frame starts with one reference")
	again.Release()
}

func TestReleaseWithoutPool(
	t *testing.T,
) {
	f := &Frame{}
	f.Retain()

	assert.NotPanics(t, f.Release)
}

func TestPoolSampleGrid(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		width    int
		height   int
		step     int
		wantStep int
		wantGrid [2]int
		wantLuma int
	}{
		{name: "even step", width: 1920, height: 1080, step: 4, wantStep: 4, wantGrid: [2]int{480, 270}, wantLuma: 1},
		{name: "odd step rounds up, partial cells count", width: 10, height: 6, step: 3, wantStep: 4, wantGrid: [2]int{3, 2}, wantLuma: 1},
		{name: "no grid", width: 10, height: 6, wantGrid: [2]int{}, wantLuma: 3},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := NewPool(testCase.width, testCase.height, PoolOptions{SampleStep: testCase.step, Chroma: true, HighBitDepth: true})
			assert.Equal(t, testCase.wantStep, pool.SampleStep())
			assert.Equal(t, testCase.wantGrid, pool.GridSize())

			f := pool.Get()
			defer f.Release()

			assert.Equal(t, testCase.wantStep, f.Samples.Step)
			assert.Len(t, pool.Planes(f), testCase.wantLuma, "a sampling pool reads the luma alone")

			planes := pool.SamplePlanes(f)
			require.Len(t, planes, 3)

			for _, p := range planes {
				assert.Equal(t, testCase.wantGrid, [2]int{p.Width, p.Height})
				assert.Len(t, p.Pix, testCase.wantGrid[0]*testCase.wantGrid[1]*2)
			}
		})
	}
}

func TestPlaneUint16(
	t *testing.T,
) {
	p := Plane{Width: 2, Height: 1, Stride: 4, BytesPerSample: 2, Pix: []byte{0x01, 0x02, 0xff, 0x03}}

	assert.Equal(t, []uint16{0x0201, 0x03ff}, p.Uint16())
}
