package findings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/black"
	"github.com/eko/qc/analyze/crop"
	"github.com/eko/qc/analyze/freeze"
	"github.com/eko/qc/analyze/levels"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// codes are the level and code of each finding, in order.
func codes(
	list []Finding,
) [][2]string {
	var out [][2]string
	for _, f := range list {
		out = append(out, [2]string{f.Level.String(), string(f.Code)})
	}

	return out
}

func interval(
	from, to float64,
) media.Interval {
	return media.Interval{Start: media.Seconds(from), End: media.Seconds(to)}
}

func TestLevel(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		level Level
		want  string
	}{
		{name: "warning", level: Warn, want: "warn"},
		{name: "note", level: Info, want: "info"},
		{name: "passed", level: OK, want: "ok"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.level.String())
		})
	}
}

func TestSortByLevel(
	t *testing.T,
) {
	list := []Finding{
		{Level: OK, Code: "a"}, {Level: Warn, Code: "b"}, {Level: Info, Code: "c"}, {Level: Warn, Code: "d"},
	}

	SortByLevel(list)

	assert.Equal(t, [][2]string{{"warn", "b"}, {"warn", "d"}, {"info", "c"}, {"ok", "a"}}, codes(list), "stable within a level")
}

// report is a technical analysis without any issue.
func report() *analysis.Report {
	return &analysis.Report{
		Info: &media.Info{Video: []media.VideoStream{{Codec: "h264", FieldOrder: "progressive"}}},
		Bitstream: &bitstream.Report{
			PeakToAverage: 1.4,
			GOP:           bitstream.GOPStats{KeyframeCount: 10, MaxInterval: media.Seconds(2)},
		},
		Video: &analysis.VideoReport{},
	}
}

func TestAnalysis(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(r *analysis.Report)
		want   [][2]string
	}{
		{
			name:   "clean",
			mutate: func(*analysis.Report) {},
			want:   [][2]string{{"ok", string(NoBlackOrFrozen)}},
		},
		{
			name: "every issue",
			mutate: func(r *analysis.Report) {
				r.Bitstream.PeakToAverage = 3.2
				r.Bitstream.GOP.MaxInterval = media.Seconds(6)
				r.Video.Black = black.Result{Segments: []media.Interval{interval(1, 2)}}
				r.Video.Freeze = freeze.Result{Segments: []media.Interval{interval(3, 4), interval(5, 6)}}
				r.Video.Crop = crop.Result{Letterbox: true}
				r.Video.Levels = levels.Result{OutOfRangeSummary: stats.Summary{P95: 0.05}}
				r.Info.Video[0].FieldOrder = "tt"
			},
			want: [][2]string{
				{"warn", string(PeakBitrate)}, {"warn", string(KeyframeInterval)},
				{"warn", string(BlackSegments)}, {"warn", string(FrozenSegments)},
				{"warn", string(BlackBars)}, {"warn", string(LevelsOutOfRange)}, {"warn", string(Interlaced)},
			},
		},
		{
			name: "pillarbox, levels at the limit",
			mutate: func(r *analysis.Report) {
				r.Video.Crop = crop.Result{Pillarbox: true}
				r.Video.Levels = levels.Result{OutOfRangeSummary: stats.Summary{P95: MaxOutOfRange}}
			},
			want: [][2]string{{"warn", string(BlackBars)}, {"ok", string(NoBlackOrFrozen)}},
		},
		{
			name: "one keyframe, however far",
			mutate: func(r *analysis.Report) {
				r.Bitstream.GOP = bitstream.GOPStats{KeyframeCount: 1, MaxInterval: media.Seconds(60)}
			},
			want: [][2]string{{"ok", string(NoBlackOrFrozen)}},
		},
		{
			name: "inspection only, field order unknown",
			mutate: func(r *analysis.Report) {
				r.Bitstream, r.Video = nil, nil
				r.Info.Video[0].FieldOrder = ""
			},
			want: nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := report()
			testCase.mutate(r)

			assert.Equal(t, testCase.want, codes(Analysis(r)))
		})
	}
}

func TestAnalysisValues(
	t *testing.T,
) {
	r := report()
	r.Bitstream.PeakToAverage = 3.2
	r.Bitstream.GOP.MaxInterval = media.Seconds(6.5)
	r.Video.Black = black.Result{Segments: []media.Interval{interval(1, 2)}}
	r.Video.Levels = levels.Result{OutOfRangeSummary: stats.Summary{P95: 0.05}}
	r.Info.Video[0].FieldOrder = "bb"

	list := Analysis(r)

	assert.Equal(t, Finding{Level: Warn, Code: PeakBitrate, Topic: TopicBitrate, Value: 3.2, Limit: MaxPeakToAverage}, list[0])
	assert.Equal(t, Finding{Level: Warn, Code: KeyframeInterval, Topic: TopicBitrate, Value: 6.5, Limit: MaxKeyframeInterval}, list[1])
	assert.Equal(t, Finding{Level: Warn, Code: BlackSegments, Topic: TopicComplexity, Spans: []media.Interval{interval(1, 2)}}, list[2])
	assert.Equal(t, Finding{Level: Warn, Code: LevelsOutOfRange, Value: 0.05, Limit: MaxOutOfRange}, list[3])
	assert.Equal(t, Finding{Level: Warn, Code: Interlaced, Text: "bb"}, list[4])
}

// vmafResult is a sampled measurement whose worst frame is not an outlier.
func vmafResult() *quality.Result {
	return &quality.Result{
		Mode: quality.ModeSampled,
		Mean: 90,
		Frames: []quality.FrameScore{
			{Index: 0, PTS: media.Seconds(0), Score: 91},
			{Index: 25, PTS: media.Seconds(1), Score: 80},
			{Index: 50, PTS: media.Seconds(2), Score: 80},
		},
	}
}

func TestComparison(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(v *quality.Result)
		want   [][2]string
	}{
		{
			name:   "sampled",
			mutate: func(*quality.Result) {},
			want:   [][2]string{{"info", string(SampledOnly)}},
		},
		{
			name: "every finding",
			mutate: func(v *quality.Result) {
				v.Fallback = "scored every frame"
				v.Sample = &quality.SampleReport{Clamped: "raised to 4 clips"}
				v.Frames[2].Score = 70
				v.Banding = &quality.Banding{Threshold: 5, Segments: []quality.BandingSegment{{Interval: interval(1, 2)}}}
			},
			want: [][2]string{
				{"info", string(Fallback)}, {"info", string(BudgetClamped)}, {"warn", string(WorstFrame)},
				{"warn", string(Banding)}, {"info", string(SampledOnly)},
			},
		},
		{
			name: "exact, no banding, budget kept",
			mutate: func(v *quality.Result) {
				v.Mode = quality.ModeExact
				v.Sample = &quality.SampleReport{}
				v.Banding = &quality.Banding{Threshold: 5}
			},
			want: [][2]string{{"ok", string(NoBanding)}},
		},
		{
			name: "no scored frame",
			mutate: func(v *quality.Result) {
				v.Mode, v.Frames = quality.ModeExact, nil
			},
			want: nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := vmafResult()
			testCase.mutate(v)

			assert.Equal(t, testCase.want, codes(Comparison(v)))
		})
	}
}

func TestComparisonValues(
	t *testing.T,
) {
	v := vmafResult()
	v.Frames[1].Score, v.Frames[2].Score = 70, 70
	v.Banding = &quality.Banding{Threshold: 5, Segments: []quality.BandingSegment{{Interval: interval(1, 2)}, {Interval: interval(4, 5)}}}
	v.Sample = &quality.SampleReport{Clamped: "raised to 4 clips"}

	list := Comparison(v)

	assert.Equal(t, Finding{Level: Info, Code: BudgetClamped, Text: "raised to 4 clips"}, list[0])
	assert.Equal(t, Finding{
		Level: Warn, Code: WorstFrame, Topic: TopicQuality, Index: 25, Value: 70,
		Spans: []media.Interval{interval(1, 1)},
	}, list[1], "the first of the worst frames")
	assert.Equal(t, Finding{
		Level: Warn, Code: Banding, Topic: TopicBanding, Limit: 5,
		Spans: []media.Interval{interval(1, 2), interval(4, 5)},
	}, list[2])
}

// verified is a verified H.264 ladder reaching its target.
func verified() *ladder.Result {
	measured := func(vmaf, xpsnr float64) *ladder.Measurement {
		return &ladder.Measurement{VMAF: vmaf, Metrics: map[string]float64{quality.SeriesXPSNRY: xpsnr}}
	}

	return &ladder.Result{
		Codec:       encode.Codec{Name: "h264"},
		Constraints: ladder.Constraints{TopVMAF: 95, Step: 6},
		Rungs: []ladder.Rung{
			{Height: 1080, Bitrate: 3_900_000, PredictedVMAF: 94.8, Measured: measured(95, 44)},
			{Height: 720, Bitrate: 2_000_000, PredictedVMAF: 89, Measured: measured(88.5, 40)},
			{Height: 540, Bitrate: 1_000_000, PredictedVMAF: 83, Measured: measured(83.4, 37)},
		},
	}
}

func TestLadder(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(r *ladder.Result)
		want   [][2]string
	}{
		{
			name:   "verified h264",
			mutate: func(*ladder.Result) {},
			want: [][2]string{
				{"ok", string(TopVMAFReached)}, {"ok", string(LighterThanApple)}, {"ok", string(Verification)},
			},
		},
		{
			name: "every finding, the rungs' in rung order",
			mutate: func(r *ladder.Result) {
				r.Rungs[0].PredictedVMAF = 90
				r.Rungs[0].Measured.VMAF = 94
				r.Rungs[0].Bitrate = 9_000_000
				r.Rungs[0].Extrapolated = true
				r.Rungs[0].Grain = &ladder.GrainCheck{Ratio: 0.5}
				r.Rungs[1].Calibrated = true
				r.Rungs[1].Grain = &ladder.GrainCheck{Ratio: 1, OK: true}
				r.Rungs[2].Extrapolated = true
				r.Rungs[2].Measured.BandedFrames, r.Rungs[2].Measured.ScoredFrames = 10, 20
				r.Rungs[1].Measured.Metrics[quality.SeriesXPSNRY] = 36
			},
			want: [][2]string{
				{"warn", string(TopVMAFMissed)}, {"warn", string(Verification)},
				{"warn", string(Extrapolated)}, {"warn", string(GrainMismatch)},
				{"info", string(Calibrated)},
				{"warn", string(Extrapolated)},
				{"warn", string(BandedRung)}, {"warn", string(RankConflict)},
			},
		},
		{
			name: "unverified AV1",
			mutate: func(r *ladder.Result) {
				r.Codec = encode.Codec{Name: "av1"}
				for i := range r.Rungs {
					r.Rungs[i].Measured = nil
				}
			},
			want: [][2]string{{"ok", string(TopVMAFReached)}},
		},
		{
			name:   "no rung",
			mutate: func(r *ladder.Result) { r.Rungs = nil },
			want:   [][2]string{{"warn", string(NoRungs)}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := verified()
			testCase.mutate(r)

			assert.Equal(t, testCase.want, codes(Ladder(r)))
		})
	}
}

func TestLadderValues(
	t *testing.T,
) {
	r := verified()
	r.Rungs[1].Calibrated = true
	r.Rungs[2].Measured.BandedFrames, r.Rungs[2].Measured.ScoredFrames = 10, 20
	r.Rungs[1].Measured.Metrics[quality.SeriesXPSNRY] = 36

	list := Ladder(r)

	assert.Equal(t, Finding{Level: OK, Code: TopVMAFReached, Value: r.Rungs[0].Measured.VMAF, Limit: 95}, list[0], "within the tolerance of the target, as verified")
	assert.Equal(t, LighterThanApple, list[1].Code)
	assert.InDelta(t, 0.5, list[1].Value, 1e-9)
	assert.InDelta(t, AppleTopH264, list[1].Limit, 1e-9)
	assert.Equal(t, Verification, list[2].Code)
	assert.InDelta(t, 0.5, list[2].Value, 1e-9)
	assert.InDelta(t, 3, list[2].Limit, 1e-9)
	assert.Equal(t, Finding{Level: Info, Code: Calibrated, Index: 1, Limit: ladder.CalibrationTolerance}, list[3])
	assert.Equal(t, Finding{Level: Warn, Code: BandedRung, Index: 2, Value: 0.5, Limit: quality.BandingThreshold}, list[4])
	assert.Equal(t, Finding{Level: Warn, Code: RankConflict, Index: 1, Other: 2}, list[5])
}

func TestLadderRenditions(
	t *testing.T,
) {
	checked := func(vmaf, halfWidth float64) *ladder.Measurement {
		return &ladder.Measurement{VMAF: vmaf, HalfWidth: halfWidth}
	}

	testCases := []struct {
		name       string
		renditions []ladder.Rendition
		perShot    *ladder.PerShot
		want       []Finding
	}{
		{
			name: "as predicted",
			renditions: []ladder.Rendition{
				{Rung: 0, Bitrate: 4_100_000, Checked: checked(94.1, 0.4)},
				{Rung: 1, Bitrate: 1_900_000},
			},
		},
		{
			name: "quality off beyond its interval",
			renditions: []ladder.Rendition{
				{Rung: 0, Bitrate: 3_900_000, Checked: checked(92.5, 0.3)},
			},
			want: []Finding{{Level: Warn, Code: RenditionQuality, Index: 0, Other: 0, Value: 92.5 - 94.8, Limit: ladder.CalibrationTolerance}},
		},
		{
			name: "bitrate off by more than HLS allows",
			renditions: []ladder.Rendition{
				{Rung: 0, Bitrate: 3_900_000},
				{Rung: 2, Bitrate: 1_150_000},
			},
			want: []Finding{{Level: Warn, Code: RenditionBitrate, Index: 2, Other: 1, Value: 0.15, Limit: 0.10}},
		},
		{
			name:       "per-shot rendition against its pooled prediction",
			perShot:    &ladder.PerShot{PredictedVMAF: 94, Shots: []ladder.ShotAllocation{{PredictedBitrate: 3_000_000}}},
			renditions: []ladder.Rendition{{Rung: 0, PerShot: true, Bitrate: 3_600_000}},
			want:       []Finding{{Level: Warn, Code: RenditionBitrate, Index: 0, Other: 0, Value: 0.2, Limit: 0.10}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := verified()
			r.Rungs[0].PerShot = testCase.perShot
			r.Shots = []ladder.Shot{{Frames: 100}}
			r.Renditions = testCase.renditions

			var got []Finding

			for _, f := range Ladder(r) {
				if f.Code == RenditionQuality || f.Code == RenditionBitrate {
					got = append(got, f)
				}
			}

			require.Len(t, got, len(testCase.want))

			for i, want := range testCase.want {
				assert.Equal(t, want.Code, got[i].Code)
				assert.Equal(t, want.Index, got[i].Index)
				assert.Equal(t, want.Other, got[i].Other)
				assert.InDelta(t, want.Value, got[i].Value, 1e-9)
				assert.InDelta(t, want.Limit, got[i].Limit, 1e-9)
			}
		})
	}
}
