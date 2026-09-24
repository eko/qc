package ladder

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// synthetic returns probes of a resolution whose quality saturates at ceiling
// and needs more bitrate (scale) as resolution grows.
func synthetic(
	height int,
	ceiling, scale float64,
	crfs ...float64,
) []Probe {
	probes := make([]Probe, len(crfs))
	for i, crf := range crfs {
		bitrate := scale * math.Exp(-0.11*(crf-23))
		probes[i] = Probe{
			Width: height * 16 / 9, Height: height, CRF: crf,
			Bitrate: int64(bitrate),
			VMAF:    ceiling * (1 - math.Exp(-bitrate/(scale*0.4))),
		}
	}

	return probes
}

func TestIsotonic(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   []float64
		want []float64
	}{
		{name: "already sorted", in: []float64{1, 2, 3}, want: []float64{1, 2, 3}},
		{name: "one inversion is pooled", in: []float64{1, 3, 2, 4}, want: []float64{1, 2.5, 2.5, 4}},
		{name: "empty", in: nil, want: []float64{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, isotonic(testCase.in))
		})
	}
}

func TestCurve(
	t *testing.T,
) {
	c := NewCurve([]Probe{
		{Height: 720, CRF: 34, Bitrate: 500_000, VMAF: 60},
		{Height: 720, CRF: 20, Bitrate: 4_000_000, VMAF: 92},
		{Height: 720, CRF: 27, Bitrate: 1_414_214, VMAF: 80},
	})

	v, ok := c.VMAFAt(1_414_214)
	require.True(t, ok)
	assert.InDelta(t, 80, v, 1e-6)

	v, ok = c.VMAFAt(840_896)
	require.True(t, ok)
	assert.InDelta(t, 70, v, 0.5, "halfway in log scale between 500k and 1.41M")

	_, ok = c.VMAFAt(8_000_000)
	assert.False(t, ok, "no extrapolation of quality")

	assert.InDelta(t, 27, c.CRFAt(1_414_214), 1e-6)
	assert.InDelta(t, 43.33, c.CRFAt(125_000), 0.05, "CRF extrapolates linearly in log bitrate")
}

func TestEnvelopeAndRungs(
	t *testing.T,
) {
	crfs := []float64{18, 23, 28, 33, 38}

	var curves []Curve
	for _, r := range []struct {
		h       int
		ceiling float64
		scale   float64
	}{{1080, 99, 6e6}, {720, 94, 3e6}, {360, 78, 0.8e6}} {
		curves = append(curves, NewCurve(synthetic(r.h, r.ceiling, r.scale, crfs...)))
	}

	hull := Envelope(curves, 200)
	require.NotEmpty(t, hull)

	assert.Equal(t, 360, hull[0].Height, "low bitrates favour low resolutions")
	assert.Equal(t, 1080, hull[len(hull)-1].Height, "high bitrates favour high resolutions")

	for i := 1; i < len(hull); i++ {
		assert.GreaterOrEqual(t, hull[i].Bitrate, hull[i-1].Bitrate)
	}

	rungs := SelectRungs(hull, Constraints{})
	require.GreaterOrEqual(t, len(rungs), 3)

	assert.GreaterOrEqual(t, rungs[0].VMAF, 95.0)
	assert.Equal(t, 1080, rungs[0].Height)

	for i := 1; i < len(rungs); i++ {
		ratio := float64(rungs[i-1].Bitrate) / float64(rungs[i].Bitrate)
		assert.GreaterOrEqual(t, ratio, 1.5-1e-3, "rung %d", i)
		assert.LessOrEqual(t, ratio, 2.5+1e-3, "rung %d", i)
		assert.LessOrEqual(t, rungs[i].Height, rungs[i-1].Height)
		assert.GreaterOrEqual(t, rungs[i].VMAF, 30.0)
		assert.GreaterOrEqual(t, rungs[i].Bitrate, int64(145_000))
	}
}

func TestSelectRungsCaps(
	t *testing.T,
) {
	curves := []Curve{NewCurve(synthetic(1080, 99, 6e6, 18, 23, 28, 33, 38))}
	hull := Envelope(curves, 100)

	rungs := SelectRungs(hull, Constraints{MaxBitrate: 3_000_000, MaxRungs: 3})

	require.Len(t, rungs, 3)
	assert.Equal(t, int64(3_000_000), rungs[0].Bitrate)
}

func TestInterpolate(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		xs, ys      []float64
		x           float64
		extrapolate bool
		want        float64
		wantOK      bool
	}{
		{name: "empty", x: 1},
		{name: "single point at its x", xs: []float64{2}, ys: []float64{5}, x: 2, want: 5, wantOK: true},
		{name: "single point elsewhere", xs: []float64{2}, ys: []float64{5}, x: 3, want: 5},
		{name: "single point extrapolated flat", xs: []float64{2}, ys: []float64{5}, x: 3, extrapolate: true, want: 5, wantOK: true},
		{name: "first point", xs: []float64{1, 2}, ys: []float64{10, 20}, x: 1, want: 10, wantOK: true},
		{name: "between points", xs: []float64{1, 2}, ys: []float64{10, 20}, x: 1.25, want: 12.5, wantOK: true},
		{name: "last point", xs: []float64{1, 2}, ys: []float64{10, 20}, x: 2, want: 20, wantOK: true},
		{name: "below without extrapolation", xs: []float64{1, 2}, ys: []float64{10, 20}, x: 0},
		{name: "above without extrapolation", xs: []float64{1, 2}, ys: []float64{10, 20}, x: 3},
		{name: "below extrapolated", xs: []float64{1, 2}, ys: []float64{10, 20}, x: 0, extrapolate: true, want: 0, wantOK: true},
		{name: "above extrapolated", xs: []float64{1, 2}, ys: []float64{10, 20}, x: 3, extrapolate: true, want: 30, wantOK: true},
		{name: "duplicate abscissa", xs: []float64{1, 2, 2}, ys: []float64{10, 20, 25}, x: 2, want: 20, wantOK: true},
		{name: "duplicate abscissa at the end", xs: []float64{1, 1}, ys: []float64{10, 20}, x: 1.5, extrapolate: true, want: 20, wantOK: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := interpolate(testCase.xs, testCase.ys, testCase.x, testCase.extrapolate)
			assert.Equal(t, testCase.wantOK, ok)
			assert.InDelta(t, testCase.want, got, 1e-9)
		})
	}
}

func TestCurveEdges(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		probes  []Probe
		wantLo  float64
		wantHi  float64
		wantLen int
	}{
		{name: "no probe", wantLen: 0},
		{
			name:    "probes without bitrate are ignored",
			probes:  []Probe{{Height: 360, CRF: 51, Bitrate: 0, VMAF: 10}, {Height: 360, CRF: 30, Bitrate: 400_000, VMAF: 60}},
			wantLo:  400_000,
			wantHi:  400_000,
			wantLen: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c := NewCurve(testCase.probes)

			lo, hi := c.Range()
			assert.InDelta(t, testCase.wantLo, lo, 1e-6)
			assert.InDelta(t, testCase.wantHi, hi, 1e-6)
			assert.Len(t, c.logR, testCase.wantLen)
		})
	}
}

func TestEnvelopeEdges(
	t *testing.T,
) {
	curve := NewCurve(synthetic(720, 94, 3e6, 20, 30))

	testCases := []struct {
		name   string
		curves []Curve
		points int
	}{
		{name: "no curve", points: 200},
		{name: "curve without bitrate", curves: []Curve{NewCurve(nil)}, points: 200},
		{name: "fewer than two points", curves: []Curve{curve}, points: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Nil(t, Envelope(testCase.curves, testCase.points))
		})
	}
}
