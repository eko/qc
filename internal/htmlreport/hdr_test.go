package htmlreport

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

// hdrDecodedReport is the decoded sample report of an HDR10 video with its
// light levels.
func hdrDecodedReport(
	transfer string,
) *analysis.Report {
	r := sampleDecodedReport()
	v := &r.Info.Video[0]
	v.Color = media.Color{Primaries: "bt2020", Transfer: transfer, Space: "bt2020nc", Range: "tv"}
	v.HDR = media.HDR{
		DynamicRange:      media.DynamicRangeHDR10,
		MasteringDisplay:  &media.MasteringDisplay{MaxLuminance: 1000, MinLuminance: 0.0001},
		ContentLightLevel: &media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972},
	}

	n := len(r.Frames.PTS)
	r.Video.Light = &light.Result{Transfer: transfer, SampleStep: 4, MaxCLL: 4891, MaxCLLRobust: 1522, MaxFALL: 973}
	r.Frames.PeakNits, r.Frames.RobustPeakNits, r.Frames.AverageNits = make([]float64, n), make([]float64, n), make([]float64, n)

	return r
}

func TestRenderAnalysisHDR(
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
			report: hdrDecodedReport(media.TransferPQ),
			want: []string{
				"Light levels", "PQ display light, absolute", "<b>1522 cd/m²</b>", "<b>4891 cd/m²</b>", "<b>973 cd/m²</b>",
				"<b>1567 / 972 cd/m²</b>", "<b>0.0001–1000 cd/m²</b>", "signalled MaxCLL", "strict peak",
				"MaxCLL · MaxFALL 973", "Signalled MaxCLL and MaxFALL match the content",
			},
		},
		{
			name: "hlg without metadata",
			report: func() *analysis.Report {
				r := hdrDecodedReport(media.TransferHLG)
				r.Info.Video[0].HDR = media.HDR{DynamicRange: media.DynamicRangeHLG}
				r.Video.Light.DisplayPeak = 1000

				return r
			}(),
			want:    []string{"HLG display light on a 1000 cd/m² display", "<b>none</b>"},
			wantNot: []string{"signalled MaxCLL"},
		},
		{name: "sdr", report: sampleDecodedReport(), wantNot: []string{"Light levels"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, RenderAnalysis(&buf, testCase.report))

			out := buf.String()
			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, not := range testCase.wantNot {
				assert.NotContains(t, out, not)
			}
		})
	}
}

func TestRenderComparisonHDR(
	t *testing.T,
) {
	testCases := []struct {
		name string
		hdr  *quality.HDRReport
		want []string
	}{
		{
			name: "pq", hdr: &quality.HDRReport{Transfer: media.TransferPQ, Metric: quality.HDRMetricPQ, Note: "PQ note"},
			want: []string{"VMAF · PQ, not HDR-calibrated", "PQ note", "wPSNR weights the error"},
		},
		{
			name: "tone mapped", hdr: &quality.HDRReport{Transfer: media.TransferPQ, Metric: quality.HDRMetricToneMap, Calibrated: true, Note: "SDR note"},
			want: []string{"VMAF · SDR tone mapped", "SDR note"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cmp := sampleComparison(quality.ModeSampled)
			cmp.VMAF.HDR = testCase.hdr
			cmp.VMAF.Metrics = []quality.MetricResult{{Name: quality.SeriesDeltaEITP}}

			var buf bytes.Buffer
			require.NoError(t, RenderComparison(&buf, cmp))

			for _, want := range testCase.want {
				assert.Contains(t, buf.String(), want)
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
		{name: "dolby vision measured", f: findings.Finding{Code: findings.DolbyVision, Value: 8, Other: 1, Limit: 1}, want: "Dolby Vision profile 8"},
		{name: "dolby vision 5", f: findings.Finding{Code: findings.DolbyVision, Value: 5}, want: "not measured"},
		{name: "hdr10+", f: findings.Finding{Code: findings.HDR10Plus}, want: "HDR10+"},
		{name: "primaries", f: findings.Finding{Code: findings.HDRPrimaries, Text: "bt709"}, want: "HDR transfer with bt709 primaries"},
		{name: "matrix", f: findings.Finding{Code: findings.HDRMatrix, Text: "bt709"}, want: "bt709 matrix"},
		{name: "bit depth", f: findings.Finding{Code: findings.HDRBitDepth, Value: 8}, want: "8-bit"},
		{name: "full range", f: findings.Finding{Code: findings.HDRFullRange}, want: "full range"},
		{name: "mastering", f: findings.Finding{Code: findings.MissingMastering}, want: "SMPTE ST 2086"},
		{name: "missing cll measured", f: findings.Finding{Code: findings.MissingContentLight, Value: 1522, Limit: 973}, want: "measured 1522 / 973"},
		{name: "missing cll", f: findings.Finding{Code: findings.MissingContentLight}, want: "HDR10 without MaxCLL/MaxFALL"},
		{name: "brighter", f: findings.Finding{Code: findings.ContentBrighter, Text: "maxcll", Value: 1522, Limit: 1000}, want: "Content brighter than its signalled MaxCLL"},
		{name: "dimmer", f: findings.Finding{Code: findings.ContentDimmer, Text: "maxfall", Value: 973, Limit: 4000}, want: "Signalled MaxFALL well above"},
		{name: "match", f: findings.Finding{Code: findings.LightLevelsMatch}, want: "match the content"},
		{name: "vmaf", f: findings.Finding{Code: findings.HDRVMAF, Text: "the note"}, want: "the note"},
		{name: "ladder target", f: findings.Finding{Code: findings.HDRLadderTarget, Text: media.TransferPQ, Value: 95}, want: "VMAF 95 here is not the quality it is in SDR"},
		{name: "ladder hdr10", f: findings.Finding{Code: findings.HDRLadderSignal, Text: media.TransferPQ, Value: 1}, want: "HDR10 metadata"},
		{name: "ladder hlg", f: findings.Finding{Code: findings.HDRLadderSignal, Text: media.TransferHLG}, want: "HLG colour"},
		{name: "ladder other", f: findings.Finding{Code: findings.HDRLadderSignal, Text: "x"}, want: "x colour"},
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

func TestLadderHDRFindings(
	t *testing.T,
) {
	var buf bytes.Buffer

	res := sampleLadder(t, "h264")
	res.HDR = hdrLadderSample()

	require.NoError(t, RenderLadder(&buf, res))
	assert.Contains(t, buf.String(), "HDR in H.264 (High 10)")
	assert.Contains(t, buf.String(), "HDR source: rungs encoded in 10 bits")
}

func hdrLadderSample() *ladder.HDRLadder {
	return &ladder.HDRLadder{
		Signal:           encode.Signal{Color: media.Color{Transfer: media.TransferPQ}},
		BitDepthUpgraded: true,
	}
}
