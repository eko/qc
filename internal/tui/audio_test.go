package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/media"
)

// withAudio adds the audio analysis of the fixture's two tracks: the
// stereo one too loud and peaking, the 5.1 one silent.
func withAudio(
	r *analysis.Report,
) *analysis.Report {
	loud := audio.Track{
		Stream: 1, Layout: "stereo", Channels: []string{"FL", "FR"},
		Loudness: loudness.Result{
			Integrated: -16, Range: 6.2, TruePeak: 0.5, MaxMomentary: -10, MaxShortTerm: -12,
			Series: loudness.Series{ShortTerm: []float64{loudness.Floor, -20, -16, -12}, TruePeak: []float64{-3, 0.5, -2, -3}},
		},
		Compliance: loudness.Compliance{Deviation: 7},
		Defects: defect.Result{
			Duration: media.Seconds(60),
			Channels: []defect.Channel{{}, {}},
			Silence:  []media.Interval{{Start: media.Seconds(20), End: media.Seconds(23)}},
		},
	}
	silent := audio.Track{Stream: 2, Layout: "5.1", Channels: []string{"FL", "FR", "FC", "LFE", "BL", "BR"}, Loudness: loudness.Result{Integrated: loudness.Floor, SamplePeak: loudness.Floor}}

	r.Audio = &analysis.AudioReport{Target: loudness.DefaultTarget(), Tracks: []audio.Track{loud, silent}}

	return r
}

func TestAudioSection(
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
			report: withAudio(sampleReport()),
			want: []string{
				"Audio", "target ebu -23 LUFS ±0.5 · TP ≤ -1 dBTP",
				"#1  aac stereo 48 kHz eng", "I -16.0 LUFS ▲", "LRA 6.2 LU", "TP 0.5 dBTP ▲", "max M/S -10.0 / -12.0",
				"#2  ac3 5.1 48 kHz", "silent", "short-term",
				"audio #1: -16.0 LUFS, 7.0 LU above the ebu target (-23 ±0.5)",
				"audio #1: true peak 0.5 dBTP over the -1 dBTP ceiling (1 time(s), first at 0:00.100)",
				"audio #1: silence from 0:20.000 to 0:23.000",
				"audio #2 is silent (digital zero)",
				"audio 0s",
			},
		},
		{name: "no audio analysis", report: sampleReport(), wantNot: []string{"short-term", "target ebu"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.report.Timings = map[string]string{"audio": "0s"}
			out := strings.Join(plainLines(renderReport(t, testCase.report, 140, "")), "\n")

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, not := range testCase.wantNot {
				assert.NotContains(t, out, not)
			}
		})
	}
}

func TestLoudnessMarks(
	t *testing.T,
) {
	assert.Equal(t, "✓", plain(loudnessMark(loudness.Compliance{Loudness: true})))
	assert.Equal(t, "▲", plain(loudnessMark(loudness.Compliance{Deviation: 1})))
	assert.Equal(t, "▼", plain(loudnessMark(loudness.Compliance{Deviation: -1})))
	assert.Equal(t, "✓", plain(peakMark(loudness.Compliance{TruePeak: true})))
	assert.Equal(t, media.AudioStream{Index: 5}, streamInfo(sampleInfo("x"), 5))
}

func TestAudioLines(
	t *testing.T,
) {
	r := withAudio(sampleReport())
	span := []media.Interval{{Start: media.Seconds(1), End: media.Seconds(2)}}

	testCases := []struct {
		name    string
		finding findings.Finding
		want    string
	}{
		{name: "on target", finding: findings.Finding{Code: findings.LoudnessOnTarget, Level: findings.OK, Value: -23.1, Text: "ebu"}, want: "audio #1: -23.1 LUFS, true peak within -1 dBTP: meets the ebu target"},
		{name: "too quiet", finding: findings.Finding{Code: findings.LoudnessOffTarget, Value: -30, Limit: -23, Text: "ebu"}, want: "7.0 LU below the ebu target"},
		{name: "leading", finding: findings.Finding{Code: findings.LeadingSilence, Value: 2.5}, want: "audio #1 starts with 2.5s of silence"},
		{name: "silent", finding: findings.Finding{Code: findings.SilentTrack, Value: -87.2}, want: "audio #1 is silent (peak -87.2 dBFS)"},
		{name: "trailing", finding: findings.Finding{Code: findings.TrailingSilence, Value: 3}, want: "audio #1 ends with 3.0s of silence"},
		{name: "dropout", finding: findings.Finding{Code: findings.ChannelSilence, Text: "FL", Spans: span}, want: "audio #1: FL silent while the others play, from 0:01.000 to 0:02.000"},
		{name: "phase", finding: findings.Finding{Code: findings.OutOfPhase, Text: "FL/FR", Spans: span}, want: "audio #1: FL/FR out of phase from 0:01.000 to 0:02.000"},
		{name: "muted", finding: findings.Finding{Code: findings.MutedChannel, Text: "FR"}, want: "FR is muted"},
		{name: "lfe", finding: findings.Finding{Code: findings.EmptyLFE}, want: "the LFE channel is empty"},
		{name: "centre", finding: findings.Finding{Code: findings.EmptyCentre}, want: "the centre channel is empty"},
		{name: "clipping", finding: findings.Finding{Code: findings.Clipping, Value: 12, Text: "FL", Spans: span}, want: "12 clipped samples (FL) in 1 segment(s), first at 0:01.000"},
		{name: "clipping without spans", finding: findings.Finding{Code: findings.Clipping, Value: 12, Text: "FL"}, want: "first at –"},
		{name: "dc", finding: findings.Finding{Code: findings.DCOffset, Value: 0.01, Limit: 0.00316, Text: "FL"}, want: "FL has a DC offset of 1.00% of full scale (> 0.32%)"},
		{name: "polarity", finding: findings.Finding{Code: findings.InvertedPolarity, Value: -0.9, Text: "FL/FR"}, want: "FL/FR in opposite polarity (correlation -0.90)"},
		{name: "mono", finding: findings.Finding{Code: findings.MonoAsStereo, Text: "FL/FR"}, want: "FL/FR carry the same signal (mono as stereo)"},
		{name: "rate", finding: findings.Finding{Code: findings.AudioSampleRate, Level: findings.Info, Value: 44100}, want: "audio #1 at 44.1 kHz: video and broadcast delivery use 48 kHz"},
		{name: "low rate", finding: findings.Finding{Code: findings.AudioSampleRate, Value: 22050}, want: "at 22.05 kHz: audio bandwidth under 11.025 kHz"},
		{name: "depth", finding: findings.Finding{Code: findings.AudioBitDepth, Value: 8}, want: "coded on 8 bits"},
		{name: "layout", finding: findings.Finding{Code: findings.AudioLayoutGuessed, Text: "5.1"}, want: "does not signal its channel layout: 5.1 assumed"},
		{name: "late", finding: findings.Finding{Code: findings.AudioOffset, Value: 0.5}, want: "starts 0.500s after the video"},
		{name: "early", finding: findings.Finding{Code: findings.AudioOffset, Value: -0.02}, want: "starts 0.020s before the video"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := audioLines(testCase.finding, r)
			if assert.Len(t, lines, 1) {
				assert.Contains(t, plain(lines[0]), testCase.want)
			}
		})
	}

	assert.Nil(t, audioLines(findings.Finding{Code: findings.Clipping, Index: 5}, r), "no such track")
	assert.Nil(t, audioLines(findings.Finding{Code: findings.Clipping}, sampleReport()), "no audio analysis")
	assert.Nil(t, audioLines(findings.Finding{Code: findings.PeakBitrate}, r), "not an audio finding")
}

func TestLoudnessSummary(
	t *testing.T,
) {
	r := withAudio(sampleReport())
	assert.Contains(t, analysisSummary(r), " · -16.0 LUFS")
	assert.Contains(t, InspectionSummary(r), " · -16.0 LUFS")

	r.Audio.Tracks = r.Audio.Tracks[1:]
	assert.Contains(t, analysisSummary(r), " · silent audio")
	assert.NotContains(t, analysisSummary(sampleReport()), "LUFS")
}
