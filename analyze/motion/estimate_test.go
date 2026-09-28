package motion

import (
	"math"
	"math/cmplx"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// camera is a synthetic camera move per frame: translation of the view
// centre (texture pixels), zoom (log scale) and roll (radians).
type camera struct {
	vx, vy, zoom, roll float64
}

// estimateSequence renders frames of the texture under a camera move and
// returns the mean per-frame estimate: content translation, log scale,
// rotation, and the smallest confidence.
func estimateSequence(
	t *testing.T,
	tex texture,
	w, h int,
	c camera,
) (translation complex128, zoom, roll, confidence float64) {
	t.Helper()

	var e estimator

	const frames = 12

	confidence = 1

	for i := range frames {
		fi := float64(i)
		img := tex.render(w, h, 300+c.vx*fi, 300+c.vy*fi, math.Exp(c.zoom*fi), c.roll*fi)

		est := e.next(img, w, h, w)
		if i == 0 {
			assert.Zero(t, est.confidence, "the first frame has no motion")

			continue
		}

		scale := 1 + est.model.z
		translation += est.model.t
		zoom += math.Log(cmplx.Abs(scale))
		roll += cmplx.Phase(scale)
		confidence = min(confidence, est.confidence)
	}

	n := float64(frames - 1)

	return translation / complex(n, 0), zoom / n, roll / n, confidence
}

func TestEstimatorAccuracy(
	t *testing.T,
) {
	tex := newTexture(600, 1)

	testCases := []struct {
		name   string
		camera camera
		// want is the content motion: the opposite of the camera's.
		wantT          complex128
		wantZ, wantRol float64
	}{
		{name: "static", camera: camera{}},
		{name: "sub-pixel pan", camera: camera{vx: 0.3}, wantT: -0.3},
		{name: "pan", camera: camera{vx: 2.6}, wantT: -2.6},
		{name: "fast diagonal", camera: camera{vx: 5.2, vy: -3.1}, wantT: complex(-5.2, 3.1)},
		{name: "zoom in", camera: camera{zoom: 0.004}, wantZ: 0.004},
		{name: "zoom out", camera: camera{zoom: -0.01}, wantZ: -0.01},
		{name: "roll", camera: camera{roll: 0.002}, wantRol: 0.002},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			translation, zoom, roll, confidence := estimateSequence(t, tex, 240, 135, testCase.camera)

			assert.InDelta(t, real(testCase.wantT), real(translation), 0.05, "x translation")
			assert.InDelta(t, imag(testCase.wantT), imag(translation), 0.05, "y translation")
			assert.InDelta(t, testCase.wantZ, zoom, 0.0003, "zoom")
			assert.InDelta(t, testCase.wantRol, roll, 0.0003, "roll")
			assert.Greater(t, confidence, 0.9)
		})
	}
}

func TestEstimatorPictures(
	t *testing.T,
) {
	flat := make([]byte, 240*135)
	for i := range flat {
		flat[i] = 128
	}

	tex := newTexture(1200, 2)

	testCases := []struct {
		name           string
		width, height  int
		frames         func(i int) []byte
		wantConfidence bool
	}{
		{name: "flat picture", width: 240, height: 135, frames: func(int) []byte { return flat }},
		{name: "too small", width: 40, height: 30, frames: func(i int) []byte { return tex.render(40, 30, 300+float64(i), 300, 1, 0) }},
		{
			name: "full resolution input is downscaled", width: 960, height: 540, wantConfidence: true,
			frames: func(i int) []byte { return tex.render(960, 540, 600+4*float64(i), 600, 4, 0) },
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var e estimator

			e.next(testCase.frames(0), testCase.width, testCase.height, testCase.width)
			est := e.next(testCase.frames(1), testCase.width, testCase.height, testCase.width)

			assert.Equal(t, testCase.wantConfidence, est.confidence > 0)
		})
	}
}

func TestEstimatorResize(
	t *testing.T,
) {
	tex := newTexture(600, 3)

	var e estimator

	e.next(tex.render(240, 135, 300, 300, 1, 0), 240, 135, 240)
	require.Equal(t, 240, e.w)

	// A new geometry restarts the estimator: no motion across the change.
	est := e.next(tex.render(200, 100, 300, 300, 1, 0), 200, 100, 200)
	assert.Zero(t, est.confidence)
	assert.Equal(t, 200, e.w)
}

func TestProjectionShift(
	t *testing.T,
) {
	profile := []int32{10, 50, 20, 80, 30, 90, 40, 60, 70, 15, 25, 35}
	shifted := make([]int32, len(profile))

	for x := range shifted {
		// shifted[x] = profile[x-2], edges padded, and brighter overall.
		shifted[x] = profile[max(0, x-2)] + 100
	}

	testCases := []struct {
		name      string
		cur, prev []int32
		want      int
	}{
		{name: "empty", want: 0},
		{name: "same", cur: profile, prev: profile, want: 0},
		{name: "shifted and brightened", cur: shifted, prev: profile, want: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, projectionShift(testCase.cur, testCase.prev))
		})
	}
}

func TestDownscaleFactor(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		width int
		want  int
	}{
		{name: "thumbnail", width: 240, want: 1},
		{name: "small picture", width: 320, want: 1},
		{name: "full HD luma", width: 1920, want: 8},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, downscaleFactor(testCase.width))
		})
	}
}

func TestSolveFlatBlock(
	t *testing.T,
) {
	_, ok := tensor{}.solve(0, 0)
	assert.False(t, ok, "a flat block has no displacement")
}
