package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// logoRows is the pixel grid of the qc logo (docs/assets/logo.svg): '#' is a
// pixel, and the trail of the c fades through the shading levels 3, 2, 1 and
// 0, like the shrinking pixels of the SVG.
var logoRows = []string{
	".######..######3210",
	"##...##.##.........",
	"##...##.##.........",
	"##...##.##.........",
	"##...##.##.........",
	".######..######3210",
	".....##............",
	".....##............",
	".....##............",
}

// logoShades draws the fading trail, one cell pair per pixel.
var logoShades = map[rune]string{'3': "▓▓", '2': "▒▒", '1': "░░", '0': " ·"}

// logoPixel is one logo pixel: two cells make it about square in a terminal.
const logoPixel = "██"

// Logo renders the pixel logo with the orange gradient of the SVG, from left
// to right, with the lines of text beside it starting at its first row (an
// empty line leaves its row alone).
func Logo(
	text ...string,
) string {
	width := len(logoRows[0])

	var b strings.Builder

	for r, row := range logoRows {
		for c, cell := range row {
			b.WriteString(logoCell(cell, float64(c)/float64(width-1)))
		}

		if r < len(text) && text[r] != "" {
			b.WriteString("   " + text[r])
		}

		if r < len(logoRows)-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// logoCell renders one pixel at gradient position t.
func logoCell(
	cell rune,
	t float64,
) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(orangeAt(t)))

	switch cell {
	case '#':
		return style.Render(logoPixel)
	case '.':
		return "  "
	}

	return style.Render(logoShades[cell])
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
	t = min(max(t, 0), 1)

	if t < 0.5 {
		return mix(orangeLight, orangeMid, t/0.5)
	}

	return mix(orangeMid, orangeDeep, (t-0.5)/0.5)
}
