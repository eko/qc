package audio

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/audiotest"
	"github.com/eko/qc/media"
)

func TestParseLayout(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		layout   string
		channels int
		want     Layout
	}{
		{name: "stereo", layout: "stereo", channels: 2, want: Layout{Name: "stereo", Channels: []Channel{FrontLeft, FrontRight}}},
		{name: "5.1 side", layout: "5.1(side)", channels: 6, want: Layout{Name: "5.1(side)", Channels: []Channel{"FL", "FR", "FC", "LFE", "SL", "SR"}}},
		{name: "custom", layout: "FL+FR+LFE", channels: 3, want: Layout{Name: "FL+FR+LFE", Channels: []Channel{"FL", "FR", "LFE"}}},
		{name: "unsignalled stereo", layout: "", channels: 2, want: Layout{Name: "stereo", Channels: []Channel{"FL", "FR"}, Guessed: true}},
		{name: "unsignalled 5.1", layout: "6 channels", channels: 6, want: Layout{Name: "5.1", Channels: []Channel{"FL", "FR", "FC", "LFE", "BL", "BR"}, Guessed: true}},
		{name: "inconsistent", layout: "stereo", channels: 1, want: Layout{Name: "mono", Channels: []Channel{"FC"}, Guessed: true}},
		{name: "unknown", layout: "", channels: 3, want: Layout{Name: "3 channels", Channels: []Channel{"C1", "C2", "C3"}, Guessed: true}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, ParseLayout(testCase.layout, testCase.channels))
		})
	}
}

func TestWeightsAndPairs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		layout  string
		weights []float64
		pairs   [][2]int
	}{
		{name: "mono", layout: "mono", weights: []float64{1}},
		{name: "stereo", layout: "stereo", weights: []float64{1, 1}, pairs: [][2]int{{0, 1}}},
		{name: "downmix", layout: "downmix", weights: []float64{1, 1}, pairs: [][2]int{{0, 1}}},
		{name: "5.1: back channels are the surrounds", layout: "5.1", weights: []float64{1, 1, 1, 0, 1.41, 1.41}, pairs: [][2]int{{0, 1}, {4, 5}}},
		{name: "5.1 side", layout: "5.1(side)", weights: []float64{1, 1, 1, 0, 1.41, 1.41}, pairs: [][2]int{{0, 1}, {4, 5}}},
		{
			name: "7.1: rear channels behind the sides", layout: "7.1",
			weights: []float64{1, 1, 1, 0, 1, 1, 1.41, 1.41}, pairs: [][2]int{{0, 1}, {6, 7}, {4, 5}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			l := ParseLayout(testCase.layout, len(testCase.weights))
			assert.Equal(t, testCase.weights, l.Weights())
			assert.Equal(t, testCase.pairs, l.Pairs())
		})
	}
}

func TestChannelIsLFE(
	t *testing.T,
) {
	assert.True(t, LFE.IsLFE())
	assert.True(t, LFE2.IsLFE())
	assert.False(t, FrontCentre.IsLFE())
}

func TestAnalyzer(
	t *testing.T,
) {
	stream := media.AudioStream{Index: 2, SampleRate: audiotest.Rate, Channels: 2, ChannelLayout: "stereo", StartTime: media.Seconds(0.5)}
	a := NewAnalyzer(stream, Options{})

	require.NoError(t, a.Add(audiotest.Programme(audiotest.Rate, 10, 1)))

	track := a.Result(media.Seconds(0.25))
	assert.Equal(t, 2, track.Stream)
	assert.Equal(t, "stereo", track.Layout)
	assert.Equal(t, []string{"FL", "FR"}, track.Channels)
	assert.False(t, track.LayoutGuessed)
	assert.Equal(t, media.Seconds(0.5), track.Start)
	assert.Equal(t, media.Seconds(0.25), track.Offset)
	assert.InDelta(t, -23, track.Loudness.Integrated, 0.5)
	assert.True(t, track.Compliance.OK(), "the default target is EBU R 128")
	assert.Len(t, track.Defects.Channels, 2)
	assert.Equal(t, "FR", track.Channel(1))
	assert.Empty(t, track.Channel(2))
	assert.Empty(t, track.Channel(-1))
}

func TestAnalyzerTarget(
	t *testing.T,
) {
	streaming, err := loudness.ParseTarget(loudness.TargetStreaming)
	require.NoError(t, err)

	a := NewAnalyzer(media.AudioStream{SampleRate: audiotest.Rate, Channels: 2}, Options{Target: streaming})
	require.NoError(t, a.Add(audiotest.Programme(audiotest.Rate, 5, 2)))

	c := a.Result(0).Compliance
	assert.False(t, c.Loudness)
	assert.InDelta(t, -7, c.Deviation, 0.5)
}

func TestAnalyzerErrors(
	t *testing.T,
) {
	a := NewAnalyzer(media.AudioStream{Index: 1, SampleRate: audiotest.Rate, Channels: 2}, Options{})
	err := a.Add([][]float32{{0}})
	require.ErrorIs(t, err, loudness.ErrChannels)
	require.ErrorIs(t, err, defect.ErrChannels)
}
