package tui

import (
	"fmt"
	"strings"

	"github.com/eko/qc/analysis"
)

// lightSection shows the light levels of an HDR video: the measured MaxCLL
// and MaxFALL next to the signalled ones, and the peak and average light of
// every frame. It is empty for SDR.
func lightSection(
	report *analysis.Report,
	width int,
) string {
	if report.Video == nil || report.Video.Light == nil || report.Frames == nil {
		return ""
	}

	l := report.Video.Light
	v, _ := report.Info.PrimaryVideo()

	signalled := "none"
	if cll := v.HDR.ContentLightLevel; cll != nil {
		signalled = fmt.Sprintf("%d / %d", cll.MaxCLL, cll.MaxFALL)
	}

	unit := "cd/m²"
	if l.DisplayPeak > 0 {
		unit = fmt.Sprintf("cd/m² on a %s cd/m² display", nits(l.DisplayPeak))
	}

	sparkWidth := width - gutter
	lines := []string{
		section.Render("Light levels") + "  " +
			stat("MaxCLL", fmt.Sprintf("%s (strict %s)", nits(l.MaxCLLRobust), nits(l.MaxCLL))) +
			stat("MaxFALL", nits(l.MaxFALL)) +
			stat("signalled", signalled) + Subtle.Render(unit),
		rowLabel("peak 99.9%") + Sparkline(report.Frames.RobustPeakNits, sparkWidth, Max, Yellow),
		rowLabel("average") + Sparkline(report.Frames.AverageNits, sparkWidth, Max, Accent),
	}

	return strings.Join(lines, "\n")
}

// hdrBadge marks the header of an HDR video with its dynamic range.
func hdrBadge(
	report *analysis.Report,
) string {
	v, ok := report.Info.PrimaryVideo()
	if !ok || !v.Color.IsHDR() && v.HDR.DolbyVision == nil {
		return ""
	}

	return "  " + badge.Render("["+string(v.HDR.DynamicRange)+"]")
}
