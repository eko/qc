package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestParse(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		file  string
		check func(t *testing.T, info *media.Info)
	}{
		{
			name: "sdr h264 with audio",
			file: "testdata/sdr_h264.json",
			check: func(t *testing.T, info *media.Info) {
				assert.Equal(t, "mov,mp4,m4a,3gp,3g2,mj2", info.Format)
				assert.Equal(t, media.Seconds(60), info.Duration)

				require.Len(t, info.Video, 1)
				video := info.Video[0]
				assert.Equal(t, "h264", video.Codec)
				assert.Equal(t, 1920, video.Width)
				assert.Equal(t, 8, video.BitDepth)
				assert.Equal(t, media.Rational{Num: 30000, Den: 1001}, video.AvgFrameRate)
				assert.Equal(t, media.DynamicRangeSDR, video.HDR.DynamicRange)

				require.Len(t, info.Audio, 1)
				assert.Equal(t, 48000, info.Audio[0].SampleRate)
				assert.Equal(t, "fra", info.Audio[0].Language)
			},
		},
		{
			name: "hdr10 hevc skips cover art",
			file: "testdata/hdr10_hevc.json",
			check: func(t *testing.T, info *media.Info) {
				require.Len(t, info.Video, 1)
				video := info.Video[0]
				assert.Equal(t, 10, video.BitDepth)
				assert.Equal(t, media.DynamicRangeHDR10, video.HDR.DynamicRange)
				require.NotNil(t, video.HDR.MasteringDisplay)
				assert.InDelta(t, 1000.0, video.HDR.MasteringDisplay.MaxLuminance, 1e-9)
				assert.InDelta(t, 0.005, video.HDR.MasteringDisplay.MinLuminance, 1e-9)
				require.NotNil(t, video.HDR.ContentLightLevel)
				assert.Equal(t, 1000, video.HDR.ContentLightLevel.MaxCLL)
				assert.Equal(t, 400, video.HDR.ContentLightLevel.MaxFALL)
			},
		},
		{
			name: "dolby vision",
			file: "testdata/dovi_hevc.json",
			check: func(t *testing.T, info *media.Info) {
				require.Len(t, info.Video, 1)
				hdr := info.Video[0].HDR
				assert.Equal(t, media.DynamicRangeDolbyVision, hdr.DynamicRange)
				require.NotNil(t, hdr.DolbyVision)
				assert.Equal(t, 8, hdr.DolbyVision.Profile)
				assert.True(t, hdr.DolbyVision.RPUPresent)
				assert.Equal(t, 1, hdr.DolbyVision.CompatibilityID)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := os.ReadFile(testCase.file)
			require.NoError(t, err)

			info, err := Parse(data)
			require.NoError(t, err)

			testCase.check(t, info)
		})
	}
}

func TestParseInline(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		json    string
		check   func(t *testing.T, info *media.Info)
		wantErr string
	}{
		{
			name:    "invalid json",
			json:    "{",
			wantErr: "decode ffprobe json",
		},
		{
			name:    "invalid frame rate",
			json:    `{"streams":[{"index":3,"codec_type":"video","r_frame_rate":"x/1"}]}`,
			wantErr: "stream 3",
		},
		{
			name:    "invalid average frame rate",
			json:    `{"streams":[{"codec_type":"video","r_frame_rate":"25/1","avg_frame_rate":"1/y"}]}`,
			wantErr: "parse rational",
		},
		{
			name: "average frame rate falls back to the nominal rate",
			json: `{"streams":[{"codec_type":"video","r_frame_rate":"25/1","avg_frame_rate":"0/0"}]}`,
			check: func(t *testing.T, info *media.Info) {
				require.Len(t, info.Video, 1)
				assert.Equal(t, media.Rational{Num: 25, Den: 1}, info.Video[0].AvgFrameRate)
			},
		},
		{
			name: "hlg",
			json: `{"streams":[{"codec_type":"video","color_transfer":"arib-std-b67"}]}`,
			check: func(t *testing.T, info *media.Info) {
				assert.Equal(t, media.DynamicRangeHLG, info.Video[0].HDR.DynamicRange)
			},
		},
		{
			name: "pq without mastering display",
			json: `{"streams":[{"codec_type":"video","color_transfer":"smpte2084"}]}`,
			check: func(t *testing.T, info *media.Info) {
				assert.Equal(t, media.DynamicRangePQ, info.Video[0].HDR.DynamicRange)
			},
		},
		{
			name: "unknown values are zero and other stream types are ignored",
			json: `{"format":{"duration":"N/A","size":"N/A"},` +
				`"streams":[{"codec_type":"subtitle"},{"codec_type":"data"}]}`,
			check: func(t *testing.T, info *media.Info) {
				assert.Zero(t, info.Duration)
				assert.Zero(t, info.Size)
				assert.Empty(t, info.Video)
				assert.Empty(t, info.Audio)
			},
		},
		{
			name: "invalid mastering luminance is zero",
			json: `{"streams":[{"codec_type":"video","side_data_list":[` +
				`{"side_data_type":"Mastering display metadata","min_luminance":"a/b","max_luminance":"10000000/10000"}]}]}`,
			check: func(t *testing.T, info *media.Info) {
				display := info.Video[0].HDR.MasteringDisplay
				require.NotNil(t, display)
				assert.Zero(t, display.MinLuminance)
				assert.InDelta(t, 1000.0, display.MaxLuminance, 1e-9)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			info, err := Parse([]byte(testCase.json))
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			testCase.check(t, info)
		})
	}
}

func TestBitDepth(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		stream ffprobeStream
		want   int
	}{
		{name: "coded depth wins", stream: ffprobeStream{BitsPerRawSample: "10", PixFmt: "yuv420p"}, want: 10},
		{name: "unknown", stream: ffprobeStream{}, want: 0},
		{name: "8-bit format", stream: ffprobeStream{PixFmt: "yuv420p"}, want: 8},
		{name: "10-bit format", stream: ffprobeStream{PixFmt: "p010le"}, want: 10},
		{name: "12-bit format", stream: ffprobeStream{PixFmt: "yuv444p12le"}, want: 12},
		{name: "invalid coded depth", stream: ffprobeStream{BitsPerRawSample: "N/A", PixFmt: "yuv420p10le"}, want: 10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, bitDepth(testCase.stream))
		})
	}
}

func TestFFprobeProbeFake(
	t *testing.T,
) {
	sdr, err := os.ReadFile("testdata/sdr_h264.json")
	require.NoError(t, err)

	errRun := errors.New("ffprobe crashed")

	testCases := []struct {
		name    string
		out     []byte
		runErr  error
		wantErr string
	}{
		{name: "parsed output", out: sdr},
		{name: "command failure", runErr: errRun, wantErr: "probe in.mp4: ffprobe crashed"},
		{name: "unparsable output", out: []byte("not json"), wantErr: "probe in.mp4: decode ffprobe json"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			prober := NewFFprobe("ffprobe")
			prober.output = func(_ context.Context, bin string, args []string) ([]byte, error) {
				assert.Equal(t, "ffprobe", bin)
				assert.Equal(t, "in.mp4", args[len(args)-1])

				return testCase.out, testCase.runErr
			}

			info, err := prober.Probe(t.Context(), "in.mp4")
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "in.mp4", info.Path)
			assert.Len(t, info.Video, 1)
		})
	}
}

func TestFFprobeProbe(
	t *testing.T,
) {
	path := testutil.Generate(t, testutil.Clip{Seconds: 1})

	info, err := NewFFprobe("ffprobe").Probe(t.Context(), path)
	require.NoError(t, err)

	assert.Equal(t, path, info.Path)
	assert.InDelta(t, 1.0, info.Duration.Seconds(), 0.1)

	video, ok := info.PrimaryVideo()
	require.True(t, ok)
	assert.Equal(t, "h264", video.Codec)
	assert.Equal(t, 320, video.Width)
	assert.Equal(t, 180, video.Height)
	assert.Equal(t, 8, video.BitDepth)
	assert.Equal(t, media.Rational{Num: 25, Den: 1}, video.AvgFrameRate)
	assert.Equal(t, media.DynamicRangeSDR, video.HDR.DynamicRange)
}

func TestFFprobeFirstFrame(
	t *testing.T,
) {
	const (
		pqStream  = `{"streams":[{"index":1,"codec_type":"video","color_transfer":"smpte2084"}]}`
		sdrStream = `{"streams":[{"index":0,"codec_type":"video","color_transfer":"bt709"}]}`
		hdrFrame  = `{"frames":[{"side_data_list":[` +
			`{"side_data_type":"Mastering display metadata","red_x":"34000/50000","red_y":"16000/50000",` +
			`"green_x":"13250/50000","green_y":"34500/50000","blue_x":"7500/50000","blue_y":"3000/50000",` +
			`"white_point_x":"15635/50000","white_point_y":"16450/50000",` +
			`"min_luminance":"1/10000","max_luminance":"10000000/10000"},` +
			`{"side_data_type":"Content light level metadata","max_content":1567,"max_average":972}]}]}`
		hdr10PlusFrame = `{"frames":[{"side_data_list":[` +
			`{"side_data_type":"HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"}]}]}`
	)

	errFrame := errors.New("frame probe failed")

	testCases := []struct {
		name      string
		stream    string
		frame     string
		frameErr  error
		wantCalls int
		wantRange media.DynamicRange
		wantErr   string
		check     func(t *testing.T, hdr media.HDR)
	}{
		{
			name: "sdr needs no frame", stream: sdrStream, wantCalls: 1, wantRange: media.DynamicRangeSDR,
		},
		{
			name: "hdr10 metadata in the bitstream", stream: pqStream, frame: hdrFrame, wantCalls: 2,
			wantRange: media.DynamicRangeHDR10,
			check: func(t *testing.T, hdr media.HDR) {
				require.NotNil(t, hdr.MasteringDisplay)
				assert.InDelta(t, 0.68, hdr.MasteringDisplay.Red.X, 1e-9)
				assert.InDelta(t, 0.69, hdr.MasteringDisplay.Green.Y, 1e-9)
				assert.InDelta(t, 0.06, hdr.MasteringDisplay.Blue.Y, 1e-9)
				assert.InDelta(t, 0.3127, hdr.MasteringDisplay.WhitePoint.X, 1e-9)
				assert.InDelta(t, 0.0001, hdr.MasteringDisplay.MinLuminance, 1e-12)
				require.NotNil(t, hdr.ContentLightLevel)
				assert.Equal(t, media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972}, *hdr.ContentLightLevel)
			},
		},
		{
			name: "hdr10+ dynamic metadata", stream: pqStream, frame: hdr10PlusFrame, wantCalls: 2,
			wantRange: media.DynamicRangeHDR10Plus,
			check: func(t *testing.T, hdr media.HDR) {
				assert.True(t, hdr.HDR10Plus)
			},
		},
		{name: "frame probe failure", stream: pqStream, frameErr: errFrame, wantErr: "first frame: frame probe failed"},
		{name: "unparsable frame output", stream: pqStream, frame: "{", wantErr: "decode ffprobe frame json"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			calls := 0
			prober := NewFFprobe("ffprobe")
			prober.output = func(_ context.Context, _ string, args []string) ([]byte, error) {
				calls++
				if calls == 1 {
					return []byte(testCase.stream), nil
				}

				assert.Contains(t, args, "-show_frames")
				assert.Contains(t, args, "1")

				return []byte(testCase.frame), testCase.frameErr
			}

			info, err := prober.Probe(t.Context(), "in.mp4")
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantCalls, calls)
			assert.Equal(t, testCase.wantRange, info.Video[0].HDR.DynamicRange)

			if testCase.check != nil {
				testCase.check(t, info.Video[0].HDR)
			}
		})
	}
}

func TestFFprobeProbeHDR(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		transfer  string
		wantRange media.DynamicRange
	}{
		{name: "hdr10 from the sei of an mp4", transfer: media.TransferPQ, wantRange: media.DynamicRangeHDR10},
		{name: "hlg", transfer: media.TransferHLG, wantRange: media.DynamicRangeHLG},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			clip := testutil.HDRClip(testCase.transfer)
			clip.Seconds = 0.4
			path := testutil.Generate(t, clip)

			info, err := NewFFprobe("ffprobe").Probe(t.Context(), path)
			require.NoError(t, err)

			video := info.Video[0]
			assert.Equal(t, testCase.wantRange, video.HDR.DynamicRange)
			assert.True(t, video.MeasurableHDR())
			assert.Equal(t, "bt2020", video.Color.Primaries)
			assert.Equal(t, 10, video.BitDepth)
		})
	}
}

func TestFFprobeFirstFrameWithoutVideo(
	t *testing.T,
) {
	calls := 0
	prober := NewFFprobe("ffprobe")
	prober.output = func(context.Context, string, []string) ([]byte, error) {
		calls++

		return []byte(`{"streams":[{"codec_type":"audio"}]}`), nil
	}

	info, err := prober.Probe(t.Context(), "in.m4a")
	require.NoError(t, err)
	assert.Empty(t, info.Video)
	assert.Equal(t, 1, calls)
}

func TestFFprobeFirstFrameIsCachedPerFileVersion(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "hdr.mp4")
	require.NoError(t, os.WriteFile(path, []byte("v1"), 0o600))

	frameCalls := 0
	prober := NewFFprobe("ffprobe")
	prober.output = func(_ context.Context, _ string, args []string) ([]byte, error) {
		if slices.Contains(args, "-show_frames") {
			frameCalls++

			return []byte(`{"frames":[{"side_data_list":[{"side_data_type":"Content light level metadata","max_content":1000,"max_average":400}]}]}`), nil
		}

		return []byte(`{"streams":[{"index":0,"codec_type":"video","color_transfer":"smpte2084"}]}`), nil
	}

	probeCLL := func() int {
		info, err := prober.Probe(t.Context(), path)
		require.NoError(t, err)

		return info.Video[0].HDR.ContentLightLevel.MaxCLL
	}

	assert.Equal(t, 1000, probeCLL())
	assert.Equal(t, 1000, probeCLL())
	assert.Equal(t, 1, frameCalls, "the same file is probed once")

	require.NoError(t, os.WriteFile(path, []byte("version 2"), 0o600))
	assert.Equal(t, 1000, probeCLL())
	assert.Equal(t, 2, frameCalls, "a changed file is probed again")
}
