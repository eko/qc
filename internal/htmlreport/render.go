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
	"strings"
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
	// Area is the part of the report the section belongs to: consecutive
	// sections of one area are listed under it.
	Area   area
	Stats  []stat
	Charts []template.HTML
	Table  *table
	// Notes say how this result was obtained (a fallback, a clamped
	// budget): shown as is.
	Notes []string
	// Method explains how the measures are defined: reference material,
	// collapsed under the section.
	Method []string
	// Commands are shell commands, shown with a copy button.
	Commands []string
	// Collapsed renders the section closed: reference material, such as the
	// encoding commands, that would push the analysis down the page. The
	// navigation, finding links and printing still open it.
	Collapsed bool
	// Topic lets findings link to the section's chart.
	Topic findings.Topic
	// Anchor is the section's id, and Warnings the number of warnings
	// linking to the section; both set by link.
	Anchor   string
	Warnings int
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
	Title string
	// Subtitle lists the facts of the title, " · " separated.
	Subtitle  string
	Generated string
	// Kind names the report above its title.
	Kind     string
	Verdict  verdict
	Cards    []card
	Findings []finding
	Sections []section
	// Areas group the sections, set by link.
	Areas []areaView
}

// summarize sets the header cards, the findings, most severe first, and
// the verdict they lead to.
func (p *page) summarize(
	cards []card,
	list []finding,
) {
	p.Cards = slices.Clone(cards)
	p.Findings = sortFindings(list)
	p.Verdict = judge(p.Findings)
}

// Meta are the facts of the subtitle, one by one.
func (p page) Meta() []string {
	if p.Subtitle == "" {
		return nil
	}

	return strings.Split(p.Subtitle, " · ")
}

// Issues are the warnings and notes, most severe first.
func (p page) Issues() []finding {
	return slices.DeleteFunc(slices.Clone(p.Findings), func(f finding) bool { return f.Level == findings.OK })
}

// Passed are the checks that passed.
func (p page) Passed() []finding {
	return slices.DeleteFunc(slices.Clone(p.Findings), func(f finding) bool { return f.Level != findings.OK })
}

// render writes a page, stamped with the time of generation.
func render(
	w io.Writer,
	p page,
) error {
	p.Generated = time.Now().Format("2006-01-02 15:04")
	p.link()

	v := view{
		page: p, CSS: template.CSS(fontFace + reportCSS), JS: template.JS(reportJS), Theme: template.JS(themeBootstrap), //nolint:gosec // embedded assets
		Logo: logoURI, Icon: iconURI, Project: projectURL,
	}

	if err := pageTemplate.Execute(w, v); err != nil {
		return fmt.Errorf("htmlreport: %w", err)
	}

	return nil
}
