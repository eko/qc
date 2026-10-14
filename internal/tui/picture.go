package tui

import (
	"cmp"
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/internal/chartimg"
	"github.com/eko/qc/internal/termimg"
	"github.com/eko/qc/ladder"
)

// Charts drawn as pictures, for terminals that show them (package
// termimg): the same frame and lines as the charts of characters, smooth.

// Size of a picture chart: as many points wide as the report has columns
// times pointsPerColumn, about the width of a cell, so that its text reads
// at the size of the terminal's; pictureRatio of that high. Pictures are
// drawn at pictureScale times their size, sharp on dense screens.
const (
	pointsPerColumn = 7.5
	pictureRatio    = 0.42
	pictureScale    = 2
	// pictureSamples is the number of points each curve is drawn through.
	pictureSamples = 600
)

// Strokes of the picture charts, in points.
const (
	envelopeWidth = 2.8
	underWidth    = 1.3
	probeDot      = 6
	codecWidth    = 2.6
	rungDot       = 7
)

// palette is the categorical palette of the HTML report (validated for
// colour-blind readers), light then dark: the resolutions of a ladder,
// the codecs of a comparison.
var (
	lightPalette = []color.RGBA{rgb(0x2a78d6), rgb(0xeb6834), rgb(0x1baf7a), rgb(0xe0a000), rgb(0xe87ba4), rgb(0x008300), rgb(0x4a3aa7)}
	darkPalette  = []color.RGBA{rgb(0x3987e5), rgb(0xd95926), rgb(0x199e70), rgb(0xc98500), rgb(0xd55181), rgb(0x22a022), rgb(0x9085e9)}
)

// pictureTheme are the colours of a picture on a dark or light terminal:
// the text, the secondary text, the grid, the curves under the envelope
// and the label of a rung, on its disc of text colour.
type pictureTheme struct {
	chartimg.Theme

	under, onInk color.RGBA
	palette      []color.RGBA
}

func themeFor(
	dark bool,
) pictureTheme {
	if dark {
		return pictureTheme{
			Theme: chartimg.Theme{Ink: rgb(0xf1f1ef), Muted: rgb(0x8f8f98), Grid: rgb(0x2c2c32)},
			under: rgb(0x55555d), onInk: rgb(0x141417), palette: darkPalette,
		}
	}

	return pictureTheme{
		Theme: chartimg.Theme{Ink: rgb(0x141417), Muted: rgb(0x6b6b73), Grid: rgb(0xe7e6e1)},
		under: rgb(0xc9c8c0), onInk: rgb(0xffffff), palette: lightPalette,
	}
}

func rgb(
	hex uint32,
) color.RGBA {
	return color.RGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff} //nolint:gosec,mnd // bytes of a 24-bit colour
}

// pictureChart is a chart width columns wide, on the frame and axes of
// the charts of characters.
func pictureChart(
	width int,
	theme pictureTheme,
	lo, hi, floor float64,
) chartimg.Chart {
	w := float64(width) * pointsPerColumn
	c := chartimg.Chart{
		Width: int(w), Height: int(w * pictureRatio), Scale: pictureScale,
		XLo: lo, XHi: hi, YLo: floor, YHi: maxVMAF, XLog: true,
		XTicks: ticks(math.Log(lo), math.Log(hi), true), XLabel: tickBitrate,
		YStep: vmafStep, YLabel: vmafLabel, Theme: theme.Theme,
	}

	return c
}

// picture draws c by protocol under a title line, or nothing with None.
func picture(
	c chartimg.Chart,
	title string,
	width int,
	protocol termimg.Protocol,
) (string, error) {
	if protocol == termimg.None {
		return "", nil
	}

	data, err := c.PNG()
	if err != nil {
		return "", err
	}

	var b strings.Builder

	b.WriteString(title + "\n")

	if err := termimg.Write(&b, data, width, protocol); err != nil {
		return "", err
	}

	return strings.TrimSuffix(b.String(), "\n"), nil
}

// rqPicture is the rate-quality chart of res as a picture: each curve as
// the ladder reads it, bold in the colour of its resolution where it is
// the upper envelope and thin and faint under it, measured probes dotted,
// the rungs numbered as in the ladder table.
func rqPicture(
	res *ladder.Result,
	width int,
	protocol termimg.Protocol,
) (string, error) {
	if protocol == termimg.None || len(res.Probes) == 0 {
		return "", nil
	}

	theme := themeFor(lipgloss.HasDarkBackground())
	lo, hi, floor := rqFrame(res)

	if hi <= lo {
		lo, hi = math.Inf(1), 0
		for _, p := range res.Probes {
			lo, hi = min(lo, float64(p.Bitrate)), max(hi, float64(p.Bitrate))
		}
	}

	c := pictureChart(width, theme, lo, hi, floor)
	_, heights := probeCurves(res.Probes)

	for _, l := range envelopeLines(res.Probes, lo, hi, pictureSamples) {
		colour := theme.palette[l.curve%len(theme.palette)]

		switch {
		case l.probes:
			c.Lines = append(c.Lines, chartimg.Line{Points: l.points, Color: colour, Width: probeDot, Dots: true})
		case l.onTop:
			c.Lines = append(c.Lines, chartimg.Line{Points: l.points, Color: colour, Width: envelopeWidth})
		default:
			c.Lines = append(c.Lines, chartimg.Line{Points: l.points, Color: theme.under, Width: underWidth})
		}
	}

	for i, h := range heights {
		c.Legend = append(c.Legend, chartimg.Key{Label: fmt.Sprintf("%dp", h), Color: theme.palette[i%len(theme.palette)]})
	}

	c.Legend = append(c.Legend, chartimg.Key{Label: "under the envelope", Color: theme.under})

	for i, r := range res.Rungs {
		c.Markers = append(c.Markers, chartimg.Marker{X: float64(r.Bitrate), Y: r.PredictedVMAF, Label: string(rungGlyph(i)), Fill: theme.Ink, Text: theme.onInk})
	}

	title := section.Render("Rate / quality") + Subtle.Render("  (VMAF vs bitrate, log scale) · rungs numbered as in the table")

	return picture(c, title, width, protocol)
}

// codecPicture draws the rungs of the ladder of each codec on one
// picture: the gap between the lines is what a codec saves on another at
// equal quality.
func codecPicture(
	ladders []*ladder.Result,
	width int,
	protocol termimg.Protocol,
) (string, error) {
	if protocol == termimg.None {
		return "", nil
	}

	width = clampWidth(width, minLadderWidth)
	theme := themeFor(lipgloss.HasDarkBackground())
	lo, hi, worst := math.Inf(1), 0.0, float64(maxVMAF)

	var lines []chartimg.Line

	var keys []chartimg.Key

	for i, l := range ladders {
		colour := theme.palette[i%len(theme.palette)]
		line := chartimg.Line{Color: colour, Width: codecWidth}

		for _, r := range l.Rungs {
			bitrate, vmaf := float64(rungBitrate(r)), rungVMAF(r)
			line.Points = append(line.Points, [2]float64{bitrate, vmaf})
			lo, hi, worst = min(lo, bitrate), max(hi, bitrate), min(worst, vmaf)
		}

		slices.SortFunc(line.Points, func(a, b [2]float64) int { return cmp.Compare(a[0], b[0]) })
		dots := line
		dots.Dots, dots.Width = true, rungDot
		lines = append(lines, line, dots)
		keys = append(keys, chartimg.Key{Label: l.Codec.Name, Color: colour})
	}

	if math.IsInf(lo, 1) {
		return "", nil
	}

	c := pictureChart(width, theme, lo/rungMargin, hi*rungMargin, vmafFloor(worst-vmafMargin))
	c.Lines, c.Legend = lines, keys
	title := section.Render("Rungs of each codec") + Subtle.Render("  (VMAF vs bitrate, log scale)")

	return picture(c, title, width, protocol)
}

// Live pictures: the panel of the ladder stage drawn as a picture in the
// dashboard, redrawn as the probes land.
const (
	// panelPictureRows is the height of the picture of a panel, in rows.
	panelPictureRows = 15
	// cellRatio is about the height of a terminal cell over its width:
	// a picture stretched over a box of cells keeps its proportions.
	cellRatio = 2.05
	// probeMargin widens the bitrates of the probes on each side, while
	// no rung frames the chart yet.
	probeMargin = 1.15
)

// lastProbe is the accent of the probe that landed last.
var (
	lastProbeDark  = rgb(0xff9f43)
	lastProbeLight = rgb(0xc2410c)
)

// pictured tells whether the panel draws its current stage as a picture:
// the probes landing, then the rungs being verified on them.
func (p LadderPanel) pictured() bool {
	switch p.Stage {
	case ladder.StageAnalysis, ladder.StageDigest, ladder.StageGrain, ladder.StageAnchor, ladder.StageShots:
		return false
	}

	return len(p.Probes) > 0
}

// PictureKey implements Pictured: it changes with each probe that lands
// and each rung placed or verified.
func (p LadderPanel) PictureKey() string {
	measured := 0

	for _, r := range p.Rungs {
		if r.Measured != nil {
			measured++
		}
	}

	return fmt.Sprintf("%s/%d/%d/%d", p.Stage, len(p.Probes), len(p.Rungs), measured)
}

// Picture implements Pictured: the curves of the probes landed so far,
// bold on their upper envelope, the measured probes dotted and the last
// one ringed in the accent; once rungs are placed, they are numbered on
// the curves, at their measured point once verified.
func (p LadderPanel) Picture(
	cols, rows int,
	dark bool,
) (string, []byte, bool) {
	if !p.pictured() {
		return "", nil, false
	}

	theme := themeFor(dark)
	lo, hi, floor := p.frame()

	c := pictureChart(cols, theme, lo, hi, floor)
	c.Height = int(float64(rows) * pointsPerColumn * cellRatio)

	_, heights := probeCurves(p.Probes)

	for _, l := range envelopeLines(p.Probes, lo, hi, pictureSamples) {
		colour := theme.palette[l.curve%len(theme.palette)]

		switch {
		case l.probes:
			c.Lines = append(c.Lines, chartimg.Line{Points: l.points, Color: colour, Width: probeDot, Dots: true})
		case l.onTop:
			c.Lines = append(c.Lines, chartimg.Line{Points: l.points, Color: colour, Width: envelopeWidth})
		default:
			c.Lines = append(c.Lines, chartimg.Line{Points: l.points, Color: theme.under, Width: underWidth})
		}
	}

	for i, h := range heights {
		c.Legend = append(c.Legend, chartimg.Key{Label: fmt.Sprintf("%dp", h), Color: theme.palette[i%len(theme.palette)]})
	}

	head := p.pictureHead()

	if last, ok := lastEncoded(p.Probes); ok && len(p.Rungs) == 0 {
		accent := lastProbeLight
		if dark {
			accent = lastProbeDark
		}

		c.Markers = append(c.Markers, chartimg.Marker{X: float64(last.Bitrate), Y: last.VMAF, Fill: accent, Text: accent})
	}

	for i, r := range p.Rungs {
		x, y := float64(r.Bitrate), r.PredictedVMAF
		if r.Measured != nil {
			x, y = float64(r.Measured.Bitrate), r.Measured.VMAF
		}

		c.Markers = append(c.Markers, chartimg.Marker{X: x, Y: y, Label: string(rungGlyph(i)), Fill: theme.Ink, Text: theme.onInk})
	}

	data, err := c.PNG()
	if err != nil {
		return "", nil, false
	}

	return head, data, true
}

// lastEncoded is the last probe actually encoded: the one that just
// landed, a predicted probe having landed nowhere.
func lastEncoded(
	probes []ladder.Probe,
) (ladder.Probe, bool) {
	for i := len(probes) - 1; i >= 0; i-- {
		if !probes[i].Predicted {
			return probes[i], true
		}
	}

	return ladder.Probe{}, false
}

// Qualities a ladder spans, which frame the live picture before its rungs
// are placed: a model predicts the whole grid, from VMAF 0 to 100.
const (
	ladderLowVMAF  = 30
	ladderHighVMAF = 99
)

// frame is the range of the live picture: around the rungs once they are
// placed; before, around the probes of the qualities a ladder spans, and
// every probe actually encoded.
func (p LadderPanel) frame() (lo, hi, floor float64) {
	if len(p.Rungs) > 0 {
		return rqFrame(&ladder.Result{Probes: p.Probes, Rungs: p.Rungs})
	}

	lo, hi, worst := math.Inf(1), 0.0, float64(maxVMAF)

	for _, pr := range p.Probes {
		if pr.Predicted && (pr.VMAF < ladderLowVMAF || pr.VMAF > ladderHighVMAF) {
			continue
		}

		lo, hi, worst = min(lo, float64(pr.Bitrate)), max(hi, float64(pr.Bitrate)), min(worst, pr.VMAF)
	}

	if math.IsInf(lo, 1) {
		for _, pr := range p.Probes {
			lo, hi, worst = min(lo, float64(pr.Bitrate)), max(hi, float64(pr.Bitrate)), min(worst, pr.VMAF)
		}
	}

	if hi <= lo {
		hi = lo * probeMargin * probeMargin
	}

	return lo / probeMargin, hi * probeMargin, vmafFloor(worst - vmafMargin)
}

// pictureHead is the line over the live picture.
func (p LadderPanel) pictureHead() string {
	if len(p.Rungs) > 0 {
		measured := 0

		for _, r := range p.Rungs {
			if r.Measured != nil {
				measured++
			}
		}

		return Bold.Render("verifying rungs") + Subtle.Render(fmt.Sprintf("  %d of %d encoded with their final settings, numbered as in the ladder", measured, len(p.Rungs)))
	}

	last := p.Probes[len(p.Probes)-1]

	return Bold.Render("probe encodes") + Subtle.Render(fmt.Sprintf("  %d landed · last: %dp crf %.0f → %s, VMAF %.1f",
		len(p.Probes), last.Height, last.CRF, Bitrate(float64(last.Bitrate)), last.VMAF))
}
