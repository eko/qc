package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

func TestResample(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		width  int
		agg    Aggregation
		want   []float64
	}{
		{name: "mean", values: []float64{1, 3, 5, 7}, width: 2, agg: Mean, want: []float64{2, 6}},
		{name: "max", values: []float64{1, 3, 5, 7}, width: 2, agg: Max, want: []float64{3, 7}},
		{name: "max keeps negative peaks", values: []float64{-5, -3}, width: 1, agg: Max, want: []float64{-3}},
		{name: "upsample repeats", values: []float64{1, 2}, width: 4, agg: Mean, want: []float64{1, 1, 2, 2}},
		{name: "uneven buckets", values: []float64{1, 2, 3}, width: 2, agg: Mean, want: []float64{1, 2.5}},
		{name: "empty", values: nil, width: 4, agg: Mean, want: nil},
		{name: "no width", values: []float64{1}, width: 0, agg: Mean, want: nil},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, resample(testCase.values, testCase.width, testCase.agg))
		})
	}
}

func TestClock(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		in    media.Duration
		short bool
		want  string
	}{
		{name: "zero", in: 0, short: true, want: "0:00"},
		{name: "short never rounds up to 60", in: media.Seconds(59.9), short: true, want: "0:59"},
		{name: "long", in: media.Seconds(61.5), short: false, want: "1:01.500"},
		{name: "hours", in: media.Seconds(3725), short: true, want: "1:02:05"},
		{name: "hours long", in: media.Seconds(3725.25), short: false, want: "1:02:05.250"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Clock(testCase.in, testCase.short))
		})
	}
}

func TestFormatters(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		format func(float64) string
		in     float64
		want   string
	}{
		{name: "bitrate mega", format: Bitrate, in: 5_600_000, want: "5.60 Mb/s"},
		{name: "bitrate kilo", format: Bitrate, in: 360_000, want: "360 kb/s"},
		{name: "bitrate unit", format: Bitrate, in: 800, want: "800 b/s"},
		{name: "compact mega", format: compactBitrate, in: 5_600_000, want: "5.6M"},
		{name: "compact kilo", format: compactBitrate, in: 360_000, want: "360k"},
		{name: "compact unit", format: compactBitrate, in: 800, want: "800"},
		{name: "bytes gibi", format: Bytes, in: 3 << 30, want: "3.00 GiB"},
		{name: "bytes mebi", format: Bytes, in: 30 << 20, want: "30.0 MiB"},
		{name: "bytes kibi", format: Bytes, in: 1536, want: "1.5 KiB"},
		{name: "bytes", format: Bytes, in: 512, want: "512 B"},
		{name: "vmaf label", format: vmafLabel, in: 94.6, want: "95"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.format(testCase.in))
		})
	}
}

func TestElapsed(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "milliseconds", in: 142_400 * time.Microsecond, want: "142ms"},
		{name: "seconds", in: 3_456_789 * time.Microsecond, want: "3.46s"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Elapsed(testCase.in))
		})
	}
}

func TestVMAFColor(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		score float64
		want  lipgloss.TerminalColor
	}{
		{name: "excellent", score: 95, want: cyan},
		{name: "good", score: 85, want: green},
		{name: "fair", score: 65, want: yellow},
		{name: "poor", score: 40, want: red},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, vmafColor(testCase.score))
		})
	}
}

func TestClampWidth(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		width int
		want  int
	}{
		{name: "narrow", width: 10, want: minReportWidth},
		{name: "in range", width: 100, want: 100},
		{name: "wide", width: 300, want: maxReportWidth},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, clampWidth(testCase.width, minReportWidth))
		})
	}
}

func TestBarChart(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		values   []float64
		width    int
		height   int
		scale    Scale
		top, mid string
		bottom   string
		topRow   string
	}{
		{
			name: "auto scale", values: []float64{0, 5, 10}, width: 40, height: 4,
			top: "10 ┤", mid: "5 ┤", bottom: "0 ┤",
		},
		{
			name: "fixed scale", values: []float64{80, 90}, width: 40, height: 5, scale: Scale{Lo: 60, Hi: 100},
			top: "100 ┤", mid: "80 ┤", bottom: "60 ┤",
		},
		{
			name: "flat series at the floor", values: []float64{3, 3}, width: 20, height: 3, scale: Scale{Lo: 3},
			top: "4 ┤", mid: "4 ┤", bottom: "3 ┤", topRow: strings.Repeat(" ", 8),
		},
		{
			name: "full series fills the top row", values: []float64{7, 7}, width: 20, height: 2,
			top: "7 ┤", bottom: "0 ┤", topRow: strings.Repeat("█", 8),
		},
		{
			name: "single row", values: []float64{1, 2}, width: 20, height: 1,
			top: "2 ┤",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := BarChart(testCase.values, testCase.width, testCase.height, Max, testCase.scale, Cyan, vmafLabel)
			require.Len(t, lines, testCase.height)

			for _, w := range widths(lines) {
				assert.Equal(t, testCase.width, w, "every row spans the width")
			}

			stripped := plainLines(strings.Join(lines, "\n"))
			assert.Contains(t, stripped[0], testCase.top)

			if testCase.bottom != "" {
				assert.Contains(t, stripped[testCase.height-1], testCase.bottom)
			}

			if testCase.mid != "" {
				assert.Contains(t, stripped[testCase.height/2], testCase.mid)
			}

			if testCase.topRow != "" {
				assert.True(t, strings.HasSuffix(stripped[0], testCase.topRow), stripped[0])
			}
		})
	}
}

func TestBarChartEmpty(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		width  int
	}{
		{name: "no values", values: nil, width: 40},
		{name: "no room after the gutter", values: []float64{1}, width: gutter},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Nil(t, BarChart(testCase.values, testCase.width, 3, Mean, Scale{}, Cyan, vmafLabel))
		})
	}
}

func TestSparkline(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		width  int
		want   string
	}{
		{name: "ramp", values: []float64{0, 1, 2, 3, 4, 5, 6, 7}, width: 8, want: "▁▂▃▄▅▆▇█"},
		{name: "flat series draws the lowest block", values: []float64{5, 5, 5}, width: 3, want: "▁▁▁"},
		{name: "empty", values: nil, width: 3, want: ""},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, plain(Sparkline(testCase.values, testCase.width, Mean, Green)))
		})
	}
}

func TestAxisLine(
	t *testing.T,
) {
	testCases := []struct {
		name            string
		start, mid, end string
		width           int
		wantWidth       int
	}{
		{name: "fits", start: "0:00", mid: "0:30", end: "1:00", width: 60, wantWidth: 60},
		{name: "wide", start: "360k", mid: "1.4M", end: "5.6M", width: 140, wantWidth: 140},
		{name: "too narrow keeps a space between labels", start: "0:00:00", mid: "0:30:00", end: "1:00:00", width: 20, wantWidth: gutter + 23},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			line := plain(axisLine(testCase.start, testCase.mid, testCase.end, testCase.width))

			assert.Equal(t, testCase.wantWidth, lipgloss.Width(line))
			assert.Equal(t, []string{testCase.start, testCase.mid, testCase.end}, strings.Fields(line))
			assert.True(t, strings.HasPrefix(line, strings.Repeat(" ", gutter)+testCase.start), "the axis starts with the plot")
		})
	}
}

func TestTimeColumn(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		total media.Duration
		at    media.Duration
		want  int
	}{
		{name: "start", total: media.Seconds(10), at: 0, want: 0},
		{name: "middle", total: media.Seconds(10), at: media.Seconds(5), want: 50},
		{name: "end clamps to the last column", total: media.Seconds(10), at: media.Seconds(10), want: 99},
		{name: "past the end", total: media.Seconds(10), at: media.Seconds(20), want: 99},
		{name: "before the start", total: media.Seconds(10), at: -media.Seconds(1), want: 0},
		{name: "empty timeline", total: 0, at: media.Seconds(3), want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, timeColumn(testCase.total, 100)(testCase.at))
		})
	}
}

func TestPadLeft(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{name: "pads", in: "ab", width: 4, want: "  ab"},
		{name: "styled text pads on its display width", in: Red.Render("ab"), width: 4, want: "  ab"},
		{name: "never truncates", in: "abcdef", width: 4, want: "abcdef"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, plain(padLeft(testCase.in, testCase.width)))
		})
	}
}

func TestFill(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		from, to int
		want     string
	}{
		{name: "inside", from: 1, to: 2, want: "·██·"},
		{name: "clipped", from: -2, to: 9, want: "████"},
		{name: "empty range", from: 3, to: 1, want: "····"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			row := []rune("····")
			fill(row, testCase.from, testCase.to, '█')
			assert.Equal(t, testCase.want, string(row))
		})
	}
}

func TestXYPlot(
	t *testing.T,
) {
	curve := []Series{{Points: [][2]float64{{100_000, 40}, {1_000_000, 80}, {10_000_000, 99}}, Style: Cyan, Line: true}}

	testCases := []struct {
		name   string
		series []Series
		width  int
		height int
		xLog   bool
		yLo    float64
		yHi    float64
		empty  bool
	}{
		{name: "log x", series: curve, width: 60, height: 6, xLog: true, yLo: 0, yHi: 100},
		{name: "linear x", series: curve, width: 60, height: 6, yLo: 0, yHi: 100},
		{name: "single point", series: []Series{{Points: [][2]float64{{1, 50}}, Style: Red}}, width: 30, height: 3, yLo: 0, yHi: 100},
		{name: "steep line", series: []Series{{Points: [][2]float64{{0, 0}, {1, 100}, {2, 0}}, Style: Red, Line: true}}, width: 20, height: 8, yLo: 0, yHi: 100},
		{name: "empty y range", series: curve, width: 40, height: 4, yLo: 50, yHi: 50},
		{name: "points out of range are clamped", series: []Series{{Points: [][2]float64{{0, -20}, {1, 150}}, Style: Red, Line: true}}, width: 30, height: 4, yLo: 0, yHi: 100},
		{name: "no series", series: nil, width: 60, height: 6, empty: true},
		{name: "no points", series: []Series{{}}, width: 60, height: 6, empty: true},
		{name: "no room after the gutter", series: curve, width: gutter, height: 6, empty: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := XYPlot(testCase.series, testCase.width, testCase.height, testCase.xLog, testCase.yLo, testCase.yHi, vmafLabel)
			if testCase.empty {
				assert.Nil(t, lines)

				return
			}

			require.Len(t, lines, testCase.height)

			for _, w := range widths(lines) {
				assert.Equal(t, testCase.width, w)
			}

			for _, line := range plainLines(strings.Join(lines, "\n")) {
				for _, r := range line[strings.Index(line, "┤")+len("┤"):] {
					assert.True(t, r == ' ' || (r >= 0x2800 && r <= 0x28FF), "plot cells are braille: %q", r)
				}
			}

			middle := []rune(plainLines(strings.Join(lines, "\n"))[testCase.height/2])[gutter:]
			assert.NotEqual(t, strings.Repeat(" ", testCase.width-gutter), string(middle), "the series crosses the middle row")
		})
	}
}

func TestXYPlotLogScaleSpreadsDecades(
	t *testing.T,
) {
	points := [][2]float64{{1_000, 50}, {10_000, 50}, {100_000, 50}}
	plot := func(xLog bool) []rune {
		lines := XYPlot([]Series{{Points: points, Style: Cyan}}, gutter+21, 1, xLog, 0, 100, vmafLabel)

		return []rune(plain(lines[0]))[gutter:]
	}

	// On a log scale the middle decade lands in the middle column.
	assert.NotEqual(t, ' ', plot(true)[10])
	assert.Equal(t, ' ', plot(false)[10])
}

func TestCanvas(
	t *testing.T,
) {
	testCases := []struct {
		name string
		draw func(c *canvas)
		want string
	}{
		{name: "top-left dot", draw: func(c *canvas) { c.set(0, 0, &Cyan) }, want: "⠁ "},
		{name: "out of bounds is ignored", draw: func(c *canvas) { c.set(-1, 0, &Cyan); c.set(4, 0, &Cyan); c.set(0, 4, &Cyan) }, want: "  "},
		{name: "marker", draw: func(c *canvas) { c.point(2, 0, &Cyan) }, want: " ⠛"},
		{name: "horizontal line right to left", draw: func(c *canvas) { c.line(3, 3, 0, 3, &Cyan) }, want: "⣀⣀"},
		{name: "vertical line upwards", draw: func(c *canvas) { c.line(0, 3, 0, 0, &Cyan) }, want: "⡇ "},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c := newCanvas(2, 1)
			testCase.draw(c)
			assert.Equal(t, testCase.want, plain(c.row(0)))
		})
	}
}

func TestScaleX(
	t *testing.T,
) {
	testCases := []struct {
		name string
		x    float64
		log  bool
		want float64
	}{
		{name: "linear", x: 42, want: 42},
		{name: "log", x: math.E, log: true, want: 1},
		{name: "log of zero is clamped", x: 0, log: true, want: math.Log(1e-9)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, scaleX(testCase.x, testCase.log), 1e-12)
		})
	}
}
