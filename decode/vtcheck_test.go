package decode

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
)

// withVTCheck replaces the check that VideoToolbox decodes as the CPU.
func withVTCheck(
	exact bool,
	err error,
) Option {
	return func(d *FFmpeg) {
		d.vtCheck = func(context.Context) (bool, error) { return exact, err }
	}
}

// skipInexactVideoToolbox skips tests of VideoToolbox's frames where it does
// not decode as the CPU (a virtualised GPU): qc decodes on the CPU there.
func skipInexactVideoToolbox(
	t *testing.T,
) {
	t.Helper()

	if exact, err := VideoToolboxExact(t.Context(), "ffmpeg"); err != nil || !exact {
		t.Skipf("VideoToolbox does not decode as the CPU on this machine (exact %v, %v)", exact, err)
	}
}

func TestVideoToolboxExact(
	t *testing.T,
) {
	if runtime.GOOS != darwin {
		t.Skip("VideoToolbox is macOS only")
	}

	exact, err := VideoToolboxExact(t.Context(), "ffmpeg")
	require.NoError(t, err)
	t.Logf("VideoToolbox decodes as the CPU: %v", exact)
}

func TestVideoToolboxExactComparesFrames(
	t *testing.T,
) {
	// The fake ffmpeg answers each decode with the hashes of its mode.
	testCases := []struct {
		name     string
		hardware string
		want     bool
	}{
		{name: "same frames", hardware: "0, 0, 0, 1, 10, aa", want: true},
		{name: "other chroma", hardware: "0, 0, 0, 1, 10, bb", want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			bin := testutil.FakeFFmpeg(t, `case "$*" in *videotoolbox*) printf '#tb 0: 1/25\n`+testCase.hardware+`\n' ;; *) printf '#tb 0: 1/25\n0, 0, 0, 1, 10, aa\n' ;; esac`)

			exact, err := VideoToolboxExact(t.Context(), bin)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, exact)
		})
	}
}

func TestVideoToolboxExactErrors(
	t *testing.T,
) {
	testCases := []struct {
		name string
		bin  string
	}{
		{name: "no ffmpeg", bin: "qc-no-such-ffmpeg"},
		{name: "the hardware decode fails", bin: testutil.FakeFFmpeg(t, `case "$*" in *videotoolbox*) exit 1 ;; *) printf 'aa\n' ;; esac`)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := VideoToolboxExact(t.Context(), testCase.bin)
			require.Error(t, err)
		})
	}

	t.Run("no temporary directory", func(t *testing.T) {
		t.Setenv("TMPDIR", "/qc/no/such/dir")

		_, err := VideoToolboxExact(t.Context(), "ffmpeg")
		require.Error(t, err)
	})

	t.Run("the clip cannot be written", func(t *testing.T) {
		saved := vtProbes
		t.Cleanup(func() { vtProbes = saved })

		vtProbes = append(vtProbes[:0:0], vtProbes[0])
		vtProbes[0].name = "no/such/dir/h264.mp4"

		_, err := VideoToolboxExact(t.Context(), "ffmpeg")
		require.Error(t, err)
	})
}

func TestModeForInexactVideoToolbox(
	t *testing.T,
) {
	segment := Request{Path: "in.mp4", Codec: "h264", PixelFormat: "yuv420p", Segment: true}

	testCases := []struct {
		name     string
		exact    bool
		err      error
		want     HWAccel
		wantWarn bool
	}{
		{name: "exact", exact: true, want: HWAccelVideoToolbox},
		{name: "other frames: the cpu", want: HWAccelNone, wantWarn: true},
		{name: "a check that cannot run leaves videotoolbox", err: errors.New("no ffmpeg"), want: HWAccelVideoToolbox},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var logs bytes.Buffer

			d := NewFFmpeg("ffmpeg", 0, WithHWAccel(HWAccelVideoToolbox), withVTCheck(testCase.exact, testCase.err),
				WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))

			assert.Equal(t, testCase.want, d.modeFor(t.Context(), segment))
			assert.Equal(t, testCase.want, d.modeFor(t.Context(), segment), "checked once")
			assert.Equal(t, testCase.wantWarn, bytes.Contains(logs.Bytes(), []byte("VideoToolbox does not decode as the CPU")))
		})
	}
}

func TestVTExactCachedPerBinary(
	t *testing.T,
) {
	bin := testutil.FakeFFmpeg(t, `printf 'aa\n'`)

	for range 2 {
		assert.True(t, NewFFmpeg(bin, 0, WithHWAccel(HWAccelVideoToolbox)).vtExact(t.Context()))
	}

	result, ok := vtChecks.Load(bin)
	require.True(t, ok, "the outcome is cached for the binary")
	assert.True(t, result.(*vtResult).ok) //nolint:forcetypeassert // the map only holds *vtResult
}
