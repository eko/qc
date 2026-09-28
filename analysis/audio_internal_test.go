package analysis

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/audiotest"
	"github.com/eko/qc/media"
)

var errAudio = errors.New("audio failed")

// fakeAudioSource decodes video like fakeSource and every audio stream as
// a 5 s programme, or fails.
type fakeAudioSource struct {
	fakeSource
	err error
}

func (s fakeAudioSource) DecodeAudio(
	_ context.Context,
	req decode.AudioRequest,
	fn func([][]float32) error,
) error {
	if s.err != nil {
		return s.err
	}

	return fn(audiotest.Programme(req.SampleRate, 5, uint64(req.Stream)))
}

// withAudio is fakeVideo with three stereo tracks, the second one the
// default, starting 20 ms before the video.
func withAudio() *media.Info {
	info := fakeVideo()
	info.Video[0].StartTime = media.Seconds(0.02)

	for i := range 3 {
		info.Audio = append(info.Audio, media.AudioStream{Index: i + 1, SampleRate: audiotest.Rate, Channels: 2, ChannelLayout: "stereo", Default: i == 1})
	}

	return info
}

// audioAnalyzer analyses info with fakeAudioSource.
func audioAnalyzer(
	info *media.Info,
	source fakeAudioSource,
) *Analyzer {
	source.fakeSource = fakeSource{frames: fakeFrames}

	return New(nil, fakeProber{info: info}, fakePackets{count: fakeFrames}, source, nil)
}

func TestAnalyzeAudio(
	t *testing.T,
) {
	streaming := loudness.Targets()[3]

	testCases := []struct {
		name       string
		info       *media.Info
		opts       Options
		wantTracks []int
		wantVideo  bool
		wantTarget loudness.Target
	}{
		{name: "every track with the frame analysis", info: withAudio(), wantTracks: []int{1, 2, 3}, wantVideo: true, wantTarget: loudness.DefaultTarget()},
		{name: "skipped", info: withAudio(), opts: Options{Audio: AudioOptions{Skip: true}}, wantVideo: true},
		{name: "not with an inspection", info: withAudio(), opts: Options{SkipVideo: true}},
		{
			name: "with an inspection when asked", info: withAudio(),
			opts:       Options{SkipVideo: true, Audio: AudioOptions{WithInspection: true, Target: streaming}},
			wantTracks: []int{1, 2, 3}, wantTarget: streaming,
		},
		{name: "default track", info: withAudio(), opts: Options{Audio: AudioOptions{DefaultTrack: true}}, wantTracks: []int{2}, wantVideo: true, wantTarget: loudness.DefaultTarget()},
		{name: "chosen tracks", info: withAudio(), opts: Options{Audio: AudioOptions{Tracks: []int{2, 0, 7}}}, wantTracks: []int{1, 3}, wantVideo: true, wantTarget: loudness.DefaultTarget()},
		{name: "no audio", info: fakeVideo(), wantVideo: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			report, err := audioAnalyzer(testCase.info, fakeAudioSource{}).Analyze(t.Context(), "clip.mp4", testCase.opts)
			require.NoError(t, err)

			assert.Equal(t, testCase.wantVideo, report.Video != nil)

			if testCase.wantTracks == nil {
				assert.Nil(t, report.Audio)
				assert.NotContains(t, report.Timings, "audio")

				return
			}

			require.NotNil(t, report.Audio)
			assert.Contains(t, report.Timings, "audio")
			assert.Equal(t, testCase.wantTarget, report.Audio.Target)

			var streams []int
			for _, track := range report.Audio.Tracks {
				streams = append(streams, track.Stream)
				assert.InDelta(t, -23, track.Loudness.Integrated, 0.5)
				assert.Equal(t, media.Seconds(-0.02), track.Offset)
			}

			assert.Equal(t, testCase.wantTracks, streams)
		})
	}
}

func TestAnalyzeAudioOptions(
	t *testing.T,
) {
	// The defect options reach the detector: at -10 dBFS, most of a
	// programme is silence.
	opts := Options{Audio: AudioOptions{DefaultTrack: true, Defect: defect.Options{SilenceThreshold: -10}}}

	report, err := audioAnalyzer(withAudio(), fakeAudioSource{}).Analyze(t.Context(), "clip.mp4", opts)
	require.NoError(t, err)
	require.Len(t, report.Audio.Tracks, 1)
	assert.NotEmpty(t, report.Audio.Tracks[0].Defects.Silence)
}

func TestAnalyzeAudioErrors(
	t *testing.T,
) {
	_, err := audioAnalyzer(withAudio(), fakeAudioSource{err: errAudio}).Analyze(t.Context(), "clip.mp4", Options{})
	require.ErrorIs(t, err, errAudio)
	assert.ErrorContains(t, err, "audio")
}

func TestAnalyzeAudioWithoutDecoder(
	t *testing.T,
) {
	// fakeSource decodes no audio: the analysis leaves it out.
	report, err := world{info: withAudio()}.analyzer().Analyze(t.Context(), "clip.mp4", Options{})
	require.NoError(t, err)
	assert.Nil(t, report.Audio)
}

func TestSelectedTracks(
	t *testing.T,
) {
	info := &media.Info{Audio: []media.AudioStream{
		{Index: 1, SampleRate: 48000, Channels: 2},
		{Index: 2, Channels: 2, Default: true},
		{Index: 3, SampleRate: 48000},
	}}

	testCases := []struct {
		name string
		info *media.Info
		opts AudioOptions
		want []int
	}{
		{name: "every decodable track", info: info, want: []int{0}},
		{name: "an undecodable default track", info: info, opts: AudioOptions{DefaultTrack: true}},
		{name: "no audio, default track", info: &media.Info{}, opts: AudioOptions{DefaultTrack: true}},
		{name: "chosen", info: info, opts: AudioOptions{Tracks: []int{0, 2}}, want: []int{0}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.opts.selected(testCase.info))
		})
	}
}

func TestInspectionDropsAudio(
	t *testing.T,
) {
	r := &Report{Audio: &AudioReport{}, Video: &VideoReport{}}
	assert.Nil(t, r.inspection().Audio)
	assert.NotNil(t, r.Audio)
}
