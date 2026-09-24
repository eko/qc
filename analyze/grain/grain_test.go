package grain

import (
	"bytes"
	"math"
	"math/rand/v2"
	"os"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grayFrames returns n frames of a smooth gradient plus Gaussian noise of
// standard deviation sigma.
func grayFrames(
	n, width, height int,
	sigma float64,
) []byte {
	rng := rand.New(rand.NewPCG(1, 2))
	out := make([]byte, 0, n*width*height)

	for range n {
		for y := range height {
			for x := range width {
				v := 60 + 0.2*float64(x) + 0.1*float64(y) + rng.NormFloat64()*sigma
				out = append(out, byte(math.Min(math.Max(math.Round(v), 0), 255)))
			}
		}
	}

	return out
}

func TestEstimatorMeasure(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		sigma     float64
		wantSigma float64
		tolerance float64
	}{
		{name: "clean gradient", sigma: 0, wantSigma: 0, tolerance: 0.3},
		{name: "light grain", sigma: 2, wantSigma: 2, tolerance: 0.3},
		{name: "heavy grain", sigma: 8, wantSigma: 8, tolerance: 0.8},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// A trailing partial frame is ignored.
			input := append(grayFrames(2, 160, 96, testCase.sigma), make([]byte, 100)...)

			e, err := NewEstimator(160, 96)
			require.NoError(t, err)

			got, err := e.Measure(bytes.NewReader(input))
			require.NoError(t, err)

			assert.Equal(t, 2, got.Frames)
			assert.InDelta(t, testCase.wantSigma, got.Sigma, testCase.tolerance)

			if testCase.sigma > 0 {
				assert.Negative(t, got.Correlation, "white noise minus its blur anti-correlates")
			}
		})
	}
}

func TestEstimatorAdd(
	t *testing.T,
) {
	frames := grayFrames(2, 160, 96, 3)

	read, err := NewEstimator(160, 96)
	require.NoError(t, err)
	want, err := read.Measure(bytes.NewReader(frames))
	require.NoError(t, err)

	added, err := NewEstimator(160, 96)
	require.NoError(t, err)
	require.NoError(t, added.Add(frames[:160*96]))
	require.NoError(t, added.Add(frames[160*96:]))

	got, err := added.Stats()
	require.NoError(t, err)
	assert.Equal(t, want, got, "adding frames one by one is reading them")

	require.ErrorIs(t, added.Add(frames[:100]), ErrFrameSize)
}

func TestEstimatorErrors(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		width, height int
		input         []byte
		readErr       error
		wantErr       error
	}{
		{name: "no width", width: 0, height: 64, wantErr: ErrFrameSize},
		{name: "negative height", width: 64, height: -1, wantErr: ErrFrameSize},
		{name: "no frame", width: 64, height: 64, wantErr: ErrNoFrames},
		{name: "less than a frame", width: 64, height: 64, input: make([]byte, 64), wantErr: ErrNoFrames},
		{name: "read failure", width: 64, height: 64, readErr: os.ErrClosed, wantErr: os.ErrClosed},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			e, err := NewEstimator(testCase.width, testCase.height)
			if err != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			if testCase.readErr != nil {
				_, err = e.Measure(iotest.ErrReader(testCase.readErr))
				require.ErrorIs(t, err, testCase.wantErr)
				assert.Contains(t, err.Error(), "read frame")

				return
			}

			_, err = e.Measure(bytes.NewReader(testCase.input))
			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}
