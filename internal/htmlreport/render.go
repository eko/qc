// Package htmlreport renders analysis results as a single self-contained HTML
// page: inline SVG charts, style and script, no external resource. The page
// reads fully without script; the script adds tooltips with exact values,
// zoom, legend toggles, sortable tables and copy buttons.
//
// Each report kind has its page (RenderAnalysis, RenderComparison,
// RenderLadder, RenderRun); the charts come from package svg, and the
// findings from package findings, which this package only words.
package htmlreport

import (
	"fmt"
	"html/template"
	"io"
	"slices"
	"time"

	"github.com/eko/qc/internal/findings"
)

// Palette: categorical slots defined in the page style, with separate steps
// for the light and dark themes, in an order validated for colour-blind
// readers.
var palette = []string{
	"var(--s1)", "var(--s2)", "var(--s3)", "var(--s4)",
	"var(--s5)", "var(--s6)", "var(--s7)", "var(--s8)",
}

// Named palette entries. The headline series of a chart (bitrate, VMAF)
// take the orange of the brand; their companions take blue.
var (
	blue   = palette[0]
	orange = palette[1]
	aqua   = palette[2]
	amber  = palette[3]
	green  = palette[5]
	red    = palette[7]
)

const (
	// blackBand and frozenBand shade black and frozen segments on time charts.
	blackBand  = "var(--band-black)"
	frozenBand = "var(--band-frozen)"
	// foreground draws in the page text colour, whatever the theme.
	foreground = "currentColor"
)

// Chart sizes, in SVG units: every chart spans the page width.
const (
	chartWidth        = 960
	timeChartHeight   = 240
	lumaChartHeight   = 160
	vmafChartHeight   = 260
	ladderChartHeight = 380

	// maxFramePoints and maxScorePoints bound the points of per-frame
	// series: more would only grow the page.
	maxFramePoints = 600
	maxScorePoints = 800

	// maxLuma is the top of the 8-bit luma scale.
	maxLuma = 255
)

type stat struct {
	Label, Value string
}

type section struct {
	Title    string
	Subtitle string
	Stats    []stat
	Charts   []template.HTML
	Table    *table
	Notes    []string
	// Commands are shell commands, shown with a copy button.
	Commands []string
	// Collapsed renders the section closed: reference material, such as the
	// encoding commands, that would push the analysis down the page. The
	// navigation, finding links and printing still open it.
	Collapsed bool
	// Topic lets findings link to the section's chart.
	Topic findings.Topic
	// Anchor is the section's id, set by link.
	Anchor string
}

type table struct {
	Head []string
	Rows [][]string
	// Spans are the time ranges of the first rows (shots, banded
	// segments): the page script turns cell LinkCol of these rows into a
	// link zooming the charts on the range.
	Spans   []span
	LinkCol int
}

type page struct {
	Title     string
	Subtitle  string
	Generated string
	// Kind names the report above its title.
	Kind     string
	Cards    []card
	Findings []finding
	Sections []section
}

// summarize sets the header cards, ending with the count of findings, and
// the findings, most severe first.
func (p *page) summarize(
	cards []card,
	list []finding,
) {
	p.Cards = append(slices.Clone(cards), findingsCard(list))
	p.Findings = sortFindings(list)
}

// render writes a page, stamped with the time of generation.
func render(
	w io.Writer,
	p page,
) error {
	p.Generated = time.Now().Format("2006-01-02 15:04")
	p.link()

	v := view{page: p, CSS: template.CSS(reportCSS), JS: template.JS(reportJS), Theme: template.JS(themeBootstrap), Logo: logoURI, Icon: iconURI, Project: projectURL} //nolint:gosec // embedded assets

	if err := pageTemplate.Execute(w, v); err != nil {
		return fmt.Errorf("htmlreport: %w", err)
	}

	return nil
}
