package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/crop"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
)

func renderReport(
	t *testing.T,
	report *analysis.Report,
	width int,
	jsonPath string,
) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, RenderReport(&buf, report, width, jsonPath))

	return buf.String()
}

func TestRenderReport(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		report   func() *analysis.Report
		width    int
		jsonPath string
		want     []string
		wantNot  []string
	}{
		{
			name:   "complete analysis",
			report: sampleReport,
			width:  100,
			want: []string{
				"◆ qc  ·  /videos/clip.mp4",
				"Container", "mov,mp4,m4a", "1:00.000", "30.0 MiB", "4.00 Mb/s",
				"audio #1", "aac 48000 Hz 2ch eng", "aac", "ac3 48000 Hz 6ch –",
				"Video", "h264 High", "1920×1080", "25.000 fps", "yuv420p · 8-bit", "bt709 / bt709 / tv", "SDR",
				"Bitrate", "avg 4.00 Mb/s", "peak 9.00 Mb/s @ 0:30", "peak/avg 2.25",
				"GOP 10 keyframes · every 5.00s (2.00s–6.00s) variable", "p50 11.7 KiB · p95 78.1 KiB · max 244.1 KiB",
				"Complexity", "SI 40.2 ▄ medium", "TI 12.4 ▁ low",
				"Timeline", "shots 10", "black 1", "freeze 1",
				"Hardest shots  (10 total)",
				"Findings",
				"⚡ probe 52ms · bitstream 71ms · video 2.1s · 1500 frames decoded",
			},
			wantNot: []string{"written to", "no black or frozen segment", "\n\n\n"},
		},
		{
			name: "container and bitstream only",
			report: func() *analysis.Report {
				r := sampleReport()
				r.Video, r.Frames = nil, nil
				r.Bitstream.PeakToAverage = 1.5
				r.Bitstream.GOP.MaxInterval = media.Seconds(2)

				return r
			},
			width:    80,
			jsonPath: "report.json",
			want:     []string{"Bitrate", "⚡ probe 52ms · bitstream 71ms · video 2.1s\n", "✓ full technical report written to report.json"},
			wantNot:  []string{"Complexity", "Timeline", "Hardest shots", "Findings", "frames decoded", "\n\n\n"},
		},
		{
			name: "decoded without frame series",
			report: func() *analysis.Report {
				r := sampleReport()
				r.Frames = nil

				return r
			},
			width:   80,
			want:    []string{"Timeline", "Hardest shots"},
			wantNot: []string{"Complexity"},
		},
		{
			name: "no shot",
			report: func() *analysis.Report {
				r := sampleReport()
				r.Video.Shots = nil

				return r
			},
			width:   80,
			want:    []string{"Timeline", "shots 0"},
			wantNot: []string{"Hardest shots", "\n\n\n"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := plain(renderReport(t, testCase.report(), testCase.width, testCase.jsonPath))

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, out, unwanted)
			}
		})
	}
}

// TestRenderReportLayout checks that charts, timelines and cards line up:
// every row of a block has the same display width.
func TestRenderReportLayout(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		width     int
		wantWidth int
	}{
		{name: "narrow terminal is clamped", width: 20, wantWidth: minReportWidth},
		{name: "odd width", width: 101, wantWidth: 101},
		{name: "wide terminal is clamped", width: 400, wantWidth: maxReportWidth},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := plainLines(renderReport(t, sampleReport(), testCase.width, ""))

			charts := linesWith(lines, "┤")
			require.Len(t, charts, bitrateChartHeight)

			rows := linesStarting(lines, "SI  ", "TI  ", "luma  ", "frame size  ", "shots  ", "black/frz  ", "keyframes  ")
			require.Len(t, rows, 7)

			axes := linesStarting(lines, "0:00 ")
			require.Len(t, axes, 2, "bitrate and timeline axes")

			for _, w := range widths(concat(charts, rows, axes)) {
				assert.Equal(t, testCase.wantWidth, w)
			}

			cards := linesStarting(lines, "╭", "│", "╰")
			require.NotEmpty(t, cards)

			cardWidths := widths(cards)
			for _, w := range cardWidths {
				assert.Equal(t, cardWidths[0], w)
				assert.LessOrEqual(t, w, testCase.wantWidth)
				assert.GreaterOrEqual(t, w, testCase.wantWidth-1)
			}
		})
	}
}

func concat(
	groups ...[]string,
) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}

	return out
}

func TestRenderReportTimeline(
	t *testing.T,
) {
	lines := plainLines(renderReport(t, sampleReport(), 72, ""))
	cols := 72 - gutter

	events := []rune(linesStarting(lines, "black/frz  ")[0])[gutter:]
	assert.Equal(t, '■', events[0], "black at the start")
	assert.Equal(t, '≡', events[cols/2], "frozen in the middle")
	assert.Equal(t, '·', events[cols/4])

	keys := []rune(linesStarting(lines, "keyframes  ")[0])[gutter:]
	assert.Equal(t, 10, strings.Count(string(keys), "╵"))

	shots := []rune(linesStarting(lines, "shots  ")[0])[gutter:]
	assert.Equal(t, '█', shots[0])
	assert.Equal(t, '▓', shots[cols-1], "consecutive shots alternate glyphs")
}

func TestShotsSection(
	t *testing.T,
) {
	lines := plainLines(shotsSection(sampleShots()))

	require.Len(t, lines, 2+maxShotRows, "title, head and the hardest shots only")
	assert.Contains(t, lines[0], "(10 total)")

	// Shot 4 (SI 41, TI 14) is the hardest; rows keep the shot numbers.
	assert.True(t, strings.HasPrefix(lines[2], "  4    0:18 → 0:24"), lines[2])
	assert.Contains(t, lines[2], "150")
	assert.Contains(t, lines[2], "2.30 Mb/s")

	assert.Empty(t, shotsSection(nil))
}

func TestReportFindings(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(r *analysis.Report)
		want   []string
	}{
		{
			name:   "every issue",
			mutate: func(*analysis.Report) {},
			want: []string{
				"▲ peak bitrate is 2.2× the average (HLS recommends ≤ 2×)",
				"▲ keyframes up to 6.00s apart: slow seeking and long ABR segments",
				"▲ black from 0:00.000 to 0:01.000",
				"▲ frozen picture from 0:30.000 to 0:32.000",
				"▲ picture content is 1920×800 at +0+140 (black bars): crop before encoding",
				"▲ 5.0% of luma samples outside 16–235 on the worst frames",
			},
		},
		{
			name: "clean title",
			mutate: func(r *analysis.Report) {
				r.Bitstream.PeakToAverage = 1.4
				r.Bitstream.GOP.MaxInterval = media.Seconds(2)
				r.Video.Black.Segments, r.Video.Freeze.Segments = nil, nil
				r.Video.Crop.Letterbox = false
				r.Video.Levels.OutOfRangeSummary = stats.Summary{}
			},
			want: []string{"✓ no black or frozen segment"},
		},
		{
			name: "single keyframe is not a long GOP",
			mutate: func(r *analysis.Report) {
				r.Bitstream.GOP.KeyframeCount = 1
				r.Video.Black.Segments, r.Video.Freeze.Segments = nil, nil
				r.Video.Crop.Letterbox = false
				r.Bitstream.PeakToAverage = 1
				r.Video.Levels.OutOfRangeSummary = stats.Summary{}
			},
			want: []string{"✓ no black or frozen segment"},
		},
		{
			name: "pillarbox and range not signalled",
			mutate: func(r *analysis.Report) {
				r.Bitstream.PeakToAverage, r.Bitstream.GOP.MaxInterval = 1, media.Seconds(2)
				r.Video.Black.Segments, r.Video.Freeze.Segments = nil, nil
				r.Video.Crop = crop.Result{Content: crop.Rect{X: 240, Width: 1440, Height: 1080}, Pillarbox: true}
				r.Info.Video[0].Color.Range = ""
			},
			want: []string{
				"▲ picture content is 1440×1080 at +240+0 (black bars): crop before encoding",
				"▲ 5.0% of luma samples outside 16–235 on the worst frames (range not signalled, limited assumed)",
				"✓ no black or frozen segment",
			},
		},
		{
			name: "interlaced source is an issue listed before good news",
			mutate: func(r *analysis.Report) {
				r.Bitstream.PeakToAverage, r.Bitstream.GOP.MaxInterval = 1, media.Seconds(2)
				r.Video.Black.Segments, r.Video.Freeze.Segments = nil, nil
				r.Video.Crop.Letterbox = false
				r.Video.Levels.OutOfRangeSummary = stats.Summary{}
				r.Info.Video[0].FieldOrder = "tt"
			},
			want: []string{"▲ interlaced source (tt)", "✓ no black or frozen segment"},
		},
		{
			name: "no video stream analysed",
			mutate: func(r *analysis.Report) {
				r.Video = nil
				r.Bitstream.PeakToAverage, r.Bitstream.GOP.MaxInterval = 1, media.Seconds(2)
			},
			want: nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := sampleReport()
			testCase.mutate(r)

			var got []string
			for _, line := range reportFindings(r) {
				got = append(got, plain(line))
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestDynamicRange(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		hdr    media.HDR
		want   string
		styled bool
	}{
		{name: "SDR", hdr: media.HDR{DynamicRange: media.DynamicRangeSDR}, want: "SDR"},
		{
			name:   "HDR10 with light levels",
			hdr:    media.HDR{DynamicRange: media.DynamicRangeHDR10, ContentLightLevel: &media.ContentLightLevel{MaxCLL: 1000, MaxFALL: 400}},
			want:   "HDR10 (MaxCLL 1000 / MaxFALL 400)",
			styled: true,
		},
		{
			name:   "Dolby Vision",
			hdr:    media.HDR{DynamicRange: media.DynamicRangeDolbyVision, DolbyVision: &media.DolbyVision{Profile: 8, Level: 6}},
			want:   "DolbyVision (profile 8.6)",
			styled: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := dynamicRange(testCase.hdr)

			assert.Equal(t, testCase.want, plain(got))
			assert.Equal(t, testCase.styled, got != plain(got), "HDR stands out")
		})
	}
}

func TestLabels(
	t *testing.T,
) {
	testCases := []struct {
		name string
		got  string
		want string
	}{
		{name: "low complexity", got: level("low"), want: "▁ low"},
		{name: "medium complexity", got: level("medium"), want: "▄ medium"},
		{name: "high complexity", got: level("high"), want: "█ high"},
		{name: "fixed GOP", got: fixed(true), want: " fixed"},
		{name: "variable GOP", got: fixed(false), want: " variable"},
		{name: "missing value", got: orDash(""), want: "–"},
		{name: "value", got: orDash("tv"), want: "tv"},
		{name: "stage timings skip missing stages", got: stageTimings(map[string]string{"b": "2s", "a": "1s"}, "a", "x", "b"), want: "a 1s · b 2s"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, plain(testCase.got))
		})
	}
}

func TestRenderErrors(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		render func() error
	}{
		{name: "report", render: func() error { return RenderReport(failingWriter{}, sampleReport(), 80, "") }},
		{name: "comparison", render: func() error { return RenderComparison(failingWriter{}, sampleComparison("exact"), 80, "") }},
		{name: "ladder", render: func() error { return RenderLadder(failingWriter{}, sampleLadder(t), 80, "", false) }},
		{name: "written", render: func() error { return RenderWritten(failingWriter{}, "a.json", "") }},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.ErrorContains(t, testCase.render(), "write report")
		})
	}
}
