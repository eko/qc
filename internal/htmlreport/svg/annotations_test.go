package svg

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var guideLabel = regexp.MustCompile(`<text x="[-0-9.]+" y="([-0-9.]+)" text-anchor="end" style="fill:[^"]*" class="guide-label">([^<]*)</text>`)

func TestAnnotations(
	t *testing.T,
) {
	line := func(y float64) [][2]float64 { return [][2]float64{{0, y}, {10, y}} }

	c := Chart{
		Title: "Loudness <LUFS>", Width: 400, Height: 200, YMin: -60, YMax: 0,
		Series: []Series{
			{Name: "short-term", Color: "var(--s2)", Points: line(-30)},
			{Name: "target -23", Color: "var(--s3)", Points: line(-23), NoTip: true, Guide: true},
			{Name: "integrated -22.5", Color: "currentColor", Points: line(-22.5), NoTip: true, Guide: true},
			{Name: "ceiling 0", Color: "red", Points: line(0), NoTip: true, Guide: true},
			{Name: "empty", Guide: true},
		},
		Zones: []Zone{{From: -23.5, To: -22.5, Color: "var(--s3)", Label: "±0.5 LU"}},
	}

	html := string(c.HTML())

	assert.Contains(t, html, `<div class="chart-head"><figcaption class="chart-title">Loudness &lt;LUFS&gt;</figcaption><div class="legend">`)
	assert.Contains(t, html, `aria-label="Loudness &lt;LUFS&gt;: short-term"`)
	assert.Contains(t, html, `class="line guide"`)
	assert.Contains(t, html, `style="fill:var(--s3)" class="zone"><title>±0.5 LU</title>`)
	assert.Contains(t, html, `class="zone-label">±0.5 LU</text>`)
	assert.Len(t, legendKey.FindAllString(html, -1), 1, "guides are labelled on the plot, not in the legend")

	labels := guideLabel.FindAllStringSubmatch(html, -1)
	require.Len(t, labels, 3)
	assert.Equal(t, "ceiling 0", labels[0][2], "top first")
	assert.Equal(t, "integrated -22.5", labels[1][2])
	assert.Equal(t, "target -23", labels[2][2])
	assert.Equal(t, "20.0", labels[0][1], "a label at the top edge stays inside the plot")
	assert.Greater(t, parse(t, labels[2][1])-parse(t, labels[1][1]), float64(guideLabelGap)-0.01, "close labels are pushed apart")

	m := regexp.MustCompile(`<script type="application/json" class="chart-data">([^<]*)</script>`).FindStringSubmatch(html)
	require.NotNil(t, m)

	var d struct {
		Series []struct {
			Style string `json:"s"`
		} `json:"series"`
		Zones []struct {
			From  float64 `json:"a"`
			Label string  `json:"l"`
		} `json:"zones"`
	}
	require.NoError(t, json.Unmarshal([]byte(m[1]), &d))
	assert.Equal(t, "guide", d.Series[1].Style)
	assert.Equal(t, -23.5, d.Zones[0].From)
	assert.Equal(t, "±0.5 LU", d.Zones[0].Label)

	plain := string(Chart{Width: 200, Height: 100, Series: []Series{{Name: "a", Points: line(1)}}}.HTML())
	assert.NotContains(t, plain, "zones")
	assert.NotContains(t, plain, "guide-label")
	assert.NotContains(t, plain, "chart-title")
}

func parse(
	t *testing.T,
	s string,
) float64 {
	t.Helper()

	var v float64
	require.NoError(t, json.Unmarshal([]byte(s), &v))

	return v
}

func TestSparkline(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		want   []string
	}{
		{name: "too few values", values: []float64{1}},
		{name: "non-finite values are skipped", values: []float64{math.NaN(), 1, math.Inf(1)}},
		{
			name:   "a trend",
			values: []float64{0, 5, 10},
			want:   []string{`<svg class="spark" viewBox="0 0 120 28"`, `class="spark-line" d="M0.0,26.0L60.0,14.0L120.0,2.0"`, `d="M0.0,26.0L60.0,14.0L120.0,2.0L120,28L0,28Z"`},
		},
		{name: "a flat line is centred", values: []float64{3, 3}, want: []string{`d="M0.0,14.0L120.0,14.0"`}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := string(Sparkline(testCase.values))
			if len(testCase.want) == 0 {
				assert.Empty(t, got)
			}

			for _, want := range testCase.want {
				assert.Contains(t, got, want)
			}

			assert.False(t, strings.Contains(got, "NaN"))
		})
	}
}
