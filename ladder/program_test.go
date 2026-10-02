package ladder

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

// programLab is a lab holding a program: the source and other videos,
// analysed, named episode-N.mov.
func programLab(
	seconds ...float64,
) (*fakeLab, []string) {
	source := analysedSource(seconds[0])
	source.Info.Path = sourcePath

	lab := newFakeLab(rateModel{}, source)
	lab.others = map[string]*analysis.Report{}

	paths := []string{sourcePath}

	for i, s := range seconds[1:] {
		name := "episode-" + string(rune('2'+i)) + ".mov"
		report := analysedSource(s)
		report.Info.Path = filepath.Join("/titles", name)
		lab.others[name] = report

		paths = append(paths, report.Info.Path)
	}

	return lab, paths
}

func TestBuildProgram(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		seconds []float64
		opts    Options
		// wantSegments is the number of segments of each video.
		wantSegments []int
		wantSampling DigestSampling
		// wantAnalyses counts the inspections and the frame analyses.
		wantAnalyses int
	}{
		{
			name:         "three episodes: seven segments each, each balanced on its own",
			seconds:      []float64{600, 600, 600},
			opts:         Options{Codec: "h264"},
			wantSegments: []int{7, 7, 7},
			wantSampling: DigestBalanced,
			wantAnalyses: 6,
		},
		{
			name:         "two episodes share the twenty segments",
			seconds:      []float64{600, 1800},
			opts:         Options{Codec: "h264"},
			wantSegments: []int{10, 10},
			wantSampling: DigestBalanced,
			wantAnalyses: 4,
		},
		{
			name:         "six episodes: four segments each at least",
			seconds:      []float64{600, 600, 600, 600, 600, 600},
			opts:         Options{Codec: "h264", SkipVerify: true},
			wantSegments: []int{4, 4, 4, 4, 4, 4},
			wantSampling: DigestBalanced,
			wantAnalyses: 12,
		},
		{
			name:         "the digest length asked is shared between the episodes",
			seconds:      []float64{600, 600, 600, 600},
			opts:         Options{Codec: "h264", SkipVerify: true, DigestDuration: media.Seconds(40)},
			wantSegments: []int{5, 5, 5, 5},
			wantSampling: DigestBalanced,
			wantAnalyses: 8,
		},
		{
			name:         "a digest too short for the episodes still takes a segment of each",
			seconds:      []float64{600, 600, 600},
			opts:         Options{Codec: "h264", SkipVerify: true, DigestDuration: media.Seconds(4)},
			wantSegments: []int{1, 1, 1},
			wantSampling: DigestBalanced,
			wantAnalyses: 6,
		},
		{
			name:         "evenly spaced segments need no analysis",
			seconds:      []float64{600, 600},
			opts:         Options{Codec: "h264", SkipVerify: true, DigestSampling: DigestUniform},
			wantSegments: []int{10, 10},
			wantSampling: DigestUniform,
			wantAnalyses: 2,
		},
		{
			name:         "the most complex scenes of each episode",
			seconds:      []float64{600, 600},
			opts:         Options{Codec: "h264", SkipVerify: true, DigestSampling: DigestTop},
			wantSegments: []int{10, 10},
			wantSampling: DigestTop,
			wantAnalyses: 4,
		},
		{
			name:         "a short episode is used whole and not analysed",
			seconds:      []float64{600, 10},
			opts:         Options{Codec: "h264", SkipVerify: true},
			wantSegments: []int{10, 1},
			wantSampling: DigestBalanced,
			wantAnalyses: 3,
		},
		{
			name:         "episodes used whole are neither analysed nor sampled",
			seconds:      []float64{10, 12},
			opts:         Options{Codec: "h264", SkipVerify: true},
			wantSegments: []int{1, 1},
			wantAnalyses: 2,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab, paths := programLab(testCase.seconds...)
			progress := &progressLog{}
			testCase.opts.Progress = progress.record

			res, err := labEngine(lab).BuildProgram(t.Context(), paths, testCase.opts)
			require.NoError(t, err)

			require.Len(t, res.Sources, len(paths))
			assert.Same(t, res.Source, res.Sources[0])
			assert.Len(t, lab.analyses, testCase.wantAnalyses)
			assert.Equal(t, testCase.wantSampling, res.Digest.Sampling)
			assert.False(t, progress.overlap.Load())

			// The digest is cut video after video, each from its own file.
			require.Len(t, lab.digests, 1)
			require.Len(t, lab.digests[0].Parts, len(paths))
			require.Len(t, res.Digest.Titles, len(paths))

			at := 0

			var total media.Duration

			for i, title := range res.Digest.Titles {
				assert.Equal(t, paths[i], title.Source)
				assert.Equal(t, testCase.wantSegments[i], title.Segments, "video %d", i)
				assert.Equal(t, paths[i], lab.digests[0].Parts[i].Source)
				assert.Equal(t, res.Digest.Segments[at:at+title.Segments], lab.digests[0].Parts[i].Segments)

				for _, s := range lab.digests[0].Parts[i].Segments {
					assert.LessOrEqual(t, s.End, media.Seconds(testCase.seconds[i]), "inside its own video")
				}

				at += title.Segments
				total += title.Duration
			}

			assert.Len(t, res.Digest.Segments, at)
			assert.Equal(t, total, res.Digest.Duration)
			assert.NotEmpty(t, res.Rungs)

			if testCase.opts.SkipVerify {
				return
			}

			for i, r := range res.Rungs {
				require.NotNil(t, r.Measured, "rung %d", i)
				require.Len(t, r.Measured.Titles, len(paths), "rung %d is read video by video", i)
			}
		})
	}
}

func TestBuildProgramTitles(
	t *testing.T,
) {
	lab, paths := programLab(600, 600, 600)
	// The second episode is harder than the model, the third easier.
	lab.hardness = []float64{1, 1.5, 0.8}
	lab.sampledBias = -0.5

	res, err := labEngine(lab).BuildProgram(t.Context(), paths, Options{Codec: "h264"})
	require.NoError(t, err)

	require.NotNil(t, res.Digest.Complexity)
	assert.InDelta(t, 8, res.Digest.Complexity.TitleTI, 1e-9, "the videos' own TI, averaged")
	assert.InDelta(t, 8, res.Digest.Complexity.TI, 0.1, "which the digest has as a whole")

	// Each video is balanced on its own: its segments have its own TI, and
	// what a rung measures on them is that video's.
	for _, title := range res.Digest.Titles {
		require.NotNil(t, title.Complexity)
		assert.InDelta(t, title.Complexity.TitleTI, title.Complexity.TI, 0.1, title.Source)
	}

	for i, r := range res.Rungs {
		titles := r.Measured.Titles

		// A harder video costs more and scores lower at the rung's settings.
		assert.Greater(t, titles[1].Bitrate, titles[0].Bitrate, "rung %d", i)
		assert.Greater(t, titles[0].Bitrate, titles[2].Bitrate, "rung %d", i)
		assert.Less(t, titles[1].VMAF, titles[0].VMAF, "rung %d", i)
		assert.InDelta(t, hardnessPenalty*0.5, titles[0].VMAF-titles[1].VMAF, 1e-6, "rung %d", i)

		for _, title := range titles {
			assert.Positive(t, title.ScoredFrames)
		}

		// The rung is the mean of its videos, which weigh the same.
		mean := (titles[0].VMAF + titles[1].VMAF + titles[2].VMAF) / 3
		assert.InDelta(t, r.Measured.VMAF, mean, 0.5, "rung %d", i)
	}

	// The top rung is scored on every frame of every video.
	assert.Equal(t, 7*50, res.Rungs[0].Measured.Titles[0].ScoredFrames)

	// Sampled rungs are levelled video by video as they are as a whole:
	// against the exact top rung, the level cancels the sampling bias.
	require.NotNil(t, res.Probing.Level)
	assert.InDelta(t, 0.5, res.Probing.Level.Offset, 1e-6)
}

func TestBuildProgramOfOne(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, analysedSource(600))

	res, err := labEngine(lab).BuildProgram(t.Context(), []string{sourcePath}, Options{Codec: "h264", SkipVerify: true})
	require.NoError(t, err)

	assert.Nil(t, res.Sources, "one video is the ladder of a title")
	assert.Empty(t, res.Digest.Titles)
	assert.Len(t, res.Digest.Segments, 20)
	assert.Empty(t, lab.digests[0].Parts)
}

func TestBuildProgramErrors(
	t *testing.T,
) {
	other := func(mutate func(*analysis.Report)) func(*fakeLab) {
		return func(lab *fakeLab) {
			mutate(lab.others["episode-2.mov"])
		}
	}

	testCases := []struct {
		name    string
		sources []string
		opts    Options
		mutate  func(*fakeLab)
		wantErr error
		wantMsg string
	}{
		{name: "no video", sources: []string{}, opts: Options{Codec: "h264"}, wantErr: ErrNoSource},
		{name: "per-shot rungs", opts: Options{Codec: "h264", PerShot: true}, wantErr: ErrProgramOption, wantMsg: "per-shot rungs are not supported"},
		{name: "per-shot resolution", opts: Options{Codec: "h264", PerShotResolution: true}, wantErr: ErrProgramOption},
		{name: "a source prepared for another one", opts: Options{Codec: "h264", Prepared: &Prepared{}}, wantErr: ErrPreparedSource},
		{
			name:    "a source prepared alone",
			opts:    Options{Codec: "h264", Prepared: &Prepared{source: sourcePath}},
			wantErr: ErrPreparedSource, wantMsg: "its videos are not those of the build",
		},
		{
			name:    "another resolution",
			opts:    Options{Codec: "h264"},
			mutate:  other(func(r *analysis.Report) { r.Info.Video[0].Width, r.Info.Video[0].Height = 1280, 720 }),
			wantErr: ErrProgramFormat,
			wantMsg: "/titles/source.mov is 1920×1080, /titles/episode-2.mov is not",
		},
		{
			name: "another frame rate",
			opts: Options{Codec: "h264"},
			mutate: other(func(r *analysis.Report) {
				r.Info.Video[0].AvgFrameRate = media.Rational{Num: 30000, Den: 1001}
			}),
			wantErr: ErrProgramFormat,
			wantMsg: "is 25.000 fps",
		},
		{
			name:    "another bit depth",
			opts:    Options{Codec: "h264"},
			mutate:  other(func(r *analysis.Report) { r.Info.Video[0].BitDepth = 10 }),
			wantErr: ErrProgramFormat,
			wantMsg: "is 8-bit",
		},
		{
			name:    "an HDR episode among SDR ones",
			opts:    Options{Codec: "h264"},
			mutate:  other(func(r *analysis.Report) { r.Info.Video[0].Color.Transfer = media.TransferPQ }),
			wantErr: ErrProgramFormat,
			wantMsg: "is SDR",
		},
		{
			name: "an SDR episode among HDR ones",
			opts: Options{Codec: "hevc"},
			mutate: func(lab *fakeLab) {
				lab.source.Info.Video[0].Color.Transfer = media.TransferPQ
				lab.source.Info.Video[0].BitDepth = 10
				lab.others["episode-2.mov"].Info.Video[0].BitDepth = 10
			},
			wantErr: ErrProgramFormat,
			wantMsg: "is HDR (" + media.TransferPQ + ")",
		},
		{
			name:    "an episode without video",
			opts:    Options{Codec: "h264"},
			mutate:  other(func(r *analysis.Report) { r.Info.Video = nil }),
			wantErr: ErrInvalidSource,
			wantMsg: "/titles/episode-2.mov",
		},
		{
			name:    "an episode that cannot be inspected",
			opts:    Options{Codec: "h264"},
			mutate:  func(lab *fakeLab) { lab.failOn = failing(opAnalyze, "episode-2.mov", 0) },
			wantErr: errFake,
			wantMsg: "ladder: inspect /titles/episode-2.mov",
		},
		{
			name:    "an episode that cannot be analysed",
			opts:    Options{Codec: "h264"},
			mutate:  func(lab *fakeLab) { lab.failOn = failing(opAnalyze, "episode-2.mov", 1) },
			wantErr: errFake,
			wantMsg: "ladder: analyse /titles/episode-2.mov",
		},
		{
			name:    "a digest that cannot be extracted",
			opts:    Options{Codec: "h264"},
			mutate:  func(lab *fakeLab) { lab.failOn = failing(opDigest, "digest.nut", 0) },
			wantErr: errFake,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab, paths := programLab(600, 600)
			if testCase.mutate != nil {
				testCase.mutate(lab)
			}

			if testCase.sources != nil {
				paths = testCase.sources
			}

			_, err := labEngine(lab).BuildProgram(t.Context(), paths, testCase.opts)
			require.ErrorIs(t, err, testCase.wantErr)

			if testCase.wantMsg != "" {
				assert.ErrorContains(t, err, testCase.wantMsg)
			}
		})
	}
}

func TestPlanProgramDigestWithoutFeatures(
	t *testing.T,
) {
	// The second episode's analysis has no SI and TI: the whole digest is
	// evenly spaced, and only the first episode is described.
	first, second := analysedSource(600), analysedSource(600)
	second.Frames = nil

	titles := []programTitle{
		{path: "one.mov", duration: media.Seconds(600), frames: first},
		{path: "two.mov", duration: media.Seconds(600), frames: second},
	}

	digest := planProgramDigest(titles, media.Seconds(20), Options{DigestSampling: DigestBalanced, SegmentDuration: media.Seconds(2)})

	assert.Equal(t, DigestUniform, digest.Sampling)
	assert.Len(t, digest.Segments, 20)
	assert.NotNil(t, digest.Titles[0].Complexity)
	assert.Nil(t, digest.Titles[1].Complexity)
	assert.Nil(t, digest.Complexity)
	assert.InDelta(t, 40.0/1200, digest.Share, 1e-9)

	t.Run("a title too short for its most complex scenes leaves uniform segments", func(t *testing.T) {
		// Frames end before the segments asked could all be placed.
		short := analysedSource(600)
		short.Frames.PTS, short.Frames.SI, short.Frames.TI = short.Frames.PTS[:200], short.Frames.SI[:200], short.Frames.TI[:200]

		titles := []programTitle{
			{path: "one.mov", duration: media.Seconds(600), frames: first},
			{path: "two.mov", duration: media.Seconds(600), frames: short},
		}

		for _, sampling := range []DigestSampling{DigestTop, DigestBalanced} {
			digest := planProgramDigest(titles, media.Seconds(20), Options{DigestSampling: sampling, SegmentDuration: media.Seconds(2)})
			assert.Equal(t, DigestUniform, digest.Sampling, sampling)
		}
	})
}

func TestEncodeProgram(
	t *testing.T,
) {
	lab, paths := programLab(600, 300)
	engine := labEngine(lab)

	res, err := engine.BuildProgram(t.Context(), paths, Options{Codec: "h264"})
	require.NoError(t, err)

	dir := t.TempDir()

	var all []Rendition

	totals := make([]int, len(paths))

	for i, path := range paths {
		opts := RenditionOptions{
			Dir:      filepath.Join(dir, "video-"+string(rune('1'+i))),
			Progress: func(p RenditionProgress) { totals[i] = p.Total },
		}

		renditions, err := engine.Encode(t.Context(), path, res, opts)
		require.NoError(t, err)
		require.Len(t, renditions, len(res.Rungs))

		all = append(all, renditions...)
	}

	for i, rd := range all {
		video := i / len(res.Rungs)

		assert.Equal(t, paths[video], rd.Source, "the video encoded")
		assert.Equal(t, filepath.Join("video-"+string(rune('1'+video)), filepath.Base(rd.Path)), rd.Name(), "told apart by their folder")
		assert.Equal(t, video, res.titleOf(rd.Source))
		assert.Same(t, res.Sources[video], res.inspectionOf(rd.Source))

		// The prediction of a rendition is what the rung measured on the
		// segments of its video.
		title := res.Rungs[rd.Rung].Measured.Titles[video]
		require.Positive(t, title.ScoredFrames)

		vmaf, bitrate := res.Prediction(rd)
		assert.InDelta(t, title.VMAF, vmaf, 1e-9)
		assert.InDelta(t, float64(title.Bitrate), bitrate, 1e-9)
	}

	// Each video is encoded over its own length: the second is half as long.
	assert.Equal(t, totals[0], 2*totals[1])

	// A video without a scored frame on a rung, or a rung not verified,
	// falls back on the rung's prediction; so does a video that is not of
	// the program.
	rung := res.Rungs[0]
	rd := all[0]

	res.Rungs[0].Measured.Titles[0].ScoredFrames = 0
	vmaf, bitrate := res.Prediction(rd)
	assert.InDelta(t, rung.PredictedVMAF, vmaf, 1e-9)
	assert.InDelta(t, float64(rung.Bitrate), bitrate, 1e-9)

	res.Rungs[0].Measured = nil
	vmaf, _ = res.Prediction(rd)
	assert.InDelta(t, rung.PredictedVMAF, vmaf, 1e-9)

	assert.Equal(t, -1, res.titleOf("/elsewhere/film.mov"))
	assert.Same(t, res.Source, res.inspectionOf("/elsewhere/film.mov"))
	assert.Equal(t, "01-1080p.mp4", Rendition{Path: "out/h264/01-1080p.mp4"}.Name(), "the file alone for one title")
}

func TestPrepareProgram(
	t *testing.T,
) {
	lab, paths := programLab(600, 600, 300)
	engine := labEngine(lab)

	prepared, err := engine.PrepareProgram(t.Context(), paths)
	require.NoError(t, err)

	defer prepared.Close()

	var results []*Result

	for _, codec := range []string{"h264", "av1"} {
		res, err := engine.BuildProgram(t.Context(), paths, Options{Codec: codec, SkipVerify: true, Prepared: prepared})
		require.NoError(t, err)

		results = append(results, res)
	}

	// The second codec reads what the first made: three inspections and
	// three frame analyses in all, and one digest, so that both are
	// measured on the very same frames.
	assert.Len(t, lab.analyses, 6)
	require.Len(t, lab.digests, 1)
	assert.Equal(t, results[0].Digest, results[1].Digest)

	for i := range paths {
		assert.Same(t, results[0].Sources[i], results[1].Sources[i], "inspected once")
	}

	// Another digest is extracted on the analyses made once.
	_, err = engine.BuildProgram(t.Context(), paths, Options{Codec: "hevc", SkipVerify: true, DigestSampling: DigestTop, Prepared: prepared})
	require.NoError(t, err)
	assert.Len(t, lab.analyses, 6)
	assert.Len(t, lab.digests, 2)

	// It is the program's: neither another program's nor one title's.
	_, err = engine.BuildProgram(t.Context(), paths[:2], Options{Codec: "h264", Prepared: prepared})
	require.ErrorIs(t, err, ErrPreparedSource)

	_, err = engine.Build(t.Context(), sourcePath, Options{Codec: "h264", Prepared: prepared})
	require.ErrorIs(t, err, ErrPreparedSource)

	// One video is prepared as Prepare does; none is refused.
	alone, err := engine.PrepareProgram(t.Context(), paths[:1])
	require.NoError(t, err)

	defer alone.Close()

	assert.Nil(t, alone.program)

	_, err = engine.Build(t.Context(), sourcePath, Options{Codec: "h264", SkipVerify: true, Prepared: alone})
	require.NoError(t, err)

	_, err = engine.PrepareProgram(t.Context(), nil)
	require.ErrorIs(t, err, ErrNoSource)

	broken, paths := programLab(600, 600)
	broken.failOn = failing(opAnalyze, "source.mov", 0)
	_, err = labEngine(broken).PrepareProgram(t.Context(), paths)
	require.ErrorIs(t, err, errFake)
}
