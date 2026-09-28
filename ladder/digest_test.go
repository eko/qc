package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/media"
)

func TestVideoOrigin(
	t *testing.T,
) {
	video := media.VideoStream{StartTime: media.Seconds(0.04)}

	testCases := []struct {
		name   string
		report *analysis.Report
		want   media.Duration
	}{
		{
			name:   "first presentation time of the bitstream",
			report: &analysis.Report{Bitstream: &bitstream.Report{Start: media.Seconds(1.4)}},
			want:   media.Seconds(1.4),
		},
		{name: "stream start without the bitstream", report: &analysis.Report{}, want: media.Seconds(0.04)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, videoOrigin(testCase.report, video))
		})
	}
}

func TestDigestSegments(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		duration  media.Duration
		segment   media.Duration
		wantCount int
	}{
		{name: "short title is used whole", duration: media.Seconds(30), segment: media.Seconds(2), wantCount: 1},
		{name: "title of the budget is used whole", duration: media.Seconds(40), segment: media.Seconds(2), wantCount: 1},
		{name: "long title is sampled evenly", duration: media.Seconds(600), segment: media.Seconds(2), wantCount: 20},
		{name: "segment longer than the budget", duration: media.Seconds(600), segment: media.Seconds(60), wantCount: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			segments := digestSegments(testCase.duration, testCase.segment, media.Seconds(40))
			require.Len(t, segments, testCase.wantCount)

			for i, s := range segments {
				assert.GreaterOrEqual(t, s.Start, media.Duration(0))
				assert.LessOrEqual(t, s.End, testCase.duration)

				if i > 0 {
					assert.Greater(t, s.Start, segments[i-1].End)
				}
			}
		})
	}
}

func TestDigestSize(
	t *testing.T,
) {
	rate := media.Rational{Num: 25, Den: 1}

	testCases := []struct {
		name      string
		video     media.VideoStream
		wantDepth int
		wantBytes float64
	}{
		{
			name:      "8-bit takes one byte per sample",
			video:     media.VideoStream{Width: 1920, Height: 1080, BitDepth: 8, AvgFrameRate: rate},
			wantDepth: 8,
			wantBytes: 1920 * 1080 * 1.5 * 1000,
		},
		{
			name:      "unknown depth is 8-bit",
			video:     media.VideoStream{Width: 1920, Height: 1080, AvgFrameRate: rate},
			wantDepth: 8,
			wantBytes: 1920 * 1080 * 1.5 * 1000,
		},
		{
			name:      "10-bit takes two bytes per sample",
			video:     media.VideoStream{Width: 1920, Height: 1080, BitDepth: 10, AvgFrameRate: rate},
			wantDepth: 10,
			wantBytes: 1920 * 1080 * 3 * 1000,
		},
		{
			name:      "12-bit is measured at 10 bits",
			video:     media.VideoStream{Width: 1920, Height: 1080, BitDepth: 12, AvgFrameRate: rate},
			wantDepth: 10,
			wantBytes: 1920 * 1080 * 3 * 1000,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			depth := digestBitDepth(testCase.video)
			assert.Equal(t, testCase.wantDepth, depth)
			assert.InDelta(t, testCase.wantBytes, rawDigestBytes(testCase.video, depth, media.Seconds(40)), 1)
		})
	}
}
