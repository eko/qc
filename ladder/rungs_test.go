package ladder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

func TestRoundCRF(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		codec string
		in    float64
		want  float64
	}{
		{name: "x264 half steps", codec: "h264", in: 27.3, want: 27.5},
		{name: "x265 half steps", codec: "hevc", in: 27.2, want: 27},
		{name: "svt-av1 integers", codec: "av1", in: 40.6, want: 41},
		{name: "clamped above", codec: "h264", in: 70, want: 51},
		{name: "clamped below", codec: "av1", in: 3, want: 10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := &build{codec: mustCodec(t, testCase.codec)}
			assert.InDelta(t, testCase.want, b.roundCRF(testCase.in), 1e-9)
		})
	}
}

func TestCurveFor(
	t *testing.T,
) {
	curves := []Curve{
		NewCurve([]Probe{{Width: 1920, Height: 1080, CRF: 20, Bitrate: 5_000_000, VMAF: 95}, {Width: 1920, Height: 1080, CRF: 27, Bitrate: 2_500_000, VMAF: 88}}),
		NewCurve([]Probe{{Width: 1280, Height: 720, CRF: 20, Bitrate: 3_000_000, VMAF: 91}, {Width: 1280, Height: 720, CRF: 34, Bitrate: 800_000, VMAF: 70}}),
		NewCurve([]Probe{{Width: 640, Height: 360, CRF: 20, Bitrate: 900_000, VMAF: 75}, {Width: 640, Height: 360, CRF: 34, Bitrate: 200_000, VMAF: 50}}),
	}

	testCases := []struct {
		name       string
		target     Target
		wantHeight int
	}{
		{name: "target resolution covers the bitrate", target: Target{Height: 1080, Bitrate: 3_000_000}, wantHeight: 1080},
		{name: "next resolution down covers the bitrate", target: Target{Height: 1080, Bitrate: 1_000_000}, wantHeight: 720},
		{name: "no curve covers: highest not above the target", target: Target{Height: 720, Bitrate: 100_000}, wantHeight: 720},
		{name: "target below every curve: lowest curve", target: Target{Height: 240, Bitrate: 500_000}, wantHeight: 360},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.wantHeight, curveFor(curves, testCase.target).Height)
		})
	}
}

func TestRungs(
	t *testing.T,
) {
	curves := []Curve{
		NewCurve([]Probe{
			{Width: 1280, Height: 720, CRF: 20, Bitrate: 4_000_000, VMAF: 92},
			{Width: 1280, Height: 720, CRF: 27, Bitrate: 2_000_000, VMAF: 85},
		}),
	}
	b := &build{
		codec:  mustCodec(t, "h264"),
		source: "my title.mov",
		video:  media.VideoStream{Width: 1280, Height: 720, AvgFrameRate: media.Rational{Num: 25, Den: 1}},
		opts:   Options{Preset: "fast", GOPDuration: media.Seconds(2)},
	}

	rungs := b.rungs([]Target{
		{Height: 720, Bitrate: 2_000_000, VMAF: 85},
		{Height: 720, Bitrate: 1_000_000, VMAF: 77},
	}, curves)
	require.Len(t, rungs, 2)

	testCases := []struct {
		name          string
		rung          Rung
		wantCRF       float64
		wantPredicted float64
		wantOutput    string
	}{
		{name: "on the curve", rung: rungs[0], wantCRF: 27, wantPredicted: 85, wantOutput: "01-720p.mp4"},
		{name: "below the curve: CRF extrapolated, envelope quality", rung: rungs[1], wantCRF: 34, wantPredicted: 77, wantOutput: "02-720p.mp4"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := testCase.rung

			assert.Equal(t, 1280, r.Width)
			assert.InDelta(t, testCase.wantCRF, r.CRF, 1e-9)
			assert.InDelta(t, testCase.wantPredicted, r.PredictedVMAF, 1e-9)
			assert.Equal(t, 2*r.Bitrate, r.MaxRate)
			assert.Equal(t, 4*r.Bitrate, r.BufSize)
			assert.True(t, strings.HasPrefix(r.Command, "ffmpeg -i 'my title.mov' "), r.Command)
			assert.True(t, strings.HasSuffix(r.Command, " "+testCase.wantOutput), r.Command)
			assert.Contains(t, r.Command, "-crf "+formatCRF(testCase.wantCRF)+" ")
		})
	}
}
