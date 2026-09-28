package htmlreport

import (
	_ "embed"
	"encoding/base64"
	"html/template"
	"strings"
	"unicode"
)

// The page's style and script are inline: a report is one file that works
// offline and can be mailed or attached as is.
var (
	//go:embed assets/report.css
	reportCSS string
	//go:embed assets/report.js
	reportJS string
	//go:embed assets/logo.svg
	logoSVG []byte
	//go:embed assets/logo-tile.svg
	logoTileSVG []byte
	//go:embed assets/inter.woff2
	interWOFF2 []byte
)

// fontFace embeds Inter (SIL Open Font License, assets/inter-OFL.txt), the
// variable weight 400–700 and optical size axes, subset to Latin, the
// typographic punctuation and the symbols of the reports (× ± · – σ Δ ≤ ≥
// →): 43 KB, 58 KB as base64, about a tenth of a short report. Every
// reader sees the same metrics and tabular figures, in print too; readers
// whose browser refuses the font get the system interface font.
var fontFace = "@font-face{font-family:\"Inter qc\";font-style:normal;font-weight:400 700;font-display:swap;" +
	"src:url(data:font/woff2;base64," + base64.StdEncoding.EncodeToString(interWOFF2) + ") format(\"woff2\")}\n"

// The pixel logo is referenced as data URIs rather than inlined: its
// gradient id would otherwise live in the same document as the charts.
var (
	logoURI = svgDataURI(logoSVG)
	iconURI = svgDataURI(logoTileSVG)
)

// svgDataURI encodes an SVG document as a data URI.
func svgDataURI(
	svg []byte,
) template.URL {
	return template.URL("data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)) //nolint:gosec // embedded asset
}

// projectURL is the home of qc, linked from every report's footer: a plain
// link, which loads nothing.
const projectURL = "https://github.com/eko/qc"

// themeBootstrap applies the remembered theme before the first paint, so a
// dark page never flashes light. Storage may be disabled (file:// pages in
// some browsers, private windows): the page then follows the system.
const themeBootstrap = `try{var t=localStorage.getItem("qc-theme");if(t==="light"||t==="dark")document.documentElement.dataset.theme=t}catch(e){}`

// view is what the page template renders.
type view struct {
	page

	CSS     template.CSS
	JS      template.JS
	Theme   template.JS
	Logo    template.URL
	Icon    template.URL
	Project string
}

// row is a table row, with the time range it covers when it has one.
type row struct {
	Cells []string
	Span  *span
}

// RowsWithSpans pairs the rows with their time ranges.
func (t *table) RowsWithSpans() []row {
	out := make([]row, len(t.Rows))

	for i, cells := range t.Rows {
		out[i] = row{Cells: cells}
		if i < len(t.Spans) {
			out[i].Span = &t.Spans[i]
		}
	}

	return out
}

// slug is the lower-case ASCII letters and digits of s, dash-separated.
func slug(
	s string,
) string {
	var b strings.Builder

	dash := false

	for _, r := range strings.ToLower(s) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}

			b.WriteRune(r)
			dash = false

			continue
		}

		dash = true
	}

	if b.Len() == 0 {
		return "section"
	}

	return b.String()
}
