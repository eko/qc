package findings

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/light"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var (
	bt2100    = media.Color{Primaries: "bt2020", Transfer: media.TransferPQ, Space: "bt2020nc", Range: "tv"}
	mastering = &media.MasteringDisplay{MaxLuminance: 1000, MinLuminance: 0.0001}
)

// hdrReport is an inspected HDR video, with its light levels when lr is
// set.
func hdrReport(
	v media.VideoStream,
	lr *light.Result,
) *analysis.Report {
	r := &analysis.Report{Info: &media.Info{Video: []media.VideoStream{v}}}
	if lr != nil {
		r.Video = &analysis.VideoReport{Light: lr}
	}

	return r
}

func TestHDRAnalysisFindings(
	t *testing.T,
) {
	hdr10 := func(cll *media.ContentLightLevel) media.VideoStream {
		return media.VideoStream{BitDepth: 10, Color: bt2100, HDR: media.HDR{
			DynamicRange: media.DynamicRangeHDR10, MasteringDisplay: mastering, ContentLightLevel: cll,
		}}
	}
	measured := &light.Result{MaxCLL: 4891, MaxCLLRobust: 1522, MaxFALL: 973}

	testCases := []struct {
		name  string
		video media.VideoStream
		light *light.Result
		want  [][2]string
		check func(t *testing.T, list []Finding)
	}{
		{name: "sdr", video: media.VideoStream{Color: media.Color{Transfer: "bt709"}}},
		{
			name: "matching light levels", video: hdr10(&media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972}), light: measured,
			want: [][2]string{{"ok", string(LightLevelsMatch)}},
		},
		{
			name: "content brighter than its MaxCLL", video: hdr10(&media.ContentLightLevel{MaxCLL: 1000, MaxFALL: 972}), light: measured,
			want: [][2]string{{"warn", string(ContentBrighter)}},
			check: func(t *testing.T, list []Finding) {
				assert.Equal(t, "maxcll", list[0].Text)
				assert.InDelta(t, 1522, list[0].Value, 1e-9)
				assert.InDelta(t, 1000, list[0].Limit, 1e-9)
			},
		},
		{
			name: "MaxFALL over-declared", video: hdr10(&media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 4000}), light: measured,
			want: [][2]string{{"info", string(ContentDimmer)}},
		},
		{
			name: "levels not measured", video: hdr10(&media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972}),
		},
		{
			name: "no content light level", video: hdr10(nil), light: measured,
			want: [][2]string{{"warn", string(MissingContentLight)}},
			check: func(t *testing.T, list []Finding) {
				assert.InDelta(t, 973, list[0].Limit, 1e-9)
			},
		},
		{
			name: "zero content light level, not measured", video: hdr10(&media.ContentLightLevel{}),
			want: [][2]string{{"warn", string(MissingContentLight)}},
		},
		{
			name:  "pq without mastering, bt709 primaries, 8 bits, full range",
			video: media.VideoStream{BitDepth: 8, Color: media.Color{Primaries: "bt709", Transfer: media.TransferPQ, Space: "bt709", Range: "pc"}, HDR: media.HDR{DynamicRange: media.DynamicRangePQ}},
			want: [][2]string{
				{"warn", string(HDRPrimaries)}, {"warn", string(HDRMatrix)}, {"warn", string(HDRBitDepth)},
				{"info", string(HDRFullRange)}, {"warn", string(MissingMastering)},
			},
		},
		{
			name: "hlg has no static metadata to check", video: media.VideoStream{BitDepth: 10, Color: media.Color{Transfer: media.TransferHLG}, HDR: media.HDR{DynamicRange: media.DynamicRangeHLG}},
			light: measured,
		},
		{
			name: "dolby vision 8.1 with hdr10+", video: media.VideoStream{BitDepth: 10, Color: bt2100, HDR: media.HDR{
				DynamicRange: media.DynamicRangeDolbyVision, DolbyVision: &media.DolbyVision{Profile: 8, CompatibilityID: 1},
				HDR10Plus: true, MasteringDisplay: mastering, ContentLightLevel: &media.ContentLightLevel{MaxCLL: 1567, MaxFALL: 972},
			}},
			want: [][2]string{{"info", string(DolbyVision)}, {"info", string(HDR10Plus)}},
			check: func(t *testing.T, list []Finding) {
				assert.InDelta(t, 8, list[0].Value, 0)
				assert.Equal(t, 1, list[0].Other)
				assert.InDelta(t, 1, list[0].Limit, 0, "its base layer is measured")
			},
		},
		{
			name: "dolby vision 5", video: media.VideoStream{HDR: media.HDR{DynamicRange: media.DynamicRangeDolbyVision, DolbyVision: &media.DolbyVision{Profile: 5}}},
			want: [][2]string{{"info", string(DolbyVision)}},
			check: func(t *testing.T, list []Finding) {
				assert.Zero(t, list[0].Limit)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			list := videoHDRFindings(hdrReport(testCase.video, testCase.light))
			assert.Equal(t, testCase.want, codes(list))

			if testCase.check != nil {
				testCase.check(t, list)
			}
		})
	}

	assert.Empty(t, videoHDRFindings(&analysis.Report{Info: &media.Info{}}))
}

func TestHDRComparisonFinding(
	t *testing.T,
) {
	testCases := []struct {
		name string
		hdr  *quality.HDRReport
		want [][2]string
	}{
		{name: "sdr", want: [][2]string{{"info", string(SampledOnly)}}},
		{
			name: "pq", hdr: &quality.HDRReport{Metric: quality.HDRMetricPQ, Note: "note"},
			want: [][2]string{{"info", string(HDRVMAF)}, {"info", string(SampledOnly)}},
		},
		{
			name: "tone mapped", hdr: &quality.HDRReport{Metric: quality.HDRMetricToneMap, Calibrated: true},
			want: [][2]string{{"ok", string(HDRVMAF)}, {"info", string(SampledOnly)}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			list := Comparison(&quality.Result{Mode: quality.ModeSampled, HDR: testCase.hdr})
			assert.Equal(t, testCase.want, codes(list))
		})
	}
}

func TestHDRLadderFindings(
	t *testing.T,
) {
	signal := encode.Signal{Color: bt2100, Mastering: mastering}

	testCases := []struct {
		name  string
		codec encode.Codec
		hdr   *ladder.HDRLadder
		want  [][2]string
		value float64
	}{
		{name: "sdr", codec: encode.Codec{Name: "hevc"}},
		{
			name: "hevc hdr10 upgraded to 10 bits", codec: encode.Codec{Name: "hevc"},
			hdr:   &ladder.HDRLadder{Signal: signal, BitDepthUpgraded: true},
			want:  [][2]string{{"ok", string(HDRLadderSignal)}, {"info", string(HDRLadderTarget)}, {"info", string(HDRBitDepthUpgraded)}},
			value: 1,
		},
		{
			name: "h264 hlg", codec: encode.Codec{Name: "h264"},
			hdr:  &ladder.HDRLadder{Signal: encode.Signal{Color: media.Color{Transfer: media.TransferHLG}}},
			want: [][2]string{{"ok", string(HDRLadderSignal)}, {"info", string(HDRLadderTarget)}, {"warn", string(HDRPlayerSupport)}},
		},
		{
			name: "nvenc hdr10", codec: encode.Codec{Name: "hevc", Hardware: encode.HardwareNVENC},
			hdr:   &ladder.HDRLadder{Signal: signal},
			want:  [][2]string{{"ok", string(HDRLadderSignal)}, {"info", string(HDRLadderTarget)}, {"info", string(HDRNVENCMetadata)}},
			value: 1,
		},
		{
			name: "scored on an SDR tone mapping: the targets mean what they do in SDR", codec: encode.Codec{Name: "hevc"},
			hdr:   &ladder.HDRLadder{Signal: signal, Metric: quality.HDRMetricToneMap},
			want:  [][2]string{{"ok", string(HDRLadderSignal)}},
			value: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := &ladder.Result{Codec: testCase.codec, HDR: testCase.hdr, Rungs: []ladder.Rung{{PredictedVMAF: 95}}}

			list := hdrLadderFindings(r)
			assert.Equal(t, testCase.want, codes(list))

			if len(list) > 0 {
				assert.InDelta(t, testCase.value, list[0].Value, 0)
				assert.Contains(t, Ladder(r), list[0])
			}
		})
	}
}

func TestAnalysisIncludesHDRFindings(
	t *testing.T,
) {
	v := media.VideoStream{BitDepth: 10, Color: bt2100, HDR: media.HDR{DynamicRange: media.DynamicRangePQ}}

	assert.Contains(t, codes(Analysis(hdrReport(v, nil))), [2]string{"warn", string(MissingMastering)})
}
