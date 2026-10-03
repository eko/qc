package sample

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

const fps = 25

var errFake = errors.New("fake failure")

// fakeVideo is a video of the fake lab: seconds long, a keyframe every gop
// frames, whose spatial and temporal information grow along the video
// (SI = 10 + its second, TI = half its second), so that its end is its
// most complex part and its start the easiest.
type fakeVideo struct {
	seconds float64
	gop     int
	codec   string
	width   int
	// noFrames makes its frame analysis come back without frames, and
	// noKeys without a keyframe.
	noFrames, noKeys bool
}

// fakeLab inspects and analyses fake videos, and records the copies asked.
type fakeLab struct {
	videos map[string]fakeVideo
	// failInspect and failAnalyse fail the inspection or the frame
	// analysis of a path.
	failInspect, failAnalyse string
	copyErr, decodeErr       error
	// sampleFrames overrides the frame count of the sample read back, and
	// samplePTS its timestamps.
	sampleFrames int
	samplePTS    []media.Duration

	copied   []encode.CopySpec
	decoded  []string
	analysed []string
}

func (l *fakeLab) Analyze(
	_ context.Context,
	path string,
	opts analysis.Options,
) (*analysis.Report, error) {
	if len(l.copied) > 0 && path == l.copied[len(l.copied)-1].Destination {
		return l.sample(path)
	}

	if opts.SkipVideo && path == l.failInspect || !opts.SkipVideo && path == l.failAnalyse {
		return nil, errFake
	}

	v, ok := l.videos[filepath.Base(path)]
	if !ok {
		return nil, errFake
	}

	n := int(v.seconds * fps)
	report := &analysis.Report{
		Info: &media.Info{
			Path: path, Duration: media.Seconds(v.seconds),
			Video: []media.VideoStream{{
				Codec: v.codec, Width: v.width, Height: v.width * 9 / 16, BitDepth: 8,
				AvgFrameRate: media.Rational{Num: fps, Den: 1},
			}},
		},
		Bitstream: &bitstream.Report{PacketCount: n, Start: media.Seconds(0.08)},
	}

	if opts.SkipVideo {
		return report, nil
	}

	l.analysed = append(l.analysed, path)

	if opts.Progress != nil {
		opts.Progress(analysis.Progress{Stage: analysis.StageDecode, Done: n, Total: n})
	}

	if v.noFrames {
		return report, nil
	}

	frames := &analysis.FrameSeries{}

	for i := range n {
		second := float64(i / fps)
		frames.PTS = append(frames.PTS, media.Seconds(float64(i)/fps))
		frames.Keyframe = append(frames.Keyframe, i%v.gop == 0 && !v.noKeys)
		frames.SI = append(frames.SI, 10+second)
		frames.TI = append(frames.TI, second/2)
	}

	report.Frames = frames

	return report, nil
}

// sample is the inspection of the sample written: the frames copied, at
// regular timestamps, unless the lab is told otherwise.
func (l *fakeLab) sample(
	path string,
) (*analysis.Report, error) {
	if l.failInspect == path {
		return nil, errFake
	}

	spec := l.copied[len(l.copied)-1]
	frames := 0

	for _, part := range spec.Parts {
		for _, seg := range part.Segments {
			frames += seg.Frames
		}
	}

	if l.sampleFrames > 0 {
		frames = l.sampleFrames
	}

	pts := l.samplePTS
	if pts == nil {
		for i := range frames {
			pts = append(pts, media.Seconds(float64(i)/fps))
		}
	}

	return &analysis.Report{Info: &media.Info{Path: path}, Bitstream: &bitstream.Report{PacketCount: frames, PTS: pts}}, nil
}

func (l *fakeLab) Copy(
	_ context.Context,
	spec encode.CopySpec,
) error {
	if l.copyErr != nil {
		return l.copyErr
	}

	l.copied = append(l.copied, spec)

	if spec.Progress != nil {
		spec.Progress(spec.Segments)
	}

	return nil
}

func (l *fakeLab) Decodes(
	_ context.Context,
	path string,
) error {
	l.decoded = append(l.decoded, path)

	return l.decodeErr
}

// newLab returns a lab of three H.264 1080p videos: a film of ten
// minutes, an episode of five, and a trailer of ten seconds.
func newLab() *fakeLab {
	return &fakeLab{videos: map[string]fakeVideo{
		"film.mp4":    {seconds: 600, gop: 50, codec: "h264", width: 1920},
		"episode.mp4": {seconds: 300, gop: 100, codec: "h264", width: 1920},
		"trailer.mp4": {seconds: 10, gop: 25, codec: "h264", width: 1920},
		"hevc.mp4":    {seconds: 300, gop: 50, codec: "hevc", width: 1920},
		"small.mp4":   {seconds: 300, gop: 50, codec: "h264", width: 1280},
		"blind.mp4":   {seconds: 300, gop: 50, codec: "h264", width: 1920, noFrames: true},
		"keyless.mp4": {seconds: 300, gop: 50, codec: "h264", width: 1920, noKeys: true},
	}}
}

func seconds(
	s float64,
) media.Duration {
	return media.Seconds(s)
}

func TestExtract(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		sources []string
		opts    Options
		// wantTaken is what each video gives, in seconds; wantKinds the
		// scenes taken of the first one.
		wantTaken    []float64
		wantAnalysed int
		check        func(t *testing.T, res *Result)
	}{
		{
			name:    "defaults: a minute of mixed scenes of one video",
			sources: []string{"film.mp4"},
			// Pieces are GOPs of 2 s: 15 complex ones, 15 representative.
			wantTaken:    []float64{60},
			wantAnalysed: 1,
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, ScenesMixed, res.Scenes)
				assert.InDelta(t, DefaultTopShare, res.TopShare, 1e-12)

				kinds := map[Scenes]int{}
				for _, seg := range res.Sources[0].Segments {
					kinds[seg.Kind]++
				}

				assert.Equal(t, map[Scenes]int{ScenesTop: 15, ScenesAverage: 15}, kinds)

				// The complex scenes first, the most complex leading; then
				// the representative ones, in the order of the video.
				for i, scene := range res.Order {
					assert.Equal(t, i < 15, scene.Kind == ScenesTop, "scene %d", i)

					if i > 15 {
						assert.Greater(t, scene.Start, res.Order[i-1].Start, "scene %d", i)
					}
				}

				assert.InDelta(t, 598, res.Order[0].Start.Seconds(), 1e-9)

				c := res.Sources[0].Complexity
				require.NotNil(t, c)
				assert.Greater(t, c.SI, c.VideoSI, "busier than the video: half of it is its most complex scenes")
			},
		},
		{
			name:         "the most complex scenes are the end of the fake videos",
			sources:      []string{"film.mp4", "episode.mp4"},
			opts:         Options{Duration: seconds(40), Scenes: ScenesTop},
			wantTaken:    []float64{20, 20},
			wantAnalysed: 2,
			check: func(t *testing.T, res *Result) {
				first := res.Sources[0].Segments[0]
				assert.InDelta(t, 580, first.Start.Seconds(), 1e-9, "the last twenty seconds of the film")
				assert.Equal(t, ScenesTop, first.Kind)
				assert.Empty(t, res.TopShare, "a share of a mixed sample only")

				// The episode has GOPs of 4 s: five of them.
				assert.Len(t, res.Sources[1].Segments, 5)
				assert.InDelta(t, 280, res.Sources[1].Segments[0].Start.Seconds(), 1e-9)

				// The sample plays the most complex scene first, whatever
				// its video: the end of the film, down to the episode's.
				assert.InDelta(t, 598, res.Order[0].Start.Seconds(), 1e-9)
				assert.Equal(t, 0, res.Order[0].Source)
				assert.Equal(t, 1, res.Order[len(res.Order)-1].Source)

				for i := 1; i < len(res.Order); i++ {
					assert.LessOrEqual(t, res.Order[i].Score(), res.Order[i-1].Score(), "scene %d", i)
				}
			},
		},
		{
			name:         "the easiest scenes are their start",
			sources:      []string{"film.mp4"},
			opts:         Options{Duration: seconds(10), Scenes: ScenesEasy},
			wantTaken:    []float64{10},
			wantAnalysed: 1,
			check: func(t *testing.T, res *Result) {
				segments := res.Sources[0].Segments
				assert.InDelta(t, 0, segments[0].Start.Seconds(), 1e-9)
				assert.InDelta(t, 10, segments[len(segments)-1].End.Seconds(), 1e-9)
				assert.Equal(t, ScenesEasy, segments[0].Kind)

				// The easiest scene first.
				for i := 1; i < len(res.Order); i++ {
					assert.GreaterOrEqual(t, res.Order[i].Score(), res.Order[i-1].Score(), "scene %d", i)
				}
			},
		},
		{
			name:         "representative scenes have the video's spatial and temporal information",
			sources:      []string{"film.mp4"},
			opts:         Options{Duration: seconds(30), Scenes: ScenesAverage},
			wantTaken:    []float64{30},
			wantAnalysed: 1,
			check: func(t *testing.T, res *Result) {
				c := res.Sources[0].Complexity
				assert.InDelta(t, c.VideoSI, c.SI, 0.5)
				assert.InDelta(t, c.VideoTI, c.TI, 0.25)

				// Spread over the video: one scene in each fifteenth of it.
				for i, seg := range res.Sources[0].Segments {
					assert.InDelta(t, 20+40*float64(i), seg.Start.Seconds(), 20, "scene %d", i)
					assert.Equal(t, seg, res.Order[i].Segment, "played in the order of the video")
				}
			},
		},
		{
			name:         "a mixed sample with a third of complex scenes",
			sources:      []string{"film.mp4"},
			opts:         Options{Duration: seconds(60), TopShare: 1.0 / 3},
			wantTaken:    []float64{60},
			wantAnalysed: 1,
			check: func(t *testing.T, res *Result) {
				top := 0
				for _, seg := range res.Sources[0].Segments {
					if seg.Kind == ScenesTop {
						top++
					}
				}

				assert.Equal(t, 10, top)
			},
		},
		{
			name:         "a video shorter than its share is taken whole, without analysis",
			sources:      []string{"trailer.mp4", "film.mp4"},
			opts:         Options{Duration: seconds(40), Scenes: ScenesTop},
			wantTaken:    []float64{10, 20},
			wantAnalysed: 1,
			check: func(t *testing.T, res *Result) {
				trailer := res.Sources[0]
				assert.True(t, trailer.Whole)
				assert.Nil(t, trailer.Complexity)
				require.Len(t, trailer.Segments, 1)
				assert.Equal(t, 250, trailer.Segments[0].Frames)
				assert.Empty(t, trailer.Segments[0].Kind)

				// Without a score, it comes after the ranked scenes.
				last := res.Order[len(res.Order)-1]
				assert.Equal(t, 0, last.Source)
				assert.Equal(t, 250, last.Frames)
			},
		},
		{
			name:         "scenes at least four seconds long",
			sources:      []string{"film.mp4"},
			opts:         Options{Duration: seconds(20), Scenes: ScenesTop, Piece: seconds(4)},
			wantTaken:    []float64{20},
			wantAnalysed: 1,
			check: func(t *testing.T, res *Result) {
				assert.Len(t, res.Sources[0].Segments, 5, "two GOPs of two seconds each")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newLab()

			var stages []string

			testCase.opts.Progress = func(p Progress) {
				if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
					stages = append(stages, p.Stage)
				}
			}

			res, err := NewEngine(lab, lab).Extract(t.Context(), testCase.sources, "out/sample.mkv", testCase.opts)
			require.NoError(t, err)

			require.Len(t, res.Sources, len(testCase.sources))
			assert.Len(t, lab.analysed, testCase.wantAnalysed)

			frames, taken := 0, 0.0

			for i, s := range res.Sources {
				assert.Equal(t, testCase.sources[i], s.Path)
				assert.InDelta(t, testCase.wantTaken[i], s.Taken.Seconds(), 1e-6, s.Path)

				// The scenes follow each other in the video, without overlap.
				for j := 1; j < len(s.Segments); j++ {
					assert.GreaterOrEqual(t, s.Segments[j].Start, s.Segments[j-1].End, "%s scene %d", s.Path, j)
				}

				frames += s.Frames
				taken += s.Taken.Seconds()
			}

			assert.Equal(t, frames, res.Frames)
			assert.InDelta(t, taken, res.Duration.Seconds(), 1e-6)
			assert.Positive(t, res.Elapsed)
			assert.Equal(t, "out/sample.mkv", res.Path)

			// What was planned is what was copied, and the copy was read back.
			require.Len(t, lab.copied, 1)
			spec := lab.copied[0]
			assert.Equal(t, "out/sample.mkv", spec.Destination)
			assert.Equal(t, "h264", spec.Codec)
			assert.InDelta(t, fps, spec.Rate.Float(), 1e-12)

			// One part per scene, in the order of the sample.
			require.Len(t, spec.Parts, len(res.Order))
			assert.Equal(t, len(res.Order), spec.Segments)

			scenes := 0
			for _, source := range res.Sources {
				scenes += len(source.Segments)
			}

			assert.Len(t, res.Order, scenes, "every scene of every video, once")

			for i, part := range spec.Parts {
				scene := res.Order[i]
				assert.Equal(t, testCase.sources[scene.Source], part.Source)
				assert.InDelta(t, 0.08, part.Origin.Seconds(), 1e-12, "seeked from the first frame of the video")
				assert.Equal(t, []encode.CopySegment{{Start: scene.Start, Frames: scene.Frames, Duration: scene.Length()}}, part.Segments)
			}

			assert.Equal(t, Check{Frames: res.Frames, Decoded: true}, res.Check)
			assert.True(t, res.Check.OK())
			assert.Equal(t, []string{"out/sample.mkv"}, lab.decoded)
			assert.Equal(t, StageInspect, stages[0])
			assert.Equal(t, []string{StageExtract, StageVerify}, stages[len(stages)-2:])

			testCase.check(t, res)
		})
	}
}

func TestExtractErrors(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		sources     []string
		destination string
		opts        Options
		mutate      func(lab *fakeLab)
		wantErr     error
		wantMsg     string
	}{
		{name: "no source", wantErr: ErrNoSource},
		{name: "a negative duration", sources: []string{"film.mp4"}, opts: Options{Duration: -1}, wantErr: ErrOptions},
		{name: "a negative piece", sources: []string{"film.mp4"}, opts: Options{Piece: -1}, wantErr: ErrOptions},
		{name: "unknown scenes", sources: []string{"film.mp4"}, opts: Options{Scenes: "hard"}, wantErr: ErrOptions, wantMsg: `unknown scenes "hard"`},
		{name: "a share of top scenes of one", sources: []string{"film.mp4"}, opts: Options{TopShare: 1}, wantErr: ErrOptions},
		{name: "a negative share", sources: []string{"film.mp4"}, opts: Options{TopShare: -0.5}, wantErr: ErrOptions},
		{name: "written over a source", sources: []string{"film.mp4", "./episode.mp4"}, destination: "episode.mp4", wantErr: ErrDestination},
		{
			name: "a source that cannot be inspected", sources: []string{"film.mp4", "episode.mp4"},
			mutate:  func(lab *fakeLab) { lab.failInspect = "episode.mp4" },
			wantErr: errFake, wantMsg: "sample: inspect episode.mp4",
		},
		{
			name: "a source without video", sources: []string{"film.mp4"},
			mutate:  func(lab *fakeLab) { lab.videos["film.mp4"] = fakeVideo{seconds: 10, gop: 25, codec: "h264"} },
			wantErr: ErrInvalidSource, wantMsg: "(film.mp4)",
		},
		{
			name: "another codec", sources: []string{"film.mp4", "hevc.mp4"},
			wantErr: ErrFormat, wantMsg: "film.mp4 is h264, hevc.mp4 is not",
		},
		{
			name: "another resolution", sources: []string{"film.mp4", "small.mp4"},
			wantErr: ErrFormat, wantMsg: "film.mp4 is 1920×1080, small.mp4 is not",
		},
		{
			name: "a source that cannot be analysed", sources: []string{"film.mp4"},
			mutate:  func(lab *fakeLab) { lab.failAnalyse = "film.mp4" },
			wantErr: errFake, wantMsg: "sample: analyse film.mp4",
		},
		{name: "an analysis without frames", sources: []string{"blind.mp4"}, wantErr: ErrInvalidSource, wantMsg: "no frame analysis"},
		{name: "a video without a keyframe", sources: []string{"keyless.mp4"}, wantErr: ErrNoKeyframe},
		{
			name: "the copy fails", sources: []string{"film.mp4"},
			mutate:  func(lab *fakeLab) { lab.copyErr = errFake },
			wantErr: errFake,
		},
		{
			name: "the sample cannot be read back", sources: []string{"film.mp4"},
			mutate:  func(lab *fakeLab) { lab.failInspect = "sample.mkv" },
			wantErr: errFake, wantMsg: "read sample.mkv back",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newLab()
			if testCase.mutate != nil {
				testCase.mutate(lab)
			}

			destination := testCase.destination
			if destination == "" {
				destination = "sample.mkv"
			}

			res, err := NewEngine(lab, lab).Extract(t.Context(), testCase.sources, destination, testCase.opts)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Nil(t, res)

			if testCase.wantMsg != "" {
				assert.ErrorContains(t, err, testCase.wantMsg)
			}
		})
	}
}

func TestExtractCheck(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		mutate   func(lab *fakeLab)
		wantNote string
		// wantDecoded tells whether the decode was reached.
		wantDecoded bool
	}{
		{
			name:     "frames lost: open GOPs",
			mutate:   func(lab *fakeLab) { lab.sampleFrames = 498 },
			wantNote: "the sample holds 498 frames where its scenes have 500: the sources may have open GOPs",
		},
		{
			name: "two frames at one timestamp",
			mutate: func(lab *fakeLab) {
				for i := range 500 {
					lab.samplePTS = append(lab.samplePTS, media.Seconds(float64(i)/fps))
				}

				lab.samplePTS[100] = lab.samplePTS[99]
			},
			wantNote: "two frames of the sample are shown at 3.960 s",
		},
		{
			name:        "a frame that does not decode",
			mutate:      func(lab *fakeLab) { lab.decodeErr = errFake },
			wantNote:    "the sample does not decode cleanly: fake failure",
			wantDecoded: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newLab()
			testCase.mutate(lab)

			// What is wrong with the file is told, not returned: it is there.
			res, err := NewEngine(lab, lab).Extract(t.Context(), []string{"film.mp4"}, "sample.mkv", Options{Duration: seconds(20), Scenes: ScenesTop})
			require.NoError(t, err)

			assert.False(t, res.Check.OK())
			assert.False(t, res.Check.Decoded)
			assert.Contains(t, res.Check.Note, testCase.wantNote)
			assert.Equal(t, testCase.wantDecoded, len(lab.decoded) > 0)
		})
	}
}

func TestOptionsDefaults(
	t *testing.T,
) {
	opts, err := Options{}.withDefaults()
	require.NoError(t, err)
	assert.Equal(t, Options{Duration: 60 * media.Duration(time.Second), Scenes: ScenesMixed, TopShare: 0.5, Piece: 2 * media.Duration(time.Second)}, opts)
	assert.Equal(t, []Scenes{ScenesTop, ScenesMixed, ScenesAverage, ScenesEasy}, AllScenes())

	// Without a callback, progress goes nowhere.
	opts.report(Progress{Stage: StageInspect})
}

func TestUsableVideo(
	t *testing.T,
) {
	video := media.VideoStream{Width: 1920, Height: 1080, AvgFrameRate: media.Rational{Num: 25, Den: 1}}

	testCases := []struct {
		name    string
		report  *analysis.Report
		wantErr bool
	}{
		{name: "nil", wantErr: true},
		{name: "no info", report: &analysis.Report{}, wantErr: true},
		{name: "no video", report: &analysis.Report{Info: &media.Info{Duration: seconds(10)}}, wantErr: true},
		{name: "no duration", report: &analysis.Report{Info: &media.Info{Video: []media.VideoStream{video}}}, wantErr: true},
		{name: "usable", report: &analysis.Report{Info: &media.Info{Duration: seconds(10), Video: []media.VideoStream{video}}}},
		{
			name: "the frame rate of the stream when the average is unknown",
			report: &analysis.Report{Info: &media.Info{Duration: seconds(10), Video: []media.VideoStream{{
				Width: 1920, Height: 1080, FrameRate: media.Rational{Num: 25, Den: 1},
			}}}},
		},
		{
			name: "the duration of the stream when the file's is unknown",
			report: &analysis.Report{Info: &media.Info{Video: []media.VideoStream{{
				Width: 1920, Height: 1080, AvgFrameRate: media.Rational{Num: 25, Den: 1}, Duration: seconds(8),
			}}}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, duration, err := usableVideo(testCase.report)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidSource)

				return
			}

			require.NoError(t, err)
			assert.InDelta(t, 25, got.AvgFrameRate.Float(), 1e-12)
			assert.Positive(t, duration)
		})
	}
}
