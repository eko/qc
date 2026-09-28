package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/internal/findings"
)

// maxCameraClasses bounds the classes listed in the camera section header.
const maxCameraClasses = 4

// cameraSection sums the camera work up: the share of each class, the
// shaky shots, and the camera speed and shake of every frame. It is empty
// when the motion analysis was skipped.
func cameraSection(
	report *analysis.Report,
	width int,
) string {
	v, frames := report.Video, report.Frames
	if v == nil || v.Motion == nil || frames == nil {
		return ""
	}

	var shares []string
	for _, c := range v.Motion.Classes[:min(len(v.Motion.Classes), maxCameraClasses)] {
		shares = append(shares, fmt.Sprintf("%s %.0f%%", c.Class, c.Share*100))
	}

	speed := make([]float64, len(frames.MotionPan))
	for i := range speed {
		speed[i] = math.Hypot(frames.MotionPan[i], frames.MotionTilt[i])
	}

	zoom := make([]float64, len(frames.MotionZoom))
	for i, z := range frames.MotionZoom {
		zoom[i] = math.Abs(z)
	}

	sparkWidth := width - gutter
	lines := []string{
		section.Render("Camera") + "  " + stat("shots", strings.Join(shares, " · ")) +
			stat("shaky", strconv.Itoa(v.Motion.ShakyShots)),
		rowLabel("pan/tilt") + Sparkline(speed, sparkWidth, Max, Accent),
		rowLabel("zoom") + Sparkline(zoom, sparkWidth, Max, Cyan),
		rowLabel("shake") + Sparkline(frames.MotionShake, sparkWidth, Max, Yellow),
	}

	return strings.Join(lines, "\n")
}

// directionArrows draw a camera direction in the shot table.
var directionArrows = map[motion.Direction]string{
	motion.DirectionLeft: "←", motion.DirectionRight: "→", motion.DirectionUp: "↑", motion.DirectionDown: "↓",
	motion.DirectionIn: "in", motion.DirectionOut: "out",
}

// cameraLabel is a shot's camera work in the shot table: its class, its
// direction, and a ~ when the camera shakes.
func cameraLabel(
	c *motion.Shot,
) string {
	if c == nil {
		return "–"
	}

	label := string(c.Class)
	if arrow, ok := directionArrows[c.Direction]; ok {
		label += " " + arrow
	}

	if c.Shaky && c.Class != motion.ClassHandheld {
		label += " ~"
	}

	return label
}

// motionFindingText words a finding of the camera motion analysis, or
// returns "" for another finding.
func motionFindingText(
	f findings.Finding,
) string {
	if f.Code != findings.ShakyShots {
		return ""
	}

	return fmt.Sprintf("%d shaky shot(s) (camera jitter up to %.1f%% of the width): handheld or unstabilised camera, costly to encode",
		len(f.Spans), f.Value)
}
