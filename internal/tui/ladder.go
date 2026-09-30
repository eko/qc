package tui

import (
	"cmp"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/ladder"
)

const (
	// minLadderWidth leaves room for the ladder table.
	minLadderWidth = 70
	// rqChartHeight is the height of the rate-quality plot of the report.
	rqChartHeight = 14
)

// RenderLadder writes a human-friendly ladder report.
func RenderLadder(
	w io.Writer,
	res *ladder.Result,
	width int,
	jsonPath string,
	commands bool,
) error {
	width = clampWidth(width, minLadderWidth)

	blocks := []string{
		ladderHeader(res),
		rqChart(res, width),
		ladderTable(res.Rungs),
	}

	if table := rungQualityTable(res.Rungs); table != "" {
		blocks = append(blocks, table)
	}

	if table := perShotTable(res.Rungs, res.ShotProbing); table != "" {
		blocks = append(blocks, table)
	}

	if table := perShotLadder(res, width); table != "" {
		blocks = append(blocks, table)
	}

	if table := renditionsTable(res); table != "" {
		blocks = append(blocks, table)
	}

	blocks = append(blocks, findingsBlock(ladderFindings(res)))

	if commands {
		blocks = append(blocks, ladderCommands(res.Rungs))
	}

	blocks = append(blocks, ladderFooter(res, jsonPath))

	return writeBlocks(w, blocks)
}

func ladderHeader(
	res *ladder.Result,
) string {
	return reportTitle("ladder") + "\n\n" +
		sourceLines("source", res.Source, false) + "\n" +
		key.Render("target") + Bold.Render(res.Codec.Name) + Subtle.Render(fmt.Sprintf(" (%s, preset %s)", res.Codec.Encoder, res.Preset)) + "\n" +
		key.Render("shape") + Subtle.Render(shapeLabel(res)) + "\n" +
		key.Render("digest") + Subtle.Render(fmt.Sprintf("%d segment(s) · %s · %.1f%% of the title · %d probe encodes%s",
		len(res.Digest.Segments), Clock(res.Digest.Duration, false), res.Digest.Share*100, len(res.Probes), probingLabel(res.Probing))) +
		grainLine(res.Grain)
}

// grainLine describes film grain synthesis, when requested.
func grainLine(
	g *ladder.GrainReport,
) string {
	if g == nil {
		return ""
	}

	text := fmt.Sprintf("source noise σ %.2f", g.Source.Sigma)

	switch {
	case g.Level > 0:
		text += fmt.Sprintf(" · film grain synthesis level %d, fidelity scored against the denoised reference", g.Level)
	case g.Detected:
		text += " · grain detected, synthesis off"
	default:
		text += " · no grain detected, synthesis off"
	}

	trials := make([]string, len(g.Trials))
	for i, tr := range g.Trials {
		trials[i] = fmt.Sprintf("%d→%.0f%%", tr.Level, tr.Ratio*100)
	}

	if len(trials) > 0 {
		text += " (grain given back: " + strings.Join(trials, ", ") + ")"
	}

	return "\n" + key.Render("grain") + Subtle.Render(text)
}

// renditionsTable lists the renditions encoded on the whole title, with
// what the ladder predicted for them on the digest.
func renditionsTable(
	res *ladder.Result,
) string {
	if len(res.Renditions) == 0 {
		return ""
	}

	lines := []string{
		section.Render("Renditions") + Subtle.Render("  (the whole title; predictions made on the digest)"),
		Subtle.Render(fmt.Sprintf("  %-24s %-11s %*s %*s %*s %*s", "file", "resolution", colBitrate, "predicted", colBitrate, "bitrate",
			colVMAF, "predicted", colMeasured, "VMAF")),
	}

	for _, rd := range res.Renditions {
		vmaf, bitrate := res.Prediction(rd)
		line := fmt.Sprintf("  %-24s %-11s %*s %*s %*s", filepath.Base(rd.Path), fmt.Sprintf("%d×%d", rd.Width, rd.Height),
			colBitrate, Bitrate(bitrate), colBitrate, Bitrate(float64(rd.Bitrate)), colVMAF, fmt.Sprintf("%.1f", vmaf))

		if c := rd.Checked; c != nil {
			line += " " + padLeft(scoreStyle(c.VMAF).Render(c.VMAFLabel()), colMeasured)
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// perShotTable compares every per-shot rung with its per-title version, both
// measured on the digest.
func perShotTable(
	rungs []ladder.Rung,
	probing *ladder.ShotProbing,
) string {
	lines := []string{section.Render("Per-shot") + Subtle.Render("  ("+perShotScope(probing)+")")}

	for i, r := range rungs {
		ps := r.PerShot
		if ps == nil {
			continue
		}

		low, high := ps.Chunks[0].CRF, ps.Chunks[0].CRF
		for _, c := range ps.Chunks {
			low, high = math.Min(low, c.CRF), math.Max(high, c.CRF)
		}

		line := fmt.Sprintf("  %-3d %-6s %3d chunks  CRF %4.1f–%-4.1f  predicted %s at VMAF %.1f", i+1, fmt.Sprintf("%dp", r.Height),
			len(ps.Chunks), low, high, Bitrate(float64(ps.PredictedBitrate)), ps.PredictedVMAF)

		if m := ps.Measured; m != nil {
			line += fmt.Sprintf("  measured %s at VMAF %.1f", Bitrate(float64(m.Bitrate)), m.VMAF)
		}

		if r.Measured != nil && ps.Measured != nil {
			line += "  " + gainStyle(ps.Gain).Render(fmt.Sprintf("%+.1f%% at equal VMAF", -ps.Gain*100))
		}

		if ps.Height > 0 && ps.Height != r.Height {
			line += Subtle.Render(fmt.Sprintf("  declares %dp", ps.Height))
		}

		lines = append(lines, line)
	}

	if len(lines) == 1 {
		return ""
	}

	return strings.Join(lines, "\n")
}

// perShotScope describes the per-shot rungs and what their probes cost.
func perShotScope(
	probing *ladder.ShotProbing,
) string {
	scope := "one CRF per shot at equal rate-quality slope, same pooled quality"
	if probing == nil {
		return scope
	}

	if probing.Resolution {
		scope = "one resolution and CRF per shot at equal rate-quality slope, same pooled quality"
	}

	scope += fmt.Sprintf("; %d exact probes of the digest", probing.Probes)
	if probing.Extra > 0 {
		scope += fmt.Sprintf(", %d of them to reach the neighbouring resolutions", probing.Extra)
	}

	return scope
}

// gainStyle colours a bitrate saving: green when per-shot saves bits.
func gainStyle(
	gain float64,
) lipgloss.Style {
	if gain > 0 {
		return Green
	}

	return Yellow
}

// probingLabel describes adaptive probing (rounds, budget, convergence)
// and the challenger probes.
func probingLabel(
	p ladder.ProbingReport,
) string {
	var parts []string

	if p.Mode == ladder.ProbingAdaptive {
		state := "budget reached"
		if p.Converged {
			state = "converged"
		}

		parts = append(parts, fmt.Sprintf("adaptive: %d rounds, budget %d, %s", p.Rounds, p.Budget, state))
	}

	if p.Challengers > 0 {
		parts = append(parts, fmt.Sprintf("%d challenger probes", p.Challengers))
	}

	if len(parts) == 0 {
		return ""
	}

	return " (" + strings.Join(parts, "; ") + ")"
}

// shapeLabel describes how the rungs were chosen.
func shapeLabel(
	res *ladder.Result,
) string {
	c := res.Constraints

	switch res.Shape {
	case ladder.ShapeCount:
		return fmt.Sprintf("%d rungs requested, VMAF %.0f down to the title's floor, resolutions automatic", c.Rungs, c.TopVMAF)
	case ladder.ShapeResolutions:
		heights := make([]string, len(c.Resolutions))
		for i, h := range c.Resolutions {
			heights[i] = fmt.Sprintf("%dp", h)
		}

		return fmt.Sprintf("resolutions requested (%s), VMAF %.0f down to the title's floor", strings.Join(heights, ", "), c.TopVMAF)
	}

	return fmt.Sprintf("automatic: from VMAF %.0f down by %.0f points, bitrate ratios %.1f–%.1f", c.TopVMAF, c.Step, c.MinRatio, c.MaxRatio)
}

// probeSeries groups probes per resolution, highest first, into plot series
// joined by lines, with their legend entries and the lowest probed VMAF.
func probeSeries(
	probes []ladder.Probe,
) ([]Series, []string, float64) {
	byHeight := map[int][][2]float64{}
	lowest := 100.0

	for _, p := range probes {
		byHeight[p.Height] = append(byHeight[p.Height], [2]float64{float64(p.Bitrate), p.VMAF})
		lowest = min(lowest, p.VMAF)
	}

	heights := make([]int, 0, len(byHeight))
	for h := range byHeight {
		heights = append(heights, h)
	}

	slices.Sort(heights)
	slices.Reverse(heights)

	series := make([]Series, 0, len(heights))
	legend := make([]string, 0, len(heights))

	for i, h := range heights {
		pts := byHeight[h]
		slices.SortFunc(pts, func(a, b [2]float64) int { return cmp.Compare(a[0], b[0]) })

		style := seriesStyles[i%len(seriesStyles)]
		series = append(series, Series{Points: pts, Style: style, Line: true})
		legend = append(legend, style.Render("━━")+Subtle.Render(fmt.Sprintf(" %dp", h)))
	}

	return series, legend, lowest
}

// rqChart plots every probe per resolution and the rungs.
func rqChart(
	res *ladder.Result,
	width int,
) string {
	series, legend, lowest := probeSeries(res.Probes)

	rungs := Series{Style: lipgloss.NewStyle().Foreground(contrast)}
	for _, r := range res.Rungs {
		rungs.Points = append(rungs.Points, [2]float64{float64(r.Bitrate), r.PredictedVMAF})
	}

	series = append(series, rungs)
	legend = append(legend, Bold.Render("⣿")+Subtle.Render(" rungs"))

	lines := []string{section.Render("Rate / quality") + Subtle.Render("  (VMAF vs bitrate, log scale)   ") + strings.Join(legend, "  ")}
	lines = append(lines, XYPlot(series, width, rqChartHeight, true, vmafFloor(lowest), 100, vmafLabel)...)
	lines = append(lines, bitrateAxis(res.Probes, width))

	return strings.Join(lines, "\n")
}

// bitrateAxis labels the log-scale bitrate axis of the probes: the middle
// label is the geometric mean.
func bitrateAxis(
	probes []ladder.Probe,
	width int,
) string {
	if len(probes) == 0 {
		return ""
	}

	lo, hi := math.Inf(1), 0.0
	for _, p := range probes {
		lo, hi = min(lo, float64(p.Bitrate)), max(hi, float64(p.Bitrate))
	}

	return axisLine(compactBitrate(lo), compactBitrate(math.Sqrt(lo*hi)), compactBitrate(hi), width)
}

// Ladder table columns widths.
const (
	colBitrate  = 11
	colVMAF     = 10
	colMeasured = 15
)

func ladderTable(
	rungs []ladder.Rung,
) string {
	if len(rungs) == 0 {
		return ""
	}

	verified := rungs[0].Measured != nil

	head := fmt.Sprintf("  %-3s %-11s %*s %6s %*s %*s", "#", "resolution", colBitrate, "bitrate", "CRF", colBitrate, "maxrate", colVMAF, "VMAF")
	if verified {
		head += fmt.Sprintf(" %*s %*s", colMeasured, "measured VMAF", colBitrate, "measured")
	}

	lines := []string{section.Render("Ladder"), Subtle.Render(head)}

	for i, r := range rungs {
		lines = append(lines, rungRow(i, r))
	}

	return strings.Join(lines, "\n")
}

// rungRow is one line of the ladder table. Coloured cells are padded after
// styling: fmt widths would count escape sequences.
func rungRow(
	i int,
	r ladder.Rung,
) string {
	text := fmt.Sprintf("%.1f", r.PredictedVMAF)
	if r.PredictionError > 0 {
		text += fmt.Sprintf("±%.1f", r.PredictionError)
	}

	predicted := scoreStyle(r.PredictedVMAF).Render(text)
	line := fmt.Sprintf("  %-3d %-11s %*s %6.1f %*s %s", i+1, fmt.Sprintf("%d×%d", r.Width, r.Height),
		colBitrate, Bitrate(float64(r.Bitrate)), r.CRF, colBitrate, Bitrate(float64(r.MaxRate)), padLeft(predicted, colVMAF))

	m := r.Measured
	if m == nil {
		return line
	}

	measured := scoreStyle(m.VMAF).Render(fmt.Sprintf("%.1f ± %.1f", m.VMAF, m.HalfWidth))
	line += fmt.Sprintf(" %s %*s", padLeft(measured, colMeasured), colBitrate, Bitrate(float64(m.Bitrate)))

	if r.Calibrated {
		line += Yellow.Render(" ✱")
	}

	return line
}

func ladderCommands(
	rungs []ladder.Rung,
) string {
	lines := []string{section.Render("Encoding commands")}
	for _, r := range rungs {
		lines = append(lines, Subtle.Render(r.Command))
	}

	for _, r := range rungs {
		if r.PerShot != nil {
			lines = append(lines, Subtle.Render(r.PerShot.Command))
		}
	}

	return strings.Join(lines, "\n")
}

func ladderFooter(
	res *ladder.Result,
	jsonPath string,
) string {
	timings := stageTimings(res.Timings, "digest", "grain", "probe", "verify", "shots")
	if timings != "" {
		timings += " · "
	}

	line := timingsLine(timings + "total " + Elapsed(res.Elapsed.Std()))
	if jsonPath != "" {
		line += "\n" + writtenLine("full report", jsonPath)
	}

	return line
}

// RenderWrittenVideo reports the annotated copy of a video written, when
// one was (path not empty).
func RenderWrittenVideo(
	w io.Writer,
	path string,
) error {
	if path == "" {
		return nil
	}

	if _, err := fmt.Fprintln(w, writtenLine("annotated video", path)); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

// RenderWritten reports the files written, when any.
func RenderWritten(
	w io.Writer,
	jsonPath, htmlPath string,
) error {
	for _, path := range []string{jsonPath, htmlPath} {
		if path == "" {
			continue
		}

		if _, err := fmt.Fprintln(w, writtenLine("report", path)); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}

	return nil
}
