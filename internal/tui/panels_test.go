package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

func TestVMAFPanel(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		progress   quality.Progress
		width      int
		want       string
		gaugeWidth int
	}{
		{
			name:     "before the first estimate",
			progress: quality.Progress{FramesScored: 12},
			width:    80,
			want:     "scoring frames… 12 scored",
		},
		{
			name:       "estimate",
			progress:   quality.Progress{Estimated: true, Mean: 80.02, HalfWidth: 0.49, Round: 1, FramesScored: 822, FramesTotal: 15903},
			width:      80,
			want:       "VMAF 80.02  ± 0.49  ·  round 1  ·  5.2% of frames scored",
			gaugeWidth: 78,
		},
		{
			name: "fixed budget",
			progress: quality.Progress{Estimated: true, Mean: 80.02, HalfWidth: 0.47, Round: 1, FramesScored: 795, FramesTotal: 15903,
				Sample: quality.Sample{Share: 0.05}},
			width:      80,
			want:       "VMAF 80.02  ± 0.47  ·  budget 5%  ·  5.0% of frames scored",
			gaugeWidth: 78,
		},
		{
			name:       "narrow panels keep a readable gauge",
			progress:   quality.Progress{Estimated: true, Mean: 50, HalfWidth: 2},
			width:      10,
			want:       "0.0% of frames scored",
			gaugeWidth: minGaugeWidth,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := plainLines(VMAFPanel{Progress: testCase.progress}.View(testCase.width))

			assert.Contains(t, lines[0], testCase.want)

			if testCase.gaugeWidth == 0 {
				assert.Len(t, lines, 1)

				return
			}

			require.Len(t, lines, 2)
			assert.Equal(t, testCase.gaugeWidth, widths(lines[1:])[0])
			assert.Contains(t, lines[1], "●")
		})
	}
}

func TestLadderPanel(
	t *testing.T,
) {
	verified := []ladder.Rung{
		{Height: 1080, PredictedVMAF: 95, Measured: &ladder.Measurement{VMAF: 92, Bitrate: 5e6}},
		{Height: 720, PredictedVMAF: 86.6, Measured: &ladder.Measurement{VMAF: 87, Bitrate: 1_150_000}},
		{Height: 360, PredictedVMAF: 64},
	}

	testCases := []struct {
		name    string
		panel   LadderPanel
		want    []string
		wantNot []string
		plot    bool
	}{
		{
			name:  "digest",
			panel: LadderPanel{Stage: ladder.StageDigest},
			want:  []string{"extracting a digest of evenly spaced segments…"},
		},
		{
			name:  "no probe yet",
			panel: LadderPanel{Stage: ladder.StageProbe},
			want:  []string{"encoding the first probes…"},
		},
		{
			name:  "probes",
			panel: LadderPanel{Stage: ladder.StageProbe, Probes: sampleProbes()},
			want:  []string{"probe encodes  last: 360p crf 20 → 800 kb/s, VMAF 76.5", "━━ 1080p", "━━ 720p", "━━ 360p"},
			plot:  true,
		},
		{
			name:  "verification before the first rung keeps the probes",
			panel: LadderPanel{Stage: ladder.StageVerify, Probes: sampleProbes()},
			want:  []string{"probe encodes"},
			plot:  true,
		},
		{
			name:  "verification",
			panel: LadderPanel{Stage: ladder.StageVerify, Rungs: verified},
			want: []string{
				"verifying rungs  (real encode of the digest with the final settings)",
				"  ▲ 1080p   5.00 Mb/s  predicted  95.0  measured  92.0 (-3.0)",
				"  ✓  720p   1.15 Mb/s  predicted  86.6  measured  87.0 (+0.4)",
			},
			wantNot: []string{"360p"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			view := testCase.panel.View(80)
			out := plain(view)

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, out, unwanted)
			}

			plot := linesWith(plainLines(view), "┤")
			if !testCase.plot {
				assert.Empty(t, plot)

				return
			}

			require.Len(t, plot, probePlotHeight)

			for _, w := range widths(plot) {
				assert.Equal(t, 80, w)
			}

			assert.Contains(t, plot[len(plot)-1], "60 ┤", "the floor is below the lowest probe")
			assert.True(t, strings.HasPrefix(strings.TrimSpace(plot[0]), "100 ┤"))
		})
	}
}

func TestTick(
	t *testing.T,
) {
	msg := tick()()
	assert.IsType(t, tickMsg{}, msg)
}
