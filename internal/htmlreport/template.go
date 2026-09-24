package htmlreport

import (
	_ "embed"
	"encoding/base64"
	"html/template"
	"strconv"
	"strings"
	"unicode"

	"github.com/eko/qc/internal/findings"
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
)

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

// link gives every section a unique anchor, and every finding the anchor
// of the first section of its topic.
func (p *page) link() {
	used := map[string]int{"top": 1, "findings": 1, "main": 1}
	topics := map[findings.Topic]string{}

	for i := range p.Sections {
		s := &p.Sections[i]

		anchor := "s-" + slug(s.Title)
		if used[anchor]++; used[anchor] > 1 {
			anchor += "-" + strconv.Itoa(used[anchor])
		}

		s.Anchor = anchor

		if _, ok := topics[s.Topic]; s.Topic != "" && !ok && len(s.Charts) > 0 {
			topics[s.Topic] = anchor
		}
	}

	for i := range p.Findings {
		p.Findings[i].Anchor = topics[p.Findings[i].Topic]
	}
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

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="generator" content="qc">
<title>{{.Title}} · qc</title>
<link rel="icon" type="image/svg+xml" href="{{.Icon}}">
<script>{{.Theme}}</script>
<style>{{.CSS}}</style>
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<nav class="topbar" aria-label="Report sections">
<div class="topbar-inner">
<a class="brand" href="#top" aria-label="qc, back to top"><img class="logo" src="{{.Logo}}" alt="qc" width="41" height="20"></a>
<div class="navlinks">{{if .Findings}}<a href="#findings">Findings</a>{{end}}{{range .Sections}}<a href="#{{.Anchor}}">{{.Title}}</a>{{end}}</div>
<div class="tools">
<button type="button" class="icon-btn" data-action="collapse" title="Collapse or expand every section" aria-label="Collapse or expand every section" hidden><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 6l4 4 4-4"/></svg></button>
<button type="button" class="icon-btn" data-action="theme" title="Toggle dark mode" aria-label="Toggle dark mode" hidden><svg viewBox="0 0 16 16" aria-hidden="true"><path class="moon" d="M13 9.5A5.5 5.5 0 0 1 6.5 3a5.5 5.5 0 1 0 6.5 6.5z"/><circle class="sun" cx="8" cy="8" r="3"/></svg></button>
</div>
</div>
</nav>
<main id="main">
<header id="top" class="hero">
<p class="eyebrow">{{.Kind}}</p>
<h1>{{.Title}}</h1>
<p class="subtitle">{{.Subtitle}}</p>
{{if .Cards}}<div class="cards">{{range .Cards}}{{if .Href}}<a class="card{{with .Tone}} {{.}}{{end}}" href="{{.Href}}">{{else}}<div class="card{{with .Tone}} {{.}}{{end}}">{{end}}<span class="card-label">{{.Label}}</span><span class="card-value{{if gt (len .Value) 14}} long{{end}}">{{.Value}}</span>{{with .Detail}}<span class="card-detail">{{.}}</span>{{end}}{{if .Href}}</a>{{else}}</div>{{end}}{{end}}</div>{{end}}
</header>
{{if .Findings}}<section class="panel findings" id="findings" aria-labelledby="findings-title">
<h2 id="findings-title">Findings</h2>
<ul>{{range .Findings}}<li class="finding {{.Level}}"><span class="level">{{.LevelLabel}}</span><span class="finding-text">{{with .Scope}}<span class="scope">{{.}}</span>{{end}}{{.Text}}{{$anchor := .Anchor}}{{range .Spans}} {{if $anchor}}<a class="ts" href="#{{$anchor}}" data-t0="{{.Attr .From}}" data-t1="{{.Attr .To}}">{{.Label}}</a>{{else}}<span class="ts-static">{{.Label}}</span>{{end}}{{end}}</span></li>{{end}}</ul>
</section>{{end}}
{{range .Sections}}
<details class="panel section" id="{{.Anchor}}" open>
<summary><span class="chevron" aria-hidden="true"></span><span class="summary-text"><h2>{{.Title}}</h2>{{if .Subtitle}}<span class="sub">{{.Subtitle}}</span>{{end}}</span></summary>
<div class="section-body">
{{if .Stats}}<div class="stats">{{range .Stats}}<div class="stat"><span>{{.Label}}</span><b>{{.Value}}</b></div>{{end}}</div>{{end}}
{{range .Charts}}{{.}}{{end}}
{{with .Table}}<div class="table-wrap"><table><thead><tr>{{range .Head}}<th scope="col">{{.}}</th>{{end}}</tr></thead><tbody>{{$link := .LinkCol}}{{range .RowsWithSpans}}<tr{{with .Span}} data-t0="{{.Attr .From}}" data-t1="{{.Attr .To}}" data-link="{{$link}}"{{end}}>{{range .Cells}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div>{{end}}
{{if .Commands}}<div class="commands">{{range .Commands}}{{if .}}<div class="cmd"><pre><code>{{.}}</code></pre><button type="button" class="copy" hidden>Copy</button></div>{{end}}{{end}}</div>{{end}}
{{range .Notes}}<pre>{{.}}</pre>{{end}}
</div>
</details>
{{end}}
<footer>Generated {{.Generated}} by <a class="project" href="{{.Project}}" rel="noopener">qc · github.com/eko/qc</a><span class="js-hint" hidden> · hover a chart for exact values, drag to zoom, double-click to reset</span></footer>
</main>
<div class="toast" role="status" aria-live="polite"></div>
<script>{{.JS}}</script>
</body>
</html>
`))
