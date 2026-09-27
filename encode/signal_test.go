package encode

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

// p3d65 is a P3 D65 mastering display of 1000 cd/m².
var p3d65 = &media.MasteringDisplay{
	MinLuminance: 0.0001, MaxLuminance: 1000,
	Red: media.Chromaticity{X: 0.68, Y: 0.32}, Green: media.Chromaticity{X: 0.265, Y: 0.69},
	Blue: media.Chromaticity{X: 0.15, Y: 0.06}, WhitePoint: media.Chromaticity{X: 0.3127, Y: 0.329},
}

var (
	bt2100PQ  = media.Color{Primaries: "bt2020", Transfer: media.TransferPQ, Space: "bt2020nc", Range: "tv"}
	bt2100HLG = media.Color{Primaries: "bt2020", Transfer: media.TransferHLG, Space: "bt2020nc", Range: "tv"}
	hdr10     = Signal{Color: bt2100PQ, Mastering: p3d65, ContentLight: &media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972}}
)

func TestSignalOf(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		video media.VideoStream
		want  Signal
	}{
		{name: "sdr carries nothing", video: media.VideoStream{Color: media.Color{Transfer: "bt709"}}},
		{
			name: "hdr10",
			video: media.VideoStream{Color: bt2100PQ, HDR: media.HDR{
				MasteringDisplay: p3d65, ContentLightLevel: &media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972},
			}},
			want: hdr10,
		},
		{
			name:  "unknown content light level left out",
			video: media.VideoStream{Color: bt2100PQ, HDR: media.HDR{MasteringDisplay: p3d65, ContentLightLevel: &media.ContentLightLevel{}}},
			want:  Signal{Color: bt2100PQ, Mastering: p3d65},
		},
		{name: "hlg", video: media.VideoStream{Color: bt2100HLG}, want: Signal{Color: bt2100HLG}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := SignalOf(testCase.video)
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.want == Signal{}, got.IsZero())
		})
	}
}

func TestSignalArgs(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		codec     string
		hw        Hardware
		p         Params
		wantVF    string
		wantParam string
		absent    string
	}{
		{
			name: "x265 hdr10", codec: "hevc", p: Params{Width: 1280, Height: 720, GOP: 48, BitDepth: 10, Signal: hdr10},
			wantVF: "scale=1280:720:flags=bicubic,format=yuv420p10le," +
				"setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc:range=tv",
			wantParam: "-x265-params log-level=error:scenecut=0:hdr10-opt=1:hdr10=1:" +
				"master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1567,972",
		},
		{
			name: "x265 pq without metadata", codec: "hevc", p: Params{Width: 640, Height: 360, BitDepth: 10, Signal: Signal{Color: bt2100PQ}},
			wantParam: "-x265-params log-level=error:hdr10-opt=1", absent: "master-display",
		},
		{
			name: "x265 hlg: colour tags only", codec: "hevc", p: Params{Width: 640, Height: 360, BitDepth: 10, Signal: Signal{Color: bt2100HLG}},
			wantVF:    "setparams=color_primaries=bt2020:color_trc=arib-std-b67:colorspace=bt2020nc:range=tv",
			wantParam: "-x265-params log-level=error", absent: "hdr10",
		},
		{
			name: "svt-av1 hdr10 with film grain", codec: "av1", p: Params{Width: 640, Height: 360, BitDepth: 10, FilmGrain: 8, Signal: hdr10},
			wantParam: "-svtav1-params film-grain=8:film-grain-denoise=1:" +
				"mastering-display=G(0.2650,0.6900)B(0.1500,0.0600)R(0.6800,0.3200)WP(0.3127,0.3290)L(1000.0000,0.0001):content-light=1567,972",
		},
		{
			name: "svt-av1 hlg: no private parameter", codec: "av1", p: Params{Width: 640, Height: 360, BitDepth: 10, Signal: Signal{Color: bt2100HLG}},
			absent: "-svtav1-params",
		},
		{
			name: "x264: colour tags only", codec: "h264", p: Params{Width: 640, Height: 360, BitDepth: 10, Signal: hdr10},
			wantVF: "setparams=color_primaries=bt2020", absent: "master-display",
		},
		{
			name: "nvenc: colour tags only", codec: "hevc", hw: HardwareNVENC, p: Params{Width: 640, Height: 360, BitDepth: 10, Signal: hdr10},
			wantVF: "format=p010le,setparams=", absent: "master-display",
		},
		{
			name: "sdr is untouched", codec: "hevc", p: Params{Width: 640, Height: 360},
			wantVF: "scale=640:360:flags=bicubic,format=yuv420p", absent: "setparams",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := LookupFor(testCase.codec, testCase.hw)
			require.NoError(t, err)

			args := strings.Join(codec.Args(testCase.p), " ")

			assert.Contains(t, args, testCase.wantVF)
			assert.Contains(t, args, testCase.wantParam)

			if testCase.absent != "" {
				assert.NotContains(t, args, testCase.absent)
			}
		})
	}
}

func TestSignalSetParamsPartial(
	t *testing.T,
) {
	assert.Empty(t, Signal{}.setParams())
	assert.Equal(t, "setparams=color_trc=smpte2084", Signal{Color: media.Color{Transfer: media.TransferPQ}}.setParams())
}
