package encode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
)

func TestDigestArgs(
	t *testing.T,
) {
	segments := []media.Interval{
		{Start: media.Seconds(1), End: media.Seconds(3)},
		{Start: media.Seconds(10.5), End: media.Seconds(12.5)},
	}
	rate := media.Rational{Num: 30000, Den: 1001}

	testCases := []struct {
		name     string
		bitDepth int
		lossless bool
		want     []string
		notWant  []string
	}{
		{
			name:     "raw 8-bit",
			bitDepth: 8,
			want:     []string{"rawvideo", "nut"},
			notWant:  []string{"ffv1"},
		},
		{
			name:     "lossless 10-bit",
			bitDepth: 10,
			lossless: true,
			want:     []string{"ffv1", "-slices", "16"},
			notWant:  []string{"rawvideo", "nut"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			args := digestArgs(DigestSpec{
				Source: "in.mov", Destination: "out.nut", Segments: segments, Rate: rate,
				BitDepth: testCase.bitDepth, Lossless: testCase.lossless,
			})

			assert.Equal(t, "out.nut", args[len(args)-1])
			assert.Equal(t, []string{
				"-ss", "1.000000", "-t", "2.000000", "-i", "in.mov",
				"-ss", "10.500000", "-t", "2.000000", "-i", "in.mov",
			}, args[4:16])

			format := pixelFormat(testCase.bitDepth)
			assert.Contains(t, args,
				"[0:v:0]format="+format+",setsar=1[v0];[1:v:0]format="+format+",setsar=1[v1];"+
					"[v0][v1]concat=n=2:v=1:a=0,setpts=N/(30000/1001*TB)[out]")
			assert.Contains(t, args, "30000/1001")

			for _, w := range testCase.want {
				assert.Contains(t, args, w)
			}

			for _, w := range testCase.notWant {
				assert.NotContains(t, args, w)
			}
		})
	}
}

func TestFFmpegErrors(
	t *testing.T,
) {
	segment := []media.Interval{{Start: 0, End: media.Seconds(1)}}
	missing := NewFFmpeg("qc-no-such-ffmpeg")

	testCases := []struct {
		name    string
		run     func(f *FFmpeg) error
		wantErr error
		wantMsg string
	}{
		{
			name: "encode with a missing binary",
			run: func(f *FFmpeg) error {
				return f.Encode(t.Context(), codecs["h264"], "in.mov", "out.mp4", Params{Width: 2, Height: 2})
			},
			wantErr: ffexec.ErrNotFound,
			wantMsg: "encode in.mov with libx264",
		},
		{
			name: "digest with a missing binary",
			run: func(f *FFmpeg) error {
				return f.Digest(t.Context(), DigestSpec{Source: "in.mov", Destination: "out.nut", Segments: segment, Rate: media.Rational{Num: 25, Den: 1}, BitDepth: 8})
			},
			wantErr: ffexec.ErrNotFound,
			wantMsg: "digest in.mov",
		},
		{
			name: "digest without segments",
			run: func(f *FFmpeg) error {
				return f.Digest(t.Context(), DigestSpec{Source: "in.mov", Destination: "out.nut", Rate: media.Rational{Num: 25, Den: 1}, BitDepth: 8})
			},
			wantErr: ErrNoSegments,
			wantMsg: "digest in.mov",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.run(missing)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Contains(t, err.Error(), testCase.wantMsg)
		})
	}
}

func TestDiscard(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		fail    bool
		wantErr bool
	}{
		{name: "drains the output", input: "frames"},
		{name: "reports read errors", fail: true, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			reader := iotest.ErrReader(os.ErrClosed)
			if !testCase.fail {
				reader = strings.NewReader(testCase.input)
			}

			err := discard(reader)
			if testCase.wantErr {
				require.ErrorIs(t, err, os.ErrClosed)

				return
			}

			require.NoError(t, err)
		})
	}
}

// TestFFmpegIntegration runs the real encoders on a tiny clip. It skips
// without ffmpeg, and per codec when the encoder is not built in.
func TestFFmpegIntegration(
	t *testing.T,
) {
	src := testutil.Generate(t, testutil.Clip{Width: 128, Height: 72, Seconds: 1, GOP: 5})
	encoders := availableEncoders(t)
	prober := probe.NewFFprobe("ffprobe")
	ffmpeg := NewFFmpeg("ffmpeg")
	rate := media.Rational{Num: 25, Den: 1}

	t.Run("digest", func(t *testing.T) {
		segments := []media.Interval{
			{Start: 0, End: media.Seconds(0.2)},
			{Start: media.Seconds(0.6), End: media.Seconds(0.8)},
		}

		testCases := []struct {
			name      string
			lossless  bool
			bitDepth  int
			file      string
			wantCodec string
		}{
			{name: "raw 8-bit", bitDepth: 8, file: "digest.nut", wantCodec: "rawvideo"},
			{name: "lossless 10-bit", bitDepth: 10, lossless: true, file: "digest.mkv", wantCodec: "ffv1"},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				dst := filepath.Join(t.TempDir(), testCase.file)
				require.NoError(t, ffmpeg.Digest(t.Context(), DigestSpec{
					Source: src, Destination: dst, Segments: segments, Rate: rate,
					BitDepth: testCase.bitDepth, Lossless: testCase.lossless,
				}))

				info, err := prober.Probe(t.Context(), dst)
				require.NoError(t, err)

				video, ok := info.PrimaryVideo()
				require.True(t, ok)
				assert.Equal(t, testCase.wantCodec, video.Codec)
				assert.Equal(t, testCase.bitDepth, video.BitDepth)
				assert.Equal(t, 128, video.Width)
				assert.InDelta(t, 0.4, info.Duration.Seconds(), 0.05, "two 0.2 s segments")
			})
		}
	})

	t.Run("encode", func(t *testing.T) {
		testCases := []struct {
			name      string
			codec     string
			bitDepth  int
			wantCodec string
		}{
			{name: "h264 8-bit", codec: "h264", bitDepth: 8, wantCodec: "h264"},
			{name: "h264 10-bit", codec: "h264", bitDepth: 10, wantCodec: "h264"},
			{name: "hevc 8-bit", codec: "hevc", bitDepth: 8, wantCodec: "hevc"},
			{name: "hevc 10-bit", codec: "hevc", bitDepth: 10, wantCodec: "hevc"},
			{name: "av1 8-bit", codec: "av1", bitDepth: 8, wantCodec: "av1"},
			{name: "av1 10-bit", codec: "av1", bitDepth: 10, wantCodec: "av1"},
		}

		for _, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				codec := codecs[testCase.codec]
				if !encoders[codec.Encoder] {
					t.Skipf("%s not available", codec.Encoder)
				}

				dst := filepath.Join(t.TempDir(), "out.mp4")
				params := Params{
					Width: 64, Height: 36, CRF: codec.ProbeCRFs[1], Preset: fastestPreset(codec),
					GOP: 5, MaxRate: 400_000, BufSize: 800_000, BitDepth: testCase.bitDepth,
				}
				require.NoError(t, ffmpeg.Encode(t.Context(), codec, src, dst, params))

				info, err := prober.Probe(t.Context(), dst)
				require.NoError(t, err)

				video, ok := info.PrimaryVideo()
				require.True(t, ok)
				assert.Equal(t, testCase.wantCodec, video.Codec)
				assert.Equal(t, 64, video.Width)
				assert.Equal(t, 36, video.Height)
				assert.Equal(t, testCase.bitDepth, video.BitDepth)
			})
		}
	})

	t.Run("encode failure carries ffmpeg's message", func(t *testing.T) {
		err := ffmpeg.Encode(t.Context(), codecs["h264"], filepath.Join(t.TempDir(), "missing.mov"), "out.mp4", Params{Width: 64, Height: 36})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "encode")
	})
}

func fastestPreset(
	codec Codec,
) string {
	switch codec.Name {
	case "av1":
		return "12"
	default:
		return "ultrafast"
	}
}

// availableEncoders lists the video encoders of the local ffmpeg.
func availableEncoders(
	t *testing.T,
) map[string]bool {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-encoders").Output()
	require.NoError(t, err)

	encoders := map[string]bool{}

	for line := range strings.Lines(string(out)) {
		if fields := strings.Fields(line); len(fields) >= 2 && strings.HasPrefix(fields[0], "V") {
			encoders[fields[1]] = true
		}
	}

	return encoders
}

// TestEncodeCarriesHDRSignal encodes an untagged raw 10-bit clip (as the
// ladder's digest is) with the HDR10 signal of its source, and checks what
// ffprobe reads back: the colour description, and the mastering display
// and content light level (SEI for x265, metadata OBUs for SVT-AV1).
func TestEncodeCarriesHDRSignal(
	t *testing.T,
) {
	raw := testutil.Generate(t, testutil.Clip{Seconds: 0.4, PixelFormat: "yuv420p10le", Codec: "rawvideo", Name: "raw.nut"})

	testCases := []struct {
		codec     string
		signal    Signal
		wantRange media.DynamicRange
		wantCLL   int
	}{
		{codec: "hevc", signal: hdr10, wantRange: media.DynamicRangeHDR10, wantCLL: 1567},
		{codec: "av1", signal: hdr10, wantRange: media.DynamicRangeHDR10, wantCLL: 1567},
		{codec: "h264", signal: Signal{Color: bt2100HLG}, wantRange: media.DynamicRangeHLG},
	}

	for _, testCase := range testCases {
		t.Run(testCase.codec, func(t *testing.T) {
			codec, err := Lookup(testCase.codec)
			require.NoError(t, err)

			out := filepath.Join(t.TempDir(), "out.mp4")
			p := Params{Width: 320, Height: 180, CRF: 30, Preset: fastestPreset(codec), GOP: 5, BitDepth: 10, Signal: testCase.signal}
			require.NoError(t, NewFFmpeg("ffmpeg").Encode(t.Context(), codec, raw, out, p))

			info, err := probe.NewFFprobe("ffprobe").Probe(t.Context(), out)
			require.NoError(t, err)

			video := info.Video[0]
			assert.Equal(t, testCase.signal.Color.Primaries, video.Color.Primaries)
			assert.Equal(t, testCase.signal.Color.Transfer, video.Color.Transfer)
			assert.Equal(t, testCase.signal.Color.Space, video.Color.Space)
			assert.Equal(t, testCase.wantRange, video.HDR.DynamicRange)

			if testCase.wantCLL > 0 {
				require.NotNil(t, video.HDR.ContentLightLevel)
				assert.Equal(t, testCase.wantCLL, video.HDR.ContentLightLevel.MaxCLL)
				require.NotNil(t, video.HDR.MasteringDisplay)
				assert.InDelta(t, 1000, video.HDR.MasteringDisplay.MaxLuminance, 1e-6)
				assert.InDelta(t, 0.265, video.HDR.MasteringDisplay.Green.X, 1e-4)
			}
		})
	}
}
