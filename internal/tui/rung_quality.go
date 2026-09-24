package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

// Rung quality table column widths.
const (
	colMetric = 11
	colDevice = 9
)

// rungQualityTable shows what the verification encodes measured next to
// VMAF: the extra metrics and the VMAF of each viewing device. It is empty
// when nothing was measured.
func rungQualityTable(
	rungs []ladder.Rung,
) string {
	metrics, devices := rungExtras(rungs)
	if len(metrics) == 0 && len(devices) == 0 {
		return ""
	}

	var head strings.Builder

	fmt.Fprintf(&head, "  %-3s %-11s %*s", "#", "resolution", colVMAF, "VMAF")

	for _, name := range metrics {
		fmt.Fprintf(&head, " %*s", colMetric, columnHead(name))
	}

	for _, d := range devices {
		fmt.Fprintf(&head, " %*s", colDevice, d)
	}

	lines := []string{section.Render("Rung quality"), Subtle.Render(head.String())}

	for i, r := range rungs {
		m := r.Measured
		if m == nil {
			continue
		}

		var line strings.Builder

		fmt.Fprintf(&line, "  %-3d %-11s %s", i+1, fmt.Sprintf("%d×%d", r.Width, r.Height),
			padLeft(scoreStyle(m.VMAF).Render(fmt.Sprintf("%.1f", m.VMAF)), colVMAF))

		for _, name := range metrics {
			fmt.Fprintf(&line, " %*s", colMetric, quality.DescribeSeries(name).Format(m.Metrics[name]))
		}

		for _, d := range devices {
			line.WriteString(" " + padLeft(scoreStyle(m.Devices[d]).Render(fmt.Sprintf("%.1f", m.Devices[d])), colDevice))
		}

		lines = append(lines, line.String())
	}

	return strings.Join(lines, "\n")
}

// rungExtras returns the headline series and devices measured on the first
// verified rung (every rung is measured alike).
func rungExtras(
	rungs []ladder.Rung,
) (metrics, devices []string) {
	i := slices.IndexFunc(rungs, func(r ladder.Rung) bool { return r.Measured != nil })
	if i < 0 {
		return nil, nil
	}

	m := rungs[i].Measured
	for _, name := range quality.HeadlineSeries() {
		if _, ok := m.Metrics[name]; ok {
			metrics = append(metrics, name)
		}
	}

	for d := range m.Devices {
		devices = append(devices, d)
	}

	slices.Sort(devices)

	return metrics, devices
}

// columnHead is the short header of a series: its label without the
// parenthesised hint ("CAMBI (banding)" is "CAMBI"), with its unit.
func columnHead(
	name string,
) string {
	info := quality.DescribeSeries(name)
	label := info.ShortLabel()

	if info.Unit != "" {
		label += " " + info.Unit
	}

	return label
}
