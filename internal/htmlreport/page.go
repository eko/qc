package htmlreport

import "html/template"

// pageTemplate lays a report out: a navigation listing the areas and their
// sections (a sidebar on wide screens, a bar on narrow ones), the overview
// (title, verdict, key numbers, findings), then every area with its
// sections. Everything reads without script; the script adds the zoom,
// tooltips, filters, search and theme toggle, unhiding their controls.
var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{"icon": icon}).Parse(`<!doctype html>
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
<div class="shell">
<nav class="sidebar" aria-label="Report sections">
<div class="side-head">
<a class="brand" href="#top" aria-label="qc, back to top"><img class="logo" src="{{.Logo}}" alt="qc" width="41" height="20"></a>
<div class="tools">
<button type="button" class="icon-btn" data-action="search" title="Jump to a section or finding (/)" aria-label="Jump to a section or finding" hidden>{{icon "search"}}</button>
<button type="button" class="icon-btn" data-action="collapse" title="Collapse or expand every section" aria-label="Collapse or expand every section" hidden>{{icon "collapse"}}</button>
<button type="button" class="icon-btn" data-action="print" title="Print or save as PDF" aria-label="Print or save as PDF" hidden>{{icon "print"}}</button>
<button type="button" class="icon-btn" data-action="theme" title="Toggle dark mode" aria-label="Toggle dark mode" hidden>{{icon "theme"}}</button>
</div>
</div>
<div class="nav">
<div class="nav-group"><p class="nav-title plain">Overview</p>
<a href="#top">{{icon "overview"}}<span class="nav-text">Summary</span></a>
{{if .Findings}}<a href="#findings">{{icon "findings"}}<span class="nav-text">Findings</span>{{with .Issues}}<span class="count {{$.Verdict.Class}}">{{len .}}</span>{{end}}</a>{{end}}
</div>
{{range .Areas}}<div class="nav-group"><p class="nav-title"><a href="#{{.Anchor}}">{{icon .Icon}}<span class="nav-text">{{.Title}}</span></a></p>
{{range .Sections}}<a href="#{{.Anchor}}"><span class="nav-text">{{.Title}}</span>{{if .Warnings}}<span class="nav-dot" title="{{.Warnings}} warning(s)"></span>{{end}}</a>
{{end}}</div>
{{end}}</div>
</nav>
<main id="main">
<header id="top" class="hero">
<div class="hero-top"><p class="eyebrow">{{.Kind}}</p><p class="stamp">Generated <time>{{.Generated}}</time></p></div>
<h1>{{.Title}}</h1>
{{with .Meta}}<ul class="meta">{{range .}}<li>{{.}}</li>{{end}}</ul>{{end}}
<a class="verdict {{.Verdict.Class}}" href="#findings"><span class="verdict-icon">{{icon .Verdict.Icon}}</span><span class="verdict-text"><span class="verdict-label">Verdict</span><strong>{{.Verdict.Title}}</strong><span class="verdict-sub">{{.Verdict.Text}}</span></span>{{with .Verdict.Counts}}<span class="tallies">{{range .}}<span class="tally {{.Class}}"><b>{{.N}}</b>{{.Label}}</span>{{end}}</span>{{end}}</a>
{{if .Cards}}<div class="cards">{{range .Cards}}<div class="card{{with .Tone}} {{.}}{{end}}"><span class="card-label">{{.Label}}{{if .Tone}}<i class="tone" aria-hidden="true"></i>{{end}}</span><span class="card-value{{if .Long}} long{{end}}">{{.Number}}{{with .Unit}} <small>{{.}}</small>{{end}}</span>{{with .Meter}}{{$m := .}}<span class="meter" role="presentation"><i style="width:{{printf "%.1f" .Fill}}%"></i>{{range .Marks}}<b style="left:{{printf "%.1f" ($m.At .)}}%"></b>{{end}}</span>{{end}}{{with .Detail}}<span class="card-detail">{{.}}</span>{{end}}{{.Spark}}</div>{{end}}</div>{{end}}
</header>
{{if .Findings}}<section class="panel findings" id="findings" aria-labelledby="findings-title">
<div class="panel-head"><h2 id="findings-title">Findings</h2><div class="filters" role="group" aria-label="Show findings" hidden></div></div>
{{with .Issues}}<ul class="finding-list">{{range .}}{{template "finding" .}}{{end}}</ul>{{end}}
{{with .Passed}}<details class="passed"><summary>{{icon "pass"}}<span>{{len .}} passed {{if eq (len .) 1}}check{{else}}checks{{end}}</span></summary><ul class="finding-list">{{range .}}{{template "finding" .}}{{end}}</ul></details>{{end}}
</section>{{end}}
{{range .Areas}}<section class="report-area" id="{{.Anchor}}" aria-labelledby="{{.Anchor}}-title">
<div class="area-head"><span class="area-icon">{{icon .Icon}}</span><div class="area-text"><h2 id="{{.Anchor}}-title">{{.Title}}</h2>{{with .Subtitle}}<p>{{.}}</p>{{end}}</div>{{with .Warnings}}<span class="badge warn">{{.}} warning{{if gt . 1}}s{{end}}</span>{{end}}</div>
{{range .Sections}}<details class="panel section" id="{{.Anchor}}"{{if not .Collapsed}} open{{end}}>
<summary><span class="chevron" aria-hidden="true"></span><span class="summary-text"><h3>{{.Title}}</h3>{{if .Subtitle}}<span class="sub">{{.Subtitle}}</span>{{end}}</span><span class="summary-meta">{{with .Warnings}}<span class="badge warn">{{.}}</span>{{end}}<a class="permalink" href="#{{.Anchor}}" title="Link to this section" aria-label="Link to {{.Title}}">{{icon "link"}}</a></span></summary>
<div class="section-body">
{{if .Stats}}<div class="stats">{{range .Stats}}<div class="stat"><span>{{.Label}}</span><b>{{.Value}}</b></div>{{end}}</div>{{end}}
{{range .Notes}}<p class="note">{{icon "info"}}<span>{{.}}</span></p>{{end}}
{{range .Charts}}{{.}}{{end}}
{{with .Table}}<div class="table-wrap"><table><thead><tr>{{range .Head}}<th scope="col">{{.}}</th>{{end}}</tr></thead><tbody>{{$link := .LinkCol}}{{range .RowsWithSpans}}<tr{{with .Span}} data-t0="{{.Attr .From}}" data-t1="{{.Attr .To}}" data-link="{{$link}}"{{end}}>{{range .Cells}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div>{{end}}
{{if .Commands}}<div class="commands">{{range .Commands}}{{if .}}<div class="cmd"><pre><code>{{.}}</code></pre><button type="button" class="copy" hidden>Copy</button></div>{{end}}{{end}}</div>{{end}}
{{with .Method}}<details class="method"><summary>{{icon "method"}}<span>Method</span></summary>{{range .}}<p>{{.}}</p>{{end}}</details>{{end}}
</div>
</details>
{{end}}</section>
{{end}}<footer class="foot"><span>Generated {{.Generated}} by <a class="project" href="{{.Project}}" rel="noopener">qc · github.com/eko/qc</a></span><span class="js-hint" hidden>Hover a chart for exact values · drag to zoom · double-click to reset · <kbd>/</kbd> to jump</span></footer>
</main>
</div>
<div class="toast" role="status" aria-live="polite"></div>
<script>{{.JS}}</script>
</body>
</html>
{{define "finding"}}<li class="finding {{.Filter}}" data-level="{{.Filter}}"><span class="sev" title="{{.LevelLabel}}">{{icon .Icon}}</span><span class="finding-body"><span class="finding-text">{{with .Scope}}<span class="scope">{{.}}</span>{{end}}{{.Text}}</span>{{$anchor := .Anchor}}{{with .Spans}}<span class="finding-links">{{range .}}{{if $anchor}}<a class="ts" href="#{{$anchor}}" data-t0="{{.Attr .From}}" data-t1="{{.Attr .To}}">{{.Label}}</a>{{else}}<span class="ts-static">{{.Label}}</span>{{end}} {{end}}</span>{{end}}</span><span class="finding-end">{{if .Anchor}}<a class="goto" href="#{{.Anchor}}">{{.Where}}{{icon "arrow"}}</a>{{end}}<span class="level">{{.LevelLabel}}</span></span></li>{{end}}
`))
