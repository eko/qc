package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

func renderComparison(
	t *testing.T,
	cmp *analysis.Comparison,
	width int,
	jsonPath string,
) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, RenderComparison(&buf, cmp, width, jsonPath))

	return plain(buf.String())
}

func TestRenderComparison(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		mode     string
		budget   *quality.SampleReport
		jsonPath string
		want     []string
		wantNot  []string
	}{
		{
			name: "sampled",
			mode: quality.ModeSampled,
			want: []string{
				"◆ qc  ·  vmaf",
				"reference   /videos/source.mov",
				"h264 · 1920×1080 · 25.000 fps · 4.00 Mb/s · 1:00.000",
				"distorted   /encodes/clip.mp4",
				"VMAF   88.40  ± 0.50  (95% CI 87.90 – 88.90)   model vmaf_v0.6.1 @ 1920×1080",
				"scored 240 / 1500 frames (16.0%)", "time 3.2s", "rounds 2", "strata 6",
				"frames min 61.2 · p5 80.3 · median 89.1 · max 97.5",
				"Quality over time  (mean per stratum)",
				"scored  ",
				"ℹ min/p5 come from sampled frames only; use --exact for quality gates",
				"⚡ inspect 120ms · vmaf 3.2s · 0 frames decoded",
			},
			wantNot: []string{"harmonic", "drops", "written to"},
		},
		{
			name: "fixed budget",
			mode: quality.ModeSampled,
			budget: &quality.SampleReport{
				Sample: quality.Sample{PerScene: 2}, Clips: 60, Boundaries: quality.BoundariesKeyframes,
				Variance: quality.VarianceSeparate, Clamped: "3 of 30 scenes hold fewer than 2 clips: all their frames scored",
			},
			want: []string{
				"VMAF   88.40  ± 0.50",
				"budget 2/scene (keyframes) · 60 clips", "strata 6",
				"ℹ budget 2/scene: 3 of 30 scenes hold fewer than 2 clips: all their frames scored",
			},
			wantNot: []string{"rounds"},
		},
		{
			name:     "exact",
			mode:     quality.ModeExact,
			jsonPath: "vmaf.json",
			want: []string{
				"VMAF   88.40  exact",
				"scored 1500 / 1500 frames (100.0%)", "harmonic 87.90",
				"Quality over time  (per frame)",
				"✓ full report written to vmaf.json",
			},
			wantNot: []string{"rounds", "(mean per stratum)", "Findings", "\n\n\n"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cmp := sampleComparison(testCase.mode)
			cmp.VMAF.Sample = testCase.budget
			out := renderComparison(t, cmp, 90, testCase.jsonPath)

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, out, unwanted)
			}
		})
	}
}

func TestRenderComparisonLayout(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		mode  string
		width int
		want  int
		rows  int
	}{
		{name: "sampled", mode: quality.ModeSampled, width: 90, want: 90, rows: 2},
		{name: "exact", mode: quality.ModeExact, width: 90, want: 90, rows: 1},
		{name: "clamped", mode: quality.ModeSampled, width: 10, want: minReportWidth, rows: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := strings.Split(renderComparison(t, sampleComparison(testCase.mode), testCase.width, ""), "\n")

			chart := linesWith(lines, "┤")
			require.Len(t, chart, qualityChartHeight)

			gauge := linesWith(lines, "●")
			require.Len(t, gauge, 1)

			scale := linesStarting(lines, "0 ")
			require.Len(t, scale, 1)
			assert.True(t, strings.HasSuffix(scale[0], "100"))

			// The scored row and the time axis.
			rows := concat(linesStarting(lines, "scored  "), linesStarting(lines, "0:00 "))
			require.Len(t, rows, testCase.rows)

			for _, w := range widths(concat(chart, gauge, scale, rows)) {
				assert.Equal(t, testCase.want, w)
			}
		})
	}
}

func TestStrataSection(
	t *testing.T,
) {
	lines := strings.Split(plain(strataSection(sampleComparison(quality.ModeSampled), 72)), "\n")
	cols := 72 - gutter

	// Strata range from 80 to 90: the chart floor leaves a margin below.
	assert.Contains(t, lines[len(lines)-3], "70 ┤")

	scored := []rune(linesStarting(lines, "scored  ")[0])[gutter:]
	require.Len(t, scored, cols)
	assert.Equal(t, '█', scored[0], "scored frames are marked")
	assert.Equal(t, '·', scored[cols-1], "the last scored frame is at 0:57")
}

func TestExactSeries(
	t *testing.T,
) {
	frames := []quality.FrameScore{
		{PTS: 0, Score: 90}, {PTS: media.Seconds(0.5), Score: 80}, {PTS: media.Seconds(3.5), Score: 60},
	}
	col := timeColumn(media.Seconds(4), 4)

	got, lowest := exactSeries(frames, 4, col)

	assert.Equal(t, []float64{85, 0, 0, 60}, got, "empty columns stay at zero")
	assert.InDelta(t, 60.0, lowest, 1e-9)
}

func TestComparisonFindings(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		result func() *quality.Result
		want   []string
	}{
		{
			name:   "sampled",
			result: func() *quality.Result { return sampleVMAF(quality.ModeSampled) },
			want:   []string{"ℹ min/p5 come from sampled frames only; use --exact for quality gates"},
		},
		{
			name: "fallback and worst frame",
			result: func() *quality.Result {
				v := sampleVMAF(quality.ModeExact)
				v.Fallback = "sampling would score more than 40% of the frames: scored every frame"
				v.Frames[42].Score = 51.3

				return v
			},
			want: []string{
				"ℹ sampling would score more than 40% of the frames: scored every frame",
				"▲ worst scored frame at 0:01.680: VMAF 51.3",
			},
		},
		{
			name: "budget adapted to the video, banding first",
			result: func() *quality.Result {
				v := sampleVMAF(quality.ModeSampled)
				v.Sample = &quality.SampleReport{Sample: quality.Sample{Share: 0.01}, Clamped: "raised to 4 clips"}
				v.Banding = &quality.Banding{Threshold: 5}

				return v
			},
			want: []string{
				"✓ no visible banding: CAMBI ≤ 5 on every scored frame",
				"ℹ budget 1%: raised to 4 clips",
				"ℹ min/p5 come from sampled frames only; use --exact for quality gates",
			},
		},
		{
			name: "no scored frames",
			result: func() *quality.Result {
				v := sampleVMAF(quality.ModeExact)
				v.Frames = nil

				return v
			},
			want: nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got []string
			for _, line := range comparisonFindings(testCase.result()) {
				got = append(got, plain(line))
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestVMAFScale(
	t *testing.T,
) {
	testCases := []struct {
		name            string
		mean, low, high float64
		want            string
	}{
		{name: "exact", mean: 50, low: 50, high: 50, want: "━━━━━●━━━━"},
		{name: "interval", mean: 50, low: 30, high: 70, want: "━━━██●██━━"},
		{name: "clamped", mean: 120, low: -10, high: 130, want: "█████████●"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, plain(vmafScale(testCase.mean, testCase.low, testCase.high, 10)))
		})
	}
}

// withExtras adds devices, metrics and banding to a sample measurement.
func withExtras(
	v *quality.Result,
) *quality.Result {
	est := func(mean, half float64) quality.Estimate {
		if v.Mode == quality.ModeExact {
			return quality.Estimate{Mean: mean, Low: mean, High: mean}
		}

		return quality.Estimate{Mean: mean, Low: mean - half, High: mean + half, HalfWidth: half}
	}

	v.Devices = []quality.DeviceResult{
		{Device: "phone", Model: vmaf.ModelSpec{Name: "vmaf_v1.0.16_5d0h", Width: 1920, Height: 1080}, Estimate: est(92.1, 0.4)},
		{Device: "4k", Model: vmaf.ModelSpec{Name: "vmaf_v1.0.16_3d0h_2160", Width: 3840, Height: 2160}, Estimate: est(85.3, 0.6)},
	}
	v.Metrics = []quality.MetricResult{
		{Name: quality.SeriesXPSNRY, Estimate: est(35.29, 0.13), Scored: stats.Summary{P5: 32.4}},
		{Name: quality.SeriesCAMBI, Estimate: est(1.47, 0.05), Scored: stats.Summary{P5: 0.1, P95: 4.45}},
		{Name: quality.SeriesMSSSIM, Estimate: est(0.98621, 0.0002), Scored: stats.Summary{P5: 0.97465}},
	}
	v.Banding = &quality.Banding{Threshold: 5, BandedFrames: 3, Segments: []quality.BandingSegment{
		{Interval: media.Interval{Start: media.Seconds(15), End: media.Seconds(15.12)}, Frames: 3, Mean: 5.1, Peak: 5.16},
	}}

	return v
}

func TestRenderComparisonExtras(
	t *testing.T,
) {
	testCases := []struct {
		name string
		mode string
		want []string
	}{
		{
			name: "sampled",
			mode: quality.ModeSampled,
			want: []string{
				"VMAF per device  (same clips, one model per viewing condition)",
				"phone    92.10  ± 0.40 (91.70 – 92.50)   vmaf_v1.0.16_5d0h @ 1920×1080",
				"4k       85.30  ± 0.60 (84.70 – 85.90)   vmaf_v1.0.16_3d0h_2160 @ 3840×2160",
				"Metrics  (95% CI from the clips VMAF sampled)",
				"XPSNR Y              35.29  ± 0.13 (35.16 – 35.42)         32.40 dB",
				"CAMBI (banding)       1.47  ± 0.05 (1.42 – 1.52)            4.45",
				"MS-SSIM             0.9862  ± 0.0002 (0.9860 – 0.9864)    0.9747",
				"▲ visible banding (CAMBI > 5) on 3 scored frames: 0:15.000 → 0:15.120 (peak 5.2)",
			},
		},
		{
			name: "exact",
			mode: quality.ModeExact,
			want: []string{
				"phone    92.10  exact",
				"Metrics  (every frame)",
				"XPSNR Y              35.29  exact                          32.40 dB",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cmp := sampleComparison(testCase.mode)
			cmp.VMAF = withExtras(cmp.VMAF)

			out := renderComparison(t, cmp, 90, "")

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}
		})
	}
}

func TestBandingFindings(
	t *testing.T,
) {
	segment := func(start float64) quality.BandingSegment {
		return quality.BandingSegment{
			Interval: media.Interval{Start: media.Seconds(start), End: media.Seconds(start + 1)}, Frames: 25, Peak: 7.25,
		}
	}

	testCases := []struct {
		name    string
		banding *quality.Banding
		want    []string
	}{
		{name: "not measured", banding: nil, want: nil},
		{
			name:    "clean",
			banding: &quality.Banding{Threshold: 5},
			want:    []string{"✓ no visible banding: CAMBI ≤ 5 on every scored frame"},
		},
		{
			name: "more segments than listed",
			banding: &quality.Banding{Threshold: 5, BandedFrames: 100, Segments: []quality.BandingSegment{
				segment(1), segment(10), segment(20), segment(30),
			}},
			want: []string{
				"▲ visible banding (CAMBI > 5) on 100 scored frames: 0:01.000 → 0:02.000 (peak 7.2), " +
					"0:10.000 → 0:11.000 (peak 7.2), 0:20.000 → 0:21.000 (peak 7.2), 1 more",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got []string
			for _, line := range comparisonFindings(&quality.Result{Banding: testCase.banding}) {
				got = append(got, plain(line))
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestRenderComparisonDrops(
	t *testing.T,
) {
	// The harmonic mean and the drops are estimated in sampled mode too.
	cmp := sampleComparison(quality.ModeSampled)
	cmp.VMAF.HarmonicMean = 86.2
	cmp.VMAF.Drops = &quality.Drops{Margin: quality.DropMargin, Threshold: 78.4, Share: 0.081, Low: 0.06, High: 0.1}

	out := renderComparison(t, cmp, 120, "")
	for _, want := range []string{
		"rounds 2", "harmonic 86.20", "drops 8.1% under 78",
		"ℹ 8% of the frames score more than 10 VMAF under the mean (below 78.4): the mean hides them, the harmonic mean is 86.2",
	} {
		assert.Contains(t, out, want)
	}

	// No drop: nothing to tell.
	cmp.VMAF.Drops.Share = 0
	out = renderComparison(t, cmp, 120, "")
	assert.NotContains(t, out, "drops")
	assert.NotContains(t, out, "the mean hides them")
}
