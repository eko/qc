package decode

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestParseHWAccel(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    HWAccel
		wantErr error
	}{
		{name: "empty", input: "", want: HWAccelNone},
		{name: "none", input: "none", want: HWAccelNone},
		{name: "cuda", input: "CUDA", want: HWAccelCUDA},
		{name: "cuda-scale", input: " cuda-scale ", want: HWAccelCUDAScale},
		{name: "unknown", input: "vaapi", wantErr: ErrHWAccel},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseHWAccel(testCase.input)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestHWAccelOrder(
	t *testing.T,
) {
	testCases := []struct {
		mode     HWAccel
		name     string
		fallback HWAccel
		rank     int
	}{
		{mode: HWAccelNone, name: "none", fallback: HWAccelNone, rank: 0},
		{mode: HWAccelCUDA, name: "cuda", fallback: HWAccelNone, rank: 1},
		{mode: HWAccelCUDAScale, name: "cuda-scale", fallback: HWAccelCUDA, rank: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.name, testCase.mode.String())
			assert.Equal(t, testCase.fallback, testCase.mode.fallback())
			assert.Equal(t, testCase.rank, testCase.mode.rank())
		})
	}
}

func TestHWAccelArgs(
	t *testing.T,
) {
	luma := frame.NewPool(320, 180, frame.PoolOptions{})
	chroma10 := frame.NewPool(1920, 1080, frame.PoolOptions{Chroma: true, HighBitDepth: true})

	testCases := []struct {
		name string
		mode HWAccel
		req  Request
		want string
	}{
		{
			name: "cuda decodes on the GPU and filters on the CPU",
			mode: HWAccelCUDA,
			req:  Request{Path: "in.mp4", Pool: luma, SourceWidth: 320, SourceHeight: 180},
			want: "-v error -nostdin -threads 0 -hwaccel cuda -i in.mp4 -map 0:v:0 -fps_mode passthrough -an -sn -dn " +
				"-vf extractplanes=y,scale=320:180:flags=bicubic,format=gray -f rawvideo -",
		},
		{
			name: "cuda-scale luma: scale on the GPU, download, select, extract",
			mode: HWAccelCUDAScale,
			req: Request{
				Path: "in.mp4", Pool: luma, SourceWidth: 1280, SourceHeight: 720,
				Start: media.Seconds(1), FrameRate: media.Rational{Num: 25, Den: 1}, Select: [][2]int{{0, 2}},
			},
			want: "-v error -nostdin -threads 0 -hwaccel cuda -hwaccel_output_format cuda -ss 0.980000 -i in.mp4 " +
				"-map 0:v:0 -fps_mode passthrough -an -sn -dn " +
				`-vf scale_cuda=320:180:interp_algo=bicubic:format=yuv420p,hwdownload,format=yuv420p,select='between(n\,0\,1)',extractplanes=y ` +
				"-f rawvideo -",
		},
		{
			name: "cuda-scale 10-bit chroma",
			mode: HWAccelCUDAScale,
			req:  Request{Path: "in.mp4", Pool: chroma10, SourceWidth: 1280, SourceHeight: 720},
			want: "-v error -nostdin -threads 0 -hwaccel cuda -hwaccel_output_format cuda -i in.mp4 " +
				"-map 0:v:0 -fps_mode passthrough -an -sn -dn " +
				"-vf scale_cuda=1920:1080:interp_algo=bicubic:format=yuv420p10le,hwdownload,format=yuv420p10le -f rawvideo -",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := strings.Join(NewFFmpeg("ffmpeg", 0).args(testCase.req, testCase.mode), " ")
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestModeFor(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		configured HWAccel
		codec      string
		degraded   map[string]HWAccel
		want       HWAccel
	}{
		{name: "cpu decoder", configured: HWAccelNone, codec: "h264", want: HWAccelNone},
		{name: "unknown codec is tried", configured: HWAccelCUDA, want: HWAccelCUDA},
		{name: "nvdec codec", configured: HWAccelCUDAScale, codec: "hevc", want: HWAccelCUDAScale},
		{name: "prores stays on the cpu", configured: HWAccelCUDA, codec: "prores", want: HWAccelNone},
		{name: "raw digest stays on the cpu", configured: HWAccelCUDAScale, codec: "rawvideo", want: HWAccelNone},
		{
			name: "a failed file keeps its fallback", configured: HWAccelCUDAScale, codec: "h264",
			degraded: map[string]HWAccel{"in.mp4": HWAccelCUDA}, want: HWAccelCUDA,
		},
		{
			name: "another file is unaffected", configured: HWAccelCUDA, codec: "h264",
			degraded: map[string]HWAccel{"other.mp4": HWAccelNone}, want: HWAccelCUDA,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d := NewFFmpeg("ffmpeg", 0, WithHWAccel(testCase.configured))
			d.degraded = testCase.degraded

			assert.Equal(t, testCase.configured, d.HWAccel())
			assert.Equal(t, testCase.want, d.modeFor(Request{Path: "in.mp4", Codec: testCase.codec}))
		})
	}
}

// fakeDecoder is ffmpeg behind a script that logs its arguments to the
// returned file and fails every command line containing one of failOn. The
// others run the real ffmpeg without their hardware decoding options, as a
// GPU machine would succeed.
func fakeDecoder(
	t *testing.T,
	failOn ...string,
) (string, string) {
	t.Helper()

	testutil.RequireFFmpeg(t)

	log := t.TempDir() + "/args.log"

	var body strings.Builder

	body.WriteString(`echo "$*" >> '` + log + "'\n")

	for _, word := range failOn {
		body.WriteString(`case "$*" in *"` + word + `"*) echo "Device creation failed" >&2; exit 1;; esac` + "\n")
	}

	body.WriteString(`for a; do
  shift
  case "$a" in -hwaccel|-hwaccel_output_format|cuda) ;; *) set -- "$@" "$a";; esac
done
exec "$REAL_FFMPEG" "$@"`)

	return testutil.FakeFFmpeg(t, body.String()), log
}

func TestDecodeFallsBack(
	t *testing.T,
) {
	path := blackClip(t, "")

	testCases := []struct {
		name         string
		mode         HWAccel
		failOn       []string
		wantWarnings int
		wantFinal    HWAccel
		wantCommands int
	}{
		{name: "no device: cuda-scale down to cpu", mode: HWAccelCUDAScale, failOn: []string{"-hwaccel"}, wantWarnings: 2, wantFinal: HWAccelNone, wantCommands: 3},
		{name: "no gpu scaler: cuda-scale down to cuda", mode: HWAccelCUDAScale, failOn: []string{"scale_cuda"}, wantWarnings: 1, wantFinal: HWAccelCUDA, wantCommands: 2},
		{name: "working hardware", mode: HWAccelCUDA, wantWarnings: 0, wantFinal: HWAccelCUDA, wantCommands: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			bin, argsLog := fakeDecoder(t, testCase.failOn...)

			var logs bytes.Buffer

			d := NewFFmpeg(bin, 0, WithHWAccel(testCase.mode), WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
			req := Request{
				Path: path, Codec: "h264", Pool: frame.NewPool(clipWidth, clipHeight, frame.PoolOptions{}),
				SourceWidth: clipWidth, SourceHeight: clipHeight, FrameRate: clipRate, MaxFrames: 3,
			}

			frames := 0
			require.NoError(t, d.Decode(t.Context(), req, func(f *frame.Frame) error {
				frames++
				f.Release()

				return nil
			}))

			assert.Equal(t, 3, frames)
			assert.Equal(t, testCase.wantWarnings, strings.Count(logs.String(), "hardware decoding failed"))
			assert.Equal(t, testCase.wantFinal, d.modeFor(req))

			commands, err := os.ReadFile(argsLog)
			require.NoError(t, err)
			assert.Equal(t, testCase.wantCommands, strings.Count(string(commands), "\n"))
		})
	}
}

func TestDecodeDoesNotFallBack(
	t *testing.T,
) {
	path := blackClip(t, "")
	bin, _ := fakeDecoder(t, "-hwaccel")
	req := Request{
		Path: path, Codec: "h264", Pool: frame.NewPool(clipWidth, clipHeight, frame.PoolOptions{}),
		SourceWidth: clipWidth, SourceHeight: clipHeight, FrameRate: clipRate,
	}
	release := func(f *frame.Frame) error {
		f.Release()

		return nil
	}

	t.Run("cpu failure is final", func(t *testing.T) {
		d := NewFFmpeg(bin, 0)
		missing := req
		missing.Path = "does-not-exist.mp4"

		require.ErrorContains(t, d.Decode(t.Context(), missing, release), "decode does-not-exist.mp4")
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		d := NewFFmpeg(bin, 0, WithHWAccel(HWAccelCUDA))

		require.ErrorIs(t, d.Decode(ctx, req, release), context.Canceled)
		assert.Equal(t, HWAccelCUDA, d.modeFor(req))
	})
}
