package tui

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/internal/termimg"
	"github.com/eko/qc/ladder"
)

const (
	// minLadderWidth leaves room for the ladder table.
	minLadderWidth = 70
	// rqChartHeight is the height of the rate-quality plot of the report.
	rqChartHeight = 18
)

// ReportOptions shape the text report of a ladder.
type ReportOptions struct {
	// Width is the width of the terminal, in columns.
	Width int
	// JSONPath is where the JSON report was written, if it was.
	JSONPath string
	// Commands lists the encoding commands of the rungs.
	Commands bool
	// Pictures draws the charts as pictures by this protocol; as text
	// with termimg.None.
	Pictures termimg.Protocol
}

// RenderLadder writes a human-friendly ladder report.
func RenderLadder(
	w io.Writer,
	res *ladder.Result,
	opts ReportOptions,
) error {
	width := clampWidth(opts.Width, minLadderWidth)

	chart := rqChart(res, width)
	if picture, err := rqPicture(res, width, opts.Pictures); err == nil && picture != "" {
		chart = picture
	}

	blocks := []string{
		ladderHeader(res),
		chart,
		ladderTable(res.Rungs),
	}

	if table := rungQualityTable(res.Rungs); table != "" {
		blocks = append(blocks, table)
	}

	if block := energyBlock(res); block != "" {
		blocks = append(blocks, block)
	}

	if table := programTable(res); table != "" {
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

	if opts.Commands {
		blocks = append(blocks, ladderCommands(res.Rungs))
	}

	blocks = append(blocks, ladderFooter(res, opts.JSONPath))

	return writeBlocks(w, blocks)
}

func ladderHeader(
	res *ladder.Result,
) string {
	return reportTitle("ladder") + "\n\n" +
		subjectLines(res) + "\n" +
		key.Render("target") + Bold.Render(res.Codec.Name) + Subtle.Render(fmt.Sprintf(" (%s, preset %s)", res.Codec.Encoder, res.Preset)) + "\n" +
		key.Render("shape") + Subtle.Render(shapeLabel(res)) + "\n" +
		key.Render("digest") + Subtle.Render(fmt.Sprintf("%d segment(s)%s · %s · %.1f%% of the %s · %d probe encodes%s",
		len(res.Digest.Segments), samplingLabel(res.Digest.Sampling), Clock(res.Digest.Duration, false), res.Digest.Share*100, digestScope(res), res.ProbeEncodes(), probingLabel(res.Probing, len(res.Probes)-res.ProbeEncodes()))) +
		grainLine(res.Grain)
}

// subjectLines present what the ladder is for: its source, or the videos
// of its program, the first of which gives their common format.
func subjectLines(
	res *ladder.Result,
) string {
	if len(res.Sources) < 2 {
		return sourceLines("source", res.Source, false)
	}

	v, _ := res.Source.Info.PrimaryVideo()
	lines := []string{
		key.Render("program") + Bold.Render(fmt.Sprintf("%d videos", len(res.Sources))) +
			Subtle.Render(fmt.Sprintf(" · %s · %d×%d · %.3f fps", v.Codec, v.Width, v.Height, v.AvgFrameRate.Float())),
	}

	for _, s := range res.Sources {
		dir, file := filepath.Split(s.Info.Path)
		lines = append(lines, key.Render("")+Subtle.Render(dir)+Bold.Render(file)+Subtle.Render(" · "+Clock(s.Info.Duration, false)))
	}

	return strings.Join(lines, "\n")
}

// digestScope is what the digest is a share of.
func digestScope(
	res *ladder.Result,
) string {
	if len(res.Sources) > 1 {
		return "program, as many segments per video"
	}

	return "title"
}

// Per-video table column widths.
const (
	colVideo     = 28
	colVideoRung = 8
)

// programTable reads every verified rung video by video, for the ladder of
// a program: the VMAF of the rung on the frames of each video, and what
// the top rung costs there.
func programTable(
	res *ladder.Result,
) string {
	if len(res.Sources) < 2 || len(res.Rungs) == 0 || res.Rungs[0].Measured == nil {
		return ""
	}

	var head strings.Builder

	fmt.Fprintf(&head, "  %-*s", colVideo, "video")

	for i, r := range res.Rungs {
		fmt.Fprintf(&head, " %*s", colVideoRung, fmt.Sprintf("%d·%dp", i+1, r.Height))
	}

	fmt.Fprintf(&head, " %*s", colBitrate, "top rung")

	lines := []string{
		section.Render("Per-video quality") + Subtle.Render("  (VMAF of each rung on the frames of each video)"),
		Subtle.Render(head.String()),
	}

	for v := range res.Sources {
		var line strings.Builder

		fmt.Fprintf(&line, "  %-*s", colVideo, truncate(sourceName(res, v), colVideo))

		for _, r := range res.Rungs {
			cell := padLeft("–", colVideoRung)

			if m := r.Measured; m != nil && v < len(m.Titles) && m.Titles[v].ScoredFrames > 0 {
				cell = padLeft(scoreStyle(m.Titles[v].VMAF).Render(fmt.Sprintf("%.1f", m.Titles[v].VMAF)), colVideoRung)
			}

			line.WriteString(" " + cell)
		}

		top := "–"
		if titles := res.Rungs[0].Measured.Titles; v < len(titles) {
			top = Bitrate(float64(titles[v].Bitrate))
		}

		fmt.Fprintf(&line, " %*s", colBitrate, top)
		lines = append(lines, line.String())
	}

	return strings.Join(lines, "\n")
}

// truncate keeps the end of a name longer than width, where file names
// differ (an episode number), behind an ellipsis.
func truncate(
	name string,
	width int,
) string {
	r := []rune(name)
	if len(r) <= width {
		return name
	}

	return "…" + string(r[len(r)-width+1:])
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
		line := fmt.Sprintf("  %-24s %-11s %*s %*s %*s", rd.Name(), fmt.Sprintf("%d×%d", rd.Width, rd.Height),
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
	predicted int,
) string {
	var parts []string

	if p.Mode == ladder.ProbingAdaptive {
		state := "budget reached"
		if p.Converged {
			state = "converged"
		}

		parts = append(parts, fmt.Sprintf("adaptive: %d rounds, budget %d, %s", p.Rounds, p.Budget, state))
	}

	if p.Model != nil {
		parts = append(parts, p.Model.Note(predicted))
	}

	if p.Challengers > 0 {
		parts = append(parts, fmt.Sprintf("%d challenger probes", p.Challengers))
	}

	if len(parts) == 0 {
		return ""
	}

	return " (" + strings.Join(parts, "; ") + ")"
}

// samplingLabel describes how the segments of the digest were placed; a
// title used whole has no sampling.
func samplingLabel(
	s ladder.DigestSampling,
) string {
	switch s {
	case ladder.DigestBalanced:
		return ", balanced on SI/TI"
	case ladder.DigestTop:
		return ", the most complex scenes"
	case ladder.DigestUniform:
		return ", evenly spaced"
	}

	return ""
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

// resolutionLegend names the resolutions of the probes, highest first,
// each in the colour of its curve.
func resolutionLegend(
	probes []ladder.Probe,
) []string {
	_, heights := probeCurves(probes)
	legend := make([]string, len(heights))

	for i, h := range heights {
		legend[i] = seriesStyles[i%len(seriesStyles)].Render("━━") + Subtle.Render(fmt.Sprintf(" %dp", h))
	}

	return legend
}

// rqChart plots every probe per resolution and the rungs, numbered as in
// the ladder table, on the range the rungs span: the probes far above the
// top rung or under the lowest one tell nothing of the ladder.
func rqChart(
	res *ladder.Result,
	width int,
) string {
	legend := resolutionLegend(res.Probes)
	plot := Plot{Width: width, Height: rqChartHeight, XLog: true, YHi: maxVMAF, YStep: vmafStep, YLabel: vmafLabel, XLabel: tickBitrate}
	plot.XLo, plot.XHi, plot.YLo = rqFrame(res)

	if len(res.Rungs) > 0 {
		marker := Bold.Foreground(contrast)

		for i, r := range res.Rungs {
			plot.Marks = append(plot.Marks, Mark{X: float64(r.Bitrate), Y: r.PredictedVMAF, Glyph: rungGlyph(i), Style: marker})
		}

		legend = append(legend, marker.Render(string(rungGlyph(0))+"…"+string(rungGlyph(len(res.Rungs)-1)))+Subtle.Render(" rungs"))
	}

	plot.Series = envelopeSeries(res.Probes, plot.XLo, plot.XHi, 2*(width-gutter))
	legend = append(legend, Subtle.Render("── under the envelope"))
	lines := []string{section.Render("Rate / quality") + Subtle.Render("  (VMAF vs bitrate, log scale)   ") + strings.Join(legend, "  ")}

	return strings.Join(append(lines, plot.Lines()...), "\n")
}

// envelopeSeries draws the curve of each resolution as the ladder reads
// it (see envelopeLines): in the colour of its resolution where it is the
// upper envelope, the curve the rungs are placed on, and faint under it.
// Measured probes are dotted; predicted ones are only the line through
// them.
func envelopeSeries(
	probes []ladder.Probe,
	lo, hi float64,
	samples int,
) []Series {
	lines := envelopeLines(probes, lo, hi, samples)
	if len(lines) == 0 {
		return nil
	}

	series := make([]Series, len(lines))

	for i, l := range lines {
		style := seriesStyles[l.curve%len(seriesStyles)]

		switch {
		case l.probes:
			series[i] = Series{Points: l.points, Style: style}
		case l.onTop:
			series[i] = Series{Points: l.points, Line: true, NoDots: true, Style: style}
		default:
			series[i] = Series{Points: l.points, Line: true, NoDots: true, Style: Subtle}
		}
	}

	return series
}

// envelopeLine is a stretch of the curve of a resolution, on or under the
// envelope, or the measured probes of that resolution.
type envelopeLine struct {
	// curve is the index of the resolution, highest first.
	curve  int
	points [][2]float64
	onTop  bool
	probes bool
}

// envelopeLines cuts the curve of each resolution, VMAF linear in the
// logarithm of the bitrate between probes as the ladder reads it, sampled
// samples times from lo to hi (the range of the probes when hi is not
// above lo), into stretches on the upper envelope and under it: those
// under first, then those on it, then the measured probes, the order they
// are drawn in.
func envelopeLines(
	probes []ladder.Probe,
	lo, hi float64,
	samples int,
) []envelopeLine {
	curves, heights := probeCurves(probes)
	if len(curves) == 0 || samples < 2 {
		return nil
	}

	if hi <= lo {
		lo, hi = math.Inf(1), 0
		for _, p := range probes {
			lo, hi = min(lo, float64(p.Bitrate)), max(hi, float64(p.Bitrate))
		}
	}

	xs := make([]float64, samples)
	for i := range xs {
		xs[i] = math.Log(lo) + (math.Log(hi)-math.Log(lo))*float64(i)/float64(samples-1)
	}

	top := make([]float64, samples)
	for i, x := range xs {
		top[i] = math.Inf(-1)
		for _, c := range curves {
			if v, ok := curveAt(c, x); ok {
				top[i] = max(top[i], v)
			}
		}
	}

	var under, on, dots []envelopeLine

	for k, c := range curves {
		for _, r := range curveRuns(c, xs, top) {
			if r.onTop {
				on = append(on, envelopeLine{curve: k, points: r.points, onTop: true})
			} else {
				under = append(under, envelopeLine{curve: k, points: r.points})
			}
		}

		var measured [][2]float64

		for _, p := range probes {
			if p.Height == heights[k] && !p.Predicted {
				measured = append(measured, [2]float64{float64(p.Bitrate), p.VMAF})
			}
		}

		if len(measured) > 0 {
			dots = append(dots, envelopeLine{curve: k, points: measured, probes: true})
		}
	}

	return slices.Concat(under, on, dots)
}

// envelopeSlack is how far under the envelope, in VMAF, a curve still
// draws it: two curves crossing share it.
const envelopeSlack = 0.05

// run is a stretch of a curve on or under the envelope.
type run struct {
	points [][2]float64
	onTop  bool
}

// curveRuns cuts curve c, sampled at the log bitrates xs, into stretches
// on the envelope top and under it. Consecutive stretches share their
// boundary sample: the line does not break where it changes colour.
func curveRuns(
	c [][2]float64,
	xs, top []float64,
) []run {
	var (
		runs []run
		last [2]float64
		have bool
	)

	for i, x := range xs {
		v, ok := curveAt(c, x)
		if !ok {
			have = false

			continue
		}

		pt := [2]float64{math.Exp(x), v}
		onTop := v >= top[i]-envelopeSlack

		switch n := len(runs); {
		case !have:
			runs = append(runs, run{points: [][2]float64{pt}, onTop: onTop})
		case runs[n-1].onTop != onTop:
			runs = append(runs, run{points: [][2]float64{last, pt}, onTop: onTop})
		default:
			runs[n-1].points = append(runs[n-1].points, pt)
		}

		last, have = pt, true
	}

	return runs
}

// probeCurves are the curves of the probes, one per resolution, highest
// first: log bitrate and VMAF, by growing bitrate.
func probeCurves(
	probes []ladder.Probe,
) ([][][2]float64, []int) {
	byHeight := map[int][][2]float64{}

	for _, p := range probes {
		if p.Bitrate > 0 {
			byHeight[p.Height] = append(byHeight[p.Height], [2]float64{math.Log(float64(p.Bitrate)), p.VMAF})
		}
	}

	heights := slices.Sorted(maps.Keys(byHeight))
	slices.Reverse(heights)

	curves := make([][][2]float64, len(heights))
	for i, h := range heights {
		curves[i] = byHeight[h]
		slices.SortFunc(curves[i], func(a, b [2]float64) int { return cmp.Compare(a[0], b[0]) })
	}

	return curves, heights
}

// curveAt reads curve c at log bitrate x, linearly between its points;
// false outside them.
func curveAt(
	c [][2]float64,
	x float64,
) (float64, bool) {
	if len(c) == 0 || x < c[0][0] || x > c[len(c)-1][0] {
		return 0, false
	}

	i := max(sort.Search(len(c), func(i int) bool { return c[i][0] >= x }), 1)
	if i >= len(c) {
		return c[len(c)-1][1], true
	}

	a, b := c[i-1], c[i]
	if b[0] == a[0] {
		return max(a[1], b[1]), true
	}

	return a[1] + (b[1]-a[1])*(x-a[0])/(b[0]-a[0]), true
}

// rqFrame is the range a rate-quality chart of res shows: the bitrates
// its rungs span, widened on each side, and VMAF from under the lowest
// rung; without rungs, every probe (lo and hi both zero).
func rqFrame(
	res *ladder.Result,
) (lo, hi, floor float64) {
	worst := float64(maxVMAF)
	for _, p := range res.Probes {
		worst = min(worst, p.VMAF)
	}

	if len(res.Rungs) == 0 {
		return 0, 0, vmafFloor(worst)
	}

	lo, hi, worst = math.Inf(1), 0, maxVMAF

	for _, r := range res.Rungs {
		lo, hi, worst = min(lo, float64(r.Bitrate)), max(hi, float64(r.Bitrate)), min(worst, r.PredictedVMAF)
	}

	return lo / rungMargin, hi * rungMargin, vmafFloor(worst - vmafMargin)
}

const (
	// maxVMAF is the top of a VMAF axis, and vmafStep the step of its
	// labels.
	maxVMAF  = 100
	vmafStep = 10
	// rungMargin widens the bitrates the rungs span, on each side, and
	// vmafMargin lowers the VMAF of the lowest: the curves are seen
	// around the rungs.
	rungMargin = 1.6
	vmafMargin = 5
)

// rungGlyphs number the rungs of a plot as the ladder table does.
const rungGlyphs = "123456789abcdefghijklmnopqrstuvwxyz"

// rungGlyph is the mark of rung i (from 0).
func rungGlyph(
	i int,
) rune {
	glyphs := []rune(rungGlyphs)

	return glyphs[min(i, len(glyphs)-1)]
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

// energyBlock tells what encoding each rung on the whole title costs, in
// CPU time and, at a power per core, in energy; and which rungs the energy
// tolerance moved to a lower resolution, for what saving.
func energyBlock(
	res *ladder.Result,
) string {
	e := res.Energy
	if e == nil {
		return ""
	}

	lines := []string{section.Render("Energy") + Subtle.Render("  (encoding the whole title, from the CPU time of the encodes of the digest)")}

	for i, r := range res.Rungs {
		if r.Encode == nil {
			continue
		}

		cost := cpuHours(r.Encode.CPUHours)
		if r.Encode.Estimated {
			cost += Subtle.Render("  estimated from the encodes of its resolution")
		}

		lines = append(lines, fmt.Sprintf("  %-3d %-11s %s", i+1, fmt.Sprintf("%d×%d", r.Width, r.Height), cost))
	}

	if e.DecodeCPUHours > 0 {
		lines = append(lines, Subtle.Render("      decoding the source  "+cpuHours(e.DecodeCPUHours)+", once if the rungs share one decode, per rung otherwise"))
	}

	if e.CPUHours > 0 {
		total := "  " + Bold.Render("all rungs  "+cpuHours(e.CPUHours+e.DecodeCPUHours))
		if e.KWh > 0 {
			total += Bold.Render(" · "+energyLabel(e.KWh)) + Subtle.Render(fmt.Sprintf(" at %g W per core", e.CoreWatts))
		} else {
			total += Subtle.Render("  (--core-watts gives the energy)")
		}

		lines = append(lines, total)
	}

	for _, l := range e.Lowered {
		lines = append(lines, Subtle.Render(fmt.Sprintf("  rung %d at %dp instead of %dp: %.1f VMAF less, same bitrate", l.Rung+1, l.To, l.From, l.VMAFLost)))
	}

	switch {
	case e.SavedCPUHours > 0:
		lines = append(lines, Green.Render(fmt.Sprintf("  energy tolerance %g VMAF: about %s saved (%.0f%% of the ladder, in proportion of pixels)",
			e.Tolerance, cpuHours(e.SavedCPUHours), 100*e.SavedCPUHours/(e.CPUHours+e.SavedCPUHours))))
	case e.Tolerance > 0 && len(e.Lowered) == 0:
		lines = append(lines, Subtle.Render(fmt.Sprintf("  energy tolerance %g VMAF: no lower resolution comes that close", e.Tolerance)))
	}

	return strings.Join(lines, "\n")
}

// cpuHours writes a CPU time in core-hours, core-minutes or core-seconds.
func cpuHours(
	h float64,
) string {
	switch {
	case h >= 1:
		return fmt.Sprintf("%.1f core-h", h)
	case h*minutesPerHour >= 1:
		return fmt.Sprintf("%.1f core-min", h*minutesPerHour)
	}

	return fmt.Sprintf("%.0f core-s", h*secondsPerHour)
}

// energyLabel writes an energy in kWh, or Wh under one.
func energyLabel(
	kWh float64,
) string {
	if kWh >= 1 {
		return fmt.Sprintf("%.2f kWh", kWh)
	}

	return fmt.Sprintf("%.1f Wh", kWh*wattsPerKilowatt)
}

const (
	minutesPerHour   = 60
	secondsPerHour   = 3600
	wattsPerKilowatt = 1000
)

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
	timings := stageTimings(res.Timings, "analysis", "digest", "grain", "probe", "verify", "shots")
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
