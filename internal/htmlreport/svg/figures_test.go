package svg

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func number(
	v float64,
) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func TestBars(
	t *testing.T,
) {
	testCases := []struct {
		name string
		bars Bars
		want []string
	}{
		{
			name: "bars, their values and a mark",
			bars: Bars{Title: "Errors", Width: 960, Format: number, Marks: []Mark{{Value: 0.5, Label: "tolerance"}}, Bars: []Bar{
				{Label: "4 anchors", Value: 0.4, Text: "0.40", Color: "var(--s1)", Detail: "bitrate 2%"},
				{Label: "a <design> whose name is far too long to fit on one line of the chart", Value: 1.2, Text: "1.20", Color: "var(--s2)"},
			}},
			want: []string{"Errors", "<title>4 anchors: 0.40 · bitrate 2%</title>", ">0.40<", ">1.20<", ">tolerance<", "a &lt;design&gt; whose name is far too long t…<", `fill="var(--s2)"`},
		},
		{name: "bars all at zero", bars: Bars{Width: 960, Format: number, Bars: []Bar{{Label: "nothing", Text: "0"}}}, want: []string{">nothing<"}},
		{name: "no bar", bars: Bars{Title: "Empty", Width: 960, Format: number}, want: []string{"Empty"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			html := string(testCase.bars.HTML())
			wellFormed(t, html)
			assert.NotContains(t, html, "NaN")
			assert.NotContains(t, html, "Inf")

			for _, want := range testCase.want {
				assert.Contains(t, html, want)
			}
		})
	}
}

func TestHeatmap(
	t *testing.T,
) {
	heat := Heatmap{
		Title: "VMAF error", Width: 470, Rows: []string{"1080p", "540p"}, Cols: []string{"20", "27"}, ColTitle: "CRF", Color: "var(--s1)",
		Values: [][]float64{{0, 1}, {2, 4}}, Text: [][]string{{"0.0", "1.0"}, {"2.0", "4.0"}},
	}

	html := string(heat.HTML())
	wellFormed(t, html)

	assert.Contains(t, html, "<title>540p · CRF 27: 4.0</title>")
	assert.Equal(t, 4, strings.Count(html, "<rect "))
	// The colour follows the value: faint at zero, full at the highest.
	assert.Contains(t, html, "var(--s1) 8%")
	assert.Contains(t, html, "var(--s1) 100%")

	// A scale shared with another heatmap: the same value, the same colour.
	heat.Max = 8
	assert.Contains(t, string(heat.HTML()), "var(--s1) 54%")
}

func TestStrips(
	t *testing.T,
) {
	strips := Strips{Title: "Coverage", Width: 960, Rows: []Strip{
		{Label: "SI", Values: []float64{10, 30, 20}, Names: []string{"a", "b", "c"}, Format: number},
		{Label: "one title", Values: []float64{5}, Names: []string{"a"}, Format: number},
		{Label: "none", Format: number},
	}}

	html := string(strips.HTML())
	wellFormed(t, html)
	assert.NotContains(t, html, "NaN")

	assert.Equal(t, 4, strings.Count(html, "<circle "))
	assert.Contains(t, html, "<title>b: 30</title>")

	// Each line spans from its lowest to its highest value.
	at := regexp.MustCompile(`<circle class="strip-dot" cx="([0-9.]+)"`).FindAllStringSubmatch(html, -1)
	require.Len(t, at, 4)
	assert.Equal(t, "260.0", at[0][1])
	assert.Equal(t, "890.0", at[1][1])
	assert.Equal(t, "575.0", at[2][1])
}

func testSurface() Surface {
	return Surface{
		Title: "Grid", Width: 960, Height: 440, Rows: []string{"1080p", "540p"}, Cols: []float64{20, 27, 41}, RowTitle: "resolution", ColTitle: "CRF",
		Panels: []SurfacePanel{{Name: "VMAF", Unit: UnitNumber, Max: 100}, {Name: "Bitrate", Unit: UnitBitrate, Log: true, Min: 1e5, Max: 1e7}},
		Shapes: []SurfaceShape{
			{Name: "Mean", Z: [][]float64{{95, 85, 50, 80, 60, 20}, {8e6, 3e6, 5e5, 3e6, 1e6, 2e5}}, Shade: []float64{0.95, 0.85, 0.5, 0.8, 0.6, 0.2}},
			{Name: "a <title>", Z: [][]float64{{97, 90, 60, 85, 70, 30}, {1e7, 4e6, 6e5, 4e6, 2e6, 1e5}}, Shade: []float64{0.97, 0.9, 0.6, 0.85, 0.7, 0.3}},
		},
		Lo:    [][]float64{{90, 80, 40, 70, 50, 10}, {5e6, 2e6, 3e5, 2e6, 8e5, 1e5}},
		Hi:    [][]float64{{99, 92, 65, 88, 72, 35}, {1e7, 5e6, 8e5, 5e6, 2e6, 3e5}},
		Color: "var(--s2)", ShadeTitle: "VMAF 0 to 100",
	}
}

func TestSurface(
	t *testing.T,
) {
	surface := testSurface()
	html := string(surface.HTML())
	assert.NotContains(t, html, "NaN")

	drawn := regexp.MustCompile(`(?s)<svg.*</svg>`).FindString(html)
	require.NotEmpty(t, drawn)
	wellFormed(t, drawn)

	// Two panels of (2-1) × (3-1) faces, each corner with its spread.
	assert.Equal(t, 4, strings.Count(html, `<polygon class="face"`))
	assert.Equal(t, 12, strings.Count(html, `<line class="spread"`))
	assert.Equal(t, 2, strings.Count(html, `<polygon class="floor"`))

	for _, want := range []string{">VMAF<", ">Bitrate<", ">1080p<", ">27<", ">resolution<", ">CRF<", ">100<", ">10.00 Mb/s<", "VMAF 0 to 100"} {
		assert.Contains(t, html, want)
	}

	// The page script redraws from the data: every shape is in it, and no
	// name can close the script.
	data := regexp.MustCompile(`<script type="application/json" class="surface-data">([^<]*)</script>`).FindStringSubmatch(html)
	require.Len(t, data, 2)

	var read Surface
	require.NoError(t, json.Unmarshal([]byte(data[1]), &read))

	assert.Equal(t, surface.Shapes, read.Shapes)
	assert.Equal(t, surface.Cols, read.Cols)
	assert.Equal(t, surface.Panels, read.Panels)
	assert.Equal(t, surface.Hi, read.Hi)

	// Without a shape there is nothing to draw, and nothing fails.
	empty := surface
	empty.Shapes = nil
	assert.NotContains(t, string(empty.HTML()), "<polygon")
}

func TestSurfaceScales(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		panel SurfacePanel
		value float64
		want  float64
	}{
		{name: "linear", panel: SurfacePanel{Max: 100}, value: 25, want: 0.25},
		{name: "linear, above the scale", panel: SurfacePanel{Max: 100}, value: 120, want: 1},
		{name: "log", panel: SurfacePanel{Log: true, Min: 1e4, Max: 1e6}, value: 1e5, want: 0.5},
		{name: "log, under the scale", panel: SurfacePanel{Log: true, Min: 1e4, Max: 1e6}, value: 0, want: 0},
		{name: "no span", panel: SurfacePanel{Min: 5, Max: 5}, value: 5, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, testCase.panel.level(testCase.value), 1e-9)
		})
	}

	// A level reads back as its value.
	log := SurfacePanel{Log: true, Min: 1e4, Max: 1e6}
	assert.InDelta(t, 1e5, log.value(log.level(1e5)), 1e-3)
}

func TestSurfaceProjection(
	t *testing.T,
) {
	v := view{cx: 240, cy: 200, scale: 100, yaw: surfaceYaw, pitch: surfacePitch}

	// Raising a point moves it up the figure and leaves its depth.
	_, low, depth := v.project(0.3, -0.2, 0)
	_, high, raised := v.project(0.3, -0.2, 1)
	assert.Less(t, high, low)
	assert.InDelta(t, depth, raised, 1e-9)

	// The nearer of two points of the floor is drawn lower.
	_, far, farDepth := v.project(1, -0.8, 0)
	_, near, nearDepth := v.project(-1, 0.8, 0)
	assert.Greater(t, nearDepth, farDepth)
	assert.Greater(t, near, far)

	assert.Equal(t, "end", hang(-0.8))
	assert.Equal(t, "start", hang(0.6))
	assert.Equal(t, "middle", hang(0.1))
}
