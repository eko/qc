package htmlreport

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/black"
	"github.com/eko/qc/analyze/freeze"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

func sampleReport() *analysis.Report {
	return &analysis.Report{
		Info: &media.Info{Path: "/videos/<clip>.mp4", Duration: media.Seconds(4), Video: []media.VideoStream{{
			Codec: "h264", Profile: "High", Width: 1920, Height: 1080, AvgFrameRate: media.Rational{Num: 25, Den: 1},
			HDR: media.HDR{DynamicRange: media.DynamicRangeSDR},
		}}},
		Bitstream: &bitstream.Report{
			AverageBitrate: 5e6, PeakBitrate: 7e6, PeakToAverage: 1.4, PeakWindow: media.Seconds(1),
			FrameSize: bitstream.SizeStats{P95: 80_000},
			GOP:       bitstream.GOPStats{KeyframeCount: 2, MeanInterval: media.Seconds(2)},
			Bitrate:   []bitstream.BitratePoint{{Start: 0, Bitrate: 4e6}, {Start: media.Seconds(1), Bitrate: 6e6}},
		},
	}
}

// sampleDecodedReport adds the decoded-frame analysis to sampleReport.
func sampleDecodedReport() *analysis.Report {
	r := sampleReport()
	r.Video = &analysis.VideoReport{
		Shots: []analysis.ShotReport{{
			Shot:   scene.Shot{Interval: media.Interval{Start: 0, End: media.Seconds(4)}, LastFrame: 99},
			Frames: 100, SIMean: 42.5, TIMean: 11.2, Bitrate: 5_000_000,
		}},
		Black:      black.Result{Segments: []media.Interval{{Start: 0, End: media.Seconds(0.5)}}},
		Freeze:     freeze.Result{Segments: []media.Interval{{Start: media.Seconds(2), End: media.Seconds(3)}}},
		Complexity: analysis.ComplexityHint{Spatial: "medium", Temporal: "low"},
	}
	r.Video.SITI.SISummary.Mean, r.Video.SITI.TISummary.Mean = 42.5, 11.2
	r.Video.Crop.Content.Width, r.Video.Crop.Content.Height = 1920, 800

	pts := make([]media.Duration, 1000)
	values := make([]float64, 1000)

	for i := range pts {
		pts[i] = media.Seconds(float64(i) / 250)
		values[i] = float64(i % 60)
	}

	r.Frames = &analysis.FrameSeries{PTS: pts, SI: values, TI: values, LumaMean: values}

	return r
}

func sampleVMAF(
	mode string,
) *quality.Result {
	return &quality.Result{
		Model: vmaf.ModelSpec{Name: "vmaf_v0.6.1", Width: 1920, Height: 1080},
		Mode:  mode, Mean: 91.2, HalfWidth: 0.45, Confidence: 0.95,
		Scored:      stats.Summary{Min: 72.4},
		FramesTotal: 100, FramesScored: 20, Elapsed: media.Seconds(1.234),
		Frames: []quality.FrameScore{
			{Index: 0, PTS: 0, Score: 90}, {Index: 50, PTS: media.Seconds(2), Score: 72.4},
		},
		Strata: []quality.StratumResult{
			{Interval: media.Interval{Start: 0, End: media.Seconds(2)}, Mean: 90.5},
			{Interval: media.Interval{Start: media.Seconds(2), End: media.Seconds(4)}, Mean: 91.9},
		},
	}
}

func sampleComparison(
	mode string,
) *analysis.Comparison {
	return &analysis.Comparison{Reference: sampleReport(), Distorted: sampleReport(), VMAF: sampleVMAF(mode)}
}

func sampleLadder(
	t *testing.T,
	codecName string,
) *ladder.Result {
	t.Helper()

	codec, err := encode.Lookup(codecName)
	require.NoError(t, err)

	return &ladder.Result{
		Source: sampleReport(), Codec: codec,
		Digest: ladder.Digest{Segments: []media.Interval{{Start: 0, End: media.Seconds(2)}}, Share: 0.5},
		Probes: []ladder.Probe{
			{Height: 1080, Bitrate: 5e6, VMAF: 95}, {Height: 1080, Bitrate: 2e6, VMAF: 88},
			{Height: 720, Bitrate: 1e6, VMAF: 80}, {Height: 720, Bitrate: 2.5e6, VMAF: 89},
		},
		Hull: []ladder.HullPoint{{Bitrate: 1e6, VMAF: 80, Height: 720}, {Bitrate: 5e6, VMAF: 95, Height: 1080}},
		Rungs: []ladder.Rung{
			{
				Width: 1920, Height: 1080, Bitrate: 5e6, CRF: 22, MaxRate: 1e7, PredictedVMAF: 95,
				Measured: &ladder.Measurement{Bitrate: 4.9e6, VMAF: 94.6, HalfWidth: 0.5},
				Command:  "ffmpeg -i x -crf 22 1080p.mp4",
			},
			{Width: 1280, Height: 720, Bitrate: 1e6, CRF: 30, MaxRate: 2e6, PredictedVMAF: 80, Command: "ffmpeg -i x -crf 30 720p.mp4"},
		},
		Elapsed: media.Seconds(42.4),
	}
}

var (
	sectionTitles = regexp.MustCompile(`<h3>([^<]*)</h3>`)
	charts        = regexp.MustCompile(`<svg viewBox="[^"]*" class="chart-svg"`)
)

func titles(
	html string,
) []string {
	var out []string
	for _, m := range sectionTitles.FindAllStringSubmatch(html, -1) {
		out = append(out, m[1])
	}

	return out
}

func TestRender(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		result     func(t *testing.T) any
		wantTitles []string
		wantCharts int
		want       []string
		wantNot    []string
	}{
		{
			name:       "analysis without decoding",
			result:     func(*testing.T) any { return sampleReport() },
			wantTitles: []string{"Bitrate"},
			wantCharts: 1,
			want: []string{
				"<h1>&lt;clip&gt;.mp4</h1>",
				`<ul class="meta"><li>h264 High</li><li>1920×1080</li><li>25.000 fps</li><li>SDR</li><li>0:04.00</li></ul>`,
				"<span>Average</span><b>5.00 Mb/s</b>", "<span>Peak (1s)</span><b>7.00 Mb/s</b>",
				"<b>1.40</b>", "<b>2 · every 2.00s</b>", "<b>78.1 KiB</b>",
			},
			wantNot: []string{`opacity="0.18"`, "<table>"},
		},
		{
			name:       "analysis with decoded frames",
			result:     func(*testing.T) any { return sampleDecodedReport() },
			wantTitles: []string{"Bitrate", "Complexity"},
			wantCharts: 3,
			want: []string{
				"<b>42.5 (medium)</b>", "<b>11.2 (low)</b>", "<b>1920×800</b>",
				"<title>black</title>", "<title>frozen</title>",
				"<td>1</td><td>0:00.00</td><td>0:04.00</td><td>100</td><td>42.5</td><td>11.2</td><td>5.00 Mb/s</td>",
			},
		},
		{
			name:       "sampled comparison",
			result:     func(*testing.T) any { return sampleComparison(quality.ModeSampled) },
			wantTitles: []string{"VMAF"},
			wantCharts: 1,
			want: []string{
				"<h1>VMAF 91.20</h1>", "&lt;clip&gt;.mp4 vs &lt;clip&gt;.mp4", "model vmaf_v0.6.1 at 1920×1080",
				"<b>± 0.45 (95% CI)</b>", "<b>20 / 100</b>", "<b>72.4</b>", "<b>1.23s</b>",
				`class="dot"`, "stratum mean",
			},
			wantNot: []string{`class="note"`},
		},
		{
			name: "exact comparison with fallback",
			result: func(*testing.T) any {
				c := sampleComparison(quality.ModeExact)
				c.VMAF.HalfWidth, c.VMAF.Fallback = 0, "sampling would cost more: scored every frame"

				return c
			},
			wantTitles: []string{"VMAF"},
			wantCharts: 1,
			want:       []string{"<b>exact</b>", "<span>sampling would cost more: scored every frame</span></p>"},
			wantNot:    []string{`class="dot"`, "stratum mean"},
		},
		{
			name:       "ladder",
			result:     func(t *testing.T) any { return sampleLadder(t, "h264") },
			wantTitles: []string{"Rate / quality", "Encoding commands"},
			wantCharts: 1,
			want: []string{
				"<h1>h264 ladder · &lt;clip&gt;.mp4</h1>",
				"<li>2 rungs</li><li>digest of 1 segments (50.0% of the title)</li><li>4 probe encodes</li><li>42s</li>",
				"<td>1920×1080</td><td>5.00 Mb/s</td><td>22.0</td><td>10.00 Mb/s</td><td>95.0</td><td>94.6 ± 0.5</td><td>4.90 Mb/s</td>",
				"<td>80.0</td><td>–</td><td>–</td>",
				"<pre><code>ffmpeg -i x -crf 22 1080p.mp4</code></pre>",
				">1080p<", ">720p<", ">envelope<", ">rung<",
			},
			wantNot: []string{">1080p probe<"},
		},
		{
			name: "pipeline run",
			result: func(t *testing.T) any {
				return &pipeline.Report{
					Analysis:   sampleDecodedReport(),
					Comparison: sampleComparison(quality.ModeSampled),
					Ladders:    []*ladder.Result{sampleLadder(t, "h264"), sampleLadder(t, "av1")},
					Elapsed:    "1m2s",
				}
			},
			wantTitles: []string{
				"Bitrate", "Complexity", "VMAF", "Rate / quality", "Rate / quality",
				"h264 encoding commands", "av1 encoding commands",
			},
			wantCharts: 3 + 1 + 1 + 1,
			want:       []string{"<li>0:04.00</li><li>1m2s</li></ul>", `<h2 id="a-h264-ladder-title">h264 ladder</h2>`, `<h2 id="a-encoding-title">Encoding</h2>`},
		},
		{
			name: "pipeline run without comparison nor ladder",
			result: func(*testing.T) any {
				return &pipeline.Report{Analysis: sampleReport(), Elapsed: "1s"}
			},
			wantTitles: []string{"Bitrate"},
			wantCharts: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			html := renderHTML(t, testCase.result(t))

			assert.True(t, strings.HasPrefix(html, "<!doctype html>"))
			assert.Contains(t, html, "Generated ")
			assert.Equal(t, testCase.wantTitles, titles(html))
			assert.Len(t, charts.FindAllString(html, -1), testCase.wantCharts)
			assert.NotContains(t, html, "<clip>", "file names are escaped")
			assertSelfContained(t, html)

			content := markup(html)
			assert.NotContains(t, content, "NaN")
			assert.NotContains(t, content, "Inf")

			for _, want := range testCase.want {
				assert.Contains(t, html, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, html, unwanted)
			}
		})
	}
}

func TestRenderErrors(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		w      *failingWriter
		result any
		want   string
	}{
		{name: "analysis", w: &failingWriter{err: errDiskFull}, result: sampleReport(), want: "htmlreport: disk full"},
		{name: "comparison", w: &failingWriter{err: errDiskFull}, result: sampleComparison(quality.ModeExact), want: "htmlreport: disk full"},
		{name: "ladder", w: &failingWriter{err: errDiskFull}, result: sampleLadder(t, "h264"), want: "htmlreport: disk full"},
		{name: "run", w: &failingWriter{err: errDiskFull}, result: &pipeline.Report{Analysis: sampleReport()}, want: "htmlreport: disk full"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.ErrorContains(t, renderResult(testCase.w, testCase.result), testCase.want)
		})
	}
}

var errDiskFull = errors.New("disk full")

type failingWriter struct {
	err error
}

func (w *failingWriter) Write(
	p []byte,
) (int, error) {
	if w.err != nil {
		return 0, w.err
	}

	return len(p), nil
}

func TestLabels(
	t *testing.T,
) {
	testCases := []struct {
		name string
		got  string
		want string
	}{
		{name: "clock", got: clock(media.Seconds(75.5)), want: "1:15.50"},
		{name: "clock with hours", got: clock(media.Seconds(3725.25)), want: "1:02:05.25"},
		{name: "bitrate mega", got: bitrateLabel(5_600_000), want: "5.60 Mb/s"},
		{name: "bitrate kilo", got: bitrateLabel(360_000), want: "360 kb/s"},
		{name: "bitrate unit", got: bitrateLabel(800), want: "800 b/s"},
		{name: "bytes mebi", got: bytesLabel(3 << 20), want: "3.0 MiB"},
		{name: "bytes kibi", got: bytesLabel(1536), want: "1.5 KiB"},
		{name: "bytes", got: bytesLabel(200), want: "200 B"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.got)
		})
	}
}

func TestFloor10(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "leaves room below", in: 72.4, want: 60},
		{name: "never below zero", in: 5, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, floor10(testCase.in), 1e-9)
		})
	}
}

func TestNonEmpty(
	t *testing.T,
) {
	assert.Equal(t, []string{"a", "b"}, nonEmpty("", "a", "", "b"))
	assert.Nil(t, nonEmpty(""))
}
