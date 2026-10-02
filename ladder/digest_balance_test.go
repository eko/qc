package ladder

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

// analysedSource is the frame analysis of a 1080p25 title: shots of four
// seconds and, per frame, a flat SI and a TI that bursts during the first
// six seconds of every half minute, which evenly spaced segments miss.
func analysedSource(
	seconds float64,
) *analysis.Report {
	r := shotSource(seconds, 4)
	frames := int(seconds * 25)
	series := &analysis.FrameSeries{
		PTS: make([]media.Duration, frames), SI: make([]float64, frames), TI: make([]float64, frames),
	}

	for i := range frames {
		series.PTS[i] = media.Seconds(float64(i) / 25)
		series.SI[i] = 40

		series.TI[i] = 4
		if i%750 < 150 {
			series.TI[i] = 24
		}
	}

	r.Frames = series

	return r
}

func TestParseDigestSampling(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		in      string
		want    DigestSampling
		wantErr bool
	}{
		{name: "default"},
		{name: "balanced", in: "balanced", want: DigestBalanced},
		{name: "uniform", in: "uniform", want: DigestUniform},
		{name: "top", in: "top", want: DigestTop},
		{name: "unknown", in: "random", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseDigestSampling(testCase.in)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidDigestSampling)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestPlanDigest(
	t *testing.T,
) {
	// The title's mean TI: a fifth of its frames at 24, the others at 4.
	const titleTI = 8

	mismatched := analysedSource(600)
	mismatched.Frames.SI = mismatched.Frames.SI[:10]

	// Two minutes of the video have no frame: no segment fits their slots.
	holed := analysedSource(600)
	holed.Frames.PTS = append(holed.Frames.PTS[:2500:2500], holed.Frames.PTS[5500:]...)
	holed.Frames.SI = append(holed.Frames.SI[:2500:2500], holed.Frames.SI[5500:]...)
	holed.Frames.TI = append(holed.Frames.TI[:2500:2500], holed.Frames.TI[5500:]...)

	// The analysis stopped after ten seconds: too short to hold segments,
	// and the evenly spaced ones start after it.
	started := analysedSource(600)
	started.Frames.PTS, started.Frames.SI, started.Frames.TI = started.Frames.PTS[:250], started.Frames.SI[:250], started.Frames.TI[:250]

	longerAudio := analysedSource(600)
	longerAudio.Info.Duration = media.Seconds(601)

	testCases := []struct {
		name         string
		source       *analysis.Report
		opts         Options
		wantSampling DigestSampling
		wantSegments int
		// wantTI is the mean TI of the digest; 0 without a frame analysis.
		wantTI float64
	}{
		{
			name:         "balanced by default: the digest has the title's TI",
			source:       analysedSource(600),
			wantSampling: DigestBalanced,
			wantSegments: 20,
			wantTI:       titleTI,
		},
		{
			name:         "uniform segments miss the bursts",
			source:       analysedSource(600),
			opts:         Options{DigestSampling: DigestUniform},
			wantSampling: DigestUniform,
			wantSegments: 20,
			wantTI:       4,
		},
		{
			name:         "top: every segment in a burst, one per shot of four seconds",
			source:       analysedSource(600),
			opts:         Options{DigestSampling: DigestTop},
			wantSampling: DigestTop,
			wantSegments: 20,
			wantTI:       24,
		},
		{
			name:         "top of an analysis without SI and TI is uniform",
			source:       sourceReport(1920, 1080, 8, 25, 600),
			opts:         Options{DigestSampling: DigestTop},
			wantSampling: DigestUniform,
			wantSegments: 20,
		},
		{
			name:         "balanced over a longer digest of longer segments",
			source:       analysedSource(600),
			opts:         Options{DigestDuration: media.Seconds(60), SegmentDuration: media.Seconds(4)},
			wantSampling: DigestBalanced,
			wantSegments: 15,
			wantTI:       titleTI,
		},
		{
			name:         "an inspection alone gives uniform segments",
			source:       sourceReport(1920, 1080, 8, 25, 600),
			wantSampling: DigestUniform,
			wantSegments: 20,
		},
		{
			name:         "features shorter than the frames are not read",
			source:       mismatched,
			wantSampling: DigestUniform,
			wantSegments: 20,
		},
		{
			name:         "a container outlasting the video is balanced over the video",
			source:       longerAudio,
			wantSampling: DigestBalanced,
			wantSegments: 20,
			wantTI:       titleTI,
		},
		{
			name:         "frames missing from a part of the title leave uniform segments",
			source:       holed,
			wantSampling: DigestUniform,
			wantSegments: 20,
			wantTI:       4,
		},
		{
			name:         "frames of the first seconds only describe no digest",
			source:       started,
			wantSampling: DigestUniform,
			wantSegments: 20,
		},
		{
			name:         "a short title is used whole",
			source:       analysedSource(30),
			wantSegments: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			digest, err := PlanDigest(testCase.source, testCase.opts)
			require.NoError(t, err)

			assert.Equal(t, testCase.wantSampling, digest.Sampling)
			require.Len(t, digest.Segments, testCase.wantSegments)

			var total media.Duration

			for i, s := range digest.Segments {
				total += s.Length()

				assert.GreaterOrEqual(t, s.Start, media.Duration(0))
				assert.LessOrEqual(t, s.End, media.Seconds(600))

				if i > 0 {
					assert.GreaterOrEqual(t, s.Start, digest.Segments[i-1].End)
				}
			}

			assert.Equal(t, total, digest.Duration)
			assert.InDelta(t, total.Seconds()/testCase.source.Info.Duration.Seconds(), digest.Share, 1e-9)

			if testCase.wantTI == 0 {
				assert.Nil(t, digest.Complexity)

				return
			}

			require.NotNil(t, digest.Complexity)
			assert.InDelta(t, titleTI, digest.Complexity.TitleTI, 1e-9)
			assert.InDelta(t, 40, digest.Complexity.TitleSI, 1e-9)
			assert.InDelta(t, 40, digest.Complexity.SI, 1e-9)
			assert.InDelta(t, testCase.wantTI, digest.Complexity.TI, 0.05)
		})
	}

}

func TestPlanDigestOnTheGOPGrid(
	t *testing.T,
) {
	// The GOPs of a 25 fps title: 50 frames by default, 100 at 4 s.
	testCases := []struct {
		name         string
		source       *analysis.Report
		opts         Options
		wantSampling DigestSampling
		wantGOP      int
		wantTI       float64
	}{
		{
			name:         "per-shot rungs: balanced segments start GOPs of the title",
			source:       analysedSource(600),
			opts:         Options{PerShot: true},
			wantSampling: DigestBalanced,
			wantGOP:      50,
			wantTI:       8,
		},
		{
			name:         "per-shot resolution too",
			source:       analysedSource(600),
			opts:         Options{PerShotResolution: true, DigestSampling: DigestTop},
			wantSampling: DigestTop,
			wantGOP:      50,
			wantTI:       24,
		},
		{
			name:         "the GOP asked",
			source:       analysedSource(600),
			opts:         Options{PerShot: true, GOPDuration: media.Seconds(4), SegmentDuration: media.Seconds(4)},
			wantSampling: DigestBalanced,
			wantGOP:      100,
			wantTI:       8,
		},
		{
			name:         "uniform segments move to the nearest GOP",
			source:       analysedSource(600),
			opts:         Options{PerShot: true, DigestSampling: DigestUniform},
			wantSampling: DigestUniform,
			wantGOP:      50,
			wantTI:       4,
		},
		{
			name:         "uniform segments of an inspection too",
			source:       sourceReport(1920, 1080, 8, 25, 600),
			opts:         Options{PerShot: true},
			wantSampling: DigestUniform,
			wantGOP:      50,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			digest, err := PlanDigest(testCase.source, testCase.opts)
			require.NoError(t, err)

			assert.Equal(t, testCase.wantSampling, digest.Sampling)
			require.Len(t, digest.Segments, int(digest.Duration/digest.Segments[0].Length()))

			for i, s := range digest.Segments {
				first := int(math.Round(s.Start.Seconds() * 25))
				assert.Zero(t, first%testCase.wantGOP, "segment %d starts on frame %d", i, first)

				if i > 0 {
					assert.GreaterOrEqual(t, s.Start, digest.Segments[i-1].End)
				}
			}

			if testCase.wantTI > 0 {
				require.NotNil(t, digest.Complexity)
				assert.InDelta(t, testCase.wantTI, digest.Complexity.TI, 0.05)
			}
		})
	}

	t.Run("without per-shot rungs segments start anywhere", func(t *testing.T) {
		digest, err := PlanDigest(sourceReport(1920, 1080, 8, 25, 636.12), Options{})
		require.NoError(t, err)

		first := digest.Segments[0].Start.Seconds() * 25
		assert.NotZero(t, int(math.Round(first))%50)
	})
}

func TestGOPGridSnap(
	t *testing.T,
) {
	grid := gopGrid{frames: 50, rate: 25}
	seconds := func(pairs ...float64) []media.Interval {
		var out []media.Interval
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, media.Interval{Start: media.Seconds(pairs[i]), End: media.Seconds(pairs[i+1])})
		}

		return out
	}

	testCases := []struct {
		name     string
		grid     gopGrid
		segments []media.Interval
		duration media.Duration
		want     []media.Interval
	}{
		{
			name:     "to the nearest GOP, a quarter of a frame early",
			grid:     grid,
			segments: seconds(14.9, 16.9, 46.7, 48.7),
			duration: media.Seconds(600),
			want:     seconds(13.99, 15.99, 45.99, 47.99),
		},
		{
			name:     "the first GOP starts the title",
			grid:     grid,
			segments: seconds(0.4, 2.4),
			duration: media.Seconds(600),
			want:     seconds(0, 2),
		},
		{
			name:     "no grid",
			segments: seconds(14.9, 16.9),
			duration: media.Seconds(600),
			want:     seconds(14.9, 16.9),
		},
		{
			name:     "unknown frame rate",
			grid:     gopGrid{frames: 50},
			segments: seconds(14.9, 16.9),
			duration: media.Seconds(600),
			want:     seconds(14.9, 16.9),
		},
		{
			name:     "segments the grid would overlap stay where they are",
			grid:     grid,
			segments: seconds(1.6, 2.6, 2.7, 3.7),
			duration: media.Seconds(600),
			want:     seconds(1.6, 2.6, 2.7, 3.7),
		},
		{
			name:     "segments the grid would push past the title stay where they are",
			grid:     grid,
			segments: seconds(57.2, 59.2),
			duration: media.Seconds(59.5),
			want:     seconds(57.2, 59.2),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.grid.snap(testCase.segments, testCase.duration)
			require.Len(t, got, len(testCase.want))

			for i := range got {
				assert.InDelta(t, testCase.want[i].Start.Seconds(), got[i].Start.Seconds(), 1e-6)
				assert.InDelta(t, testCase.want[i].End.Seconds(), got[i].End.Seconds(), 1e-6)
			}
		})
	}
}

func TestDigestLength(
	t *testing.T,
) {
	t.Run("40 s whatever the length of the title", func(t *testing.T) {
		digest, err := PlanDigest(analysedSource(3600), Options{})
		require.NoError(t, err)
		assert.Len(t, digest.Segments, 20)
		assert.Equal(t, media.Seconds(40), digest.Duration)
	})

	t.Run("the length asked", func(t *testing.T) {
		digest, err := PlanDigest(analysedSource(3600), Options{DigestDuration: media.Seconds(98)})
		require.NoError(t, err)
		assert.Len(t, digest.Segments, 49)
		assert.Equal(t, DigestBalanced, digest.Sampling)
	})

	t.Run("a build extracts the length asked", func(t *testing.T) {
		lab := newFakeLab(rateModel{}, analysedSource(3600))

		res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "h264", SkipVerify: true, DigestDuration: media.Seconds(80)})
		require.NoError(t, err)
		assert.Equal(t, media.Seconds(80), res.Digest.Duration)
	})
}

func TestPlanDigestErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		source  *analysis.Report
		opts    Options
		wantErr error
	}{
		{name: "unknown sampling", source: analysedSource(600), opts: Options{DigestSampling: "random"}, wantErr: ErrInvalidDigestSampling},
		{name: "no source", wantErr: ErrInvalidSource},
		{name: "source without video", source: &analysis.Report{Info: &media.Info{}}, wantErr: ErrInvalidSource},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := PlanDigest(testCase.source, testCase.opts)
			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestBuildDigestSampling(
	t *testing.T,
) {
	frameAnalysis := analysis.Options{
		Audio: analysis.AudioOptions{Skip: true},
		Video: analysis.VideoOptions{SkipMotion: true},
	}

	testCases := []struct {
		name   string
		source *analysis.Report
		opts   Options
		// wantAnalyses counts the analyses of the source: its inspection,
		// then its frame analysis when the build makes one.
		wantAnalyses int
		wantSampling DigestSampling
		wantStage    bool
	}{
		{
			name:         "the source is analysed first, then its digest balanced",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", SkipVerify: true},
			wantAnalyses: 2,
			wantSampling: DigestBalanced,
			wantStage:    true,
		},
		{
			name:         "an analysis at hand is not made again",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", SkipVerify: true, Analysis: analysedSource(600)},
			wantAnalyses: 1,
			wantSampling: DigestBalanced,
		},
		{
			name:         "the source is analysed first, then its most complex scenes taken",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", SkipVerify: true, DigestSampling: DigestTop},
			wantAnalyses: 2,
			wantSampling: DigestTop,
			wantStage:    true,
		},
		{
			name:         "a uniform digest needs no analysis",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", SkipVerify: true, DigestSampling: DigestUniform},
			wantAnalyses: 1,
			wantSampling: DigestUniform,
		},
		{
			name:         "a uniform digest is still described by the analysis at hand",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", SkipVerify: true, DigestSampling: DigestUniform, Analysis: analysedSource(600)},
			wantAnalyses: 1,
			wantSampling: DigestUniform,
		},
		{
			name:         "an inspection given as the analysis is completed",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", SkipVerify: true, Analysis: sourceReport(1920, 1080, 8, 25, 600)},
			wantAnalyses: 2,
			wantSampling: DigestBalanced,
			wantStage:    true,
		},
		{
			name:         "an analysis without SI and TI leaves uniform segments",
			source:       shotSource(600, 4),
			opts:         Options{Codec: "h264", SkipVerify: true},
			wantAnalyses: 2,
			wantSampling: DigestUniform,
			wantStage:    true,
		},
		{
			name:         "a title used whole is not analysed",
			source:       analysedSource(30),
			opts:         Options{Codec: "h264", SkipVerify: true},
			wantAnalyses: 1,
		},
		{
			name:         "per-shot rungs read the analysis at hand",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", PerShot: true, Analysis: analysedSource(600)},
			wantAnalyses: 1,
			wantSampling: DigestBalanced,
		},
		{
			name:         "per-shot rungs read the analysis the digest was balanced on",
			source:       analysedSource(600),
			opts:         Options{Codec: "h264", PerShot: true},
			wantAnalyses: 2,
			wantSampling: DigestBalanced,
			wantStage:    true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, testCase.source)
			progress := &progressLog{}
			testCase.opts.Progress = progress.record

			res, err := labEngine(lab).Build(t.Context(), sourcePath, testCase.opts)
			require.NoError(t, err)

			require.Len(t, lab.analyses, testCase.wantAnalyses)
			assert.True(t, lab.analyses[0].SkipVideo, "the build starts by inspecting the source")

			assert.Equal(t, testCase.wantSampling, res.Digest.Sampling)
			require.Len(t, lab.digests, 1)
			assert.Equal(t, res.Digest.Segments, lab.digests[0].Segments)
			assert.False(t, progress.overlap.Load())

			if !testCase.wantStage {
				assert.NotContains(t, res.Timings, StageAnalysis)
				assert.Empty(t, progress.stage(StageAnalysis))

				return
			}

			assert.Equal(t, frameAnalysis.Audio, lab.analyses[1].Audio)
			assert.Equal(t, frameAnalysis.Video, lab.analyses[1].Video)
			assert.False(t, lab.analyses[1].SkipVideo)

			assert.Contains(t, res.Timings, StageAnalysis)
			assert.Equal(t, []Progress{
				{Stage: StageAnalysis},
				{Stage: StageAnalysis, Done: 1, Total: 2},
				{Stage: StageAnalysis, Done: 2, Total: 2},
			}, progress.stage(StageAnalysis))
		})
	}
}

func TestBuildDigestSamplingErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		opts    Options
		failOn  func(op, target string, seen int) bool
		wantErr error
		wantMsg string
	}{
		{
			name:    "unknown sampling",
			opts:    Options{Codec: "h264", DigestSampling: "random"},
			wantErr: ErrInvalidDigestSampling,
			wantMsg: "ladder: invalid digest sampling",
		},
		{
			name:    "source analysis",
			opts:    Options{Codec: "h264"},
			failOn:  failing(opAnalyze, "source.mov", 1),
			wantErr: errFake,
			wantMsg: "ladder: analyse /titles/source.mov",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, analysedSource(600))
			lab.failOn = testCase.failOn

			_, err := labEngine(lab).Build(t.Context(), sourcePath, testCase.opts)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Contains(t, err.Error(), testCase.wantMsg)
			assert.Empty(t, lab.digests)
		})
	}
}

func TestBuildCancelledDuringSourceAnalysis(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})

	t.Cleanup(func() { close(release) })

	lab := newFakeLab(rateModel{}, analysedSource(600))
	// The frame analysis hangs once the build is cancelled: the build must
	// not wait for it.
	lab.failOn = func(op, target string, seen int) bool {
		if op == opAnalyze && target == "source.mov" && seen == 1 {
			cancel()
			<-release
		}

		return false
	}

	_, err := labEngine(lab).Build(ctx, sourcePath, Options{Codec: "h264"})
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "ladder: analyse /titles/source.mov")
	assert.Empty(t, lab.digests)
}
