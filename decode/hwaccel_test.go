package decode

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"runtime"
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
		{name: "videotoolbox", input: "VideoToolbox", want: HWAccelVideoToolbox},
		{name: "auto", input: "auto", want: HWAccelAuto},
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
		{mode: HWAccelVideoToolbox, name: "videotoolbox", fallback: HWAccelNone, rank: 1},
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
	pq := &ToneMap{Input: media.Color{Transfer: media.TransferPQ}}

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
			name: "videotoolbox decodes on the hardware and filters on the CPU",
			mode: HWAccelVideoToolbox,
			req:  Request{Path: "in.mp4", Pool: luma, SourceWidth: 320, SourceHeight: 180},
			want: "-v error -nostdin -threads 0 -hwaccel videotoolbox -i in.mp4 -map 0:v:0 -fps_mode passthrough -an -sn -dn " +
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
		{
			name: "cuda-scale tone maps on the CPU",
			mode: HWAccelCUDAScale,
			req:  Request{Path: "in.mp4", Pool: chroma10, SourceWidth: 1920, SourceHeight: 1080, ToneMap: pq},
			want: "-v error -nostdin -threads 0 -hwaccel cuda -hwaccel_output_format cuda -i in.mp4 " +
				"-map 0:v:0 -fps_mode passthrough -an -sn -dn " +
				"-vf scale_cuda=1920:1080:interp_algo=bicubic:format=yuv420p10le,hwdownload,format=yuv420p10le," +
				"scale=1920:1080:flags=bicubic:in_transfer=smpte2084:in_primaries=bt2020:in_color_matrix=bt2020nc:in_range=tv:" +
				"out_transfer=bt709:out_primaries=bt709:out_color_matrix=bt709:out_range=tv:intent=perceptual -f rawvideo -",
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
		req        Request
		degraded   map[string]HWAccel
		want       HWAccel
	}{
		{name: "cpu decoder", configured: HWAccelNone, req: Request{Codec: "h264"}, want: HWAccelNone},
		{name: "unknown codec is tried", configured: HWAccelCUDA, want: HWAccelCUDA},
		{name: "nvdec codec", configured: HWAccelCUDAScale, req: Request{Codec: "hevc"}, want: HWAccelCUDAScale},
		{name: "prores stays on the cpu", configured: HWAccelCUDA, req: Request{Codec: "prores"}, want: HWAccelNone},
		{name: "raw digest stays on the cpu", configured: HWAccelCUDAScale, req: Request{Codec: "rawvideo"}, want: HWAccelNone},
		{
			name: "a failed file keeps its fallback", configured: HWAccelCUDAScale, req: Request{Codec: "h264"},
			degraded: map[string]HWAccel{"in.mp4": HWAccelCUDA}, want: HWAccelCUDA,
		},
		{
			name: "another file is unaffected", configured: HWAccelCUDA, req: Request{Codec: "h264"},
			degraded: map[string]HWAccel{"other.mp4": HWAccelNone}, want: HWAccelCUDA,
		},
		{
			name: "videotoolbox 10-bit hevc", configured: HWAccelVideoToolbox,
			req: Request{Codec: "hevc", PixelFormat: "yuv420p10le"}, want: HWAccelVideoToolbox,
		},
		{
			name: "videotoolbox leaves mpeg-2 to the cpu", configured: HWAccelVideoToolbox,
			req: Request{Codec: "mpeg2video", PixelFormat: "yuv420p"}, want: HWAccelNone,
		},
		{
			name: "videotoolbox leaves 4:2:2 to the cpu", configured: HWAccelVideoToolbox,
			req: Request{Codec: "h264", PixelFormat: "yuv422p10le"}, want: HWAccelNone,
		},
		{
			name: "videotoolbox needs the pixel format", configured: HWAccelVideoToolbox,
			req: Request{Codec: "h264"}, want: HWAccelNone,
		},
		{
			name: "auto decodes segments with videotoolbox", configured: HWAccelAuto,
			req: Request{Codec: "h264", PixelFormat: "yuv420p", Segment: true}, want: HWAccelVideoToolbox,
		},
		{name: "auto decodes the rest on the cpu", configured: HWAccelAuto, req: Request{Codec: "h264", PixelFormat: "yuv420p"}, want: HWAccelNone},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d := &FFmpeg{bin: "ffmpeg", hwaccel: testCase.configured, degraded: testCase.degraded}

			req := testCase.req
			req.Path = "in.mp4"

			assert.Equal(t, testCase.want, d.modeFor(req))
		})
	}
}

func TestHWAccelResolve(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		mode         HWAccel
		goos         string
		want         HWAccel
		wantReported HWAccel
	}{
		{name: "auto on macOS", mode: HWAccelAuto, goos: "darwin", want: HWAccelAuto, wantReported: HWAccelNone},
		{name: "auto elsewhere", mode: HWAccelAuto, goos: "linux", want: HWAccelNone, wantReported: HWAccelNone},
		{name: "explicit modes are kept", mode: HWAccelCUDA, goos: "linux", want: HWAccelCUDA, wantReported: HWAccelCUDA},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.mode.Resolve(testCase.goos)
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.wantReported, (&FFmpeg{hwaccel: got}).HWAccel())
			assert.Equal(t, got == HWAccelCUDA || got == HWAccelCUDAScale, got.CUDA())
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

func TestSampledArgs(
	t *testing.T,
) {
	sampled := frame.NewPool(320, 180, frame.PoolOptions{SampleStep: 4})
	outputs := " -map [luma] -fps_mode passthrough -an -sn -dn -frames:v 2 -f rawvideo -" +
		" -map [grid] -fps_mode passthrough -an -sn -dn -frames:v 2 -f rawvideo pipe:3"
	branches := "split=2[l][g];[l]extractplanes=y,scale=320:180:flags=bicubic,format=gray[luma];" +
		"[g]scale=80:45:flags=neighbor,format=yuv444p10le[grid]"

	testCases := []struct {
		name string
		mode HWAccel
		req  Request
		want string
	}{
		{
			name: "cpu, with a selection",
			req:  Request{Path: "in.mp4", Pool: sampled, MaxFrames: 2, Select: [][2]int{{0, 2}}},
			want: "-v error -nostdin -threads 0 -i in.mp4 -filter_complex [0:v:0]select='between(n\\,0\\,1)'," +
				branches + outputs,
		},
		{
			name: "videotoolbox converts to the planar format first",
			mode: HWAccelVideoToolbox,
			req:  Request{Path: "in.mp4", Pool: sampled, MaxFrames: 2, PixelFormat: "yuv420p10le"},
			want: "-v error -nostdin -threads 0 -hwaccel videotoolbox -i in.mp4 -filter_complex [0:v:0]format=yuv420p10le," +
				branches + outputs,
		},
		{
			name: "cuda-scale scales on the GPU first",
			mode: HWAccelCUDAScale,
			req:  Request{Path: "in.mp4", Pool: sampled, MaxFrames: 2},
			want: "-v error -nostdin -threads 0 -hwaccel cuda -hwaccel_output_format cuda -i in.mp4 -filter_complex " +
				"[0:v:0]scale_cuda=320:180:interp_algo=bicubic:format=yuv420p10le,hwdownload,format=yuv420p10le," +
				branches + outputs,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := strings.Join(NewFFmpeg("ffmpeg", 0).sampledArgs(testCase.req, testCase.mode), " ")
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestSegmentDecoders(
	t *testing.T,
) {
	h264 := Request{Path: "in.mp4", Codec: "h264", PixelFormat: "yuv420p"}

	// Auto means VideoToolbox on macOS only: elsewhere it decodes on the CPU.
	auto := 1
	if runtime.GOOS == darwin {
		auto = runtime.NumCPU()
	}

	testCases := []struct {
		name string
		opts []Option
		req  Request
		want int
	}{
		{name: "cpu: one decode", req: h264, want: 1},
		{name: "nvdec: one decode", opts: []Option{WithHWAccel(HWAccelCUDA)}, req: h264, want: 1},
		{name: "videotoolbox: a session per core", opts: []Option{WithHWAccel(HWAccelVideoToolbox)}, req: h264, want: runtime.NumCPU()},
		{name: "auto", opts: []Option{WithHWAccel(HWAccelAuto)}, req: h264, want: auto},
		{
			name: "fewer sessions",
			opts: []Option{WithHWAccel(HWAccelVideoToolbox), WithVideoToolboxSessions(4)}, req: h264, want: 4,
		},
		{
			name: "a codec videotoolbox leaves to the cpu",
			opts: []Option{WithHWAccel(HWAccelVideoToolbox)},
			req:  Request{Path: "in.mov", Codec: "prores", PixelFormat: "yuv422p10le"}, want: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, NewFFmpeg("ffmpeg", 0, testCase.opts...).SegmentDecoders(testCase.req))
		})
	}
}

func TestVideoToolboxSessions(
	t *testing.T,
) {
	segment := Request{Path: "in.mp4", Codec: "h264", PixelFormat: "yuv420p", Segment: true}

	testCases := []struct {
		name     string
		opts     []Option
		sessions int
	}{
		{name: "explicit", opts: []Option{WithVideoToolboxSessions(2)}, sessions: 2},
		{name: "a session per core by default", sessions: runtime.NumCPU()},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// VideoToolbox explicitly: auto only means it on macOS.
			d := NewFFmpeg("ffmpeg", 0, append([]Option{WithHWAccel(HWAccelVideoToolbox)}, testCase.opts...)...)

			releases := make([]func(), testCase.sessions)
			for i := range releases {
				var mode HWAccel

				mode, releases[i] = d.acquire(segment)
				require.Equal(t, HWAccelVideoToolbox, mode, "session %d", i)
			}

			beyond, releaseBeyond := d.acquire(segment)
			assert.Equal(t, HWAccelNone, beyond, "decodes beyond the sessions run on the cpu")
			releaseBeyond()

			releases[0]()

			again, releaseAgain := d.acquire(segment)
			assert.Equal(t, HWAccelVideoToolbox, again, "a released session is reused")
			releaseAgain()

			for _, release := range releases[1:] {
				release()
			}
		})
	}

	cpu, release := NewFFmpeg("ffmpeg", 0, WithVideoToolboxSessions(1)).acquire(segment)
	assert.Equal(t, HWAccelNone, cpu, "no session without videotoolbox")
	release()
}
