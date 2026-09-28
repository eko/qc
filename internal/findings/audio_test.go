package findings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/media"
)

// cleanTrack is a stereo track on the EBU target, without defects,
// starting 1 s into the container's timeline.
func cleanTrack() audio.Track {
	return audio.Track{
		Stream:     1,
		Layout:     "stereo",
		Channels:   []string{"FL", "FR"},
		Start:      media.Seconds(1),
		Loudness:   loudness.Result{Integrated: -23.1, TruePeak: -3, SamplePeak: -3.2},
		Compliance: loudness.Compliance{Deviation: -0.1, Loudness: true, TruePeak: true},
		Defects: defect.Result{
			Duration: media.Seconds(60),
			Channels: []defect.Channel{{}, {}},
			Pairs:    []defect.Pair{{Left: 0, Right: 1, Correlation: 0.8}},
		},
	}
}

// audioReport is report() with the tracks, described as 48 kHz streams.
func audioReport(
	tracks ...audio.Track,
) *analysis.Report {
	r := report()
	r.Audio = &analysis.AudioReport{Target: loudness.DefaultTarget(), Tracks: tracks}

	for _, t := range tracks {
		r.Info.Audio = append(r.Info.Audio, media.AudioStream{Index: t.Stream, SampleRate: 48000, Channels: len(t.Channels)})
	}

	return r
}

func TestAudio(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(tr *audio.Track)
		want   [][2]string
	}{
		{name: "on target", mutate: func(*audio.Track) {}, want: [][2]string{{"ok", string(LoudnessOnTarget)}}},
		{
			name: "too loud and peaking",
			mutate: func(tr *audio.Track) {
				tr.Loudness.Integrated, tr.Loudness.TruePeak = -16, 0.5
				tr.Compliance = loudness.Compliance{Deviation: 7}
			},
			want: [][2]string{{"warn", string(LoudnessOffTarget)}, {"warn", string(TruePeakOver)}},
		},
		{
			name: "silent",
			mutate: func(tr *audio.Track) {
				tr.Loudness.Integrated = loudness.Floor
				tr.Defects.Pairs[0].Identical = true
			},
			want: [][2]string{{"warn", string(SilentTrack)}},
		},
		{
			name: "format",
			mutate: func(tr *audio.Track) {
				tr.LayoutGuessed = true
				tr.Offset = media.Seconds(-0.5)
			},
			want: [][2]string{{"info", string(AudioLayoutGuessed)}, {"info", string(AudioOffset)}, {"ok", string(LoudnessOnTarget)}},
		},
		{
			name: "silences",
			mutate: func(tr *audio.Track) {
				tr.Defects.Silence = []media.Interval{interval(0, 2.5), interval(20, 23), interval(30, 33), interval(57, 60)}
			},
			want: [][2]string{
				{"ok", string(LoudnessOnTarget)}, {"info", string(LeadingSilence)},
				{"info", string(TrailingSilence)}, {"warn", string(AudioSilence)},
			},
		},
		{
			name: "channels",
			mutate: func(tr *audio.Track) {
				tr.Defects.Channels[0] = defect.Channel{Silence: []media.Interval{interval(5, 9)}, DC: -0.01}
				tr.Defects.Channels[1] = defect.Channel{Muted: true, ClippedSamples: 10, Clipping: []media.Interval{interval(1, 2)}}
			},
			want: [][2]string{
				{"ok", string(LoudnessOnTarget)}, {"warn", string(ChannelSilence)}, {"warn", string(DCOffset)},
				{"warn", string(MutedChannel)}, {"warn", string(Clipping)},
			},
		},
		{
			name: "inverted polarity",
			mutate: func(tr *audio.Track) {
				tr.Defects.Pairs[0] = defect.Pair{Inverted: true, OutOfPhase: []media.Interval{interval(0, 60)}}
			},
			want: [][2]string{{"ok", string(LoudnessOnTarget)}, {"warn", string(InvertedPolarity)}},
		},
		{
			name: "out of phase, mono",
			mutate: func(tr *audio.Track) {
				tr.Defects.Pairs[0] = defect.Pair{Identical: true, OutOfPhase: []media.Interval{interval(10, 20)}}
			},
			want: [][2]string{{"ok", string(LoudnessOnTarget)}, {"warn", string(OutOfPhase)}, {"info", string(MonoAsStereo)}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tr := cleanTrack()
			testCase.mutate(&tr)

			assert.Equal(t, testCase.want, codes(Audio(audioReport(tr))))
		})
	}
}

func TestAudioValues(
	t *testing.T,
) {
	tr := cleanTrack()
	tr.Loudness.TruePeak = 0.2
	tr.Loudness.Series.TruePeak = []float64{-3, 0.2, -0.5, -3, 0.1}
	tr.Compliance.TruePeak = false
	tr.Defects.Silence = []media.Interval{interval(20, 23)}
	tr.Defects.Channels[0].ClippedSamples, tr.Defects.Channels[1].ClippedSamples = 3, 4
	tr.Defects.Channels[0].Clipping = []media.Interval{interval(1, 2)}
	tr.Defects.Channels[1].Clipping = []media.Interval{interval(3, 4)}

	list := Audio(audioReport(cleanTrack(), tr))
	require.Len(t, list, 4)

	assert.Equal(t, Finding{Level: OK, Code: LoudnessOnTarget, Topic: "audio-1", Value: -23.1, Limit: -23, Text: "ebu"}, list[0])

	peak := list[1]
	assert.Equal(t, TruePeakOver, peak.Code)
	assert.Equal(t, 1, peak.Index, "the second track")
	assert.InDelta(t, 0.2, peak.Value, 1e-9)
	assert.InDelta(t, -1.0, peak.Limit, 1e-9)
	assert.Equal(t, []media.Interval{interval(1.1, 1.3), interval(1.4, 1.5)}, peak.Spans, "steps over the ceiling, on the container's timeline")

	assert.Equal(t, []media.Interval{interval(21, 24)}, list[2].Spans)

	clip := list[3]
	assert.Equal(t, Clipping, clip.Code)
	assert.InDelta(t, 7, clip.Value, 1e-9)
	assert.Equal(t, "FL, FR", clip.Text)
	assert.Equal(t, []media.Interval{interval(2, 3), interval(4, 5)}, clip.Spans)
}

func TestAudioFormat(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		stream media.AudioStream
		want   [][2]string
	}{
		{name: "48 kHz", stream: media.AudioStream{SampleRate: 48000}},
		{name: "44.1 kHz", stream: media.AudioStream{SampleRate: 44100}, want: [][2]string{{"info", string(AudioSampleRate)}}},
		{name: "22.05 kHz", stream: media.AudioStream{SampleRate: 22050}, want: [][2]string{{"warn", string(AudioSampleRate)}}},
		{name: "8-bit PCM", stream: media.AudioStream{SampleRate: 48000, BitDepth: 8}, want: [][2]string{{"warn", string(AudioBitDepth)}}},
		{name: "unknown", stream: media.AudioStream{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tr := cleanTrack()
			assert.Equal(t, testCase.want, codes(formatFindings(&tr, testCase.stream)))
		})
	}
}

func TestAudioSurround(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		channels []string
		mutate   func(ch []defect.Channel)
		want     [][2]string
	}{
		{name: "empty LFE", channels: []string{"FL", "FR", "FC", "LFE", "BL", "BR"}, mutate: func(ch []defect.Channel) { ch[3].Muted = true }, want: [][2]string{{"info", string(EmptyLFE)}}},
		{name: "empty centre", channels: []string{"FL", "FR", "FC", "LFE", "BL", "BR"}, mutate: func(ch []defect.Channel) { ch[2].Muted = true }, want: [][2]string{{"warn", string(EmptyCentre)}}},
		{
			name: "surround and centre silences are by design", channels: []string{"FL", "FR", "FC", "LFE", "BL", "BR"},
			mutate: func(ch []defect.Channel) {
				ch[2].Silence = []media.Interval{interval(1, 5)}
				ch[4].Silence = []media.Interval{interval(1, 5)}
			},
		},
		{name: "mono", channels: []string{"FC"}, mutate: func(ch []defect.Channel) { ch[0].Silence = []media.Interval{interval(1, 5)} }, want: [][2]string{{"warn", string(ChannelSilence)}}},
		{name: "unknown layout", channels: []string{"C1", "C2", "C3"}, mutate: func(ch []defect.Channel) { ch[2].Silence = []media.Interval{interval(1, 5)} }, want: [][2]string{{"warn", string(ChannelSilence)}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tr := cleanTrack()
			tr.Channels = testCase.channels
			tr.Defects.Channels = make([]defect.Channel, len(testCase.channels))
			tr.Defects.Pairs = nil
			testCase.mutate(tr.Defects.Channels)

			assert.Equal(t, testCase.want, codes(channelFindings(&tr)))
		})
	}
}

func TestAudioWithout(
	t *testing.T,
) {
	assert.Nil(t, Audio(report()))
	assert.Equal(t, media.AudioStream{Index: 9}, streamOf(&media.Info{}, 9))
	assert.Equal(t, Topic("audio-3"), AudioTopic(3))
	assert.False(t, unnamed("C"))
	assert.False(t, unnamed("FC"))
	assert.True(t, unnamed("C12"))
}

func TestAnalysisWithAudio(
	t *testing.T,
) {
	assert.Equal(t, [][2]string{{"ok", string(NoBlackOrFrozen)}, {"ok", string(LoudnessOnTarget)}}, codes(Analysis(audioReport(cleanTrack()))))
}
