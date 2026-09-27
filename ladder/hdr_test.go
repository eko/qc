package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var hdr10Source = media.VideoStream{
	Color: media.Color{Primaries: "bt2020", Transfer: media.TransferPQ, Space: "bt2020nc", Range: "tv"},
	HDR: media.HDR{
		DynamicRange:      media.DynamicRangeHDR10,
		MasteringDisplay:  &media.MasteringDisplay{MaxLuminance: 1000, MinLuminance: 0.0001},
		ContentLightLevel: &media.ContentLightLevel{MaxCLL: 1000, MaxFALL: 400},
	},
}

func TestHDRLadder(
	t *testing.T,
) {
	measured := &media.ContentLightLevel{MaxCLL: 900, MaxFALL: 300}
	noCLL := hdr10Source
	noCLL.HDR.ContentLightLevel = nil

	testCases := []struct {
		name         string
		video        media.VideoStream
		opts         Options
		wantNil      bool
		wantDepth    int
		wantUpgraded bool
		wantCLL      *media.ContentLightLevel
		wantMetric   quality.HDRMetric
	}{
		{name: "sdr", video: media.VideoStream{}, opts: Options{BitDepth: 8}, wantNil: true, wantDepth: 8},
		{
			name: "8-bit asked", video: hdr10Source, opts: Options{BitDepth: 8},
			wantDepth: 10, wantUpgraded: true, wantCLL: hdr10Source.HDR.ContentLightLevel, wantMetric: quality.HDRMetricPQ,
		},
		{
			name: "10-bit asked, tone mapped, signalled level wins", video: hdr10Source,
			opts:      Options{BitDepth: 10, ContentLight: measured, HDRMetric: quality.HDRMetricToneMap},
			wantDepth: 10, wantCLL: hdr10Source.HDR.ContentLightLevel, wantMetric: quality.HDRMetricToneMap,
		},
		{
			name: "measured level stands in", video: noCLL, opts: Options{ContentLight: measured},
			wantDepth: 10, wantUpgraded: true, wantCLL: measured, wantMetric: quality.HDRMetricPQ,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			opts := testCase.opts
			got := hdrLadder(&opts, testCase.video)

			assert.Equal(t, testCase.wantDepth, opts.BitDepth)

			if testCase.wantNil {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Equal(t, testCase.wantUpgraded, got.BitDepthUpgraded)
			assert.Equal(t, testCase.wantCLL, got.Signal.ContentLight)
			assert.Equal(t, testCase.wantMetric, got.Metric)
			assert.Equal(t, testCase.video.Color, got.Signal.Color)
		})
	}
}

func TestWithSignal(
	t *testing.T,
) {
	digest := &analysis.Report{Info: &media.Info{Path: "digest.nut", Video: []media.VideoStream{{Width: 64}}}}

	testCases := []struct {
		name   string
		report *analysis.Report
		video  media.VideoStream
		same   bool
	}{
		{name: "sdr source", report: digest, video: media.VideoStream{}, same: true},
		{name: "no report", report: nil, video: hdr10Source, same: true},
		{name: "no video", report: &analysis.Report{Info: &media.Info{}}, video: hdr10Source, same: true},
		{name: "hdr source", report: digest, video: hdr10Source},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := withSignal(testCase.report, testCase.video)
			if testCase.same {
				assert.Same(t, testCase.report, got)

				return
			}

			assert.Equal(t, hdr10Source.Color, got.Info.Video[0].Color)
			assert.Equal(t, hdr10Source.HDR, got.Info.Video[0].HDR)
			assert.Equal(t, 64, got.Info.Video[0].Width)
			assert.Empty(t, digest.Info.Video[0].Color.Transfer, "the digest's own report is unchanged")
		})
	}
}

// TestBuildHDR builds the ladder of an HDR10 source: every encode is 10-bit
// and carries the source's signal, and so does every command.
func TestBuildHDR(
	t *testing.T,
) {
	source := sourceReport(1920, 1080, 10, 25, 60)
	source.Info.Video[0].Color, source.Info.Video[0].HDR = hdr10Source.Color, hdr10Source.HDR

	lab := newFakeLab(rateModel{}, source)

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "hevc"})
	require.NoError(t, err)

	require.NotNil(t, res.HDR)
	assert.True(t, res.HDR.BitDepthUpgraded)

	for _, p := range lab.params {
		assert.Equal(t, 10, p.BitDepth)
		assert.Equal(t, encode.SignalOf(hdr10Source), p.Signal)
	}

	for _, r := range res.Rungs {
		assert.Contains(t, r.Command, "master-display=")
		assert.Contains(t, r.Command, "color_trc=smpte2084")
	}
}
