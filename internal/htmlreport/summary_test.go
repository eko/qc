package htmlreport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/crop"
	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
)

// texts are the level and text of each finding.
func texts(
	list []finding,
) [][2]string {
	var out [][2]string
	for _, f := range list {
		out = append(out, [2]string{f.Level.String(), f.Scope + f.Text})
	}

	return out
}

// cardValues are the label and value of each card.
func cardValues(
	cards []card,
) [][2]string {
	out := make([][2]string, len(cards))
	for i, c := range cards {
		out[i] = [2]string{c.Label, c.Value}
	}

	return out
}

func TestAnalysisSummary(
	t *testing.T,
) {
	troubled := func() *analysis.Report {
		r := sampleDecodedReport()
		r.Info.Video[0].FieldOrder, r.Info.Video[0].BitDepth = "tt", 10
		r.Bitstream.PeakToAverage, r.Bitstream.GOP.MaxInterval = 3.1, media.Seconds(6)
		r.Video.Crop = crop.Result{Letterbox: true, Content: crop.Rect{Width: 1920, Height: 800, Y: 140}}
		r.Video.Levels.OutOfRangeSummary.P95 = 0.05
		r.Video.Levels.Levels.Black, r.Video.Levels.Levels.White = 16, 235

		for range maxSegmentFindings {
			r.Video.Freeze.Segments = append(r.Video.Freeze.Segments, media.Interval{Start: media.Seconds(1), End: media.Seconds(2)})
		}

		return r
	}

	testCases := []struct {
		name         string
		report       func() *analysis.Report
		wantCards    [][2]string
		wantFindings [][2]string
	}{
		{
			name:   "bitstream only",
			report: sampleReport,
			wantCards: [][2]string{
				{"Duration", "0:04.00"}, {"Video", "1920×1080"}, {"Codec", "h264"}, {"Bitrate", "5.00 Mb/s"},
			},
		},
		{
			name:   "decoded, black and frozen segments",
			report: sampleDecodedReport,
			wantCards: [][2]string{
				{"Duration", "0:04.00"}, {"Video", "1920×1080"}, {"Codec", "h264"}, {"Bitrate", "5.00 Mb/s"}, {"Shots", "1"},
			},
			wantFindings: [][2]string{{levelWarn, "Black picture: 1 segment"}, {levelWarn, "Frozen picture: 1 segment"}},
		},
		{
			name:   "every issue",
			report: troubled,
			wantCards: [][2]string{
				{"Duration", "0:04.00"}, {"Video", "1920×1080"}, {"Codec", "h264"}, {"Bitrate", "5.00 Mb/s"}, {"Shots", "1"},
			},
			wantFindings: [][2]string{
				{levelWarn, "Peak bitrate is 3.1× the average (HLS recommends ≤ 2×)"},
				{levelWarn, "Keyframes up to 6.0s apart: slow seeking and long ABR segments"},
				{levelWarn, "Black picture: 1 segment"},
				{levelWarn, "Frozen picture: 13 segments (first 12 linked)"},
				{levelWarn, "Picture content is 1920×800 at +0+140 (black bars): crop before encoding"},
				{levelWarn, "5.0% of luma samples outside 16–235 on the worst frames (range not signalled, limited assumed)"},
				{levelWarn, "Interlaced source (tt)"},
			},
		},
		{
			name: "clean decoded title",
			report: func() *analysis.Report {
				r := sampleDecodedReport()
				r.Video.Black.Segments, r.Video.Freeze.Segments = nil, nil
				r.Info.Video[0].Color.Range = "tv"
				r.Video.Levels.OutOfRangeSummary.P95 = 0.05

				return r
			},
			wantCards: [][2]string{
				{"Duration", "0:04.00"}, {"Video", "1920×1080"}, {"Codec", "h264"}, {"Bitrate", "5.00 Mb/s"}, {"Shots", "1"},
			},
			wantFindings: [][2]string{
				{levelWarn, "5.0% of luma samples outside 0–0 on the worst frames"},
				{levelOK, "No black or frozen segment"},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cards, list := summary(testCase.report())

			assert.Equal(t, testCase.wantCards, cardValues(cards))
			assert.Equal(t, testCase.wantFindings, texts(list))
		})
	}

	// Segments are linked to the complexity chart, 12 at most.
	_, list := summary(troubled())
	assert.Equal(t, findings.TopicComplexity, list[3].Topic)
	assert.Len(t, list[3].Spans, maxSegmentFindings)
	assert.Equal(t, span{2, 3}, list[3].Spans[0])

	// Without a bitstream report, no bitrate card nor finding.
	r := sampleReport()
	r.Bitstream, r.Info.Video[0].FrameCount = nil, 100
	cards, _ := summary(r)
	assert.Equal(t, "100 frames", cards[0].Detail)
	assert.Len(t, cards, 3)
}

func TestComparisonSummary(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		result       func() *analysis.Comparison
		wantCards    [][2]string
		wantTones    []string
		wantFindings [][2]string
	}{
		{
			name:   "sampled with a bad frame and banding",
			result: func() *analysis.Comparison { return comparisonWithExtras(quality.ModeSampled, true) },
			wantCards: [][2]string{
				{"VMAF", "91.20"}, {"Worst frame", "72.4"}, {"Frames scored", "20 / 100"},
				{"Model", "vmaf_v0.6.1"}, {"Banding", "1 segment"},
			},
			wantTones: []string{toneGood, toneWarn, "", "", toneWarn},
			wantFindings: [][2]string{
				{levelWarn, "Worst scored frame 50: VMAF 72.4"},
				{levelWarn, "Visible banding on 2 scored frames (CAMBI peak 7.0)"},
				{levelInfo, "Min and 5th percentile come from sampled frames only: use --exact for quality gates"},
			},
		},
		{
			name: "exact, clean, fallback",
			result: func() *analysis.Comparison {
				c := comparisonWithExtras(quality.ModeExact, false)
				c.VMAF.Fallback, c.VMAF.Mean = "scored every frame", 80
				c.VMAF.Frames[1].Score = 79

				return c
			},
			wantCards: [][2]string{
				{"VMAF", "80.00"}, {"Worst frame", "79.0"}, {"Frames scored", "20 / 100"},
				{"Model", "vmaf_v0.6.1"}, {"Banding", "None visible"},
			},
			wantTones: []string{"", "", "", "", toneGood},
			wantFindings: [][2]string{
				{levelInfo, "scored every frame"},
				{levelOK, "No visible banding: CAMBI ≤ 5 on every scored frame"},
			},
		},
		{
			name: "no frame",
			result: func() *analysis.Comparison {
				c := sampleComparison(quality.ModeExact)
				c.VMAF.Frames, c.VMAF.FramesTotal = nil, 0

				return c
			},
			wantCards: [][2]string{{"VMAF", "91.20"}, {"Frames scored", "20 / 0"}, {"Model", "vmaf_v0.6.1"}},
			wantTones: []string{toneGood, "", ""},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cards, list := summary(testCase.result())

			assert.Equal(t, testCase.wantCards, cardValues(cards))
			assert.Equal(t, testCase.wantFindings, texts(list))

			tones := make([]string, len(cards))
			for i, c := range cards {
				tones[i] = c.Tone
			}

			assert.Equal(t, testCase.wantTones, tones)
		})
	}

	// The worst frame is an instant linked to the quality chart.
	_, list := summary(comparisonWithExtras(quality.ModeSampled, true))
	assert.Equal(t, []span{{2, 2}}, list[0].Spans)
	assert.Equal(t, findings.TopicQuality, list[0].Topic)
	assert.Equal(t, "0:02.000", list[0].Spans[0].Label())
}

func TestBandingFindingsCap(
	t *testing.T,
) {
	b := &quality.Banding{Threshold: 5}
	for range maxSegmentFindings + 3 {
		b.Segments = append(b.Segments, quality.BandingSegment{Frames: 1, Peak: 6})
	}

	f := comparisonFindings(&quality.Result{Banding: b})

	require.Len(t, f, maxSegmentFindings+1)
	assert.Equal(t, "3 more banded segments in the Banding section", f[maxSegmentFindings].Text)
	assert.Nil(t, f[maxSegmentFindings].Spans)
}

func TestLadderSummary(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		result       func(t *testing.T) *ladder.Result
		wantCards    [][2]string
		wantFindings [][2]string
	}{
		{
			name: "verified h264 ladder",
			result: func(t *testing.T) *ladder.Result {
				r := sampleLadder(t, "h264")
				r.Constraints = ladder.Constraints{TopVMAF: 95, Step: 6}
				r.Preset = "fast"

				return r
			},
			wantCards: [][2]string{
				{"Rungs", "2"}, {"Top rung", "5.00 Mb/s"}, {"Bottom rung", "1.00 Mb/s"}, {"Probe encodes", "4"}, {"Digest", "50.0%"},
			},
			wantFindings: [][2]string{
				{levelOK, "VMAF 94.6 at 4.90 Mb/s (1080p): target 95 reached"},
				{levelOK, "Top rung 36% lighter than Apple's static 1080p rung (7.8 Mb/s)"},
				{levelOK, "Verification: measured VMAF within 0.4 of the prediction on every rung"},
			},
		},
		{
			name: "troubled av1 ladder",
			result: func(t *testing.T) *ladder.Result {
				r := sampleLadder(t, "av1")
				r.Constraints = ladder.Constraints{TopVMAF: 97, Step: 0.5}
				r.Probing = ladder.ProbingReport{Mode: ladder.ProbingAdaptive, Rounds: 3}
				r.Rungs[1].Extrapolated, r.Rungs[1].Calibrated = true, true
				r.Rungs[1].Grain = &ladder.GrainCheck{Ratio: 0.5, Source: grain.Stats{Sigma: 4}, Output: grain.Stats{Sigma: 2}}

				return r
			},
			wantCards: [][2]string{
				{"Rungs", "2"}, {"Top rung", "5.00 Mb/s"}, {"Bottom rung", "1.00 Mb/s"}, {"Probe encodes", "4"}, {"Digest", "50.0%"},
			},
			wantFindings: [][2]string{
				{levelWarn, "Top rung at VMAF 94.6 (1080p), below the target 97"},
				{levelWarn, "Verification: measured VMAF within 0.4 of the prediction on every rung"},
				{levelWarn, "720p rung targets a quality outside the probed range of that resolution: trust the measured value"},
				{levelWarn, "720p rung: synthesised grain at 50% of the source's (σ 2.00 vs 4.00)"},
				{levelInfo, "720p rung missed its prediction: CRF corrected and re-measured"},
			},
		},
		{
			name: "no rung",
			result: func(t *testing.T) *ladder.Result {
				r := sampleLadder(t, "h264")
				r.Rungs = nil

				return r
			},
			wantCards:    [][2]string{{"Rungs", "0"}, {"Probe encodes", "4"}, {"Digest", "50.0%"}},
			wantFindings: [][2]string{{levelWarn, "No rung could be selected"}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cards, list := summary(testCase.result(t))

			assert.Equal(t, testCase.wantCards, cardValues(cards))
			assert.Equal(t, testCase.wantFindings, texts(list))
		})
	}

	unverified := sampleLadder(t, "h264")
	unverified.Rungs[0].Measured = nil

	cards, list := summary(unverified)
	assert.Equal(t, "fixed", cards[3].Detail)
	assert.NotContains(t, texts(list)[len(list)-1][1], "Verification", "nothing verified, nothing reported")
}

func TestRunSummary(
	t *testing.T,
) {
	run := &pipeline.Report{
		Analysis:   sampleDecodedReport(),
		Comparison: comparisonWithExtras(quality.ModeSampled, true),
		Ladders:    []*ladder.Result{sampleLadder(t, "h264"), {Codec: encode.Codec{Name: "av1"}}},
	}

	cards, list := summary(run)

	assert.Equal(t, [][2]string{
		{"Duration", "0:04.00"}, {"Video", "1920×1080"}, {"Codec", "h264"}, {"Bitrate", "5.00 Mb/s"}, {"Shots", "1"},
		{"VMAF", "91.20"}, {"h264 ladder", "2 rungs"}, {"av1 ladder", "0 rungs"},
	}, cardValues(cards))
	assert.Equal(t, "top 4.90 Mb/s · VMAF 94.6", cards[6].Detail, "the top rung as verified, not as planned")

	scopes := map[string]int{}
	for _, f := range list {
		scopes[f.Scope]++
	}

	assert.Equal(t, map[string]int{"Source": 2, "VMAF": 3, "h264 ladder": 3, "av1 ladder": 1}, scopes)
	assert.Equal(t, findings.Warn, list[0].Level, "warnings first")
}

// comparedLadder is a verified ladder of codec with the given (bitrate in
// kb/s, VMAF) rungs, top first.
func comparedLadder(
	t *testing.T,
	codec string,
	pairs ...float64,
) *ladder.Result {
	t.Helper()

	l := sampleLadder(t, codec)
	l.Rungs = nil

	for i := 0; i < len(pairs); i += 2 {
		l.Rungs = append(l.Rungs, ladder.Rung{
			Height: 1080, Bitrate: int64(pairs[i] * 1000), PredictedVMAF: pairs[i+1],
			Measured: &ladder.Measurement{Bitrate: int64(pairs[i] * 1000), VMAF: pairs[i+1]},
		})
	}

	return l
}

func TestCodecFindings(
	t *testing.T,
) {
	h264 := comparedLadder(t, "h264", 8000, 93, 4000, 87, 2000, 81)

	testCases := []struct {
		name      string
		ladders   []*ladder.Result
		wantLevel findings.Level
		want      string
	}{
		{
			name:      "a newer codec saving bitrate",
			ladders:   []*ladder.Result{h264, comparedLadder(t, "av1", 5600, 93, 2800, 87, 1400, 81)},
			wantLevel: findings.Info,
			want:      "av1 needs 30% less bitrate than h264 at VMAF 93.0, the highest quality both ladders reach (30% less on average from VMAF 81)",
		},
		{
			name:      "a newer codec costlier at the top",
			ladders:   []*ladder.Result{h264, comparedLadder(t, "av1", 8560, 93, 3600, 87, 1800, 81)},
			wantLevel: findings.Warn,
			want: "av1 needs 7% more bitrate than h264 at VMAF 93.0, the highest quality both ladders reach (6% less on average from VMAF 81); " +
				"7% more at VMAF 93 at worst. A newer codec is expected to need less: check the encoder preset, what the digest holds, " +
				"and the other metrics of the rungs (VMAF v1 counts chroma, which encoders weigh differently)",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			list := codecFindings(testCase.ladders)
			require.Len(t, list, 1)
			assert.Equal(t, testCase.wantLevel, list[0].Level)
			assert.Equal(t, testCase.want, list[0].Text)
		})
	}

	assert.Empty(t, codecFindings([]*ladder.Result{h264}))

	_, list := summary(&pipeline.Report{Analysis: sampleDecodedReport(), Ladders: testCases[1].ladders})
	scopes := map[string]int{}

	for _, f := range list {
		scopes[f.Scope]++
	}

	assert.Equal(t, 1, scopes["Codecs"], "the comparison is part of a run's findings")
}

func TestLadderFindingTopDigest(
	t *testing.T,
) {
	l := sampleLadder(t, "h264")
	l.Digest.Sampling = ladder.DigestTop
	l.Digest.Complexity = &ladder.DigestComplexity{TitleTI: 13.2, TI: 36.7}

	var texts []string
	for _, f := range ladderFindings(l) {
		texts = append(texts, f.Text)
	}

	assert.Contains(t, texts, "Estimated on the most complex scenes of the title (TI 36.7 for 13.2 over the title): "+
		"the bitrates are what those scenes need, not the title's average, and codecs compare as they do on those scenes")
}

func TestSpan(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   span
		want string
	}{
		{name: "instant", in: span{75.5, 75.5}, want: "1:15.500"},
		{name: "range", in: span{1, 3725.25}, want: "0:01.000 – 1:02:05.250"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.in.Label())
		})
	}

	assert.Equal(t, "1.500", span{}.Attr(1.5))
}

func TestPlural(
	t *testing.T,
) {
	assert.Equal(t, "1 note", plural(1, "note"))
	assert.Equal(t, "3 notes", plural(3, "note"))
	assert.Empty(t, bitDepthLabel(0))
	assert.Empty(t, presetLabel(""))
}

// TestFindingWordingUnknownCode checks that a finding a page does not know
// is left out rather than worded wrongly.
func TestFindingWordingUnknownCode(
	t *testing.T,
) {
	unknown := findings.Finding{Code: "teleport"}

	_, ok := analysisFinding(unknown, sampleReport())
	assert.False(t, ok)

	assert.Empty(t, comparisonFinding(unknown, sampleVMAF(quality.ModeExact)))

	_, ok = ladderFinding(unknown, sampleLadder(t, "h264"))
	assert.False(t, ok)
}
