package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestColorIsHDR(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		color Color
		hdr   bool
		full  bool
	}{
		{name: "pq", color: Color{Transfer: TransferPQ, Range: "tv"}, hdr: true},
		{name: "hlg full range", color: Color{Transfer: TransferHLG, Range: "pc"}, hdr: true, full: true},
		{name: "sdr jpeg range", color: Color{Transfer: "bt709", Range: "jpeg"}, full: true},
		{name: "unknown", color: Color{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.hdr, testCase.color.IsHDR())
			assert.Equal(t, testCase.full, testCase.color.FullRange())
		})
	}
}

func TestVideoStreamMeasurableHDR(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		video VideoStream
		want  bool
	}{
		{name: "hdr10", video: VideoStream{Color: Color{Transfer: TransferPQ}}, want: true},
		{name: "sdr", video: VideoStream{Color: Color{Transfer: "bt709"}}},
		{
			name:  "dolby vision 8.1 base layer",
			video: VideoStream{Color: Color{Transfer: TransferPQ}, HDR: HDR{DolbyVision: &DolbyVision{Profile: 8, CompatibilityID: 1}}},
			want:  true,
		},
		{
			name:  "dolby vision 5 is IPTPQc2",
			video: VideoStream{Color: Color{Transfer: TransferPQ}, HDR: HDR{DolbyVision: &DolbyVision{Profile: 5}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.video.MeasurableHDR())
		})
	}
}
