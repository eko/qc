package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

// TestSnapshots is a development tool, skipped by default: it writes ANSI
// renderings of typical dashboard states to the directory named by
// $QC_SNAPSHOTS, to review the layout or turn them into images:
//
//	mkdir -p /tmp/qc && QC_SNAPSHOTS=/tmp/qc go test -run TestSnapshots ./internal/tui/
//	freeze /tmp/qc/2-vmaf.ansi -o vmaf.png   # charmbracelet/freeze
//
// It asserts nothing: the other tests cover the same states.
func TestSnapshots(
	t *testing.T,
) {
	dir := os.Getenv("QC_SNAPSHOTS")
	if dir == "" {
		t.Skip("set QC_SNAPSHOTS to write dashboard snapshots")
	}

	// TestMain already renders in true colour on a dark background.
	labels := []string{"Inspect", "Frame analysis", "VMAF", "Ladder · h264", "Ladder · av1"}
	d := newDashboard("run", "title.mp4", labels, func() {})
	d.width = 110
	d.started = time.Now().Add(-47 * time.Second)
	d.now = time.Now()

	write := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(d.View()), 0o600))
	}

	d.Update(startMsg{i: 0})
	d.stages[0].started = d.now.Add(-47 * time.Second)
	d.Update(doneMsg{i: 0, summary: "h264 · 1920×1080 · 25 fps · 10m36s"})
	d.stages[0].ended = d.stages[0].started.Add(142 * time.Millisecond)
	d.Update(startMsg{i: 1})
	d.stages[1].started = d.now.Add(-21 * time.Second)
	d.Update(progressMsg{i: 1, done: 9876, total: 15903})
	write("1-analysis")

	d.Update(doneMsg{i: 1, summary: "212 shots · SI 38 · TI 11"})
	d.stages[1].ended = d.stages[1].started.Add(34 * time.Second)
	d.Update(startMsg{i: 2})
	d.stages[2].started = d.now.Add(-9 * time.Second)
	d.Update(progressMsg{i: 2, done: 822, total: 15903, detail: "round 1"})
	d.Update(panelMsg{i: 2, panel: VMAFPanel{Progress: quality.Progress{
		Estimated: true, Mean: 80.02, HalfWidth: 0.49, Round: 1, FramesScored: 822, FramesTotal: 15903, Mode: quality.ModeSampled,
	}}})
	write("2-vmaf")

	d.Update(doneMsg{i: 2, summary: "80.02 ± 0.49"})
	d.stages[2].ended = d.stages[2].started.Add(30 * time.Second)
	d.Update(startMsg{i: 3})
	d.Update(unitMsg{i: 3, unit: "encodes"})
	d.stages[3].started = d.now.Add(-38 * time.Second)

	probes := []ladder.Probe{
		{Height: 1080, CRF: 20, Bitrate: 5_600_000, VMAF: 97.1}, {Height: 1080, CRF: 27, Bitrate: 2_300_000, VMAF: 91.8},
		{Height: 1080, CRF: 34, Bitrate: 1_050_000, VMAF: 82.3}, {Height: 720, CRF: 20, Bitrate: 2_700_000, VMAF: 93.0},
		{Height: 720, CRF: 27, Bitrate: 1_150_000, VMAF: 86.6}, {Height: 720, CRF: 34, Bitrate: 520_000, VMAF: 74.8},
		{Height: 540, CRF: 20, Bitrate: 1_600_000, VMAF: 88.2}, {Height: 540, CRF: 27, Bitrate: 700_000, VMAF: 79.4},
		{Height: 540, CRF: 34, Bitrate: 330_000, VMAF: 64.9}, {Height: 360, CRF: 20, Bitrate: 800_000, VMAF: 76.5},
		{Height: 360, CRF: 27, Bitrate: 360_000, VMAF: 64.1},
	}
	d.Update(progressMsg{i: 3, done: len(probes), total: 15, detail: "probe encodes"})
	d.Update(panelMsg{i: 3, panel: LadderPanel{Stage: ladder.StageProbe, Probes: probes}})
	write("3-ladder-probes")

	rungs := []ladder.Rung{
		{Height: 1080, PredictedVMAF: 95.0, Measured: &ladder.Measurement{VMAF: 95.3, Bitrate: 2_970_000}},
		{Height: 720, PredictedVMAF: 91.2, Measured: &ladder.Measurement{VMAF: 91.5, Bitrate: 1_150_000}},
		{Height: 720, PredictedVMAF: 85.1, Measured: &ladder.Measurement{VMAF: 85.4, Bitrate: 633_000}},
		{Height: 540, PredictedVMAF: 70.6, Measured: &ladder.Measurement{VMAF: 69.4, Bitrate: 275_000}},
	}
	d.Update(progressMsg{i: 3, done: 4, total: 6, detail: "verification"})
	d.Update(panelMsg{i: 3, panel: LadderPanel{Stage: ladder.StageVerify, Rungs: rungs}})
	write("4-ladder-verify")

	d.Update(doneMsg{i: 3, summary: "6 rungs · top 1080p @ 3.11 Mb/s"})
	d.stages[3].ended = d.stages[3].started.Add(99 * time.Second)
	d.Update(startMsg{i: 4})
	d.stages[4].started = d.now.Add(-2 * time.Minute)
	d.Update(doneMsg{i: 4, summary: "7 rungs · top 1080p @ 2.05 Mb/s"})
	d.stages[4].ended = d.stages[4].started.Add(131 * time.Second)
	d.Update(finishMsg{})
	write("5-done")
}
