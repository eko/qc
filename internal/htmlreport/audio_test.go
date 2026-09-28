package htmlreport

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/media"
)

// iv is [from, to) seconds.
func iv(
	from, to float64,
) media.Interval {
	return media.Interval{Start: media.Seconds(from), End: media.Seconds(to)}
}

// audioReport adds two tracks to sampleDecodedReport: a stereo one, the
// default, too loud with a clipped, a silent and an out-of-phase segment;
// a silent one.
func audioReport() *analysis.Report {
	r := sampleDecodedReport()
	r.Info.Audio = []media.AudioStream{
		{Index: 1, Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, Language: "fra", Default: true},
		{Index: 2, Codec: "pcm_s24le", SampleRate: 48000, Channels: 2},
	}

	series := loudness.Series{
		Momentary: []float64{-30, -20, -18, -60}, ShortTerm: []float64{-30, -25, -20, -22}, TruePeak: []float64{-3, 0.4, -2, loudness.Floor},
	}
	loud := audio.Track{
		Stream: 1, Layout: "stereo", Channels: []string{"FL", "FR"}, Start: media.Seconds(0.1),
		Loudness:   loudness.Result{Integrated: -17.5, Range: 4.2, TruePeak: 0.4, MaxMomentary: -18, MaxShortTerm: -20, Series: series},
		Compliance: loudness.Compliance{Deviation: 5.5},
		Defects: defect.Result{
			Duration: media.Seconds(4),
			Silence:  []media.Interval{iv(1, 3)},
			Channels: []defect.Channel{{Clipping: []media.Interval{iv(0.1, 0.2)}, ClippedSamples: 9}, {Silence: []media.Interval{iv(0.5, 0.9)}}},
			Pairs:    []defect.Pair{{Left: 0, Right: 1, Correlation: 0.4, OutOfPhase: []media.Interval{iv(2, 3)}, Series: []float64{1, 0.5, -0.6, 0}}},
			Levels:   [][]float64{{-30, -20, -20, -60}, {-31, -21, -21, -60}},
		},
	}
	silent := audio.Track{Stream: 2, Layout: "stereo", Channels: []string{"FL", "FR"}, Loudness: loudness.Result{Integrated: loudness.Floor, SamplePeak: -87.2}}

	r.Audio = &analysis.AudioReport{Target: loudness.DefaultTarget(), Tracks: []audio.Track{loud, silent}}

	return r
}

func TestRenderAnalysisAudio(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		report  *analysis.Report
		want    []string
		wantNot []string
	}{
		{
			name:   "tracks",
			report: audioReport(),
			want: []string{
				`id="s-audio-1"`, "Audio #1", "aac LC · stereo · 48 kHz · fra · default", "-17.5 LUFS ✗", "ebu: -23 ±0.5", "0.4 dBTP ✗",
				"4.2 LU", "-18.0 / -20.0 LUFS", "FL/FR correlation", "short-term (LUFS)", "momentary (LUFS)", "target -23", "±0.5 LU",
				"FL (dBFS)", "true peak over ceiling", "ceiling -1 dBTP", "FL/FR out of phase", "FL clipping", "FR silent",
				"Audio #2", "The track is silent", "Loudness", "-17.5 LUFS", "ebu target ✗",
				"Audio #1: -17.5 LUFS, 5.5 LU above the ebu target (-23 ±0.5 LUFS)",
				"Audio #1: true peak 0.4 dBTP over the -1 dBTP ceiling (1 passage)",
				"Audio #1: 1 silence", "Audio #2 is silent (peak -87.2 dBFS)",
			},
		},
		{name: "no audio analysis", report: sampleDecodedReport(), wantNot: []string{"Audio #", "Loudness"}},
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

func TestAudioCard(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(r *analysis.Report)
		want   card
	}{
		{
			name:   "the default track, off target",
			mutate: func(*analysis.Report) {},
			want:   card{Label: "Loudness", Value: "-17.5 LUFS", Detail: "TP 0.4 dBTP · ebu target ✗", Tone: toneWarn},
		},
		{
			name: "on target",
			mutate: func(r *analysis.Report) {
				r.Audio.Tracks[0].Compliance = loudness.Compliance{Loudness: true, TruePeak: true}
			},
			want: card{Label: "Loudness", Value: "-17.5 LUFS", Detail: "TP 0.4 dBTP · ebu target ✓", Tone: toneGood},
		},
		{
			name:   "a silent default track",
			mutate: func(r *analysis.Report) { r.Info.Audio[0].Default, r.Info.Audio[1].Default = false, true },
			want:   card{Label: "Loudness", Value: "silent", Detail: "audio #2", Tone: toneWarn},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := audioReport()
			testCase.mutate(r)

			got, ok := audioCard(r)
			require.True(t, ok)
			assert.Equal(t, testCase.want, got)
		})
	}

	_, ok := audioCard(sampleReport())
	assert.False(t, ok)
}

func TestAudioFinding(
	t *testing.T,
) {
	r := audioReport()
	spans := []media.Interval{iv(1, 2)}

	testCases := []struct {
		name    string
		finding findings.Finding
		want    string
	}{
		{name: "on target", finding: findings.Finding{Code: findings.LoudnessOnTarget, Value: -23, Text: "ebu"}, want: "Audio #1: -23.0 LUFS, true peak within -1 dBTP: meets the ebu target"},
		{name: "too quiet", finding: findings.Finding{Code: findings.LoudnessOffTarget, Value: -30, Limit: -23, Text: "ebu"}, want: "7.0 LU below"},
		{name: "silent", finding: findings.Finding{Code: findings.SilentTrack, Value: loudness.Floor}, want: "Audio #1 is silent (digital zero)"},
		{name: "dropout", finding: findings.Finding{Code: findings.ChannelSilence, Text: "FL", Spans: spans}, want: "Audio #1: FL silent while the others play (1 segment)"},
		{name: "phase", finding: findings.Finding{Code: findings.OutOfPhase, Text: "FL/FR", Spans: spans}, want: "Audio #1: FL/FR out of phase (1 segment)"},
		{name: "leading", finding: findings.Finding{Code: findings.LeadingSilence, Value: 2}, want: "starts with 2.0 s of silence"},
		{name: "trailing", finding: findings.Finding{Code: findings.TrailingSilence, Value: 3}, want: "ends with 3.0 s of silence"},
		{name: "muted", finding: findings.Finding{Code: findings.MutedChannel, Text: "FR"}, want: "FR is muted"},
		{name: "lfe", finding: findings.Finding{Code: findings.EmptyLFE}, want: "the LFE channel is empty"},
		{name: "centre", finding: findings.Finding{Code: findings.EmptyCentre}, want: "the centre channel is empty"},
		{name: "clipping", finding: findings.Finding{Code: findings.Clipping, Value: 9, Text: "FL", Spans: spans}, want: "9 clipped samples (FL), 1 segment"},
		{name: "dc", finding: findings.Finding{Code: findings.DCOffset, Value: 0.01, Limit: 0.00316, Text: "FL"}, want: "FL has a DC offset of 1.00% of full scale"},
		{name: "polarity", finding: findings.Finding{Code: findings.InvertedPolarity, Value: -0.9, Text: "FL/FR"}, want: "opposite polarity (correlation -0.90)"},
		{name: "mono", finding: findings.Finding{Code: findings.MonoAsStereo, Text: "FL/FR"}, want: "carry the same signal"},
		{name: "rate", finding: findings.Finding{Code: findings.AudioSampleRate, Level: findings.Info, Value: 44100}, want: "at 44.1 kHz: video and broadcast delivery use 48 kHz"},
		{name: "low rate", finding: findings.Finding{Code: findings.AudioSampleRate, Value: 16000}, want: "at 16 kHz: audio bandwidth under 8 kHz"},
		{name: "depth", finding: findings.Finding{Code: findings.AudioBitDepth, Value: 8}, want: "coded on 8 bits"},
		{name: "layout", finding: findings.Finding{Code: findings.AudioLayoutGuessed, Text: "stereo"}, want: "stereo assumed"},
		{name: "late", finding: findings.Finding{Code: findings.AudioOffset, Value: 0.25}, want: "starts 0.250 s after the video"},
		{name: "early", finding: findings.Finding{Code: findings.AudioOffset, Value: -0.25}, want: "starts 0.250 s before the video"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := audioFinding(testCase.finding, r)
			require.True(t, ok)
			assert.Contains(t, got.Text, testCase.want)
		})
	}

	_, ok := audioFinding(findings.Finding{Code: findings.PeakBitrate}, r)
	assert.False(t, ok, "not an audio finding")

	_, ok = audioFinding(findings.Finding{Code: findings.Clipping, Index: 4}, r)
	assert.False(t, ok, "no such track")
}

func TestDefectTableBounded(
	t *testing.T,
) {
	track := audioReport().Audio.Tracks[0]
	track.Defects.Silence = make([]media.Interval, maxDefectRows+5)

	tb := defectTable(&track)
	require.NotNil(t, tb)
	assert.Len(t, tb.Rows, maxDefectRows)
	assert.Len(t, tb.Spans, maxDefectRows)
	assert.Nil(t, referenceLine(nil, 1))
	assert.Equal(t, media.AudioStream{Index: 7}, audioStream(&media.Info{}, 7))
}
