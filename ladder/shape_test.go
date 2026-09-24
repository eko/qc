package ladder

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shapeCurves are the synthetic curves of three resolutions.
func shapeCurves() []Curve {
	crfs := []float64{18, 23, 28, 33, 38}

	return []Curve{
		NewCurve(synthetic(1080, 99, 6e6, crfs...)),
		NewCurve(synthetic(720, 94, 3e6, crfs...)),
		NewCurve(synthetic(360, 78, 0.8e6, crfs...)),
	}
}

func TestConstraintsShape(
	t *testing.T,
) {
	testCases := []struct {
		name string
		c    Constraints
		want Shape
	}{
		{name: "default", c: Constraints{}, want: ShapeAuto},
		{name: "count", c: Constraints{Rungs: 5}, want: ShapeCount},
		{name: "resolutions win over count", c: Constraints{Rungs: 2, Resolutions: []int{720, 360}}, want: ShapeResolutions},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.c.Shape())
		})
	}
}

func TestConstraintsValidate(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		c       Constraints
		wantErr bool
	}{
		{name: "auto", c: Constraints{}},
		{name: "count", c: Constraints{Rungs: 4}},
		{name: "resolutions", c: Constraints{Resolutions: []int{1080, 720}}},
		{name: "matching count", c: Constraints{Rungs: 2, Resolutions: []int{1080, 720}}},
		{name: "negative count", c: Constraints{Rungs: -1}, wantErr: true},
		{name: "count differs from resolutions", c: Constraints{Rungs: 3, Resolutions: []int{1080, 720}}, wantErr: true},
		{name: "resolution above the source", c: Constraints{Resolutions: []int{2160}}, wantErr: true},
		{name: "zero resolution", c: Constraints{Resolutions: []int{0}}, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.c.Validate(1080)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidShape)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestCurveBitrateFor(
	t *testing.T,
) {
	c := NewCurve([]Probe{
		{Height: 720, CRF: 34, Bitrate: 500_000, VMAF: 60},
		{Height: 720, CRF: 27, Bitrate: 1_000_000, VMAF: 80},
		{Height: 720, CRF: 20, Bitrate: 4_000_000, VMAF: 90},
	})

	testCases := []struct {
		name       string
		curve      Curve
		vmaf       float64
		want       float64
		wantInside bool
	}{
		{name: "on a probe", curve: c, vmaf: 80, want: 1_000_000, wantInside: true},
		{name: "lowest probe", curve: c, vmaf: 60, want: 500_000, wantInside: true},
		{name: "between probes (log scale)", curve: c, vmaf: 85, want: 2_000_000, wantInside: true},
		{name: "extrapolated below", curve: c, vmaf: 40, want: 250_000, wantInside: false},
		{name: "extrapolated above", curve: c, vmaf: 100, want: 16_000_000, wantInside: false},
		{name: "empty curve", curve: Curve{}, vmaf: 80, want: 0, wantInside: false},
		{name: "flat curve", curve: NewCurve([]Probe{{Bitrate: 300_000, VMAF: 50}, {Bitrate: 600_000, VMAF: 50}}), vmaf: 70, want: 600_000},
		{name: "flat curve at its value", curve: NewCurve([]Probe{{Bitrate: 300_000, VMAF: 50}}), vmaf: 50, want: 300_000, wantInside: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, inside := testCase.curve.BitrateFor(testCase.vmaf)
			assert.InDelta(t, testCase.want, got, 1)
			assert.Equal(t, testCase.wantInside, inside)
		})
	}
}

func TestCurveBitrateForFlatEnds(
	t *testing.T,
) {
	// Isotonic pooling can leave flat ends: extrapolation uses the nearest
	// segment whose quality changes.
	c := NewCurve([]Probe{
		{Bitrate: 250_000, VMAF: 50}, {Bitrate: 500_000, VMAF: 50},
		{Bitrate: 1_000_000, VMAF: 70}, {Bitrate: 2_000_000, VMAF: 70},
	})

	below, _ := c.BitrateFor(40)
	above, _ := c.BitrateFor(80)

	// Below: from the cheapest point at 50 along (250 k, 50)–(1 M, 70).
	// Above: from the last point below 70 along (500 k, 50)–(2 M, 70).
	assert.InDelta(t, 125_000, below, 1)
	assert.InDelta(t, 4_000_000, above, 1)
}

func TestPlanRungs(
	t *testing.T,
) {
	curves := shapeCurves()
	hull := Envelope(curves, 200)

	testCases := []struct {
		name  string
		c     Constraints
		check func(t *testing.T, rungs []Target)
	}{
		{
			name: "auto shape is SelectRungs",
			c:    Constraints{},
			check: func(t *testing.T, rungs []Target) {
				assert.Equal(t, SelectRungs(hull, Constraints{}), rungs)
			},
		},
		{
			name: "count: exact number, evenly spaced quality",
			c:    Constraints{Rungs: 5},
			check: func(t *testing.T, rungs []Target) {
				require.Len(t, rungs, 5)
				assert.GreaterOrEqual(t, rungs[0].VMAF, 95.0)

				step := rungs[1].VMAF - rungs[2].VMAF
				for i := 1; i < len(rungs); i++ {
					assert.Less(t, rungs[i].Bitrate, rungs[i-1].Bitrate)
					assert.LessOrEqual(t, rungs[i].Height, rungs[i-1].Height)

					if i > 1 {
						assert.InDelta(t, step, rungs[i-1].VMAF-rungs[i].VMAF, 1e-9)
					}
				}
			},
		},
		{
			name: "count of one is the top rung",
			c:    Constraints{Rungs: 1},
			check: func(t *testing.T, rungs []Target) {
				require.Len(t, rungs, 1)
				assert.Equal(t, 1080, rungs[0].Height)
			},
		},
		{
			name: "resolutions: one rung per entry, highest first, on its own curve",
			c:    Constraints{Resolutions: []int{360, 1080, 720, 720}, TopVMAF: 93},
			check: func(t *testing.T, rungs []Target) {
				require.Len(t, rungs, 4)
				assert.Equal(t, []int{1080, 720, 720, 360}, heightsOf(rungs))

				for _, r := range rungs {
					assert.True(t, r.Fixed)
				}

				assert.InDelta(t, 93, rungs[0].VMAF, 1e-9)
				assert.Greater(t, rungs[1].Bitrate, rungs[2].Bitrate)
			},
		},
		{
			name: "resolutions: the top rung honours the bitrate cap",
			c:    Constraints{Resolutions: []int{1080, 360}, MaxBitrate: 1_000_000},
			check: func(t *testing.T, rungs []Target) {
				assert.Equal(t, int64(1_000_000), rungs[0].Bitrate)
			},
		},
		{
			name: "cap below the probes yields no rung",
			c:    Constraints{Rungs: 3, MaxBitrate: 1},
			check: func(t *testing.T, rungs []Target) {
				assert.Empty(t, rungs)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rungs, err := PlanRungs(hull, curves, testCase.c)
			require.NoError(t, err)
			testCase.check(t, rungs)
		})
	}
}

func TestPlanRungsErrors(
	t *testing.T,
) {
	curves := shapeCurves()
	hull := Envelope(curves, 200)

	testCases := []struct {
		name string
		c    Constraints
	}{
		{name: "empty quality range", c: Constraints{Rungs: 3, TopVMAF: 20, MinVMAF: 90}},
		{name: "resolution without probes", c: Constraints{Resolutions: []int{1080, 540}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := PlanRungs(hull, curves, testCase.c)
			require.ErrorIs(t, err, ErrInvalidShape)
		})
	}

	rungs, err := PlanRungs(nil, nil, Constraints{Rungs: 2})
	require.NoError(t, err)
	assert.Empty(t, rungs, "no envelope, no rung")
}

func TestHullVMAF(
	t *testing.T,
) {
	hull := []HullPoint{{Bitrate: 100_000, VMAF: 20}, {Bitrate: 400_000, VMAF: 60}}

	assert.InDelta(t, 20, hullVMAF(hull, 50_000), 1e-9, "below: clamped")
	assert.InDelta(t, 40, hullVMAF(hull, 200_000), 1e-9, "log midpoint")
	assert.InDelta(t, 60, hullVMAF(hull, 1_000_000), 1e-9, "above: clamped")
}

func heightsOf(
	rungs []Target,
) []int {
	out := make([]int, len(rungs))
	for i, r := range rungs {
		out[i] = r.Height
	}

	return out
}

func TestBuildWithImposedResolutions(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 10))

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec:       "h264",
		SkipVerify:  true,
		Constraints: Constraints{Resolutions: []int{1080, 720, 720, 360}},
	})
	require.NoError(t, err)

	assert.Equal(t, ShapeResolutions, res.Shape)
	assert.Equal(t, []int{1080, 720, 720, 360}, rungHeights(res.Rungs))

	probed := map[int]bool{}
	for _, p := range res.Probes {
		probed[p.Height] = true
	}

	heights := make([]int, 0, len(probed))
	for h := range probed {
		heights = append(heights, h)
	}

	slices.Sort(heights)
	assert.Equal(t, []int{360, 720, 1080}, heights, "only the imposed resolutions are probed")
}

func TestBuildRejectsInvalidShape(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, sourceReport(1280, 720, 8, 25, 10))

	_, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec:       "h264",
		Constraints: Constraints{Resolutions: []int{1080}},
	})

	require.ErrorIs(t, err, ErrInvalidShape)
}

func TestBuildRejectsEmptyQualityRange(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, sourceReport(1280, 720, 8, 25, 10))

	_, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec:       "h264",
		SkipVerify:  true,
		Constraints: Constraints{Rungs: 3, TopVMAF: 20, MinVMAF: 90},
	})

	require.ErrorIs(t, err, ErrInvalidShape)
}

func rungHeights(
	rungs []Rung,
) []int {
	out := make([]int, len(rungs))
	for i, r := range rungs {
		out[i] = r.Height
	}

	return out
}

func TestOutsideRange(
	t *testing.T,
) {
	c := NewCurve([]Probe{{Bitrate: 500_000, VMAF: 60}, {Bitrate: 2_000_000, VMAF: 85}})

	testCases := []struct {
		name string
		vmaf float64
		want bool
	}{
		{name: "inside", vmaf: 70, want: false},
		{name: "slightly below", vmaf: 59, want: false},
		{name: "far below", vmaf: 50, want: true},
		{name: "far above", vmaf: 95, want: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, outsideRange(c, testCase.vmaf))
		})
	}

	lo, hi := Curve{}.QualityRange()
	assert.Zero(t, lo)
	assert.Zero(t, hi)
}
