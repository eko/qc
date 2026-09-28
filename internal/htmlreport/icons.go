package htmlreport

import "html/template"

// Icons of the page, drawn inline on a 24-unit grid with round 2-unit
// strokes in currentColor: one consistent set, no request, sharp at any
// size and tinted by the surrounding text.
const (
	iconSearch   = "search"
	iconOverview = "overview"
	iconFindings = "findings"
	iconVideo    = "video"
	iconAudio    = "audio"
	iconQuality  = "quality"
	iconLadder   = "ladder"
	iconEncoding = "encoding"
	iconPass     = "pass"
	iconWarn     = "warn"
	iconInfo     = "info"
	iconFail     = "fail"
	iconCollapse = "collapse"
	iconTheme    = "theme"
	iconPrint    = "print"
	iconLink     = "link"
	iconMethod   = "method"
	iconArrow    = "arrow"
)

// iconPaths are the shapes of the icons.
var iconPaths = map[string]string{ //nolint:gosec // SVG shapes, not credentials
	iconSearch:   `<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`,
	iconOverview: `<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`,
	iconFindings: `<path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z"/><path d="M4 22v-7"/>`,
	iconVideo:    `<rect x="2" y="6" width="14" height="12" rx="2"/><path d="m16 13 5.2 3.5a.5.5 0 0 0 .8-.4V7.9a.5.5 0 0 0-.8-.4L16 11"/>`,
	iconAudio:    `<path d="M2 10v3"/><path d="M6 6v11"/><path d="M10 3v18"/><path d="M14 8v7"/><path d="M18 5v13"/><path d="M22 10v3"/>`,
	iconQuality:  `<path d="m12 14 4-4"/><path d="M3.3 19a10 10 0 1 1 17.4 0"/>`,
	iconLadder:   `<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="M8 17v-3"/><path d="M12 17v-6"/><path d="M16 17V8"/><path d="M20 17V5"/>`,
	iconEncoding: `<path d="m4 17 6-6-6-6"/><path d="M12 19h8"/>`,
	iconPass:     `<circle cx="12" cy="12" r="9.5"/><path d="m8 12.5 2.7 2.7L16 9.8"/>`,
	iconWarn:     `<path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><path d="M12 9v4"/><path d="M12 17h.01"/>`,
	iconInfo:     `<circle cx="12" cy="12" r="9.5"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`,
	iconFail:     `<path d="M7.9 2h8.2L22 7.9v8.2L16.1 22H7.9L2 16.1V7.9z"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/>`,
	iconCollapse: `<path d="m7 20 5-5 5 5"/><path d="m7 4 5 5 5-5"/>`,
	iconTheme: `<g class="moon"><path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9z"/></g>` +
		`<g class="sun"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></g>`,
	iconPrint:  `<path d="M6 9V3h12v6"/><path d="M6 18H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-2"/><rect x="6" y="14" width="12" height="7" rx="1"/>`,
	iconLink:   `<path d="M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7"/><path d="M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7"/>`,
	iconMethod: `<path d="M2 4h6a4 4 0 0 1 4 4v13a3 3 0 0 0-3-3H2z"/><path d="M22 4h-6a4 4 0 0 0-4 4v13a3 3 0 0 1 3-3h7z"/>`,
	iconArrow:  `<path d="M5 12h14"/><path d="m13 6 6 6-6 6"/>`,
}

// icon draws a named icon, decorative: the text next to it carries the
// meaning for assistive technologies.
func icon(
	name string,
) template.HTML {
	return template.HTML(`<svg class="i i-` + name + `" viewBox="0 0 24 24" aria-hidden="true">` + iconPaths[name] + `</svg>`) //nolint:gosec // constant shapes
}
