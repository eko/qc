package ladder

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

func TestGeometry(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		source    media.VideoStream
		height    int
		wantWidth int
	}{
		{name: "16:9", source: media.VideoStream{Width: 1920, Height: 1080}, height: 720, wantWidth: 1280},
		{name: "width rounded to even", source: media.VideoStream{Width: 1920, Height: 1080}, height: 270, wantWidth: 480},
		{name: "odd width rounds to the nearest even", source: media.VideoStream{Width: 1998, Height: 1080}, height: 360, wantWidth: 666},
		{name: "scope", source: media.VideoStream{Width: 2048, Height: 858}, height: 540, wantWidth: 1288},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := &build{video: testCase.source}

			w, h := b.geometry(testCase.height)
			assert.Equal(t, testCase.wantWidth, w)
			assert.Equal(t, testCase.height, h)
		})
	}
}

func TestHeights(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		source  int
		heights []int
		want    []int
	}{
		{name: "candidates above the source are dropped", source: 1080, heights: DefaultHeights(), want: []int{1080, 720, 540, 360, 270}},
		{name: "duplicates are probed once", source: 720, heights: []int{720, 360, 720}, want: []int{720, 360}},
		{name: "source below every candidate", source: 240, heights: DefaultHeights(), want: []int{240}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := &build{video: media.VideoStream{Height: testCase.source}, opts: Options{Heights: testCase.heights}}
			assert.Equal(t, testCase.want, b.heights())
		})
	}
}

func TestExtraProbes(
	t *testing.T,
) {
	top := []Probe{
		{Width: 1920, Height: 1080, CRF: 20, Bitrate: 5_000_000, VMAF: 93},
		{Width: 1920, Height: 1080, CRF: 27, Bitrate: 2_000_000, VMAF: 88},
	}

	// lower returns 720p probes whose top one reaches vmaf at bitrate.
	lower := func(bitrate int64, vmaf float64) []Probe {
		return []Probe{
			{Width: 1280, Height: 720, CRF: 20, Bitrate: bitrate, VMAF: vmaf},
			{Width: 1280, Height: 720, CRF: 27, Bitrate: bitrate / 2, VMAF: vmaf - 8},
		}
	}

	h264 := mustCodec(t, "h264")
	extra1080 := Probe{Width: 1920, Height: 1080, CRF: 13, Extra: true}
	extra720 := Probe{Width: 1280, Height: 720, CRF: 13, Extra: true}

	// The 720p curves rise 8 VMAF per doubling of bitrate at their top;
	// 1080p reaches 93 at 5 Mb/s, and 95 at about 7.2 Mb/s extrapolated.
	testCases := []struct {
		name   string
		codec  encode.Codec
		top    float64
		probes []Probe
		want   []Probe
	}{
		{
			name:   "top resolution short of the top quality, 720p might reach it cheaper",
			codec:  h264,
			top:    95,
			probes: slices.Concat(top, lower(2_500_000, 89)),
			want:   []Probe{extra1080, extra720},
		},
		{
			name:   "top resolution short of the top quality, 720p hopeless even extrapolated",
			codec:  h264,
			top:    95,
			probes: slices.Concat(top, lower(1_000_000, 70)),
			want:   []Probe{extra1080},
		},
		{
			name:   "top resolution reaches the top quality, 720p might reach it cheaper",
			codec:  h264,
			top:    93,
			probes: slices.Concat(top, lower(2_500_000, 89)),
			want:   []Probe{extra720},
		},
		{
			name:   "720p cannot beat the top rung even extrapolated",
			codec:  h264,
			top:    93,
			probes: slices.Concat(top, lower(2_500_000, 84)),
		},
		{
			name:   "720p above the top quality already",
			codec:  h264,
			top:    93,
			probes: slices.Concat(top, lower(2_500_000, 96)),
		},
		{
			name:  "no probes",
			codec: h264,
			top:   95,
		},
		{
			name:   "extra CRF below the codec range",
			codec:  encode.Codec{Name: "h264", MinCRF: 15, MaxCRF: 51},
			top:    95,
			probes: slices.Concat(top, lower(2_500_000, 89)),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := &build{
				codec: testCase.codec,
				video: media.VideoStream{Width: 1920, Height: 1080},
				opts:  Options{Constraints: Constraints{TopVMAF: testCase.top}},
			}

			assert.Equal(t, testCase.want, b.extraProbes(testCase.probes))
		})
	}
}

func TestGOP(
	t *testing.T,
) {
	testCases := []struct {
		name string
		rate media.Rational
		gop  media.Duration
		want int
	}{
		{name: "2 s at 25 fps", rate: media.Rational{Num: 25, Den: 1}, gop: media.Seconds(2), want: 50},
		{name: "2 s at 29.97 fps", rate: media.Rational{Num: 30000, Den: 1001}, gop: media.Seconds(2), want: 60},
		{name: "at least one frame", rate: media.Rational{Num: 25, Den: 1}, gop: media.Seconds(0.001), want: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := &build{video: media.VideoStream{AvgFrameRate: testCase.rate}, opts: Options{GOPDuration: testCase.gop}}
			assert.Equal(t, testCase.want, b.gop())
		})
	}
}

func TestCurvesOf(
	t *testing.T,
) {
	probes := []Probe{
		{Height: 360, CRF: 20, Bitrate: 500_000, VMAF: 70},
		{Height: 1080, CRF: 20, Bitrate: 5_000_000, VMAF: 95},
		{Height: 720, CRF: 20, Bitrate: 2_000_000, VMAF: 90},
		{Height: 1080, CRF: 27, Bitrate: 2_500_000, VMAF: 88},
	}

	curves := curvesOf(probes)

	heights := make([]int, len(curves))
	for i, c := range curves {
		heights[i] = c.Height
	}

	assert.Equal(t, []int{1080, 720, 360}, heights, "highest resolution first")

	lo, hi := curves[0].Range()
	assert.InDelta(t, 2_500_000, lo, 1)
	assert.InDelta(t, 5_000_000, hi, 1)
}

func TestProjectBitrate(
	t *testing.T,
) {
	// curve builds a 720p curve through (bitrate, VMAF) points.
	curve := func(points ...[2]float64) Curve {
		probes := make([]Probe, len(points))
		for i, p := range points {
			probes[i] = Probe{Width: 1280, Height: 720, Bitrate: int64(p[0]), VMAF: p[1]}
		}

		return NewCurve(probes)
	}

	testCases := []struct {
		name   string
		curve  Curve
		target float64
		want   float64
		wantOK bool
	}{
		{name: "a single probe", curve: curve([2]float64{1e6, 80}), target: 95},
		{name: "flat top", curve: curve([2]float64{1e6, 80}, [2]float64{2e6, 80}), target: 95},
		{
			name:   "two probes: straight extension",
			curve:  curve([2]float64{1e6, 80}, [2]float64{2e6, 88}),
			target: 92,
			want:   2e6 * math.Sqrt2,
			wantOK: true,
		},
		{
			name:   "concave: the slope halves at every step",
			curve:  curve([2]float64{1e6, 72}, [2]float64{2e6, 88}, [2]float64{4e6, 96}),
			target: 98,
			want:   4e6 * math.Sqrt2,
			wantOK: true,
		},
		{
			name:   "concave: levels off below the target",
			curve:  curve([2]float64{1e6, 72}, [2]float64{2e6, 88}, [2]float64{4e6, 96}),
			target: 105,
		},
		{
			name:   "a flat middle segment: straight extension",
			curve:  curve([2]float64{1e6, 80}, [2]float64{2e6, 80}, [2]float64{4e6, 88}),
			target: 92,
			want:   4e6 * math.Sqrt2,
			wantOK: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := projectBitrate(testCase.curve, testCase.target)
			assert.Equal(t, testCase.wantOK, ok)
			assert.InDelta(t, testCase.want, got, 1)
		})
	}
}
