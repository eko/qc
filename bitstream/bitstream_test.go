package bitstream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

const frame = 40 * time.Millisecond // 25 fps

// constantStream builds n packets of size bytes, with a keyframe every gop frames.
func constantStream(
	n, size, gop int,
) []media.Packet {
	packets := make([]media.Packet, n)
	for i := range packets {
		packets[i] = media.Packet{
			PTS:      time.Duration(i) * frame,
			DTS:      time.Duration(i) * frame,
			Duration: frame,
			Size:     size,
			Keyframe: i%gop == 0,
		}
	}

	return packets
}

func TestAnalyze(
	t *testing.T,
) {
	spiky := constantStream(100, 1000, 25)
	spiky[60].Size = 26000

	reordered := constantStream(50, 1000, 25)
	reordered[1], reordered[2] = reordered[2], reordered[1]

	delayed := constantStream(25, 1000, 25)
	for i := range delayed {
		delayed[i].PTS += 80 * time.Millisecond
	}

	irregular := constantStream(100, 1000, 1000)
	for _, i := range []int{0, 10, 60, 70} {
		irregular[i].Keyframe = true
	}

	testCases := []struct {
		name  string
		input []media.Packet
		check func(t *testing.T, r Report)
	}{
		{
			name:  "empty",
			input: nil,
			check: func(t *testing.T, r Report) {
				assert.Zero(t, r.PacketCount)
				assert.Empty(t, r.Bitrate)
			},
		},
		{
			name:  "constant bitrate with fixed gop",
			input: constantStream(100, 1000, 25),
			check: func(t *testing.T, r Report) {
				assert.Equal(t, media.Seconds(4), r.Duration)
				assert.Equal(t, int64(200_000), r.AverageBitrate)
				assert.Equal(t, int64(200_000), r.PeakBitrate)
				assert.InDelta(t, 1.0, r.PeakToAverage, 1e-9)
				require.Len(t, r.Bitrate, 4)
				assert.Equal(t, int64(200_000), r.Bitrate[3].Bitrate)
				assert.Equal(t, 4, r.GOP.KeyframeCount)
				assert.Equal(t, media.Seconds(1), r.GOP.MeanInterval)
				assert.True(t, r.GOP.Fixed)
			},
		},
		{
			name:  "peak is located on the spike",
			input: spiky,
			check: func(t *testing.T, r Report) {
				assert.Equal(t, int64(24*1000*8+26000*8), r.PeakBitrate)
				assert.LessOrEqual(t, r.PeakAt.Std(), 60*frame)
				assert.Greater(t, r.PeakAt.Std(), 60*frame-time.Second)
				assert.Equal(t, 26000, r.FrameSize.Max)
				assert.Equal(t, 1000, r.FrameSize.P95)
			},
		},
		{
			name:  "decode order is sorted by presentation time",
			input: reordered,
			check: func(t *testing.T, r Report) {
				assert.Equal(t, media.Seconds(2), r.Duration)
				assert.Equal(t, []media.Duration{0, media.Seconds(1)}, r.Keyframes)
				assert.Equal(t, media.Duration(frame), r.PTS[1])
			},
		},
		{
			name:  "a delayed first frame is the start, pts stay relative",
			input: delayed,
			check: func(t *testing.T, r Report) {
				assert.Equal(t, media.Duration(80*time.Millisecond), r.Start)
				assert.Zero(t, r.PTS[0])
				assert.Equal(t, media.Duration(frame), r.PTS[1])
			},
		},
		{
			name:  "partial last bucket is normalised by its length",
			input: constantStream(30, 1000, 30),
			check: func(t *testing.T, r Report) {
				require.Len(t, r.Bitrate, 2)
				assert.Equal(t, int64(200_000), r.Bitrate[1].Bitrate)
			},
		},
		{
			name:  "single instantaneous packet",
			input: []media.Packet{{Size: 100, Keyframe: true}},
			check: func(t *testing.T, r Report) {
				assert.Zero(t, r.Duration)
				assert.Zero(t, r.AverageBitrate)
				assert.Zero(t, r.PeakToAverage)
				assert.Equal(t, []BitratePoint{{Start: 0, Bitrate: 0}}, r.Bitrate)
				assert.Equal(t, 1, r.GOP.KeyframeCount)
				assert.Zero(t, r.GOP.MeanInterval)
			},
		},
		{
			name:  "irregular gop",
			input: irregular,
			check: func(t *testing.T, r Report) {
				assert.Equal(t, 4, r.GOP.KeyframeCount)
				assert.Equal(t, media.Duration(10*frame), r.GOP.MinInterval)
				assert.Equal(t, media.Duration(50*frame), r.GOP.MaxInterval)
				assert.False(t, r.GOP.Fixed)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.check(t, Analyze(testCase.input, Options{}))
		})
	}
}

func TestAnalyzeOptions(
	t *testing.T,
) {
	spiky := constantStream(100, 1000, 25)
	spiky[60].Size = 26000

	report := Analyze(spiky, Options{Interval: 2 * time.Second, PeakWindow: 2 * time.Second})

	assert.Equal(t, media.Seconds(2), report.Interval)
	assert.Equal(t, media.Seconds(2), report.PeakWindow)
	require.Len(t, report.Bitrate, 2)
	assert.Equal(t, int64((49*1000+26000)*8/2), report.PeakBitrate)
}
