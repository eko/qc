package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

const (
	// minGaugeWidth keeps the live VMAF gauge readable on narrow panels.
	minGaugeWidth = 20
	// probePlotHeight is the height of the live rate-quality plot.
	probePlotHeight = 10
)

// Panel is the live view of the running stage.
type Panel interface {
	// View renders the panel width columns wide.
	View(width int) string
}

// Pictured is a panel that can also draw itself as a picture, for
// terminals that show pictures in place.
type Pictured interface {
	Panel
	// Picture draws the panel cols columns wide and rows high on a dark
	// or light terminal, with the line of text over it; false when the
	// panel has no picture at its stage.
	Picture(cols, rows int, dark bool) (head string, png []byte, ok bool)
	// PictureKey changes whenever the picture would.
	PictureKey() string
}

// VMAFPanel shows a converging VMAF measurement.
type VMAFPanel struct {
	Progress quality.Progress
}

// View implements Panel.
func (p VMAFPanel) View(
	width int,
) string {
	q := p.Progress
	if !q.Estimated {
		return Subtle.Render(fmt.Sprintf("scoring frames… %d scored", q.FramesScored))
	}

	stage := fmt.Sprintf("round %d", q.Round)
	if !q.Sample.IsZero() {
		stage = "budget " + q.Sample.String()
	}

	score := scoreStyle(q.Mean).Bold(true).Render(fmt.Sprintf("%.2f", q.Mean))
	line := Bold.Render("VMAF ") + score + Subtle.Render(fmt.Sprintf("  ± %.2f  ·  %s  ·  %.1f%% of frames scored",
		q.HalfWidth, stage, 100*float64(q.FramesScored)/float64(max(q.FramesTotal, 1))))

	return line + "\n" + vmafScale(q.Mean, q.Mean-q.HalfWidth, q.Mean+q.HalfWidth, max(minGaugeWidth, width-2))
}

// LadderPanel shows probes landing on the rate-quality plane, then the
// verification of each rung.
type LadderPanel struct {
	Stage  string
	Done   int
	Total  int
	Probes []ladder.Probe
	Rungs  []ladder.Rung
	// Videos is the number of videos of the ladder of a program; 0 or 1
	// for one title.
	Videos int
}

// View implements Panel.
func (p LadderPanel) View(
	width int,
) string {
	switch {
	case p.Stage == ladder.StageAnalysis && p.Videos > 1:
		return Subtle.Render(fmt.Sprintf("analysing the %d videos, to place their digest…", p.Videos))
	case p.Stage == ladder.StageDigest && p.Videos > 1:
		return Subtle.Render(fmt.Sprintf("extracting a digest of segments spread over the %d videos…", p.Videos))
	case p.Stage == ladder.StageAnalysis:
		return Subtle.Render("analysing the title, to place its digest…")
	case p.Stage == ladder.StageDigest:
		return Subtle.Render("extracting a digest of segments spread over the title…")
	case p.Stage == ladder.StageGrain:
		return Subtle.Render("measuring the grain of the digest, calibrating film grain synthesis…")
	case p.Stage == ladder.StageAnchor:
		return Subtle.Render("anchoring the probes at the rungs' preset: encoding the top and bottom rungs…")
	case p.Stage == ladder.StageVerify && len(p.Rungs) > 0:
		return p.rungsView()
	case p.Stage == ladder.StageShots:
		return p.shotsView()
	case len(p.Probes) == 0:
		return Subtle.Render("encoding the first probes…")
	}

	// The chart of the report, while it composes: the same frame as the
	// picture, the curves bold on their envelope, the last probe encoded
	// ringed, the rungs numbered once placed.
	plot := Plot{Width: width, Height: probePlotHeight, XLog: true, YHi: maxVMAF, YStep: vmafStep, YLabel: vmafLabel, XLabel: tickBitrate}
	plot.XLo, plot.XHi, plot.YLo = p.frame()
	plot.Series = envelopeSeries(p.Probes, plot.XLo, plot.XHi, 2*(width-gutter))

	if last, ok := lastEncoded(p.Probes); ok && len(p.Rungs) == 0 {
		plot.Marks = append(plot.Marks, Mark{X: float64(last.Bitrate), Y: last.VMAF, Glyph: '◉', Style: Accent})
	}

	for i, r := range p.Rungs {
		x, y := float64(r.Bitrate), r.PredictedVMAF
		if r.Measured != nil {
			x, y = float64(r.Measured.Bitrate), r.Measured.VMAF
		}

		plot.Marks = append(plot.Marks, Mark{X: x, Y: y, Glyph: rungGlyph(i), Style: Bold.Foreground(contrast)})
	}

	head := p.pictureHead() + "   " + strings.Join(resolutionLegend(p.Probes), " ")

	return head + "\n" + strings.Join(plot.Lines(), "\n")
}

// rungsView lists the verified rungs with their gap to the prediction.
func (p LadderPanel) rungsView() string {
	lines := []string{Bold.Render("verifying rungs") + Subtle.Render("  (real encode of the digest with the final settings)")}

	for _, r := range p.Rungs {
		m := r.Measured
		if m == nil {
			continue
		}

		delta := m.VMAF - r.PredictedVMAF
		mark := Green.Render("✓")

		if math.Abs(delta) > ladder.CalibrationTolerance {
			mark = Yellow.Render("▲")
		}

		lines = append(lines, fmt.Sprintf("  %s %4dp  %10s  predicted %5.1f  measured %5.1f %s",
			mark, r.Height, Bitrate(float64(m.Bitrate)), r.PredictedVMAF, m.VMAF, Subtle.Render(fmt.Sprintf("(%+.1f)", delta))))
	}

	return strings.Join(lines, "\n")
}

// shotsView follows the per-shot stage: exact per-shot probes, then the
// per-shot verification of each rung.
func (p LadderPanel) shotsView() string {
	lines := []string{Bold.Render("per-shot") + Subtle.Render(fmt.Sprintf("  (every frame scored: %d measurement(s) done)", p.Done))}

	for _, r := range p.Rungs {
		ps := r.PerShot
		if ps == nil || ps.Measured == nil {
			continue
		}

		lines = append(lines, fmt.Sprintf("  %4dp  %10s  VMAF %5.1f  %s", r.Height, Bitrate(float64(ps.Measured.Bitrate)), ps.Measured.VMAF,
			gainStyle(ps.Gain).Render(fmt.Sprintf("%+.1f%% at equal VMAF", -ps.Gain*100))))
	}

	return strings.Join(lines, "\n")
}
