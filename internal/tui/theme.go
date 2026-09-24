// Package tui renders analysis reports and progress for terminals.
package tui

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/media"
)

// Palette. Every colour has a light and a dark variant so reports stay
// readable whatever the terminal background.
var (
	accent   = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FF9F43"}
	cyan     = lipgloss.AdaptiveColor{Light: "#0077A8", Dark: "#5FD7FF"}
	green    = lipgloss.AdaptiveColor{Light: "#2E7D32", Dark: "#87D787"}
	yellow   = lipgloss.AdaptiveColor{Light: "#A66A00", Dark: "#FFD75F"}
	red      = lipgloss.AdaptiveColor{Light: "#C62828", Dark: "#FF6B6B"}
	magenta  = lipgloss.AdaptiveColor{Light: "#AD1457", Dark: "#FF87D7"}
	subtle   = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6C6C6C"}
	contrast = lipgloss.AdaptiveColor{Light: "#000000", Dark: "#FFFFFF"}
)

// Styles.
var (
	Subtle  = lipgloss.NewStyle().Foreground(subtle)
	Bold    = lipgloss.NewStyle().Bold(true)
	Accent  = lipgloss.NewStyle().Foreground(accent)
	Cyan    = lipgloss.NewStyle().Foreground(cyan)
	Green   = lipgloss.NewStyle().Foreground(green)
	Yellow  = lipgloss.NewStyle().Foreground(yellow)
	Red     = lipgloss.NewStyle().Foreground(red)
	Magenta = lipgloss.NewStyle().Foreground(magenta)

	title   = lipgloss.NewStyle().Bold(true).Foreground(accent)
	section = lipgloss.NewStyle().Bold(true).Foreground(cyan)
	key     = lipgloss.NewStyle().Foreground(subtle).Width(keyWidth)
	card    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle).Padding(0, 1)
)

// seriesStyles colours the resolutions of rate-quality plots, highest first.
var seriesStyles = []lipgloss.Style{Cyan, Green, Yellow, Magenta, Red, Accent, Subtle}

const (
	// brand opens every report and the dashboard.
	brand = "◆ qc"

	// keyWidth is the width of the key column of key/value lines.
	keyWidth = 12

	// Report widths are clamped: below the minimum charts lose their shape,
	// above the maximum lines become hard to follow.
	minReportWidth = 60
	maxReportWidth = 140
)

// Marks are rendered on use, never at package initialisation: rendering an
// adaptive colour queries the terminal background.

// warnLine flags an issue.
func warnLine(
	s string,
) string {
	return Yellow.Render("▲ ") + s
}

// okLine reports good news.
func okLine(
	s string,
) string {
	return Green.Render("✓ ") + s
}

// infoLine adds neutral context.
func infoLine(
	s string,
) string {
	return Subtle.Render("ℹ " + s)
}

// timingsLine is the footer of every report: where the time went.
func timingsLine(
	s string,
) string {
	return Accent.Render("⚡ ") + Subtle.Render(s)
}

// clampWidth bounds a report width to [lo, maxReportWidth].
func clampWidth(
	width, lo int,
) int {
	return min(max(width, lo), maxReportWidth)
}

// reportTitle is the first line of a report: the brand and what it is about.
func reportTitle(
	kind string,
) string {
	return title.Render(brand) + Subtle.Render("  ·  "+kind)
}

// writtenLine confirms that a report file was written.
func writtenLine(
	what, path string,
) string {
	return okLine(what + " written to " + Bold.Render(path))
}

// scoreStyle colours a VMAF score by its usual reading.
func scoreStyle(
	score float64,
) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(vmafColor(score))
}

// vmafColor maps a score to the usual reading of VMAF: excellent (≥ 93),
// good (≥ 80), fair (≥ 60) or poor.
func vmafColor(
	score float64,
) lipgloss.TerminalColor {
	switch {
	case score >= 93:
		return cyan
	case score >= 80:
		return green
	case score >= 60:
		return yellow
	}

	return red
}

// Bitrate formats bits per second.
func Bitrate(
	bps float64,
) string {
	switch {
	case bps >= 1e6:
		return fmt.Sprintf("%.2f Mb/s", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.0f kb/s", bps/1e3)
	}

	return fmt.Sprintf("%.0f b/s", bps)
}

// compactBitrate is a short bitrate label for chart axes.
func compactBitrate(
	bps float64,
) string {
	switch {
	case bps >= 1e6:
		return fmt.Sprintf("%.1fM", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.0fk", bps/1e3)
	}

	return fmt.Sprintf("%.0f", bps)
}

// Bytes formats a byte count.
func Bytes(
	b float64,
) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GiB", b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", b/(1<<10))
	}

	return fmt.Sprintf("%.0f B", b)
}

// Clock formats a duration as [h:]m:ss, with milliseconds unless short.
func Clock(
	d media.Duration,
	short bool,
) string {
	t := d.Std()
	h, m := int(t.Hours()), int(t.Minutes())%60
	s := (t % time.Minute).Seconds()

	prefix := strconv.Itoa(m)
	if h > 0 {
		prefix = fmt.Sprintf("%d:%02d", h, m)
	}

	if short {
		return fmt.Sprintf("%s:%02d", prefix, int(s))
	}

	return fmt.Sprintf("%s:%06.3f", prefix, s)
}

// Elapsed formats a wall-clock duration.
func Elapsed(
	d time.Duration,
) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}

	return d.Round(10 * time.Millisecond).String()
}

// vmafLabel is the y-axis label of VMAF charts.
func vmafLabel(
	v float64,
) string {
	return fmt.Sprintf("%.0f", v)
}

// vmafFloor is the bottom of a VMAF axis showing values down to lowest.
func vmafFloor(
	lowest float64,
) float64 {
	return math.Max(0, math.Floor(lowest/10)*10)
}
