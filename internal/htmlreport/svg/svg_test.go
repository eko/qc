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

var (
	circleX   = regexp.MustCompile(`<circle cx="([-0-9.]+)" cy="([-0-9.]+)"`)
	axisText  = regexp.MustCompile(`class="axis" text-anchor="(start|middle|end)">([^<]*)<`)
	xLabels   = regexp.MustCompile(`<g class="xaxis">(.*?)</g>`)
	legendKey = regexp.MustCompile(`<button type="button" class="key" data-key="([^"]*)"[^>]*><i class="sw ([a-z]+)"[^>]*></i>([^<]*)</button>`)
)

// labels are the texts of the axis labels in a part of a chart.
func labels(
	part string,
) []string {
	var out []string
	for _, m := range axisText.FindAllStringSubmatch(part, -1) {
		out = append(out, m[2])
	}

	return out
}

func TestChartSVG(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		chart   Chart
		want    []string
		wantNot []string
		check   func(t *testing.T, svg string)
	}{
		{
			name:  "empty chart keeps a default range",
			chart: Chart{Width: 200, Height: 100},
			want:  []string{`viewBox="0 0 200 100"`, `class="grid"`, ">0<", ">1<", `aria-label="Chart: "`},
		},
		{
			name: "area with bands",
			chart: Chart{
				Width: 400, Height: 200, Y: UnitNumber,
				Series: []Series{{Name: "bitrate", Color: "var(--s1)", Points: [][2]float64{{0, 1}, {10, 3}}, Area: true}},
				Bands:  []Band{{From: 2, To: 4, Color: "#000", Label: "black <1>"}, {From: 5, To: 5, Color: "#f00", Label: "cut"}},
			},
			want: []string{
				`style="fill:var(--s1)" class="area"`, " Z\"",
				`<rect x="128.0" y="10" width="64.0" height="162.0" style="fill:#000" class="band"><title>black &lt;1&gt;</title>`,
				`width="1.0" height="162.0" style="fill:#f00"`,
				`<g class="series" data-s="0" data-key="bitrate">`,
			},
			check: func(t *testing.T, svg string) {
				// Automatic y range: rounded up to the next tick.
				assert.Equal(t, []string{"0", "1", "2", "3"}, labels(strings.Split(svg, `class="bands"`)[0]))
			},
		},
		{
			name: "bold line",
			chart: Chart{
				Width: 400, Height: 200, YMin: 0, YMax: 100,
				Series: []Series{{Name: "mean", Color: "#6c5ce7", Points: [][2]float64{{0, 50}, {1, 150}}, Bold: true}},
			},
			want: []string{`class="line bold"`, `d="M64.0,91.0 L384.0,10.0"`},
			check: func(t *testing.T, svg string) {
				assert.NotContains(t, svg, "L384.0,-", "values above the range are clamped")
			},
		},
		{
			name: "markers carry escaped tooltips and are clamped",
			chart: Chart{
				Width: 400, Height: 200, YMin: 50, YMax: 100, X: UnitTime, Y: UnitNumber,
				Series: []Series{{Name: "a<b", Color: "red", Points: [][2]float64{{0, 20}, {1, 80}}, Markers: true}},
			},
			want:    []string{`<title>a&lt;b: 0:00.000, 20.00</title>`, `r="4"`, `cy="172.0"`},
			wantNot: []string{"<path"},
		},
		{
			name: "log x ticks on 1, 2, 5 steps",
			chart: Chart{
				Width: 400, Height: 200, LogX: true, YMax: 100, X: UnitBitrate,
				Series: []Series{{Name: "curve", Color: "red", Points: [][2]float64{{100_000, 10}, {1_000_000, 50}, {10_000_000, 90}}, Markers: true}},
			},
			check: func(t *testing.T, svg string) {
				xs := circleX.FindAllStringSubmatch(svg, -1)
				require.Len(t, xs, 3)
				assert.Equal(t, []string{"64.0", "224.0", "384.0"}, []string{xs[0][1], xs[1][1], xs[2][1]}, "decades are evenly spaced")

				x := labels(xLabels.FindString(svg))
				assert.Equal(t, []string{"100 kb/s", "200 kb/s", "500 kb/s", "1 Mb/s", "2 Mb/s", "5 Mb/s", "10 Mb/s"}, x)
				assert.Contains(t, svg, `text-anchor="start">100 kb/s`, "edge labels hug the plot")
				assert.Contains(t, svg, `text-anchor="end">10 Mb/s`)
			},
		},
		{
			name: "log x with a zero bitrate stays finite",
			chart: Chart{
				Width: 400, Height: 200, LogX: true, YMax: 100, X: UnitBitrate,
				Series: []Series{{Name: "curve", Color: "red", Points: [][2]float64{{0, 10}, {1_000, 50}}}},
			},
			wantNot: []string{"NaN", "Inf"},
		},
		{
			name: "single point and empty y range",
			chart: Chart{
				Width: 400, Height: 200, YMin: 50,
				Series: []Series{{Name: "one", Color: "red", Points: [][2]float64{{3, 20}}}, {Name: "empty"}},
			},
			want:    []string{"M64.0,172.0"},
			wantNot: []string{"NaN", "Inf"},
		},
		{
			name: "time axis starts at zero with clock ticks",
			chart: Chart{
				Width: 960, Height: 240, X: UnitTime, Y: UnitNumber,
				Series: []Series{{Name: "SI", Color: "red", Points: [][2]float64{{0.04, 1}, {7200, 2}}}},
			},
			check: func(t *testing.T, svg string) {
				assert.Equal(t, []string{"0:00:00", "0:30:00", "1:00:00", "1:30:00", "2:00:00"}, labels(xLabels.FindString(svg)))
			},
		},
		{
			name: "sampled line: bucket means over a min–max envelope",
			chart: Chart{
				Width: 400, Height: 200, YMin: 0, YMax: 10, MaxPoints: 2,
				Series: []Series{{Name: "VMAF", Color: "red", Samples: &Samples{X: []float64{0, 1, 2, 3}, Y: []float64{0, 10, 4, 6}}}},
			},
			want: []string{
				`<path d="M117.3,10.0 L330.7,74.8 L330.7,107.2 L117.3,172.0 Z" style="fill:red" class="envelope"/>`,
				`<path d="M117.3,91.0 L330.7,91.0" style="stroke:red" class="line">`,
			},
		},
		{
			name: "sampled markers are real samples, tip-only series are not drawn",
			chart: Chart{
				Width: 400, Height: 200, YMin: 0, YMax: 10, MaxPoints: 2,
				Series: []Series{
					{Name: "VMAF", Color: "red", Markers: true, Samples: &Samples{X: []float64{0, 1, 2, 3}, Y: []float64{1, 2, 3, 4}}},
					{Name: "PSNR", TipOnly: true, Samples: &Samples{X: []float64{0}, Y: []float64{99}}},
				},
			},
			check: func(t *testing.T, svg string) {
				xs := circleX.FindAllStringSubmatch(svg, -1)
				require.Len(t, xs, 2)
				assert.Equal(t, []string{"64.0", "277.3"}, []string{xs[0][1], xs[1][1]}, "samples 0 and 2, not averages")
				assert.NotContains(t, svg, "PSNR")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			svg := string(testCase.chart.SVG())

			wellFormed(t, svg)
			assert.True(t, strings.HasPrefix(svg, `<svg viewBox=`))
			assert.True(t, strings.HasSuffix(svg, `</svg>`))

			for _, want := range testCase.want {
				assert.Contains(t, svg, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, svg, unwanted)
			}

			if testCase.check != nil {
				testCase.check(t, svg)
			}
		})
	}
}

func TestChartHTML(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		chart      Chart
		wantMode   string
		wantLegend [][3]string
	}{
		{
			name:       "single series is named",
			chart:      Chart{Width: 400, Height: 200, Series: []Series{{Name: "bitrate", Color: "red", Area: true, Points: [][2]float64{{0, 1}}}}},
			wantMode:   "x",
			wantLegend: [][3]string{{"bitrate", "area", "bitrate"}},
		},
		{
			name: "legend keys group series and skip hidden ones",
			chart: Chart{
				Width: 400, Height: 200, Nearest: true,
				Series: []Series{
					{Name: "720p", Color: "red", Points: [][2]float64{{1, 1}}},
					{Name: "720p probe", Key: "720p", Color: "red", Markers: true, NoLegend: true, Points: [][2]float64{{1, 1}}},
					{Name: "rung", Color: "blue", Markers: true, Points: [][2]float64{{1, 1}}},
					{Name: "PSNR", TipOnly: true},
					{Color: "blue", Points: [][2]float64{{1, 1}}},
				},
			},
			wantMode:   "point",
			wantLegend: [][3]string{{"720p", "line", "720p"}, {"rung", "dot", "rung"}},
		},
		{
			name:     "no named series, no legend",
			chart:    Chart{Width: 400, Height: 200, Series: []Series{{Color: "red", Points: [][2]float64{{0, 1}}}}},
			wantMode: "x",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			html := string(testCase.chart.HTML())

			assert.True(t, strings.HasPrefix(html, `<figure class="chart" data-mode="`+testCase.wantMode+`">`))
			assert.True(t, strings.HasSuffix(html, `</script></figure>`))
			assert.Contains(t, html, `<div class="plot"><svg viewBox=`)

			var legend [][3]string
			for _, m := range legendKey.FindAllStringSubmatch(html, -1) {
				legend = append(legend, [3]string{m[1], m[2], m[3]})
			}

			assert.Equal(t, testCase.wantLegend, legend)
			require.Len(t, payloads(t, html), 1)
		})
	}
}

func TestChartData(
	t *testing.T,
) {
	chart := Chart{
		Width: 960, Height: 240, X: UnitTime, Y: UnitNumber, YMax: 100,
		Series: []Series{
			{
				Name: "VMAF", Key: "vmaf", Color: "var(--s1)", Digits: 2, Markers: true,
				// Unsorted, with a non-finite value: sorted by time, dropped.
				Samples: &Samples{
					X:      []float64{2.08, 0.04, 1.001, 3},
					Y:      []float64{71.254, 93.5, 88.125, math.Inf(1)},
					Frames: []int{52, 1, 25, 75},
				},
			},
			{Name: "stratum mean", Color: "red", Step: true, Points: [][2]float64{{0, 90}, {2, 90}, {2, 80}, {4, 80}}, Digits: 1},
			{Name: "visible", NoTip: true, Points: [][2]float64{{0, 5}, {4, 5}}},
			{Name: "PSNR", TipOnly: true, Samples: &Samples{X: []float64{0.04, 1}, Y: []float64{41, 42}, Frames: []int{1}}},
		},
		Bands: []Band{{From: 1, To: 2, Color: "red", Label: "frozen"}},
	}

	all := payloads(t, string(chart.HTML()))
	require.Len(t, all, 1)
	d := all[0]

	assert.Equal(t, UnitTime, d.X)
	assert.Equal(t, UnitNumber, d.Y)
	assert.Equal(t, [4]float64{padLeft, padTop, 960 - padLeft - padRight, 240 - padTop - padBottom}, d.Box)
	assert.Equal(t, [2]float64{0, 100}, d.YR)
	assert.InDelta(t, 25, d.YStep, 1e-9)
	assert.Equal(t, []bandData{{From: 1, To: 2, Label: "frozen", Color: "red"}}, d.Bands)
	require.Len(t, d.Series, 4)

	vmaf := d.Series[0]
	assert.Equal(t, "vmaf", vmaf.Key)
	assert.Equal(t, "markers", vmaf.Style)
	assert.Equal(t, 2, vmaf.Digits)
	assert.Equal(t, []float64{0.04, 1.001, 2.08}, vmaf.X.values(), "times to the millisecond, sorted")
	assert.Equal(t, []float64{93.5, 88.13, 71.25}, vmaf.Y.values(), "values to the digits shown")
	require.NotNil(t, vmaf.Frames)
	assert.Equal(t, []float64{1, 25, 52}, vmaf.Frames.values())

	step := d.Series[1]
	assert.Equal(t, "step", step.Style)
	assert.Equal(t, []float64{0, 2, 2, 4}, step.X.values())
	assert.Nil(t, step.Frames)

	assert.True(t, d.Series[2].NoTip)
	assert.True(t, d.Series[3].TipOnly)
	assert.Nil(t, d.Series[3].Frames, "frames that do not match the samples are dropped")
}

func TestEncodeColumn(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		digits int
		want   column
		step   float64
	}{
		{name: "empty", want: column{}},
		{name: "single value", values: []float64{7}, want: column{Start: 7, N: 1}},
		{
			name:   "constant frame rate times are a progression",
			values: []float64{0, 1001.0 / 30000, 2002.0 / 30000, 3003.0 / 30000},
			digits: 3,
			want:   column{N: 4},
			step:   1001.0 / 30000,
		},
		{
			name:   "other values are quantized deltas",
			values: []float64{10, 12.34, 11.5},
			digits: 1,
			want:   column{N: 3, Scale: 10, Deltas: []int64{100, 23, -8}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := encodeColumn(testCase.values, testCase.digits)
			assert.InDelta(t, testCase.step, got.Step, 1e-12)

			step := got.Step
			got.Step = 0
			assert.Equal(t, testCase.want, got)
			got.Step = step

			for i, v := range got.values() {
				assert.InDelta(t, testCase.values[i], v, 0.5/math.Pow(10, float64(testCase.digits)))
			}
		})
	}
}

func TestWriteData(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		size     int
		wantGzip bool
	}{
		{name: "small data stays readable JSON", size: 100},
		{name: "large data is gzipped", size: compressAbove, wantGzip: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d := chartData{Series: []seriesData{{Name: strings.Repeat("<a>", testCase.size/3)}}}

			raw, err := json.Marshal(d)
			require.NoError(t, err)

			var b strings.Builder
			writeData(&b, raw)

			html := b.String()
			assert.Equal(t, testCase.wantGzip, strings.Contains(html, `data-encoding="gzip"`))
			assert.NotContains(t, html, "<a>", "markup in names cannot escape the script")

			got := payloads(t, html)
			require.Len(t, got, 1)
			assert.Equal(t, d.Series[0].Name, got[0].Series[0].Name)
		})
	}
}

func TestTicks(
	t *testing.T,
) {
	testCases := []struct {
		name string
		got  any
		want any
	}{
		{name: "nice step 1", got: niceStep(4, 4), want: 1.0},
		{name: "nice step 2.5", got: niceStep(10, 4), want: 2.5},
		{name: "nice step 5", got: niceStep(18, 4), want: 5.0},
		{name: "nice step 10", got: niceStep(38, 4), want: 10.0},
		{name: "byte steps are binary", got: UnitBytes.step(200_000, 4), want: 50.0 * 1024},
		{name: "small byte spans step in bytes", got: UnitBytes.step(1000, 4), want: 250.0},
		{name: "megabyte steps", got: UnitBytes.step(8<<20, 4), want: 2.0 * (1 << 20)},
		{name: "time step", got: timeStep(60), want: 10.0},
		{name: "time step beyond the list", got: timeStep(1e6), want: 21600.0},
		{name: "linear ticks", got: linearTicks(0.5, 3, 1), want: []float64{1, 2, 3}},
		{name: "log ticks", got: logTicks(150_000, 1_200_000), want: []float64{200_000, 500_000, 1_000_000}},
		{name: "log ticks fall back to the bounds", got: logTicks(1_100, 1_900), want: []float64{1_100, 1_900}},
		{name: "log ticks drop the 2s on wide ranges", got: logTicks(1, 1_000), want: []float64{1, 5, 10, 50, 100, 500, 1_000}},
		{name: "log range rounds out", got: []float64{first(logRange(180_000, 4_300_000)), second(logRange(180_000, 4_300_000))}, want: []float64{100_000, 5_000_000}},
		{name: "log range of one value", got: []float64{first(logRange(2e6, 2e6)), second(logRange(2e6, 2e6))}, want: []float64{2e6, 5e6}},
		{name: "log range without data", got: []float64{first(logRange(math.Inf(1), math.Inf(-1))), second(logRange(math.Inf(1), math.Inf(-1)))}, want: []float64{1, 10}},
		{name: "nice floor", got: []float64{niceFloor(7), niceFloor(3), niceFloor(1.5), niceFloor(20)}, want: []float64{5, 2, 1, 20}},
		{name: "nice ceil", got: []float64{niceCeil(7), niceCeil(3), niceCeil(1.5), niceCeil(10)}, want: []float64{10, 5, 2, 10}},
		{name: "step digits", got: []int{stepDigits(10), stepDigits(0.5), stepDigits(0.05), stepDigits(0.0001), stepDigits(0)}, want: []int{0, 1, 2, 3, 0}},
		{name: "time label", got: timeLabel(75.5, 1), want: "1:15.5"},
		{name: "time label with hours", got: timeLabel(3725.25, 3), want: "1:02:05.250"},
		{name: "number tick", got: UnitNumber.tick(7.5, 2.5), want: "7.5"},
		{name: "time tick", got: UnitTime.tick(90, 30), want: "1:30"},
		{name: "bitrate ticks", got: []string{bitrateTick(2_500_000), bitrateTick(500_000), bitrateTick(800)}, want: []string{"2.5 Mb/s", "500 kb/s", "800 b/s"}},
		{name: "byte ticks", got: []string{UnitBytes.tick(1.5*(1<<20), 0), bytesTick(40 << 10), bytesTick(100)}, want: []string{"1.5 MiB", "40 KiB", "100 B"}},
		{name: "labels", got: []string{UnitTime.Format(1.5), UnitBitrate.Format(2e6), UnitBytes.Format(2048), UnitNumber.Format(1)}, want: []string{"0:01.500", "2.00 Mb/s", "2.0 KiB", "1.00"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.got)
		})
	}
}

func first(
	a, _ float64,
) float64 {
	return a
}

func second(
	_, b float64,
) float64 {
	return b
}

func TestLogYChart(
	t *testing.T,
) {
	chart := Chart{
		Width: 400, Height: 200, X: UnitTime, Y: UnitBitrate, LogY: true, YMax: 3,
		Series: []Series{{Name: "rung", Color: "red", Step: true, Points: [][2]float64{{0, 180_000}, {4, 180_000}, {4, 4_300_000}, {8, 4_300_000}}}},
	}

	svg := string(chart.SVG())
	wellFormed(t, svg)

	// The range rounds out to 100 kb/s – 5 Mb/s, whatever YMax says; the
	// labels are the 1, 2, 5 values in it.
	assert.Equal(t, []string{"100 kb/s", "200 kb/s", "500 kb/s", "1 Mb/s", "2 Mb/s", "5 Mb/s"}, labels(strings.Split(svg, `class="bands"`)[0]))

	p := chart.plot()
	assert.InDelta(t, p.bottom, p.y(100_000), 1e-9)
	assert.InDelta(t, p.top, p.y(5_000_000), 1e-9)
	assert.InDelta(t, p.y(100_000)-p.y(1_000_000), p.y(500_000)-p.y(5_000_000), 1e-9, "a decade spans the same height")
	assert.InDelta(t, p.bottom, p.y(1), 1e-9, "values below the range are clamped")

	all := payloads(t, string(chart.HTML()))
	require.Len(t, all, 1)
	assert.True(t, all[0].LogY)
	assert.Equal(t, [2]float64{100_000, 5_000_000}, all[0].YR)
	assert.Zero(t, all[0].YStep)
}

func TestDownsample(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		xs, ys []float64
		n      int
		want   [][2]float64
	}{
		{name: "empty", xs: nil, ys: nil, n: 10, want: nil},
		{name: "no budget", xs: []float64{1}, ys: []float64{1}, n: 0, want: nil},
		{name: "few points are kept", xs: []float64{1, 2}, ys: []float64{10, 20}, n: 10, want: [][2]float64{{1, 10}, {2, 20}}},
		{name: "mismatched lengths use the shortest", xs: []float64{1, 2, 3}, ys: []float64{10}, n: 10, want: [][2]float64{{1, 10}}},
		{
			name: "buckets are averaged",
			xs:   []float64{0, 1, 2, 3, 4, 5},
			ys:   []float64{10, 20, 30, 40, 50, 60},
			n:    3,
			want: [][2]float64{{0.5, 15}, {2.5, 35}, {4.5, 55}},
		},
		{
			name: "uneven buckets",
			xs:   []float64{0, 1, 2, 3, 4},
			ys:   []float64{0, 1, 2, 3, 4},
			n:    2,
			want: [][2]float64{{0.5, 0.5}, {3, 3}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Downsample(testCase.xs, testCase.ys, testCase.n))
		})
	}
}

func TestEnvelope(
	t *testing.T,
) {
	assert.Equal(t, [][3]float64{{0.5, 1, 9}, {2.5, 2, 3}}, envelope([]float64{0, 1, 2, 3}, []float64{9, 1, 2, 3}, 2))
}

func TestFormat(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		unit  Unit
		value float64
		want  string
	}{
		{name: "time", unit: UnitTime, value: 3725.25, want: "1:02:05.250"},
		{name: "megabits", unit: UnitBitrate, value: 2_345_000, want: "2.35 Mb/s"},
		{name: "kilobits", unit: UnitBitrate, value: 145_000, want: "145 kb/s"},
		{name: "bits", unit: UnitBitrate, value: 800, want: "800 b/s"},
		{name: "mebibytes", unit: UnitBytes, value: 3 << 20, want: "3.0 MiB"},
		{name: "kibibytes", unit: UnitBytes, value: 1536, want: "1.5 KiB"},
		{name: "bytes", unit: UnitBytes, value: 512, want: "512 B"},
		{name: "number", unit: UnitNumber, value: 93.456, want: "93.46"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.unit.Format(testCase.value))
		})
	}
}

func TestPoints(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		samples Samples
		want    [][2]float64
	}{
		{name: "every sample", samples: Samples{X: []float64{0, 1}, Y: []float64{5, 6}}, want: [][2]float64{{0, 5}, {1, 6}}},
		{name: "unmatched values dropped", samples: Samples{X: []float64{0, 1, 2}, Y: []float64{5}}, want: [][2]float64{{0, 5}}},
		{name: "none", samples: Samples{}, want: [][2]float64{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.samples.Points())
		})
	}
}

func TestChartDataDetails(
	t *testing.T,
) {
	detail := func(title string) Detail {
		return Detail{Title: title, Fields: []Field{{Label: "CRF", Value: title}}}
	}

	chart := Chart{
		Width: 960, Height: 240, X: UnitBitrate, Y: UnitNumber, Nearest: true,
		Series: []Series{
			{
				Name: "probes", Markers: true,
				// Unsorted, one non-finite: details follow their samples.
				Samples: &Samples{
					X:       []float64{3e6, 1e6, 2e6},
					Y:       []float64{95, 80, math.NaN()},
					Details: []Detail{detail("a"), detail("b"), detail("c")},
				},
			},
			{
				Name: "short", Markers: true,
				Samples: &Samples{X: []float64{1e6, 2e6}, Y: []float64{80, 90}, Details: []Detail{detail("a")}},
			},
		},
	}

	all := payloads(t, string(chart.HTML()))
	require.Len(t, all, 1)

	probes := seriesNamed(t, all[0], "probes")
	assert.Equal(t, []float64{1e6, 3e6}, probes.X.values())
	assert.Equal(t, []detailData{
		{Title: "b", Fields: [][2]string{{"CRF", "b"}}},
		{Title: "a", Fields: [][2]string{{"CRF", "a"}}},
	}, probes.Details)
	assert.Nil(t, probes.Frames)

	assert.Nil(t, seriesNamed(t, all[0], "short").Details, "details that do not match the samples are dropped")
}
