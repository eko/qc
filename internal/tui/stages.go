package tui

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
)

// StageSummary words the result of a pipeline stage in one line, for the
// dashboard.
func StageSummary(
	r pipeline.StageResult,
) string {
	switch r.Stage.Kind {
	case pipeline.KindInspect:
		return InspectionSummary(r.Analysis)
	case pipeline.KindAnalysis:
		return analysisSummary(r.Analysis)
	case pipeline.KindVMAF:
		return vmafSummary(r.Comparison.VMAF)
	case pipeline.KindLadder:
		return ladderSummary(r.Ladder)
	case pipeline.KindOverlay:
		return "annotated copy · " + filepath.Base(r.Overlay)
	}

	return ""
}

// InspectionSummary describes the primary video stream of an inspection in
// one line.
func InspectionSummary(
	report *analysis.Report,
) string {
	v, _ := report.Info.PrimaryVideo()

	return fmt.Sprintf("%s · %d×%d · %.3g fps · %s", v.Codec, v.Width, v.Height, v.AvgFrameRate.Float(),
		report.Info.Duration.Std().Round(time.Second)) + loudnessSummary(report)
}

// analysisSummary counts the shots and gives the mean complexity.
func analysisSummary(
	report *analysis.Report,
) string {
	v := report.Video

	return fmt.Sprintf("%d shots · SI %.0f · TI %.0f", len(v.Shots), v.SITI.SISummary.Mean, v.SITI.TISummary.Mean) + loudnessSummary(report)
}

// loudnessSummary is the integrated loudness of the first audio track
// analysed, or "".
func loudnessSummary(
	report *analysis.Report,
) string {
	if report.Audio == nil || len(report.Audio.Tracks) == 0 {
		return ""
	}

	l := report.Audio.Tracks[0].Loudness
	if l.Integrated <= loudness.Floor {
		return " · silent audio"
	}

	return fmt.Sprintf(" · %.1f LUFS", l.Integrated)
}

// vmafSummary is the score with its precision, and where it ran.
func vmafSummary(
	v *quality.Result,
) string {
	gpu := ""
	if v.Backend != "" {
		gpu = " · CUDA"
	}

	if v.HalfWidth > 0 {
		return fmt.Sprintf("%.2f ± %.2f%s", v.Mean, v.HalfWidth, gpu)
	}

	return fmt.Sprintf("%.2f (exact)%s", v.Mean, gpu)
}

// ladderSummary counts the rungs and describes the top one, naming the
// encoder when it is a GPU one.
func ladderSummary(
	res *ladder.Result,
) string {
	if len(res.Rungs) == 0 {
		return "no rung"
	}

	top := res.Rungs[0]

	summary := fmt.Sprintf("%d rungs · top %dp @ %.2f Mb/s", len(res.Rungs), top.Height, float64(top.Bitrate)/bitsPerMegabit)
	if res.Codec.Hardware != encode.HardwareCPU {
		summary += " · " + res.Codec.Encoder
	}

	return summary
}
