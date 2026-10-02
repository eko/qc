package ladder

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/internal/balance"
	"github.com/eko/qc/media"
)

// DigestSampling is how the segments of the digest are placed in the title.
type DigestSampling string

// Digest samplings.
const (
	// DigestBalanced keeps one segment in each part of the title, as
	// DigestUniform does, and moves each inside its part until the frames
	// of the digest have the spatial and temporal information (SI, TI) of
	// the whole title, on average: an encode of the digest then costs what
	// the same encode of the title does, far closer than evenly spaced
	// segments get (see docs/validation.md). It reads the frame analysis
	// of the source (Options.Analysis, or one made first), and falls back
	// on uniform segments when that analysis has no SI and TI.
	DigestBalanced DigestSampling = "balanced"
	// DigestUniform spaces the segments evenly, whatever the content.
	DigestUniform DigestSampling = "uniform"
	// DigestTop takes the most complex stretches of the title, one per
	// shot at most: those where the product of the spatial and temporal
	// information is highest, which are the ones that cost the most bits.
	// The ladder is then that of the demanding scenes, not of the title:
	// its bitrates are what those scenes need, above the title's average.
	// Like DigestBalanced, it reads the frame analysis of the source and
	// falls back on uniform segments without SI and TI.
	DigestTop DigestSampling = "top"
)

// ErrInvalidDigestSampling is returned for an unknown digest sampling.
var ErrInvalidDigestSampling = errors.New("invalid digest sampling")

// ParseDigestSampling reads a digest sampling ("" is the default one).
func ParseDigestSampling(
	s string,
) (DigestSampling, error) {
	switch d := DigestSampling(s); d {
	case "", DigestBalanced, DigestUniform, DigestTop:
		return d, nil
	}

	return "", fmt.Errorf("%w %q (supported: balanced, uniform, top)", ErrInvalidDigestSampling, s)
}

// maxRawDigestBytes switches the digest to lossless compression above it:
// raw video costs nothing to decode, but a long 4K digest would not fit on
// most temporary disks.
const maxRawDigestBytes = 4 << 30

// samplesPerPixel420 is the number of samples per pixel in 4:2:0 (one luma
// plus two quarter-size chroma planes).
const samplesPerPixel420 = 1.5

// makeDigest extracts the digest of the source into the work directory and
// inspects it once, so that every measurement reuses that inspection.
func (b *build) makeDigest(
	ctx context.Context,
	duration media.Duration,
) (Digest, error) {
	// The frame analysis the build waited for (see StageAnalysis), or the
	// one at hand, which also describes a uniform digest.
	digest := planDigest(cmp.Or(b.analysis, b.opts.Analysis), duration, b.opts, b.digestGrid())

	// An earlier build of the source extracted these segments already:
	// every codec reads the same file.
	if path, report, ok := b.prepared.sharedDigest(digest.Segments); ok {
		b.useDigest(path, report)

		return digest, nil
	}

	dir := b.prepared.digestDir(b.workDir)
	depth := digestBitDepth(b.video)
	lossless := rawDigestBytes(b.video, depth, digest.Duration) > maxRawDigestBytes

	path := filepath.Join(dir, "digest.nut")
	if lossless {
		path = filepath.Join(dir, "digest.mkv")
	}

	spec := encode.DigestSpec{
		Source: b.source, Destination: path, Segments: digest.Segments,
		Rate: b.video.AvgFrameRate, BitDepth: depth, Lossless: lossless, Origin: b.origin,
	}

	if err := b.engine.digester.Digest(ctx, spec); err != nil {
		return Digest{}, fmt.Errorf("ladder: %w", err)
	}

	report, err := b.engine.inspector.Analyze(ctx, path, analysis.Options{SkipVideo: true})
	if err != nil {
		return Digest{}, fmt.Errorf("ladder: inspect digest: %w", err)
	}

	report = withSignal(report, b.video)
	b.prepared.keepDigest(digest.Segments, path, report)
	b.useDigest(path, report)

	return digest, nil
}

// useDigest makes the digest at path, inspected as report, what the build
// encodes and scores against.
func (b *build) useDigest(
	path string,
	report *analysis.Report,
) {
	b.digest, b.digestReport = path, report
	b.reference, b.referenceReport = path, report
}

// PlanDigest returns the digest Build extracts from a source, without
// extracting it: its segments and how they stand for the title. source is
// the frame analysis of the title a balanced digest reads
// (Options.DigestSampling); with an inspection alone, the segments are
// evenly spaced.
func PlanDigest(
	source *analysis.Report,
	opts Options,
) (Digest, error) {
	if _, err := ParseDigestSampling(string(opts.DigestSampling)); err != nil {
		return Digest{}, fmt.Errorf("ladder: %w", err)
	}

	video, duration, err := usableVideo(source)
	if err != nil {
		return Digest{}, err
	}

	opts = opts.withDigestDefaults()

	return planDigest(source, duration, opts, digestGrid(opts, video)), nil
}

// gopGrid is the grid of the GOPs of the encodes on the frames of the
// title: a GOP every frames frames, at rate frames a second. The zero value
// is no grid.
type gopGrid struct {
	frames int
	rate   float64
}

// digestGrid returns the grid the segments of the digest start on: the
// GOPs of the encodes when per-shot rungs are asked, none otherwise.
//
// A per-shot rung gives every shot, a run of whole GOPs of the title, the
// model of the piece of it the digest holds. A segment starting anywhere
// straddles shots and GOPs: its pieces start with a keyframe the title has
// not there and stop short of a GOP, so they cost and score unlike the shot
// they stand for. On the grid, a segment is a GOP of one shot, encoded as
// the title encodes it. In replays of the allocation on a title encoded
// whole, that alone turned a loss into a gain (see docs/validation.md).
func digestGrid(
	opts Options,
	video media.VideoStream,
) gopGrid {
	if !opts.PerShot && !opts.PerShotResolution {
		return gopGrid{}
	}

	rate := video.AvgFrameRate.Float()

	return gopGrid{frames: gopFrames(opts.withGOPDefault().GOPDuration, rate), rate: rate}
}

// digestGrid is the grid of the digest of the build (see digestGrid).
func (b *build) digestGrid() gopGrid {
	return digestGrid(b.opts, b.video)
}

// snap moves evenly spaced segments onto the grid: each to the GOP nearest
// to its start, a quarter of a frame early as placed segments are (see
// balance.Segments). Segments the grid would make overlap, or push past
// the title, are left where they are.
func (g gopGrid) snap(
	segments []media.Interval,
	duration media.Duration,
) []media.Interval {
	if g.frames <= 1 || g.rate <= 0 {
		return segments
	}

	out := make([]media.Interval, len(segments))
	frame := media.Seconds(1 / g.rate)

	for i, s := range segments {
		gop := math.Round(s.Start.Seconds() * g.rate / float64(g.frames))
		start := media.Seconds(gop*float64(g.frames)/g.rate) - frame/4

		out[i] = media.Interval{Start: max(start, 0), End: max(start, 0) + s.Length()}

		if out[i].End > duration || (i > 0 && out[i].Start < out[i-1].End) {
			return segments
		}
	}

	return out
}

// planDigest places the segments of the digest of a title of this duration:
// balanced on the frame analysis source when opts ask for it and source has
// the features, evenly spaced otherwise. A title used whole has no
// sampling. On a grid, the segments start where GOPs of the title do.
func planDigest(
	source *analysis.Report,
	duration media.Duration,
	opts Options,
	grid gopGrid,
) Digest {
	segments := digestSegments(duration, opts.SegmentDuration, opts.DigestDuration)
	digest := Digest{Sampling: DigestUniform}

	if duration <= opts.DigestDuration {
		digest.Sampling = ""
	} else {
		segments = grid.snap(segments, duration)
	}

	if frames, ok := digestFrames(source); ok && digest.Sampling != "" {
		frames.Stride = grid.frames

		if placed := placeSegments(source, frames, duration, len(segments), opts); placed != nil {
			segments, digest.Sampling = placed, opts.DigestSampling
		}

		digest.Complexity = digestComplexity(frames, segments)
	}

	for _, s := range segments {
		digest.Duration += s.Length()
	}

	digest.Segments = segments
	digest.Share = digest.Duration.Seconds() / max(duration.Seconds(), 1e-9)

	return digest
}

// placeSegments places count segments on the frames of an analysed source
// as opts.DigestSampling asks; nil for uniform segments, or when the frames
// cannot place them.
func placeSegments(
	source *analysis.Report,
	frames balance.Frames,
	duration media.Duration,
	count int,
	opts Options,
) []media.Interval {
	switch opts.DigestSampling {
	case DigestBalanced:
		// The slots cover the video: a container may outlast it (a longer
		// audio track).
		extent := min(duration, videoEnd(frames.PTS))

		return balance.Segments(frames, extent, opts.SegmentDuration, count)
	case DigestTop:
		return balance.Top(frames, source.ShotCuts(), opts.SegmentDuration, count, complexity)
	}

	return nil
}

// complexity scores a segment of the digest for DigestTop: the product of
// its mean spatial and temporal information (the first two features of
// digestFrames). Among the scores tried on full-title encodes, it ranked
// the 2 s windows of a title by their encoded size best across titles (see
// docs/validation.md); it does not rank them by quality.
func complexity(
	means []float64,
) float64 {
	return means[0] * means[1]
}

// digestFrames returns the frames of an analysed source with the features
// its digest is balanced on, and false when the analysis lacks them: the
// spatial and temporal information of every frame (ITU-T P.910), and the
// square root of the temporal one, a frame costing less than
// proportionally to its motion. The source's own bitrate would spare the
// analysis, but tells nothing: a mezzanine is near constant bitrate (see
// docs/validation.md).
func digestFrames(
	report *analysis.Report,
) (balance.Frames, bool) {
	if report == nil || report.Frames == nil {
		return balance.Frames{}, false
	}

	series := report.Frames
	if len(series.PTS) < 2 || len(series.SI) != len(series.PTS) || len(series.TI) != len(series.PTS) {
		return balance.Frames{}, false
	}

	root := make([]float64, len(series.TI))
	for i, ti := range series.TI {
		root[i] = math.Sqrt(max(ti, 0))
	}

	return balance.Frames{PTS: series.PTS, Features: [][]float64{series.SI, series.TI, root}}, true
}

// videoEnd is the end of the last frame, taken as long as the average one.
func videoEnd(
	pts []media.Duration,
) media.Duration {
	last := pts[len(pts)-1]

	return last + (last-pts[0])/media.Duration(len(pts)-1)
}

// digestComplexity compares the frames of the segments with the title's.
func digestComplexity(
	frames balance.Frames,
	segments []media.Interval,
) *DigestComplexity {
	title, _ := frames.Means()

	digest, ok := frames.Means(segments...)
	if !ok {
		return nil
	}

	return &DigestComplexity{TitleSI: title[0], TitleTI: title[1], SI: digest[0], TI: digest[1]}
}

// digestSource is the digest as the source of chunked encodes: its
// timestamps start at 0, at the source's frame rate.
func (b *build) digestSource() encode.ChunkSource {
	return encode.ChunkSource{Path: b.digest, Rate: b.video.AvgFrameRate}
}

// videoOrigin is the presentation time of the first frame of video on the
// container's timeline: the first presentation time of the bitstream, as
// decoding seeks from (decode.Request.Origin), or the stream's start when
// the bitstream was not read.
func videoOrigin(
	report *analysis.Report,
	video media.VideoStream,
) media.Duration {
	if report.Bitstream != nil {
		return report.Bitstream.Start
	}

	return video.StartTime
}

// digestBitDepth keeps the source depth (8 or 10 bits), so that 10-bit
// sources are measured at 10 bits.
func digestBitDepth(
	video media.VideoStream,
) int {
	if video.BitDepth > 8 {
		return 10
	}

	return 8
}

// rawDigestBytes is the size of the digest as raw 4:2:0 video. Samples above
// 8 bits take two bytes.
func rawDigestBytes(
	video media.VideoStream,
	depth int,
	duration media.Duration,
) float64 {
	bytesPerSample := 1.0
	if depth > 8 {
		bytesPerSample = 2
	}

	frames := duration.Seconds() * video.AvgFrameRate.Float()

	return float64(video.Width*video.Height) * samplesPerPixel420 * bytesPerSample * frames
}

// digestSegments spreads segments evenly over the title: systematic sampling
// keeps every part of the title represented. A title no longer than budget
// is used whole.
func digestSegments(
	duration, segment, budget media.Duration,
) []media.Interval {
	if duration <= budget {
		return []media.Interval{{Start: 0, End: duration}}
	}

	count := max(1, int(budget/segment))
	step := duration / media.Duration(count)
	out := make([]media.Interval, count)

	// Each segment is centred in its share of the title.
	for i := range out {
		start := media.Duration(i)*step + (step-segment)/2
		out[i] = media.Interval{Start: start, End: start + segment}
	}

	return out
}
