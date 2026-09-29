package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
)

func TestBuildLevel(
	t *testing.T,
) {
	testCases := []struct {
		name string
		// bias is the error of the sampled frames, shared by every sampled
		// measurement.
		bias float64
	}{
		{name: "sampled frames harder than the digest", bias: -0.8},
		{name: "sampled frames easier than the digest", bias: 1.2},
		{name: "sampled frames like the digest", bias: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
			lab.sampledBias = testCase.bias

			res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "h264"})
			require.NoError(t, err)

			level := res.Probing.Level
			require.NotNil(t, level)
			assert.InDelta(t, -testCase.bias, level.Offset, 1e-9, "exact − sampled")
			assert.InDelta(t, level.Exact, level.Sampled+level.Offset, 1e-9)
			assert.Equal(t, res.Rungs[0].Height, level.Height, "measured at the top rung's resolution")

			for _, p := range res.Probes {
				exact := lab.model.vmaf(encode.Params{Width: p.Width, Height: p.Height, CRF: p.CRF})
				assert.InDelta(t, exact, p.VMAF, 1e-9, "%dp crf %.1f: probe at its exact level", p.Height, p.CRF)
			}

			for i, r := range res.Rungs {
				require.NotNil(t, r.Measured)

				exact := lab.model.vmaf(encode.Params{Width: r.Width, Height: r.Height, CRF: r.CRF, MaxRate: r.MaxRate})
				assert.InDelta(t, exact, r.Measured.VMAF, 1e-9, "rung %d: measured at its exact level", i)
			}

			exact := 0

			for _, q := range lab.compared {
				if q.Exact {
					exact++
				}
			}

			assert.GreaterOrEqual(t, exact, 2, "the level encode and the top rung are scored on every frame")
			assert.Contains(t, res.Timings, StageLevel)
		})
	}
}

func TestCalibrationTolerance(
	t *testing.T,
) {
	testCases := []struct {
		name string
		rung int
		want float64
	}{
		{name: "top rung", rung: 0, want: TopCalibrationTolerance},
		{name: "other rungs", rung: 3, want: CalibrationTolerance},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, calibrationTolerance(testCase.rung), 1e-12)
		})
	}
}
