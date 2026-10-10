package features

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/quality"
)

var errLab = errors.New("lab failure")

// lab stands for the analyser and for ffmpeg: a video of two segments of
// four frames.
type lab struct {
	frames  *analysis.FrameSeries
	gops    int
	failOn  string
	stats   []encode.StatsSpec
	scaling []encode.ScaleLossSpec
	// compared are the scores asked for; perSegment is the length of a
	// segment, and unscored makes them score no frame.
	mu         sync.Mutex
	compared   []comparison
	perSegment int
	unscored   bool
}

type comparison struct {
	reference, distorted string
	opts                 analysis.CompareOptions
}

func newLab() *lab {
	return &lab{
		gops: 2, perSegment: 4,
		frames: &analysis.FrameSeries{
			SI:          []float64{10, 10, 10, 10, 30, 30, 30, 30},
			TI:          []float64{99, 2, 2, 2, 99, 8, 8, 8},
			LumaMean:    []float64{100, 100, 100, 100, 50, 50, 50, 50},
			MotionPan:   []float64{0, -3, 3, -3, 0, 0, 0, 0},
			MotionShake: []float64{0, 1, 1, 1, 0, 0, 0, 0},
		},
	}
}

func (l *lab) Analyze(
	context.Context,
	string,
	analysis.Options,
) (*analysis.Report, error) {
	if l.failOn == "analyze" {
		return nil, errLab
	}

	return &analysis.Report{Frames: l.frames}, nil
}

func (l *lab) Compare(
	_ context.Context,
	reference, distorted string,
	opts analysis.CompareOptions,
) (*analysis.Comparison, error) {
	if l.failOn == "compare" {
		return nil, errLab
	}

	l.mu.Lock()
	l.compared = append(l.compared, comparison{reference: reference, distorted: distorted, opts: opts})
	l.mu.Unlock()

	if l.unscored {
		return &analysis.Comparison{VMAF: &quality.Result{}}, nil
	}

	// A clip in the first segment only, unless every frame is asked for:
	// the encode of the second CRF scores 20 points lower.
	base := 90.0
	if filepath.Base(distorted) == "stats-1.mp4" {
		base = 70
	}

	frames := []quality.FrameScore{{Index: 1, Score: base}, {Index: 2, Score: base - 10}}
	if opts.Quality.Exact {
		frames = append(frames, quality.FrameScore{Index: l.perSegment, Score: base - 30})
	}

	return &analysis.Comparison{VMAF: &quality.Result{Frames: frames}}, nil
}

func (l *lab) Stats(
	_ context.Context,
	spec encode.StatsSpec,
) ([][]encode.GOPStats, error) {
	if l.failOn == "stats" {
		return nil, errLab
	}

	l.stats = append(l.stats, spec)

	out := make([][]encode.GOPStats, len(spec.CRFs))
	for c := range out {
		for g := range l.gops {
			out[c] = append(out[c], encode.GOPStats{
				Frames: spec.GOP, Bits: int64(1000 * (g + 1) * (2 - c)), Texture: 800, Motion: 100,
				PSNR: 40 - 5*float64(c), Skip: 0.25 * float64(g), Intra: 0.1,
			})
		}
	}

	return out, nil
}

func (l *lab) ScaleLoss(
	_ context.Context,
	spec encode.ScaleLossSpec,
) ([][]float64, error) {
	if l.failOn == "scale" {
		return nil, errLab
	}

	l.scaling = append(l.scaling, spec)

	out := make([][]float64, len(spec.Heights))
	for h := range out {
		// One frame kept per segment: the second one untouched by the trip.
		out[h] = []float64{40 - float64(h), math.Inf(1)}
	}

	return out, nil
}

func spec() Spec {
	return Spec{Path: "digest.nut", Width: 1920, Height: 1080, Rate: 2, Frames: 4, Heights: []int{1080, 720, 720, 360}, WorkDir: "work"}
}

func TestExtract(
	t *testing.T,
) {
	l := newLab()

	// One frame in ten is kept for the scale loss: with segments of four
	// frames, the lab's two frames fall in the first segment only; segments
	// of ten frames give one each.
	s := spec()
	s.Frames = 10
	l.frames = &analysis.FrameSeries{SI: make([]float64, 20), TI: make([]float64, 20)}

	set, err := NewExtractor(l, l).Extract(t.Context(), s)
	require.NoError(t, err)
	assert.Equal(t, 2, set.Segments)
	assert.Equal(t, []float64{40, 60}, set.Values["scale_720"], "a trip that loses nothing is capped")
	assert.Equal(t, []float64{39, 60}, set.Values["scale_360"])

	require.Len(t, l.scaling, 1)
	assert.Equal(t, encode.ScaleLossSpec{Source: "digest.nut", Width: 1920, Height: 1080, Heights: []int{720, 360}, Step: 10, WorkDir: "work"}, l.scaling[0])

	require.Len(t, l.stats, 1)
	assert.Equal(t, encode.StatsSpec{
		Source: "digest.nut", Width: 640, Height: 360, Preset: "ultrafast", CRFs: []float64{23, 35}, GOP: 10, WorkDir: "work", Keep: true,
	}, l.stats[0])
}

func TestExtractScoresTheStatisticsPass(
	t *testing.T,
) {
	inspection := &analysis.Report{}

	testCases := []struct {
		name    string
		precise bool
		want    quality.Options
		// wantSecond is the VMAF of the second segment at CRF 23: scored
		// with every frame, the mean of the scored frames otherwise.
		wantSecond float64
	}{
		{name: "a ladder build scores clips of each segment", want: quality.Options{Sample: quality.Sample{PerScene: 3}}, wantSecond: 85},
		{name: "a corpus scores every frame", precise: true, want: quality.Options{Exact: true}, wantSecond: 60},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			l, s := newLab(), spec()
			s.Heights, s.Precise, s.Inspection = []int{1080}, testCase.precise, inspection

			set, err := NewExtractor(l, l).Extract(t.Context(), s)
			require.NoError(t, err)

			// Each encode of the pass against the video, inspected once.
			require.Len(t, l.compared, 2)

			for _, c := range l.compared {
				assert.Equal(t, "digest.nut", c.reference)
				assert.Contains(t, []string{filepath.Join("work", "stats-0.mp4"), filepath.Join("work", "stats-1.mp4")}, c.distorted)
				assert.Same(t, inspection, c.opts.Reference)
				assert.Equal(t, testCase.want, c.opts.Quality)
			}

			// On the logistic scale: 85 and 65 are the means of the two
			// frames scored in the first segment.
			assert.InDelta(t, link(85), set.Values["x264_23_vmaf"][0], 1e-12)
			assert.InDelta(t, link(65), set.Values["x264_35_vmaf"][0], 1e-12)
			assert.InDelta(t, link(testCase.wantSecond), set.Values["x264_23_vmaf"][1], 1e-12)
		})
	}
}

func TestExtractFrameAndEncoderFeatures(
	t *testing.T,
) {
	l := newLab()
	s := spec()
	s.Heights = []int{1080}

	set, err := NewExtractor(l, l).Extract(t.Context(), s)
	require.NoError(t, err)
	assert.Empty(t, l.scaling, "no lower resolution: no scale loss")

	assert.Equal(t, 2, set.Segments)
	assert.Equal(t, []float64{10, 30}, set.Values["si"])
	assert.Equal(t, []float64{2, 8}, set.Values["ti"], "the first frame of a segment follows another part of the title")
	assert.InDelta(t, math.Log1p(2), set.Values["ti_log"][0], 1e-12)
	assert.Equal(t, []float64{100, 50}, set.Values["luma"])
	assert.Equal(t, []float64{3, 0}, set.Values["motion_pan"], "a speed, whatever its direction")
	assert.Equal(t, []float64{1, 0}, set.Values["motion_shake"])
	assert.Equal(t, []float64{0, 0}, set.Values["motion_zoom"], "an analysis without that series")

	// 2000 bits in a GOP of four frames at two frames a second, on 640×360.
	assert.InDelta(t, math.Log(2001.0/2/(640*360)), set.Values["x264_23_bits"][0], 1e-12)
	assert.InDelta(t, math.Log(801.0/2/(640*360)), set.Values["x264_35_texture"][1], 1e-12)
	assert.Equal(t, []float64{40, 40}, set.Values["x264_23_psnr"])
	assert.Equal(t, []float64{35, 35}, set.Values["x264_35_psnr"])
	assert.Equal(t, []float64{0, 0.25}, set.Values["x264_23_skip"])

	assert.Len(t, set.Names(), 9+12+2)
	assert.IsIncreasing(t, set.Names())

	mean := set.Mean()
	assert.InDelta(t, 20, mean["si"], 1e-12)
	assert.InDelta(t, 0.125, mean["x264_35_skip"], 1e-12)
}

func TestExtractErrors(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		change func(l *lab, s *Spec)
		want   error
	}{
		{name: "no geometry", change: func(_ *lab, s *Spec) { s.Frames = 0 }, want: ErrFrames},
		{name: "analysis fails", change: func(l *lab, _ *Spec) { l.failOn = "analyze" }, want: errLab},
		{name: "no frame analysis", change: func(l *lab, _ *Spec) { l.frames = nil }, want: ErrFrames},
		{name: "shorter than a segment", change: func(_ *lab, s *Spec) { s.Frames = 9 }, want: ErrFrames},
		{name: "statistics fail", change: func(l *lab, _ *Spec) { l.failOn = "stats" }, want: errLab},
		{name: "statistics of fewer GOPs", change: func(l *lab, _ *Spec) { l.gops = 1 }, want: ErrFrames},
		{name: "scoring fails", change: func(l *lab, _ *Spec) { l.failOn = "compare" }, want: errLab},
		{name: "no frame scored", change: func(l *lab, _ *Spec) { l.unscored = true }, want: ErrFrames},
		{name: "scale loss fails", change: func(l *lab, _ *Spec) { l.failOn = "scale" }, want: errLab},
		{name: "scale loss misses a segment", change: func(_ *lab, _ *Spec) {}, want: ErrFrames},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			l, s := newLab(), spec()
			testCase.change(l, &s)

			_, err := NewExtractor(l, l).Extract(t.Context(), s)
			require.ErrorIs(t, err, testCase.want)
		})
	}
}
