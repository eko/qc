// Package corpus measures what a ladder model is trained on (package
// predict): an excerpt of a title, cut into segments of one GOP, encoded
// on a grid of resolutions and CRFs with every frame scored, so that each
// segment gets its own bitrate and VMAF at every point of the grid.
package corpus

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// Errors of a corpus measurement.
var (
	// ErrSource is returned for a source without a usable video stream,
	// or too short to give a segment.
	ErrSource = errors.New("source cannot give a corpus")
	// ErrFrames is returned when an encode of the excerpt does not hold
	// the frames of its segments.
	ErrFrames = errors.New("encode does not match the segments of the excerpt")
)

// Defaults of Options.
const (
	// DefaultSegments is the number of segments of an excerpt: two
	// minutes of a title, taken evenly across it.
	DefaultSegments = 60
	// segmentSeconds is the length of a segment, and of the GOPs of the
	// encodes: those of a ladder.
	segmentSeconds = 2
)

// Stages reported through Progress.
const (
	StageExcerpt  = "excerpt"
	StageFeatures = "features"
	StageGrid     = "grid"
)

// Progress reports the advancement of a measurement: Done of Total encodes
// of the grid, the ones an earlier run measured included.
type Progress struct {
	Stage string
	Done  int
	Total int
}

// Options configures a measurement. The zero value is valid except Codec.
type Options struct {
	// Codec is h264, hevc or av1; Preset its preset (default: the one of
	// the ladders of the codec).
	Codec  string
	Preset string
	// Heights are the resolutions of the grid (default
	// ladder.DefaultHeights), those above the source left out.
	Heights []int
	// CRFs are the CRFs of the grid (default GridCRFs of the codec).
	CRFs []float64
	// Segments is the number of segments of the excerpt (default
	// DefaultSegments); a shorter title gives the segments it holds.
	Segments int
	// WorkDir holds the excerpt (default: a new temporary directory).
	WorkDir string
	// Measured tells which encodes an earlier run already measured: they
	// are not measured again.
	Measured func(row predict.Row) bool
	// Progress, when set, is called as the measurement advances.
	Progress func(Progress)
}

// Builder measures corpora.
type Builder struct {
	inspector ladder.Inspector
	encoder   ladder.Encoder
	digester  ladder.Digester
	// featurer is optional: without it a corpus holds no features, and a
	// model trained on it predicts from measured anchors only.
	featurer ladder.Featurer
}

// Option configures a Builder.
type Option func(*Builder)

// WithFeatures makes the corpus hold the features of every title, which a
// model needs to predict from the analysis of a video.
func WithFeatures(
	featurer ladder.Featurer,
) Option {
	return func(b *Builder) {
		b.featurer = featurer
	}
}

// NewBuilder returns a Builder measuring with inspector, encoding with
// encoder and cutting excerpts with digester: the ports of a ladder
// engine.
func NewBuilder(
	inspector ladder.Inspector,
	encoder ladder.Encoder,
	digester ladder.Digester,
	opts ...Option,
) *Builder {
	b := &Builder{inspector: inspector, encoder: encoder, digester: digester}
	for _, opt := range opts {
		opt(b)
	}

	return b
}

// GridCRFs are the default CRFs of the grid of codec, half a probe step
// apart: from one probe step beyond its best probe CRF, where ladders
// extend their curves, to two beyond its worst, which the low rungs of
// content that compresses well reach (a reality show needed 1080p at CRF
// 45 and more with x264).
func GridCRFs(
	codec encode.Codec,
) []float64 {
	probes := codec.ProbeCRFs
	half := (probes[1] - probes[0]) / 2

	var crfs []float64

	for crf := probes[0] - 2*half; crf <= probes[len(probes)-1]+4*half; crf += half {
		if crf >= codec.MinCRF && crf <= codec.MaxCRF {
			crfs = append(crfs, crf)
		}
	}

	return crfs
}

// ErrCRFCount is returned for a grid of fewer than two CRFs.
var ErrCRFCount = errors.New("a grid needs at least two CRFs")

// SpreadCRFs returns count CRFs spread evenly over the span of the default
// grid of codec (GridCRFs: 13 to 48 for x264), each on a CRF the encoder
// accepts. Fewer than the default measure a corpus faster and read less
// well between their points; more read better, for a gain measured small
// beyond the default (see docs/validation.md). When rounding to the
// encoder's CRFs brings two of them together, fewer than count come back.
//
// Counts of the form 5k+1 for x264 (6, 11, 21) keep the CRFs of the
// default grid, or a part of them: a corpus measured with one can be
// completed with another.
func SpreadCRFs(
	codec encode.Codec,
	count int,
) ([]float64, error) {
	if count < 2 {
		return nil, fmt.Errorf("corpus: %w, got %d", ErrCRFCount, count)
	}

	span := GridCRFs(codec)
	first, last, step := span[0], span[len(span)-1], codec.Step()

	var crfs []float64

	for i := range count {
		crf := first + (last-first)*float64(i)/float64(count-1)
		crf = math.Round(crf/step) * step

		if !slices.Contains(crfs, crf) {
			crfs = append(crfs, crf)
		}
	}

	return crfs, nil
}

// Measure cuts the excerpt of source and encodes it at every point of the
// grid, scoring every frame; emit receives the row of each encode as soon
// as it is measured, so that an interrupted measurement keeps what it did.
func (b *Builder) Measure(
	ctx context.Context,
	source string,
	opts Options,
	emit func(predict.Row) error,
) error {
	codec, err := encode.Lookup(opts.Codec)
	if err != nil {
		return fmt.Errorf("corpus: %w", err)
	}

	opts = opts.withDefaults(codec)

	inspection, video, layout, err := b.inspect(ctx, source, opts.Segments)
	if err != nil {
		return err
	}

	jobs, pending := plan(source, codec, opts, video, len(layout.placed))

	described := predict.Row{
		Kind: predict.FeaturesKind, Source: source, SourceHeight: video.Height, SegmentSeconds: segmentSeconds,
		Segments: len(layout.placed), Version: features.Version,
	}
	describe := b.featurer != nil && (opts.Measured == nil || !opts.Measured(described))

	if pending == 0 && !describe {
		return nil
	}

	dir, cleanup, err := workDir(opts.WorkDir)
	if err != nil {
		return err
	}
	defer cleanup()

	opts.report(Progress{Stage: StageExcerpt})

	ex, err := b.cut(ctx, source, inspection, video, layout, dir)
	if err != nil {
		return err
	}
	defer os.Remove(ex.path)

	if describe {
		opts.report(Progress{Stage: StageFeatures})

		if described.Features, err = b.describe(ctx, ex, video, opts.Heights, dir); err == nil {
			err = emit(described)
		}

		if err != nil {
			return err
		}
	}

	return b.grid(ctx, codec, ex, jobs, len(jobs)-pending, dir, opts, emit)
}

// grid measures the encodes of jobs not measured yet, done of them being
// so, and gives each row to emit.
func (b *Builder) grid(
	ctx context.Context,
	codec encode.Codec,
	ex *excerpt,
	jobs []predict.Row,
	done int,
	dir string,
	opts Options,
	emit func(predict.Row) error,
) error {
	for _, job := range jobs {
		if opts.Measured != nil && opts.Measured(job) {
			continue
		}

		opts.report(Progress{Stage: StageGrid, Done: done, Total: len(jobs)})

		row, err := b.measure(ctx, codec, ex, job, dir)
		if err != nil {
			return err
		}

		if err := emit(row); err != nil {
			return err
		}

		done++
	}

	opts.report(Progress{Stage: StageGrid, Done: done, Total: len(jobs)})

	return nil
}

// inspect inspects source and returns its video stream and how its
// excerpt of segments segments is cut.
func (b *Builder) inspect(
	ctx context.Context,
	source string,
	segments int,
) (*analysis.Report, media.VideoStream, layout, error) {
	inspection, err := b.inspector.Analyze(ctx, source, analysis.Options{SkipVideo: true})
	if err != nil {
		return nil, media.VideoStream{}, layout{}, fmt.Errorf("corpus: inspect %s: %w", source, err)
	}

	video, ok := inspection.Info.PrimaryVideo()
	if !ok || video.Height <= 0 || video.AvgFrameRate.Float() <= 0 {
		return nil, media.VideoStream{}, layout{}, fmt.Errorf("corpus: %s: %w", source, ErrSource)
	}

	l := layoutOf(inspection, video, segments)
	if len(l.placed) == 0 {
		return nil, media.VideoStream{}, layout{}, fmt.Errorf("corpus: %s: %w: shorter than a segment", source, ErrSource)
	}

	return inspection, video, l, nil
}

func (o Options) withDefaults(
	codec encode.Codec,
) Options {
	if o.Preset == "" {
		o.Preset = codec.DefaultPreset
	}

	if len(o.Heights) == 0 {
		o.Heights = ladder.DefaultHeights()
	}

	if len(o.CRFs) == 0 {
		o.CRFs = GridCRFs(codec)
	}

	if o.Segments <= 0 {
		o.Segments = DefaultSegments
	}

	return o
}

func (o Options) report(
	p Progress,
) {
	if o.Progress != nil {
		o.Progress(p)
	}
}

// plan lists the encodes of the grid of a source, as rows to fill, and
// counts those still to measure.
func plan(
	source string,
	codec encode.Codec,
	opts Options,
	video media.VideoStream,
	segments int,
) ([]predict.Row, int) {
	var (
		jobs    []predict.Row
		pending int
		seen    []int
	)

	for _, height := range opts.Heights {
		if height > video.Height || slices.Contains(seen, height) {
			continue
		}

		seen = append(seen, height)
		width := int(math.Round(float64(height)*float64(video.Width)/float64(video.Height)/2)) * 2

		for _, crf := range opts.CRFs {
			job := predict.Row{
				Kind: predict.GridKind, Source: source, Codec: codec.Name, Preset: opts.Preset,
				Width: width, Height: height, CRF: crf, SourceHeight: video.Height, SegmentSeconds: segmentSeconds,
				Segments: segments,
			}
			jobs = append(jobs, job)

			if opts.Measured == nil || !opts.Measured(job) {
				pending++
			}
		}
	}

	return jobs, pending
}

// excerpt is the part of a title a corpus measures.
type excerpt struct {
	path string
	// report is the inspection of the excerpt, which every measurement
	// reads.
	report *analysis.Report
	// segments is its number of segments, each of frames frames, at rate
	// frames a second.
	segments, frames int
	rate             float64
	bitDepth         int
}

// layout is how the excerpt of a title is cut: its segments, each of
// frames frames at rate.
type layout struct {
	rate   media.Rational
	frames int
	placed []media.Interval
}

// layoutOf places the segments of the excerpt of a video: evenly across
// it, at its steady frame rate. A video whose frames are not all at their
// time (see steadyRate) leaves room after the last segment: its frames are
// counted, and a gap in its timestamps pushes the last ones later.
func layoutOf(
	inspection *analysis.Report,
	video media.VideoStream,
	segments int,
) layout {
	l := layout{rate: steadyRate(video)}
	rate := l.rate.Float()
	l.frames = int(math.Round(segmentSeconds * rate))

	total := int(video.FrameCount)
	if total <= 0 {
		total = int(math.Floor(inspection.Info.Duration.Seconds() * rate))
	}

	if l.rate != video.AvgFrameRate {
		total -= l.frames / spareShare
	}

	l.placed = Place(total, l.frames, segments, rate)

	return l
}

// cut extracts the excerpt of the source into dir: the segments of its
// layout, each of exactly as many frames, as raw video, which the encodes
// of the grid read without decoding.
func (b *Builder) cut(
	ctx context.Context,
	source string,
	inspection *analysis.Report,
	video media.VideoStream,
	l layout,
	dir string,
) (*excerpt, error) {
	steady, rate, frames, placed := l.rate, l.rate.Float(), l.frames, l.placed

	origin := video.StartTime
	if inspection.Bitstream != nil {
		origin = inspection.Bitstream.Start
	}

	ex := &excerpt{path: filepath.Join(dir, "excerpt.nut"), segments: len(placed), frames: frames, rate: rate, bitDepth: 8}
	if video.BitDepth > 8 {
		ex.bitDepth = 10
	}

	spec := encode.DigestSpec{
		Source: source, Destination: ex.path, Segments: placed, Rate: steady,
		Origin: origin, BitDepth: ex.bitDepth, Frames: frames,
	}
	if err := b.digester.Digest(ctx, spec); err != nil {
		return nil, fmt.Errorf("corpus: cut the excerpt of %s: %w", source, err)
	}

	report, err := b.inspector.Analyze(ctx, ex.path, analysis.Options{SkipVideo: true})
	if err != nil {
		return nil, fmt.Errorf("corpus: inspect the excerpt of %s: %w", source, err)
	}

	ex.report = report

	return ex, nil
}

// describe returns the features of the segments of the excerpt.
func (b *Builder) describe(
	ctx context.Context,
	ex *excerpt,
	video media.VideoStream,
	heights []int,
	dir string,
) (map[string][]float64, error) {
	set, err := b.featurer.Extract(ctx, features.Spec{
		Path: ex.path, Width: video.Width, Height: video.Height, Rate: ex.rate,
		Frames: ex.frames, Heights: heights, Inspection: ex.report, Precise: true, WorkDir: dir,
	})
	if err != nil {
		return nil, fmt.Errorf("corpus: %w", err)
	}

	if set.Segments != ex.segments {
		return nil, fmt.Errorf("corpus: %w: features of %d segments for %d", ErrFrames, set.Segments, ex.segments)
	}

	return set.Values, nil
}

// spareShare is the share of a segment left unused at the end of a title
// (a fifth: ten frames of a 2 s segment at 25 fps).
const spareShare = 5

// rateTolerance is how far the average frame rate of a video may be from
// its nominal one and still be that rate with frames missing.
const rateTolerance = 0.02

// steadyRate is the frame rate the excerpt of a video is cut and encoded
// at: its nominal rate when its average one is close to it. A video with
// a few frames missing (a gap in its timestamps) averages a little under
// its real rate (24.93 for 25 in a real title), and segments counted at
// that average would not hold whole GOPs.
func steadyRate(
	video media.VideoStream,
) media.Rational {
	nominal, average := video.FrameRate.Float(), video.AvgFrameRate.Float()
	if nominal > 0 && math.Abs(average/nominal-1) <= rateTolerance {
		return video.FrameRate
	}

	return video.AvgFrameRate
}

// Place returns count segments of frames frames taken evenly across a
// title of total frames at rate frames a second: each centred in its part
// of the title and starting on a frame (a quarter of a frame early, so
// that a seek rounding either way lands on it). A title too short for
// count segments gives the whole segments it holds.
func Place(
	total, frames, count int,
	rate float64,
) []media.Interval {
	if frames <= 0 || rate <= 0 {
		return nil
	}

	count = min(count, total/frames)
	segments := make([]media.Interval, 0, max(count, 0))

	for i := range count {
		part := float64(total) / float64(count)
		first := int(math.Floor(float64(i)*part + (part-float64(frames))/2))
		start := max((float64(first)-0.25)/rate, 0)

		segments = append(segments, media.Interval{
			Start: media.Seconds(start),
			End:   media.Seconds(start + float64(frames)/rate),
		})
	}

	return segments
}

// measure encodes the excerpt as the probes of a ladder are encoded, with
// the settings of job, scores every frame and fills the row segment by
// segment.
func (b *Builder) measure(
	ctx context.Context,
	codec encode.Codec,
	ex *excerpt,
	job predict.Row,
	dir string,
) (predict.Row, error) {
	path := filepath.Join(dir, "encode.mp4")
	defer os.Remove(path)

	name := fmt.Sprintf("%s %s %dp CRF %g", job.Codec, job.Preset, job.Height, job.CRF)
	params := encode.Params{Width: job.Width, Height: job.Height, CRF: job.CRF, Preset: job.Preset, GOP: ex.frames, BitDepth: ex.bitDepth}

	if err := b.encoder.Encode(ctx, codec, ex.path, path, params); err != nil {
		return predict.Row{}, fmt.Errorf("corpus: %s: %w", name, err)
	}

	cmp, err := b.inspector.Compare(ctx, ex.path, path, analysis.CompareOptions{
		Reference: ex.report,
		Quality:   quality.Options{Exact: true},
	})
	if err != nil {
		return predict.Row{}, fmt.Errorf("corpus: %s: measure: %w", name, err)
	}

	sizes := cmp.Distorted.Bitstream.FrameSizes
	if want := ex.frames * ex.segments; len(sizes) != want || len(cmp.VMAF.Frames) != want {
		return predict.Row{}, fmt.Errorf("corpus: %s: %w: %d frames, %d scored, %d expected", name, ErrFrames, len(sizes), len(cmp.VMAF.Frames), want)
	}

	job.Bits, job.VMAF = make([]int64, ex.segments), make([]float64, ex.segments)

	for i, size := range sizes {
		job.Bits[i/ex.frames] += int64(size) * 8
	}

	for _, score := range cmp.VMAF.Frames {
		if score.Index < 0 || score.Index >= len(sizes) {
			return predict.Row{}, fmt.Errorf("corpus: %s: %w: a score for frame %d", name, ErrFrames, score.Index)
		}

		job.VMAF[score.Index/ex.frames] += score.Score / float64(ex.frames)
	}

	return job, nil
}

// workDir returns dir, or a new temporary directory removed by cleanup.
func workDir(
	dir string,
) (string, func(), error) {
	if dir != "" {
		return dir, func() {}, nil
	}

	dir, err := os.MkdirTemp("", "qc-corpus-*")
	if err != nil {
		return "", nil, fmt.Errorf("corpus: work dir: %w", err)
	}

	return dir, func() { _ = os.RemoveAll(dir) }, nil
}
