package tui

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/light"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// hdrReport is the sample report of an HDR10 video with its light levels.
func hdrReport(
	transfer string,
) *analysis.Report {
	r := sampleReport()
	v := &r.Info.Video[0]
	v.Color = media.Color{Primaries: "bt2020", Transfer: transfer, Space: "bt2020nc", Range: "tv"}
	v.HDR = media.HDR{
		DynamicRange:      media.DynamicRangeHDR10,
		MasteringDisplay:  &media.MasteringDisplay{MaxLuminance: 1000},
		ContentLightLevel: &media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972},
	}

	n := len(r.Frames.PTS)
	r.Video.Light = &light.Result{Transfer: transfer, SampleStep: 4, MaxCLL: 4891, MaxCLLRobust: 1522, MaxFALL: 973}
	r.Frames.PeakNits = series(n, func(i int) float64 { return 900 + float64(i%50) })
	r.Frames.RobustPeakNits = series(n, func(i int) float64 { return 800 + float64(i%50) })
	r.Frames.AverageNits = series(n, func(i int) float64 { return 200 + float64(i%30) })

	return r
}

func TestRenderReportHDR(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		report  *analysis.Report
		want    []string
		wantNot []string
	}{
		{
			name:   "pq",
			report: hdrReport(media.TransferPQ),
			want: []string{
				"[HDR10]", "Light levels", "MaxCLL 1522 (strict 4891)", "MaxFALL 973", "signalled 1567 / 972", "peak 99.9%", "average",
				"signalled MaxCLL and MaxFALL match the content (measured 1522 / 973 cd/m²)",
			},
		},
		{
			name: "hlg on the nominal display",
			report: func() *analysis.Report {
				r := hdrReport(media.TransferHLG)
				r.Info.Video[0].HDR = media.HDR{DynamicRange: media.DynamicRangeHLG}
				r.Video.Light.DisplayPeak = 1000

				return r
			}(),
			want: []string{"[HLG]", "signalled none", "cd/m² on a 1000 cd/m² display"},
		},
		{name: "sdr", report: sampleReport(), wantNot: []string{"Light levels", "[SDR]"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, RenderReport(&buf, testCase.report, 120, ""))

			out := plain(buf.String())
			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, not := range testCase.wantNot {
				assert.NotContains(t, out, not)
			}
		})
	}
}

func TestHDRFindingText(
	t *testing.T,
) {
	testCases := []struct {
		name string
		f    findings.Finding
		want string
	}{
		{name: "dolby vision measured", f: findings.Finding{Code: findings.DolbyVision, Value: 8, Other: 1, Limit: 1}, want: "Dolby Vision profile 8 (base layer compatibility 1)"},
		{name: "dolby vision 5", f: findings.Finding{Code: findings.DolbyVision, Value: 5}, want: "not measured"},
		{name: "hdr10+", f: findings.Finding{Code: findings.HDR10Plus}, want: "HDR10+"},
		{name: "primaries", f: findings.Finding{Code: findings.HDRPrimaries, Text: "bt709"}, want: "bt709 primaries"},
		{name: "matrix", f: findings.Finding{Code: findings.HDRMatrix, Text: "bt709"}, want: "bt709 matrix"},
		{name: "bit depth", f: findings.Finding{Code: findings.HDRBitDepth, Value: 8}, want: "8-bit"},
		{name: "full range", f: findings.Finding{Code: findings.HDRFullRange}, want: "full range"},
		{name: "mastering", f: findings.Finding{Code: findings.MissingMastering}, want: "SMPTE ST 2086"},
		{name: "missing cll measured", f: findings.Finding{Code: findings.MissingContentLight, Value: 1522, Limit: 973}, want: "measured 1522 / 973"},
		{name: "missing cll", f: findings.Finding{Code: findings.MissingContentLight}, want: "HDR10 without MaxCLL/MaxFALL"},
		{name: "brighter", f: findings.Finding{Code: findings.ContentBrighter, Text: "maxcll", Value: 1522, Limit: 1000}, want: "brighter than its signalled MaxCLL: measured 1522 vs 1000"},
		{name: "dimmer", f: findings.Finding{Code: findings.ContentDimmer, Text: "maxfall", Value: 973, Limit: 4000}, want: "signalled MaxFALL well above the content: 4000 vs measured 973"},
		{name: "match", f: findings.Finding{Code: findings.LightLevelsMatch, Value: 1522, Limit: 973}, want: "match the content"},
		{name: "vmaf", f: findings.Finding{Code: findings.HDRVMAF, Text: "the note"}, want: "the note"},
		{name: "ladder hdr10", f: findings.Finding{Code: findings.HDRLadderSignal, Text: media.TransferPQ, Value: 1}, want: "PQ colour description and the HDR10 metadata"},
		{name: "ladder hlg", f: findings.Finding{Code: findings.HDRLadderSignal, Text: media.TransferHLG}, want: "carries the HLG colour description"},
		{name: "ladder other", f: findings.Finding{Code: findings.HDRLadderSignal, Text: "x"}, want: "carries the x colour"},
		{name: "upgraded", f: findings.Finding{Code: findings.HDRBitDepthUpgraded}, want: "10 bits"},
		{name: "h264", f: findings.Finding{Code: findings.HDRPlayerSupport}, want: "H.264"},
		{name: "nvenc", f: findings.Finding{Code: findings.HDRNVENCMetadata}, want: "NVENC"},
		{name: "not hdr", f: findings.Finding{Code: findings.Interlaced}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := hdrFindingText(testCase.f)
			if testCase.want == "" {
				assert.Empty(t, got)

				return
			}

			assert.Contains(t, got, testCase.want)
		})
	}
}

func TestHDRVMAFLabel(
	t *testing.T,
) {
	assert.Equal(t, "VMAF on PQ, not HDR-calibrated", hdrVMAFLabel(&quality.HDRReport{Transfer: media.TransferPQ}))
	assert.Equal(t, "VMAF on an SDR tone mapping", hdrVMAFLabel(&quality.HDRReport{Metric: quality.HDRMetricToneMap}))
}

func TestRenderComparisonHDR(
	t *testing.T,
) {
	cmp := sampleComparison(quality.ModeSampled)
	cmp.VMAF.HDR = &quality.HDRReport{Transfer: media.TransferHLG, Metric: quality.HDRMetricPQ, Note: "HLG note"}

	var buf bytes.Buffer
	require.NoError(t, RenderComparison(&buf, cmp, 120, ""))

	out := plain(buf.String())
	assert.Contains(t, out, "hdr VMAF on HLG, not HDR-calibrated")
	assert.Contains(t, out, "HLG note")
}

func TestRenderLadderHDR(
	t *testing.T,
) {
	res := sampleLadder(t)
	res.HDR = &ladder.HDRLadder{Signal: encode.Signal{Color: media.Color{Transfer: media.TransferHLG}}, BitDepthUpgraded: true}

	var buf bytes.Buffer
	require.NoError(t, RenderLadder(&buf, res, 120, "", false))

	out := plain(buf.String())
	assert.Contains(t, out, "HDR rungs: every encode carries the HLG colour description")
	assert.Contains(t, out, "HDR in H.264 (High 10)")
}
