package tui

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// logoRows is the pixel grid of the qc logo: '#' is a pixel of the letters,
// '>' one of the play button in the bowl of the q, which turns the q into
// a screen. It has an even number of rows: a terminal cell carries two of
// them.
var logoRows = []string{
	".#######...######",
	"##.....##.##.....",
	"##.....##.##.....",
	"##..>..##.##.....",
	"##..>>.##.##.....",
	"##..>..##.##.....",
	"##.....##.##.....",
	"##.....##.##.....",
	".########..######",
	".......##........",
	".......##........",
	".................",
}

// logoGlint is the colour of the light sweeping across the logo.
var logoGlint = [3]float64{0xff, 0xf4, 0xe2}

// Half blocks: a cell shows its top pixel, its bottom pixel or both (the
// top one as the foreground of ▀, the bottom one as its background). A
// terminal cell is about twice as tall as wide, so its halves are square
// pixels.
const (
	upperHalf = "▀"
	lowerHalf = "▄"
)

// Glint shape and timing: a soft band of light, sweepSigma pixels wide,
// crossing the logo in sweepFrames frames.
const (
	sweepSigma    = 2.2
	sweepStrength = 0.75
	sweepFrames   = 14
	sweepDelay    = 28 * time.Millisecond
	// noSweep renders the logo without light.
	noSweep = math.MaxFloat64
)

// textGap separates the logo from the text beside it.
const textGap = "   "

// Logo renders the pixel logo in half blocks with the brand gradient, from
// the light top-left to the deep bottom-right like the SVG, with the lines
// of text beside it starting at its first row (an empty line leaves its row
// alone).
func Logo(
	text ...string,
) string {
	return logoFrame(noSweep, text)
}

// AnimateLogo writes the logo to w as a light sweeps across it once, then
// leaves it at rest: a short welcome (under half a second) for interactive
// terminals. Callers skip it when the output is not a terminal, colours are
// off or motion is not wanted.
func AnimateLogo(
	w io.Writer,
	text ...string,
) error {
	rows := len(logoRows) / 2
	width := float64(len(logoRows[0]))

	for i := range sweepFrames + 1 {
		sweep := noSweep
		if i < sweepFrames {
			// From before the first column to past the last one.
			sweep = -2*sweepSigma + (width+4*sweepSigma)*float64(i)/float64(sweepFrames-1)
		}

		if i > 0 {
			if _, err := fmt.Fprintf(w, "\x1b[%dA\r", rows-1); err != nil {
				return fmt.Errorf("animate logo: %w", err)
			}
		}

		if _, err := io.WriteString(w, logoFrame(sweep, text)); err != nil {
			return fmt.Errorf("animate logo: %w", err)
		}

		if i < sweepFrames {
			time.Sleep(sweepDelay)
		}
	}

	return nil
}

// logoFrame renders the logo lit by a glint centred on column sweep.
func logoFrame(
	sweep float64,
	text []string,
) string {
	var b strings.Builder

	for r := 0; r < len(logoRows); r += 2 {
		for c := range logoRows[r] {
			b.WriteString(logoCell(pixelColor(r, c, sweep), pixelColor(r+1, c, sweep)))
		}

		if line := r / 2; line < len(text) && text[line] != "" {
			b.WriteString(textGap + text[line])
		}

		if r+2 < len(logoRows) {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// logoCell renders a cell from its top and bottom pixel colours ("" for
// none).
func logoCell(
	top, bottom string,
) string {
	switch {
	case top == "" && bottom == "":
		return " "
	case bottom == "":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(top)).Render(upperHalf)
	case top == "":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(bottom)).Render(lowerHalf)
	}

	return lipgloss.NewStyle().Foreground(lipgloss.Color(top)).Background(lipgloss.Color(bottom)).Render(upperHalf)
}

// pixelColor is the colour of the pixel at row r, column c: the diagonal
// brand gradient for the letters, its contrasted middle orange for the play
// button,
// lit near the glint. It is "" for no
// pixel.
func pixelColor(
	r, c int,
	sweep float64,
) string {
	var rgb [3]float64

	switch logoRows[r][c] {
	case '.':
		return ""
	case '>':
		rgb = orangeMid
	default:
		rgb = orangeRGB(diagonal(r, c))
	}

	if sweep != noSweep {
		// The glint leans like the gradient: rows lower down catch it later.
		d := float64(c) - sweep - 0.5*float64(r)/2
		rgb = mixRGB(rgb, logoGlint, sweepStrength*math.Exp(-d*d/(2*sweepSigma*sweepSigma)))
	}

	return hex(rgb)
}

// diagonal is the position of a pixel along the gradient, mostly
// horizontal with a slight fall towards the bottom like the SVG's.
func diagonal(
	r, c int,
) float64 {
	width, height := float64(len(logoRows[0])-1), float64(len(logoRows)-1)

	return 0.8*float64(c)/width + 0.2*float64(r)/height
}

// The brand gradient: light orange to a saturated, contrasted orange.
var (
	orangeLight = [3]float64{0xff, 0xc5, 0x6b}
	orangeMid   = [3]float64{0xff, 0x8a, 0x1f}
	orangeDeep  = [3]float64{0xf2, 0x53, 0x0d}
)

// orangeAt is the brand gradient colour at t ∈ [0, 1], through its middle
// stop like the SVG.
func orangeAt(
	t float64,
) string {
	return hex(orangeRGB(t))
}

// orangeRGB is orangeAt as components.
func orangeRGB(
	t float64,
) [3]float64 {
	t = min(max(t, 0), 1)

	if t < 0.5 {
		return mixRGB(orangeLight, orangeMid, t/0.5)
	}

	return mixRGB(orangeMid, orangeDeep, (t-0.5)/0.5)
}

// mixRGB interpolates linearly between two colours.
func mixRGB(
	from, to [3]float64,
	t float64,
) [3]float64 {
	var c [3]float64
	for i := range c {
		c[i] = from[i] + (to[i]-from[i])*t
	}

	return c
}

// hex formats a colour as #rrggbb.
func hex(
	c [3]float64,
) string {
	return fmt.Sprintf("#%02x%02x%02x", int(math.Round(c[0])), int(math.Round(c[1])), int(math.Round(c[2])))
}
