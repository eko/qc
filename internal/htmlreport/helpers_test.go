package htmlreport

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/pipeline"
)

// The types below mirror the chart data package svg embeds, as the page
// script reads it: tests decode pages as a browser would.
type (
	chartData struct {
		X      svg.Unit     `json:"x"`
		Y      svg.Unit     `json:"y"`
		Log    bool         `json:"log,omitempty"`
		LogY   bool         `json:"logy,omitempty"`
		Series []seriesData `json:"series"`
		Bands  []bandData   `json:"bands,omitempty"`
	}

	seriesData struct {
		Name    string       `json:"n"`
		Key     string       `json:"k"`
		Style   string       `json:"s"`
		NoTip   bool         `json:"notip,omitempty"`
		TipOnly bool         `json:"tiponly,omitempty"`
		Digits  int          `json:"d"`
		X       column       `json:"x"`
		Y       column       `json:"y"`
		Frames  *column      `json:"f,omitempty"`
		Details []detailData `json:"info,omitempty"`
	}

	detailData struct {
		Title  string      `json:"t"`
		Fields [][2]string `json:"r"`
	}

	bandData struct {
		Label string `json:"l"`
	}

	column struct {
		Start  float64 `json:"a,omitempty"`
		Step   float64 `json:"s,omitempty"`
		N      int     `json:"n"`
		Scale  float64 `json:"q,omitempty"`
		Deltas []int64 `json:"d,omitempty"`
	}
)

var chartScripts = regexp.MustCompile(`<script type="application/(?:json|octet-stream)" class="chart-data"( data-encoding="gzip")?>([^<]*)</script>`)

// payloads decodes the data of every chart of a page, as the page script
// does.
func payloads(
	t *testing.T,
	html string,
) []chartData {
	t.Helper()

	var out []chartData

	for _, m := range chartScripts.FindAllStringSubmatch(html, -1) {
		raw := []byte(m[2])

		if m[1] != "" {
			packed, err := base64.StdEncoding.DecodeString(m[2])
			require.NoError(t, err)

			zr, err := gzip.NewReader(bytes.NewReader(packed))
			require.NoError(t, err)

			raw, err = io.ReadAll(zr)
			require.NoError(t, err)
		}

		var d chartData
		require.NoError(t, json.Unmarshal(raw, &d), string(raw))
		out = append(out, d)
	}

	return out
}

// values decodes a column, as the page script does.
func (c column) values() []float64 {
	out := make([]float64, c.N)

	var acc int64

	for i := range out {
		if c.Deltas != nil {
			acc += c.Deltas[i]
			out[i] = float64(acc) / c.Scale

			continue
		}

		out[i] = c.Start + float64(i)*c.Step
	}

	return out
}

// seriesNamed is the series of a chart with that name.
func seriesNamed(
	t *testing.T,
	d chartData,
	name string,
) seriesData {
	t.Helper()

	for _, s := range d.Series {
		if s.Name == name {
			return s
		}
	}

	require.Failf(t, "no such series", "%q", name)

	return seriesData{}
}

// markup is the page without its embedded style and script, whose code
// legitimately mentions NaN, Infinity or <circle.
func markup(
	html string,
) string {
	return strings.Replace(strings.Replace(html, reportJS, "", 1), reportCSS, "", 1)
}

var externalResource = regexp.MustCompile(`(?i)(?:src|href|action)\s*=\s*["']?(?:https?:)?//|url\(\s*["']?(?:https?:)?//|@import|\bfetch\(|XMLHttpRequest|sendBeacon|WebSocket\(`)

// assertSelfContained checks that a page loads nothing from elsewhere.
func assertSelfContained(
	t *testing.T,
	html string,
) {
	t.Helper()

	// The embedded assets are checked once, by TestAssetsSelfContained. The
	// footer's link to the project is the one external URL: a link, which
	// loads nothing.
	page := strings.Replace(markup(html), `href="`+projectURL+`"`, "", 1)
	assert.Empty(t, externalResource.FindAllString(page, -1), "no external request")
	assert.Contains(t, html, "<style>")
	assert.Contains(t, html, "</html>")
}

// renderResult renders a result with the page of its kind, as the CLI does.
func renderResult(
	w io.Writer,
	result any,
) error {
	switch r := result.(type) {
	case *analysis.Report:
		return RenderAnalysis(w, r)
	case *analysis.Comparison:
		return RenderComparison(w, r)
	case *ladder.Result:
		return RenderLadder(w, r)
	case *pipeline.Report:
		return RenderRun(w, r)
	}

	return fmt.Errorf("unsupported result %T", result)
}

// renderHTML is the page of a result.
func renderHTML(
	t *testing.T,
	result any,
) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, renderResult(&buf, result))

	return buf.String()
}

// summary is the header cards and the findings of a result's page.
func summary(
	result any,
) ([]card, []finding) {
	var p page

	switch r := result.(type) {
	case *analysis.Report:
		p.summarize(analysisCards(r), analysisFindings(r))
	case *analysis.Comparison:
		p.summarize(comparisonCards(r.VMAF), comparisonFindings(r.VMAF))
	case *ladder.Result:
		p.summarize(ladderCards(r), ladderFindings(r))
	case *pipeline.Report:
		p.summarize(runSummary(r))
	}

	return p.Cards, p.Findings
}

// Finding levels, as texts lists them.
var (
	levelWarn = findings.Warn.String()
	levelInfo = findings.Info.String()
	levelOK   = findings.OK.String()
)
