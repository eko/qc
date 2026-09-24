package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// measuredLadder is the sample ladder with XPSNR, CAMBI and device VMAF on
// its verified rungs: the second rung is banded, and VMAF ranks it above the
// first while XPSNR does not.
func measuredLadder(
	t *testing.T,
) *ladder.Result {
	t.Helper()

	res := sampleLadder(t)
	for i := range res.Rungs {
		m := res.Rungs[i].Measured
		if m == nil {
			continue
		}

		m.Metrics = map[string]float64{quality.SeriesXPSNRY: 40 - 3*float64(i), quality.SeriesCAMBI: 1 + float64(i)}
		m.Devices = map[string]float64{vmaf.DevicePhone: m.VMAF + 3, vmaf.Device4K: m.VMAF - 4}
	}

	res.Rungs[1].Measured.BandedFrames, res.Rungs[1].Measured.ScoredFrames = 6, 60
	res.Rungs[1].Measured.VMAF = res.Rungs[0].Measured.VMAF + 3

	return res
}

func TestRenderLadderRungQuality(
	t *testing.T,
) {
	out := renderLadder(t, measuredLadder(t), 120, "", false)

	for _, want := range []string{
		"Rung quality",
		"XPSNR Y dB",
		"CAMBI",
		"40.00",
		"37.00",
		"4k",
		"phone",
		"rung 2 (720p): visible banding on 10% of the scored frames (CAMBI > 5)",
		"VMAF ranks 720p higher",
	} {
		assert.Contains(t, out, want)
	}

	assert.NotContains(t, out, " PSNR Y", "only measured metrics get a column")
	assert.NotContains(t, out, "CAMBI (banding)", "headers are short")
}

func TestRungQualityTable(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		rungs []ladder.Rung
	}{
		{name: "no rung"},
		{name: "unverified rungs", rungs: []ladder.Rung{{Height: 720}}},
		{name: "VMAF only", rungs: []ladder.Rung{{Height: 720, Measured: &ladder.Measurement{VMAF: 90}}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Empty(t, rungQualityTable(testCase.rungs))
		})
	}
}

func TestRungQualityTableSkipsUnverified(
	t *testing.T,
) {
	rungs := []ladder.Rung{
		{Width: 1280, Height: 720, Measured: &ladder.Measurement{VMAF: 90, Metrics: map[string]float64{quality.SeriesCAMBI: 1.5}}},
		{Width: 640, Height: 360},
	}

	table := rungQualityTable(rungs)
	assert.Contains(t, table, "1280×720")
	assert.NotContains(t, table, "640×360")
}
