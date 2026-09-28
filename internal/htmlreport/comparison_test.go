package htmlreport

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// comparisonWithExtras is a comparison with device scores, metrics and CAMBI
// per frame, banded or not.
func comparisonWithExtras(
	mode string,
	banded bool,
) *analysis.Comparison {
	c := sampleComparison(mode)
	v := c.VMAF

	est := func(mean, half float64) quality.Estimate {
		if mode == quality.ModeExact {
			return quality.Estimate{Mean: mean, Low: mean, High: mean}
		}

		return quality.Estimate{Mean: mean, Low: mean - half, High: mean + half, HalfWidth: half}
	}

	v.Devices = []quality.DeviceResult{{
		Device: "phone", Model: vmaf.ModelSpec{Name: "vmaf_v1.0.16_5d0h", Width: 1920, Height: 1080},
		Estimate: est(93.5, 0.4), Scored: stats.Summary{P5: 88.25},
	}}
	v.Metrics = []quality.MetricResult{
		{Name: quality.SeriesPSNRYUV, Estimate: est(40.12, 0.1), Scored: stats.Summary{P5: 36.5, Min: 33, Max: 45}},
		{Name: quality.SeriesCAMBI, Estimate: est(2.5, 0.2), Scored: stats.Summary{P95: 6.25, Max: 7}},
	}

	cambi := 1.0
	if banded {
		cambi = 7
	}

	for i := range v.Frames {
		v.Frames[i].Metrics = map[string]float64{quality.SeriesCAMBI: cambi}
	}

	v.Banding = &quality.Banding{Threshold: quality.BandingThreshold}
	if banded {
		v.Banding.BandedFrames = len(v.Frames)
		v.Banding.Segments = []quality.BandingSegment{{
			Interval: media.Interval{Start: 0, End: media.Seconds(1.5)}, Frames: len(v.Frames), Mean: 7, Peak: 7,
		}}
	}

	return c
}

func TestComparisonExtras(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		result  *analysis.Comparison
		want    []string
		wantNot []string
	}{
		{
			name:   "sampled, banded",
			result: comparisonWithExtras(quality.ModeSampled, true),
			want: []string{
				"<td>phone</td><td>vmaf_v1.0.16_5d0h</td><td>1920×1080</td><td>93.50</td><td>93.10 – 93.90</td><td>88.25</td>",
				"95% confidence intervals from the clips VMAF sampled",
				"<td>PSNR YUV 14:1:1</td><td>40.12</td><td>40.02 – 40.22</td><td>36.50</td><td>33.00</td><td>45.00</td><td>dB</td>",
				"<td>CAMBI (banding)</td><td>2.50</td><td>2.30 – 2.70</td><td>6.25</td>",
				"above 5 banding is visible", "<b>2 / 2 scored</b>", "<title>banding</title>",
				"<td>0:00.00</td><td>0:01.50</td><td>2</td><td>7.00</td><td>7.00</td>",
			},
		},
		{
			name:    "exact, clean",
			result:  comparisonWithExtras(quality.ModeExact, false),
			want:    []string{"<td>93.50</td><td>exact</td>", "every frame", "<b>0 / 2 scored</b>"},
			wantNot: []string{"<title>banding</title>", "banded frames"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			html := renderHTML(t, testCase.result)

			assert.Equal(t, []string{"VMAF", "VMAF per device", "Metrics", "Banding"}, titles(html))
			assert.Len(t, charts.FindAllString(html, -1), 2)

			for _, want := range testCase.want {
				assert.Contains(t, html, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, html, unwanted)
			}
		})
	}
}

func TestBandingSectionWithoutCAMBIFrames(
	t *testing.T,
) {
	v := comparisonWithExtras(quality.ModeSampled, false).VMAF
	v.Frames = nil

	s := bandingSection(v)

	assert.Equal(t, "Banding", s.Title)
	assert.Equal(t, "0 / 0 scored", s.Stats[0].Value)
	assert.Nil(t, s.Table)
}

func TestComparisonBudget(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		budget  *quality.SampleReport
		want    []string
		wantNot []string
	}{
		{
			name:    "precision-driven",
			wantNot: []string{"Budget"},
		},
		{
			name:   "share of frames",
			budget: &quality.SampleReport{Sample: quality.Sample{Share: 0.05}, Clips: 199, Variance: quality.VariancePooled},
			want:   []string{"<span>Precision</span><b>± 0.45 (95% CI)</b></div><div class=\"stat\"><span>Budget</span><b>5% of frames · 199 clips</b>"},
		},
		{
			name: "clamped clips per scene",
			budget: &quality.SampleReport{
				Sample: quality.Sample{PerScene: 1}, Clips: 12, Boundaries: quality.BoundariesShots,
				Variance: quality.VarianceCollapsed, Clamped: "one scene: 2 clips scored to estimate the variance",
			},
			want: []string{"1/scene (shots&#43;keyframes) · 12 clips", "one scene: 2 clips scored to estimate the variance"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c := sampleComparison(quality.ModeSampled)
			c.VMAF.Sample = testCase.budget
			html := renderHTML(t, c)

			for _, want := range testCase.want {
				assert.Contains(t, html, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, html, unwanted)
			}
		})
	}
}
