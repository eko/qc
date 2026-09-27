package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// qualityChartHeight is the height of the quality-over-time chart.
const qualityChartHeight = 5

// RenderComparison writes a human-friendly VMAF comparison report.
func RenderComparison(
	w io.Writer,
	cmp *analysis.Comparison,
	width int,
	jsonPath string,
) error {
	width = clampWidth(width, minReportWidth)
	v := cmp.VMAF

	return writeBlocks(w, []string{
		reportTitle("vmaf"),
		sides(cmp),
		scoreSection(v, width),
		devicesSection(v),
		metricsSection(v),
		strataSection(cmp, width),
		findingsBlock(comparisonFindings(v)),
		vmafFooter(cmp, jsonPath),
	})
}

// sides describes the reference and the distorted videos.
func sides(
	cmp *analysis.Comparison,
) string {
	return sourceLines("reference", cmp.Reference, true) + "\n" + sourceLines("distorted", cmp.Distorted, true)
}

// sourceLines names a video file and summarises its primary stream on the
// line below.
func sourceLines(
	name string,
	r *analysis.Report,
	withBitrate bool,
) string {
	v, _ := r.Info.PrimaryVideo()
	dir, file := filepath.Split(r.Info.Path)

	parts := []string{v.Codec, fmt.Sprintf("%d×%d", v.Width, v.Height), fmt.Sprintf("%.3f fps", v.AvgFrameRate.Float())}
	if withBitrate {
		parts = append(parts, Bitrate(float64(r.Bitstream.AverageBitrate)))
	}

	parts = append(parts, Clock(r.Info.Duration, false))

	return key.Render(name) + Subtle.Render(dir) + Bold.Render(file) + "\n" +
		key.Render("") + Subtle.Render(strings.Join(parts, " · "))
}

func scoreSection(
	v *quality.Result,
	width int,
) string {
	score := scoreStyle(v.Mean).Bold(true).Render(fmt.Sprintf("%.2f", v.Mean))
	sampled := v.Mode == quality.ModeSampled

	precision := Green.Render("exact")
	if sampled {
		precision = fmt.Sprintf("± %.2f  %s", v.HalfWidth,
			Subtle.Render(fmt.Sprintf("(%.0f%% CI %.2f – %.2f)", v.Confidence*100, v.Low, v.High)))
	}

	head := section.Render("VMAF") + "   " + score + "  " + precision +
		Subtle.Render(fmt.Sprintf("   model %s @ %d×%d", v.Model.Name, v.Model.Width, v.Model.Height))

	share := float64(v.FramesScored) / float64(max(v.FramesTotal, 1))
	stats := []string{
		stat("scored", fmt.Sprintf("%d / %d frames (%.1f%%)", v.FramesScored, v.FramesTotal, share*100)),
		stat("time", Elapsed(v.Elapsed.Std())),
	}

	if gpu := v.GPUSummary(); gpu != "" {
		stats = append(stats, stat("gpu", gpu))
	}

	if h := v.HDR; h != nil {
		stats = append(stats, stat("hdr", badge.Render(hdrVMAFLabel(h))))
	}

	switch {
	case v.Sample != nil:
		stats = append(stats, stat("budget", v.Sample.Summary()), stat("strata", strconv.Itoa(len(v.Strata))))
	case sampled:
		stats = append(stats, stat("rounds", strconv.Itoa(v.Rounds)), stat("strata", strconv.Itoa(len(v.Strata))))
	default:
		stats = append(stats, stat("harmonic", fmt.Sprintf("%.2f", v.HarmonicMean)))
	}

	dist := stat("frames", fmt.Sprintf("min %.1f · p5 %.1f · median %.1f · max %.1f",
		v.Scored.Min, v.Scored.P5, v.Scored.P50, v.Scored.Max))

	return strings.Join([]string{head, gauge(v, width), strings.Join(stats, ""), dist}, "\n")
}

// gauge draws the 0–100 VMAF scale under a chart gutter, with its labels.
func gauge(
	v *quality.Result,
	width int,
) string {
	cols := width - gutter
	scale := Subtle.Render(strings.Repeat(" ", gutter) + "0" + strings.Repeat(" ", max(1, cols/2-3)) + "50" +
		strings.Repeat(" ", max(1, cols-cols/2-3)) + "100")

	return rowLabel("") + vmafScale(v.Mean, v.Low, v.High, cols) + "\n" + scale
}

// vmafScale draws the 0–100 VMAF scale, cols wide, coloured by quality zone,
// with the confidence interval [low, high] filled and the score marked.
func vmafScale(
	mean, low, high float64,
	cols int,
) string {
	pos := func(x float64) int { return min(cols-1, max(0, int(x/100*float64(cols)))) }
	lo, hi, at := pos(low), pos(high), pos(mean)

	var b strings.Builder

	for c := range cols {
		style := scoreStyle((float64(c) + 0.5) / float64(cols) * 100)

		switch {
		case c == at:
			b.WriteString(Bold.Render("●"))
		case c >= lo && c <= hi:
			b.WriteString(style.Render("█"))
		default:
			b.WriteString(style.Render("━"))
		}
	}

	return b.String()
}

// strataSection charts quality over time and, when sampled, where the
// scored frames are.
func strataSection(
	cmp *analysis.Comparison,
	width int,
) string {
	v := cmp.VMAF
	cols := width - gutter
	duration := cmp.Reference.Bitstream.Duration
	col := timeColumn(duration, cols)

	series, lowest := exactSeries(v.Frames, cols, col)
	subtitle := "  (per frame)"

	if v.Mode == quality.ModeSampled {
		series, lowest = strataSeries(v.Strata, cols, col)
		subtitle = "  (mean per stratum)"
	}

	// Bars start one decade below the lowest score, so that it still shows.
	floor := max(0, vmafFloor(lowest)-10)
	lines := []string{section.Render("Quality over time") + Subtle.Render(subtitle)}
	lines = append(lines, BarChart(series, width, qualityChartHeight, Mean, Scale{Lo: floor, Hi: 100}, Accent, vmafLabel)...)

	if v.Mode == quality.ModeSampled {
		scored := []rune(strings.Repeat("·", cols))
		for _, f := range v.Frames {
			scored[col(f.PTS)] = '█'
		}

		lines = append(lines, rowLabel("scored")+Cyan.Render(string(scored)))
	}

	lines = append(lines, timeAxis(duration, width))

	return strings.Join(lines, "\n")
}

// exactSeries is the mean score of the frames falling in each column, and
// the lowest of them.
func exactSeries(
	frames []quality.FrameScore,
	cols int,
	col func(media.Duration) int,
) ([]float64, float64) {
	series := make([]float64, cols)
	sums, counts := make([]float64, cols), make([]int, cols)

	for _, f := range frames {
		sums[col(f.PTS)] += f.Score
		counts[col(f.PTS)]++
	}

	lowest := 100.0

	for c := range series {
		if counts[c] > 0 {
			series[c] = sums[c] / float64(counts[c])
			lowest = min(lowest, series[c])
		}
	}

	return series, lowest
}

// strataSeries is the mean of the stratum covering each column, and the
// lowest stratum mean.
func strataSeries(
	strata []quality.StratumResult,
	cols int,
	col func(media.Duration) int,
) ([]float64, float64) {
	series := make([]float64, cols)
	lowest := 100.0

	for _, s := range strata {
		for c := col(s.Start); c <= col(s.End-1); c++ {
			series[c] = s.Mean
		}

		lowest = min(lowest, s.Mean)
	}

	return series, lowest
}

func vmafFooter(
	cmp *analysis.Comparison,
	jsonPath string,
) string {
	line := timingsLine(fmt.Sprintf("inspect %s · vmaf %s · %d frames decoded",
		cmp.Timings["inspect"], cmp.Timings["vmaf"], cmp.VMAF.FramesDecoded))

	if jsonPath != "" {
		line += "\n" + writtenLine("full report", jsonPath)
	}

	return line
}

// devicesSection lists the VMAF of each viewing device, with its interval.
func devicesSection(
	v *quality.Result,
) string {
	if len(v.Devices) == 0 {
		return ""
	}

	lines := []string{section.Render("VMAF per device") + Subtle.Render("  (same clips, one model per viewing condition)")}

	for _, d := range v.Devices {
		lines = append(lines, fmt.Sprintf("  %-7s %s  %s%s", d.Device,
			scoreStyle(d.Mean).Bold(true).Render(fmt.Sprintf("%6.2f", d.Mean)),
			interval(v, d.Estimate, quality.DescribeSeries("")),
			Subtle.Render(fmt.Sprintf("   %s @ %d×%d", d.Model.Name, d.Model.Width, d.Model.Height))))
	}

	return strings.Join(lines, "\n")
}

// metricsSection tables the other metrics: mean, interval and worst 5% of
// the scored frames.
func metricsSection(
	v *quality.Result,
) string {
	if len(v.Metrics) == 0 {
		return ""
	}

	subtitle := "  (95% CI from the clips VMAF sampled)"
	if v.Mode == quality.ModeExact {
		subtitle = "  (every frame)"
	}

	lines := []string{
		section.Render("Metrics") + Subtle.Render(subtitle),
		Subtle.Render(fmt.Sprintf("  %-17s %8s  %-26s %9s", "metric", "mean", "interval", "worst 5%")),
	}

	for _, m := range v.Metrics {
		info := quality.DescribeSeries(m.Name)
		lines = append(lines, fmt.Sprintf("  %-17s %s  %s %9s %s", info.Label,
			Bold.Render(fmt.Sprintf("%8s", info.Format(m.Mean))),
			padRight(interval(v, m.Estimate, info), 26),
			info.Format(info.Worst(m.Scored)), Subtle.Render(info.Unit)))
	}

	return strings.Join(lines, "\n")
}

// interval renders "± h (low – high)" in sampled mode, "exact" otherwise.
func interval(
	v *quality.Result,
	e quality.Estimate,
	info quality.SeriesInfo,
) string {
	if v.Mode == quality.ModeExact {
		return Green.Render("exact")
	}

	return fmt.Sprintf("± %s ", info.Format(e.HalfWidth)) +
		Subtle.Render("("+info.Format(e.Low)+" – "+info.Format(e.High)+")")
}

// padRight pads a styled string to width visible columns.
func padRight(
	s string,
	width int,
) string {
	return s + strings.Repeat(" ", max(0, width-lipgloss.Width(s)))
}
