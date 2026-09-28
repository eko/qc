package htmlreport

import (
	"bytes"
	"html/template"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
)

func TestTooltipData(
	t *testing.T,
) {
	t.Run("analysis: every frame with its time and number", func(t *testing.T) {
		r := sampleDecodedReport()
		r.Frames.Size = make([]int, len(r.Frames.PTS))
		r.Frames.Keyframe = make([]bool, len(r.Frames.PTS))

		for i := range r.Frames.Size {
			r.Frames.Size[i] = 1000 + i
			r.Frames.Keyframe[i] = i%250 == 0
		}

		data := payloads(t, renderHTML(t, r))
		require.Len(t, data, 4)

		bitrate := seriesNamed(t, data[0], "bitrate")
		assert.Equal(t, []float64{0, 1}, bitrate.X.values())
		assert.Equal(t, []float64{4e6, 6e6}, bitrate.Y.values())
		assert.Equal(t, svg.UnitBitrate, data[0].Y)

		size := seriesNamed(t, data[1], "frame size")
		assert.Equal(t, svg.UnitBytes, data[1].Y)
		assert.Len(t, size.Y.values(), 1000)
		assert.InDelta(t, 1999, size.Y.values()[999], 1e-9)

		keys := seriesNamed(t, data[1], "keyframe")
		assert.Equal(t, []float64{0, 1, 2, 3}, keys.X.values())
		assert.Equal(t, []float64{0, 250, 500, 750}, keys.Frames.values())

		si := seriesNamed(t, data[2], "SI")
		assert.Equal(t, svg.UnitTime, data[2].X)
		assert.Equal(t, 1, si.Digits)

		times, frames, values := si.X.values(), si.Frames.values(), si.Y.values()
		require.Len(t, times, 1000, "tooltips read every frame, not the drawn buckets")
		assert.InDelta(t, 3.996, times[999], 1e-9)
		assert.InDelta(t, 999, frames[999], 1e-9)
		assert.InDelta(t, 39, values[999], 1e-9)
		assert.InDelta(t, 59, values[59], 1e-9)

		luma := seriesNamed(t, data[3], "luma mean")
		assert.Len(t, luma.Y.values(), 1000)
	})

	t.Run("sampled VMAF: scored frames, strata and metrics", func(t *testing.T) {
		data := payloads(t, renderHTML(t, comparisonWithExtras(quality.ModeSampled, true)))
		require.Len(t, data, 2)

		vmaf := seriesNamed(t, data[0], "VMAF")
		assert.Equal(t, "markers", vmaf.Style)
		assert.Equal(t, []float64{0, 2}, vmaf.X.values())
		assert.Equal(t, []float64{0, 50}, vmaf.Frames.values())
		assert.Equal(t, []float64{90, 72.4}, vmaf.Y.values())

		strata := seriesNamed(t, data[0], "stratum mean")
		assert.Equal(t, "step", strata.Style)
		assert.Equal(t, []float64{90.5, 90.5, 91.9, 91.9}, strata.Y.values())

		tip := seriesNamed(t, data[0], "CAMBI (banding)")
		assert.True(t, tip.TipOnly)
		assert.Equal(t, []float64{0, 50}, tip.Frames.values())
		assert.Equal(t, []float64{7, 7}, tip.Y.values())

		cambi := seriesNamed(t, data[1], "CAMBI")
		assert.Equal(t, []float64{7, 7}, cambi.Y.values())
		assert.True(t, seriesNamed(t, data[1], "visible").NoTip)
		assert.Equal(t, "banding", data[1].Bands[0].Label)
	})

	t.Run("ladder: bitrate, VMAF, resolution and CRF of every point", func(t *testing.T) {
		r := sampleLadder(t, "h264")
		r.Probes[0].CRF, r.Probes[0].Width, r.Probes[0].HalfWidth, r.Probes[0].Extra = 23, 1920, 0.4, true

		data := payloads(t, renderHTML(t, r))
		require.Len(t, data, 1)
		assert.True(t, data[0].Log)
		assert.Equal(t, svg.UnitBitrate, data[0].X)

		probes := seriesNamed(t, data[0], "1080p probe")
		assert.Equal(t, "1080p", probes.Key)
		assert.Equal(t, []float64{2e6, 5e6}, probes.X.values())
		assert.Equal(t, []float64{88, 95}, probes.Y.values())
		require.Len(t, probes.Details, 2)
		assert.Equal(t, detailData{Title: "Probe · 1920×1080 (extra)", Fields: [][2]string{
			{"bitrate", "5.00 Mb/s"}, {"VMAF", "95.00 ± 0.40"}, {"CRF", "23.0"},
		}}, probes.Details[1])

		rungs := seriesNamed(t, data[0], "rung")
		require.Len(t, rungs.Details, 2)
		assert.Equal(t, "Rung 1 · 1920×1080", rungs.Details[1].Title, "sorted by bitrate")
		assert.Contains(t, rungs.Details[1].Fields, [2]string{"CRF", "22.0"})
		assert.Contains(t, rungs.Details[1].Fields, [2]string{"measured", "4.90 Mb/s · VMAF 94.60 ± 0.50"})

		measured := seriesNamed(t, data[0], "measured")
		assert.Equal(t, []float64{4.9e6}, measured.X.values())
		assert.True(t, seriesNamed(t, data[0], "envelope").NoTip)
		assert.True(t, seriesNamed(t, data[0], "1080p").NoTip)
	})
}

func TestEscaping(
	t *testing.T,
) {
	const evil = `<img src=x onerror=alert(1)>"'&</script>`

	r := sampleDecodedReport()
	r.Info.Path = "/videos/" + evil + ".mp4"
	r.Info.Video[0].Codec, r.Info.Video[0].Profile = evil, evil
	r.Info.Video[0].FieldOrder = evil

	c := comparisonWithExtras(quality.ModeSampled, true)
	c.VMAF.Model.Name, c.VMAF.Fallback = evil, evil

	l := sampleLadder(t, "h264")
	l.Rungs[0].Command = "ffmpeg -i '" + evil + "'"
	l.Preset = evil

	html := renderHTML(t, &pipeline.Report{Analysis: r, Comparison: c, Ladders: []*ladder.Result{l}, Elapsed: evil})

	assert.Equal(t, 1, strings.Count(html, "<img"), "markup from metadata is escaped: the logo is the only image")
	assert.NotContains(t, markup(html), "</script>\"")
	assert.Contains(t, html, "&lt;img src=x onerror=alert(1)&gt;&#34;&#39;&amp;&lt;/script&gt;")
	assertSelfContained(t, html)

	// Chart data carries names as JSON with HTML escaped.
	for _, d := range payloads(t, html) {
		for _, s := range d.Series {
			assert.NotContains(t, s.Name, " ")
		}
	}

	assert.Equal(t, strings.Count(html, "<script"), strings.Count(html, "</script>"), "no script is cut short")
}

func TestAssetsSelfContained(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		asset string
	}{
		{name: "style", asset: reportCSS},
		{name: "script", asset: reportJS},
		{name: "theme bootstrap", asset: themeBootstrap},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.NotEmpty(t, testCase.asset)
			assert.Empty(t, externalResource.FindAllString(testCase.asset, -1), "no external request")
			assert.NotContains(t, testCase.asset, "</script", "cannot close its element")
			assert.NotContains(t, testCase.asset, "innerHTML", "text goes through textContent")
		})
	}
}

func TestPageStructure(
	t *testing.T,
) {
	html := renderHTML(t, &pipeline.Report{
		Analysis:   sampleDecodedReport(),
		Comparison: comparisonWithExtras(quality.ModeSampled, true),
		Ladders:    []*ladder.Result{sampleLadder(t, "h264"), sampleLadder(t, "av1")},
		Elapsed:    "1m",
	})

	assert.Contains(t, html, `<p class="eyebrow">Full run</p>`)
	assert.Contains(t, html, `<a class="card warn" href="#findings">`)
	assert.Contains(t, html, `<section class="panel findings" id="findings"`)

	ids := regexp.MustCompile(`<details class="panel section" id="([^"]+)"( open)?>`).FindAllStringSubmatch(html, -1)
	require.NotEmpty(t, ids)

	// Reference material starts collapsed; the analysis starts open.
	for _, m := range ids {
		collapsed := strings.HasPrefix(m[1], "s-encoding-commands")
		assert.Equal(t, collapsed, m[2] == "", "%s collapsed: %v", m[1], collapsed)
	}

	seen := map[string]bool{}
	for _, m := range ids {
		assert.False(t, seen[m[1]], "anchors are unique: %s", m[1])
		seen[m[1]] = true
		assert.Contains(t, html, `<a href="#`+m[1]+`">`, "the navigation links every section")
	}

	assert.True(t, seen["s-encoding-commands-2"], "repeated titles get a suffix")

	// Findings link to the chart showing them; the shot table rows carry
	// their time range.
	assert.Contains(t, html, `<a class="ts" href="#s-complexity" data-t0="0.000" data-t1="0.500">0:00.000 – 0:00.500</a>`)
	assert.Contains(t, html, `<a class="ts" href="#s-vmaf-91-20-clip-mp4-vs-clip-mp4" data-t0="2.000" data-t1="2.000">0:02.000</a>`)
	assert.Contains(t, html, `<a class="ts" href="#s-banding" data-t0="0.000" data-t1="1.500">`)
	assert.Contains(t, html, `<tr data-t0="0.000" data-t1="4.000" data-link="1"><td>1</td>`)
	assert.Contains(t, html, `<tr data-t0="0.000" data-t1="1.500" data-link="0"><td>0:00.00</td>`)
	assert.Contains(t, html, `<button type="button" class="copy" hidden>Copy</button>`)
	assert.NotContains(t, html, `<code></code>`, "empty commands are skipped")
}

func TestLink(
	t *testing.T,
) {
	p := page{
		Sections: []section{
			{Title: "Quality", Topic: findings.TopicQuality},
			{Title: "Quality", Topic: findings.TopicQuality, Charts: []template.HTML{"x"}},
			{Title: "Top!"},
			{Title: "é"},
		},
		Findings: []finding{{Topic: findings.TopicQuality}, {Topic: findings.TopicBanding}},
	}

	p.link()

	var anchors []string
	for _, s := range p.Sections {
		anchors = append(anchors, s.Anchor)
	}

	assert.Equal(t, []string{"s-quality", "s-quality-2", "s-top", "s-section"}, anchors)
	assert.Equal(t, "s-quality-2", p.Findings[0].Anchor, "the first section of the topic with a chart")
	assert.Empty(t, p.Findings[1].Anchor)
}

func TestRowsWithSpans(
	t *testing.T,
) {
	tb := &table{Rows: [][]string{{"a"}, {"b"}}, Spans: []span{{1, 2}}}
	rows := tb.RowsWithSpans()

	require.Len(t, rows, 2)
	assert.Equal(t, &span{1, 2}, rows[0].Span)
	assert.Nil(t, rows[1].Span)
}

func TestMetricTipsBudget(
	t *testing.T,
) {
	v := comparisonWithExtras(quality.ModeExact, false).VMAF
	assert.Len(t, metricTips(v), 1, "the metrics measured on the frames")

	v.Frames = make([]quality.FrameScore, maxTipValues)
	assert.Nil(t, metricTips(v), "too many values to embed")
}

// TestLongTitle renders a two-hour title at 25 fps, every frame analysed
// and scored, and bounds the page size.
func TestLongTitle(
	t *testing.T,
) {
	if testing.Short() {
		t.Skip("renders 180k frames")
	}

	const (
		frames = longTitleFrames
		// maxSize bounds a two-hour report to a few megabytes.
		maxSize = 3 << 20
	)

	r := sampleDecodedReport()
	f := &analysis.FrameSeries{
		PTS: make([]media.Duration, frames), SI: make([]float64, frames), TI: make([]float64, frames),
		LumaMean: make([]float64, frames), Size: make([]int, frames), Keyframe: make([]bool, frames),
	}

	c := comparisonWithExtras(quality.ModeExact, false)
	c.VMAF.Frames = make([]quality.FrameScore, frames)

	for i := range frames {
		// Smooth signals with noise, like real content.
		wave := math.Sin(float64(i) / 500)
		f.PTS[i] = media.Seconds(float64(i) / 25)
		f.SI[i], f.TI[i], f.LumaMean[i] = 40+10*wave+float64(i%7)/10, 12+5*wave+float64(i%5)/10, 110+20*wave
		f.Size[i], f.Keyframe[i] = 20_000+int(5000*wave)+i%977, i%50 == 0
		c.VMAF.Frames[i] = quality.FrameScore{
			Index: i, PTS: f.PTS[i], Score: 90 + 3*wave + float64(i%11)/10,
			Metrics: map[string]float64{quality.SeriesPSNRYUV: 40 + wave, quality.SeriesCAMBI: 1 + wave},
		}
	}

	r.Frames = f

	for _, result := range []any{r, c} {
		var buf bytes.Buffer
		require.NoError(t, renderResult(&buf, result))

		html := buf.String()
		assert.Less(t, len(html), maxSize, "a two-hour report stays small")
		assert.Contains(t, html, `data-encoding="gzip"`)

		for _, d := range payloads(t, html) {
			for _, s := range d.Series {
				if s.Frames == nil || s.Name == "keyframe" {
					continue
				}

				times := s.X.values()
				require.Len(t, times, frames, s.Name)
				assert.InDelta(t, float64(frames-1)/25, times[frames-1], 1e-6, s.Name)
				assert.InDelta(t, frames-1, s.Frames.values()[frames-1], 1e-9, s.Name)
			}
		}
	}
}
