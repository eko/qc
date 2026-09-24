package encode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/internal/testutil"
)

func TestNoiseArgs(
	t *testing.T,
) {
	testCases := []struct {
		name string
		step int
		want string
	}{
		{name: "every 5 frames", step: 5, want: "select='not(mod(n\\,5))',scale=320:180,format=yuv420p,extractplanes=y"},
		{name: "no step means every frame", step: 0, want: "select='not(mod(n\\,1))',scale=320:180,format=yuv420p,extractplanes=y"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, []string{
				"-v", "error", "-nostdin", "-i", "in.mp4",
				"-vf", testCase.want,
				"-frames:v", "8", "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "gray", "-",
			}, noiseArgs("in.mp4", 320, 180, testCase.step))
		})
	}
}

func TestFilmGrainArgs(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		codec string
		grain int
		want  bool
	}{
		{name: "AV1 with grain", codec: "av1", grain: 25, want: true},
		{name: "AV1 without grain", codec: "av1"},
		{name: "H.264 ignores grain", codec: "h264", grain: 25},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			args := codecs[testCase.codec].Args(Params{Width: 64, Height: 36, FilmGrain: testCase.grain})
			if testCase.want {
				assert.Contains(t, args, "film-grain=25:film-grain-denoise=1")
			} else {
				assert.NotContains(t, args, "-svtav1-params")
			}
		})
	}
}

func TestGrainErrors(
	t *testing.T,
) {
	missing := NewFFmpeg("qc-no-such-ffmpeg")

	_, err := missing.Noise(t.Context(), "in.mp4", 64, 36, 1)
	require.ErrorIs(t, err, ffexec.ErrNotFound)
	assert.Contains(t, err.Error(), "noise of in.mp4")

	_, err = missing.Noise(t.Context(), "in.mp4", 0, 36, 1)
	require.ErrorIs(t, err, grain.ErrFrameSize)
	assert.Contains(t, err.Error(), "noise of in.mp4: invalid frame size 0x36")

	err = missing.DecodeRaw(t.Context(), "in.mp4", "out.nut", false)
	require.ErrorIs(t, err, ffexec.ErrNotFound)
	assert.Contains(t, err.Error(), "decode in.mp4")
}

func TestGrainIntegration(
	t *testing.T,
) {
	clean := testutil.Generate(t, testutil.Clip{Width: 320, Height: 180, Seconds: 1, Source: "color", Filter: "lutyuv=y=128"})
	grainy := testutil.Generate(t, testutil.Clip{
		Width: 320, Height: 180, Seconds: 1, Source: "color", Filter: "lutyuv=y=128,noise=alls=20:allf=t",
		Codec: "libsvtav1", Name: "grain.mp4", Args: []string{"-crf", "40", "-svtav1-params", "film-grain=30:film-grain-denoise=1"},
	})
	f := NewFFmpeg("ffmpeg")

	cleanNoise, err := f.Noise(t.Context(), clean, 320, 180, 5)
	require.NoError(t, err)
	assert.Less(t, cleanNoise.Sigma, 0.5)
	assert.Equal(t, 5, cleanNoise.Frames, "25 frames, one in 5")

	grainNoise, err := f.Noise(t.Context(), grainy, 320, 180, 5)
	require.NoError(t, err)
	assert.Greater(t, grainNoise.Sigma, 1.0, "grain is synthesised on decode")

	withGrain, withoutGrain := filepath.Join(t.TempDir(), "with.nut"), filepath.Join(t.TempDir(), "without.nut")
	require.NoError(t, f.DecodeRaw(t.Context(), grainy, withGrain, true))
	require.NoError(t, f.DecodeRaw(t.Context(), grainy, withoutGrain, false))

	cleaned, err := f.Noise(t.Context(), withoutGrain, 320, 180, 5)
	require.NoError(t, err)
	assert.Less(t, cleaned.Sigma, grainNoise.Sigma/2, "the grain-free decode leaves the grain out")

	a, err := os.ReadFile(withGrain)
	require.NoError(t, err)
	b, err := os.ReadFile(withoutGrain)
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
}
