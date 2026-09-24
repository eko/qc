package htmlreport

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

func TestRungTableExtras(
	t *testing.T,
) {
	rungs := []ladder.Rung{
		{Width: 1920, Height: 1080, Measured: &ladder.Measurement{
			VMAF:    94,
			Metrics: map[string]float64{quality.SeriesXPSNRY: 38.25, quality.SeriesCAMBI: 1.2, quality.SeriesPSNRCb: 44},
			Devices: map[string]float64{vmaf.DevicePhone: 97.4},
		}},
		{Width: 1280, Height: 720, Measured: &ladder.Measurement{VMAF: 85, Metrics: map[string]float64{quality.SeriesXPSNRY: 35.5}}},
		{Width: 640, Height: 360},
	}

	testCases := []struct {
		name     string
		rungs    []ladder.Rung
		wantHead []string
		wantRows [][]string
	}{
		{
			name:     "VMAF only",
			rungs:    []ladder.Rung{{Width: 640, Height: 360}},
			wantHead: []string{"#", "resolution", "bitrate", "CRF", "maxrate", "predicted VMAF", "measured VMAF", "measured bitrate"},
		},
		{
			name:     "headline metrics in report order, devices, dashes where not measured",
			rungs:    rungs,
			wantHead: []string{"XPSNR Y", "CAMBI", "phone VMAF"},
			wantRows: [][]string{{"38.25", "1.20", "97.4"}, {"35.50", "–", "–"}, {"–", "–", "–"}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			table := rungTable(testCase.rungs)

			if testCase.wantRows == nil {
				assert.Equal(t, testCase.wantHead, table.Head)

				return
			}

			assert.Equal(t, testCase.wantHead, table.Head[8:])

			for i, want := range testCase.wantRows {
				assert.Equal(t, want, table.Rows[i][8:], "row %d", i)
			}
		})
	}
}

func TestLadderFindingsRungQuality(
	t *testing.T,
) {
	res := sampleLadder(t, "h264")
	res.Rungs[0].Measured.Metrics = map[string]float64{quality.SeriesXPSNRY: 33}
	res.Rungs[0].Measured.BandedFrames, res.Rungs[0].Measured.ScoredFrames = 4, 40
	res.Rungs[1].Measured = &ladder.Measurement{VMAF: 90, Metrics: map[string]float64{quality.SeriesXPSNRY: 36}}

	var texts []string
	for _, f := range ladderFindings(res) {
		texts = append(texts, f.Text)
	}

	assert.Contains(t, texts, "Rung 1 (1080p): visible banding on 10% of the scored frames (CAMBI > 5): a 10-bit encode fixes it better than more bitrate")
	assert.Contains(t, texts, "Rungs 1 (1080p) and 2 (720p): VMAF ranks 1080p higher (94.6 vs 90.0) but XPSNR ranks it lower (33.00 vs 36.00 dB)")
}
