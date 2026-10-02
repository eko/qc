package ladder

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/internal/balance"
	"github.com/eko/qc/media"
)

var (
	// ErrNoSource is returned when a program has no video.
	ErrNoSource = errors.New("no source")
	// ErrProgramFormat is returned when the videos of a program have not
	// the same format: their digest is their frames concatenated, as they
	// are.
	ErrProgramFormat = errors.New("the videos of a program must share their format")
	// ErrProgramOption is returned for options a program does not support.
	ErrProgramOption = errors.New("not supported for a program of several videos")
)

// Digest of a program: how many segments each video gives when
// Options.DigestDuration is unset.
const (
	// programSegments is the number of segments the videos share: as many
	// as the digest of a single title has.
	programSegments = defaultDigestSeconds / defaultSegmentSeconds
	// minTitleSegments is the least a video gives, however many they are:
	// its quality on every rung is read on them.
	minTitleSegments = 4
)

// programTitle is one video of a program.
type programTitle struct {
	path       string
	inspection *analysis.Report
	video      media.VideoStream
	duration   media.Duration
	// frames is its frame analysis, once made.
	frames *analysis.Report
}

// program is what makes a build the build of a program: its videos, the
// first being the source of the build, and whether the length of the digest
// was asked for.
type program struct {
	sources []string
	// digestAsked is set when Options.DigestDuration was given: it is then
	// shared between the videos.
	digestAsked bool
	// titles are the videos once inspected (see inspectProgram).
	titles []programTitle
}

// BuildProgram computes one ladder for several videos: the episodes of a
// programme, the titles of a collection, encoded with the same rungs. The
// digest takes as many segments in every video, whatever its length, so
// that each weighs the same in the ladder: 20 segments shared between them,
// and no less than 4 each (Options.DigestDuration, when set, is the length
// of the whole digest, shared equally). A balanced digest balances every
// video on its own: its segments have its own spatial and temporal
// information.
//
// The videos must share their resolution, frame rate, bit depth and
// transfer characteristics: their frames are concatenated as they are.
// Every verified rung is also read video by video (Measurement.Titles): a
// video the shared ladder serves badly shows there. Result.Source is the
// first video, whose path the commands of the rungs read; Result.Sources
// lists them all. Per-shot rungs are not supported, and Options.Analysis is
// not read; the ladders of several codecs share what PrepareProgram
// returns (Options.Prepared). One video alone is built as Build
// builds it.
func (e *Engine) BuildProgram(
	ctx context.Context,
	sources []string,
	opts Options,
) (*Result, error) {
	switch len(sources) {
	case 0:
		return nil, fmt.Errorf("ladder: %w", ErrNoSource)
	case 1:
		return e.Build(ctx, sources[0], opts)
	}

	if opts.PerShot || opts.PerShotResolution {
		return nil, fmt.Errorf("ladder: per-shot rungs are %w", ErrProgramOption)
	}

	opts.Analysis = nil

	return e.build(ctx, sources[0], opts, &program{sources: sources, digestAsked: opts.DigestDuration > 0})
}

// inspectProgram inspects the videos of program p, whose first is the
// source res holds, and lists their inspections in res; prepared holds
// what earlier builds made of them. It does nothing for the ladder of one
// title (p nil).
func (e *Engine) inspectProgram(
	ctx context.Context,
	p *program,
	res *Result,
	prepared *Prepared,
) (err error) {
	if p == nil {
		return nil
	}

	if p.titles, err = e.titles(ctx, p, res.Source, prepared); err != nil {
		return err
	}

	for _, t := range p.titles {
		res.Sources = append(res.Sources, t.inspection)
	}

	return nil
}

// titles inspects the videos of the program after the first (unless an
// earlier build did, see prepared), whose
// inspection and video are given, and checks that they share its format.
func (e *Engine) titles(
	ctx context.Context,
	p *program,
	first *analysis.Report,
	prepared *Prepared,
) ([]programTitle, error) {
	out := make([]programTitle, 0, len(p.sources))

	for i, path := range p.sources {
		kept := prepared.title(path)
		inspection := cmp.Or(kept.inspection, first)

		if i > 0 && kept.inspection == nil {
			var err error

			inspection, err = e.inspector.Analyze(ctx, path, analysis.Options{SkipVideo: true})
			if err != nil {
				return nil, fmt.Errorf("ladder: inspect %s: %w", path, err)
			}
		}

		video, duration, err := usableVideo(inspection)
		if err != nil {
			return nil, fmt.Errorf("%w (%s)", err, path)
		}

		if i > 0 {
			if diff := FormatDifference(out[0].video, video); diff != "" {
				return nil, fmt.Errorf("ladder: %w: %s is %s, %s is not", ErrProgramFormat, out[0].path, diff, path)
			}
		}

		prepared.keepTitle(path, preparedTitle{inspection: inspection, frames: kept.frames})
		out = append(out, programTitle{path: path, inspection: inspection, video: video, duration: duration, frames: kept.frames})
	}

	return out, nil
}

// rateTolerance tells two frame rates apart: 25 and 24.975 differ, two
// spellings of 30000/1001 do not.
const rateTolerance = 1e-3

// FormatDifference names what the first video is that the other is not,
// among what one ladder for both needs alike: the resolution, the frame rate,
// the bit depth and the transfer characteristics. It is empty when they
// match.
func FormatDifference(
	first, other media.VideoStream,
) string {
	switch {
	case first.Width != other.Width || first.Height != other.Height:
		return fmt.Sprintf("%d×%d", first.Width, first.Height)
	case math.Abs(first.AvgFrameRate.Float()-other.AvgFrameRate.Float()) > rateTolerance:
		return fmt.Sprintf("%.3f fps", first.AvgFrameRate.Float())
	case digestBitDepth(first) != digestBitDepth(other):
		return fmt.Sprintf("%d-bit", digestBitDepth(first))
	case (first.Color.IsHDR() || other.Color.IsHDR()) && first.Color.Transfer != other.Color.Transfer:
		if first.Color.IsHDR() {
			return "HDR (" + first.Color.Transfer + ")"
		}

		return "SDR"
	}

	return ""
}

// isProgram reports whether the build is that of several videos.
func (b *build) isProgram() bool {
	return len(b.titles) > 1
}

// perTitle is the length of the digest of each video of the program: its
// share of Options.DigestDuration when it was asked for, of the default
// number of segments otherwise, a segment at least, and no less than
// minTitleSegments by default.
func (b *build) perTitle() media.Duration {
	segment := b.opts.SegmentDuration
	n := len(b.titles)

	if b.digestAsked {
		return max(segment, media.Duration(int(b.opts.DigestDuration/segment)/n)*segment)
	}

	return media.Duration(max(minTitleSegments, (programSegments+n-1)/n)) * segment
}

// programAnalysed reports whether the digest of the program reads the frame
// analysis of its videos: a digest placed on the content, of videos longer
// than what the digest takes of them.
func (b *build) programAnalysed() bool {
	if b.opts.DigestSampling == DigestUniform {
		return false
	}

	for _, t := range b.titles {
		if t.duration > b.perTitle() {
			return true
		}
	}

	return false
}

// analysisScale is the resolution of the progress of the analysis of a
// program: each video counts for that many steps.
const analysisScale = 1000

// analyseProgram makes the frame analysis of every video of the program the
// digest samples, one after the other, reporting their progress as one.
func (b *build) analyseProgram(
	ctx context.Context,
) error {
	for i := range b.titles {
		t := &b.titles[i]
		if t.duration <= b.perTitle() || t.frames != nil {
			continue
		}

		opts := analysis.Options{
			Audio: analysis.AudioOptions{Skip: true},
			Video: analysis.VideoOptions{SkipMotion: true},
			Progress: func(p analysis.Progress) {
				if p.Stage == analysis.StageDecode && p.Total > 0 {
					b.reportProgress(Progress{
						Stage: StageAnalysis,
						Done:  i*analysisScale + p.Done*analysisScale/p.Total,
						Total: len(b.titles) * analysisScale,
					})
				}
			},
		}

		report, err := b.engine.inspector.Analyze(ctx, t.path, opts)
		if err != nil {
			return fmt.Errorf("ladder: analyse %s: %w", t.path, err)
		}

		t.frames = report

		// The builds of the other codecs read this analysis.
		b.prepared.keepTitle(t.path, preparedTitle{inspection: t.inspection, frames: report})
	}

	return nil
}

// makeProgramDigest extracts the digest of the program: the segments of
// every video, one video after the other.
func (b *build) makeProgramDigest(
	ctx context.Context,
) (Digest, error) {
	digest := planProgramDigest(b.titles, b.perTitle(), b.opts)
	parts := make([]encode.DigestPart, len(b.titles))
	rate := b.video.AvgFrameRate.Float()
	at, frame := 0, 0

	b.titleSpans = make([][2]int, len(b.titles))

	for i, t := range b.titles {
		segments := digest.Segments[at : at+digest.Titles[i].Segments]
		at += len(segments)

		parts[i] = encode.DigestPart{Source: t.path, Origin: videoOrigin(t.inspection, t.video), Segments: segments}

		// Where the frames of the video are in the digest.
		first := frame
		for _, s := range segments {
			frame += int(math.Round(s.Length().Seconds() * rate))
		}

		b.titleSpans[i] = [2]int{first, frame}
	}

	// An earlier build of the program extracted these segments already:
	// every codec reads the same file.
	if path, report, ok := b.prepared.sharedDigest(digest.Segments); ok {
		b.useDigest(path, report)

		return digest, nil
	}

	path, report, err := b.extractDigest(ctx, b.prepared.digestDir(b.workDir), digest.Duration, encode.DigestSpec{Parts: parts})
	if err != nil {
		return Digest{}, err
	}

	b.prepared.keepDigest(digest.Segments, path, report)

	return digest, nil
}

// planProgramDigest places the segments of the digest of a program: perTitle
// of every video, evenly spaced, on its most complex scenes, or balanced as
// a whole, as opts ask. A video no longer than perTitle is used whole.
// Without the frame analysis of a sampled video, the segments are uniform.
func planProgramDigest(
	titles []programTitle,
	perTitle media.Duration,
	opts Options,
) Digest {
	segments := make([][]media.Interval, len(titles))
	frames := make([]balance.Frames, len(titles))
	sampled, analysed := 0, 0

	for i, t := range titles {
		segments[i] = digestSegments(t.duration, opts.SegmentDuration, perTitle)

		if t.duration > perTitle {
			sampled++

			var ok bool
			if frames[i], ok = digestFrames(t.frames); ok {
				analysed++
			}
		}
	}

	digest := Digest{}

	if sampled > 0 {
		digest.Sampling = DigestUniform

		if analysed == sampled && placeProgram(titles, frames, segments, perTitle, opts) {
			digest.Sampling = opts.DigestSampling
		}
	}

	var total media.Duration

	for i, t := range titles {
		dt := DigestTitle{Source: t.path, Segments: len(segments[i])}

		for _, s := range segments[i] {
			dt.Duration += s.Length()
		}

		if all, ok := digestFrames(t.frames); ok {
			dt.Complexity = digestComplexity(all, segments[i])
		}

		digest.Titles = append(digest.Titles, dt)
		digest.Segments = append(digest.Segments, segments[i]...)
		digest.Duration += dt.Duration
		total += t.duration
	}

	digest.Complexity = programComplexity(digest.Titles)
	digest.Share = digest.Duration.Seconds() / max(total.Seconds(), 1e-9)

	return digest
}

// placeProgram replaces the evenly spaced segments of the sampled videos by
// segments placed on their content, as opts.DigestSampling asks, and
// reports whether it did: all of them or none.
//
// Every video is balanced on its own: its segments have its own spatial
// and temporal information, so that what a rung measures on them is that
// video's, and the digest, where each video weighs the same, has the
// average of theirs. Balancing the digest as a whole reaches that average
// too, but lets the videos compensate each other: on three real titles it
// gave a cartoon segments half as busy as the cartoon and a reality show
// segments 40% busier than the show, and their per-video readings meant
// nothing.
func placeProgram(
	titles []programTitle,
	frames []balance.Frames,
	segments [][]media.Interval,
	perTitle media.Duration,
	opts Options,
) bool {
	placed := make([][]media.Interval, len(titles))

	for i, t := range titles {
		if t.duration <= perTitle {
			continue
		}

		switch opts.DigestSampling {
		case DigestBalanced:
			extent := min(t.duration, videoEnd(frames[i].PTS))
			placed[i] = balance.Segments(frames[i], extent, opts.SegmentDuration, len(segments[i]))
		case DigestTop:
			placed[i] = balance.Top(frames[i], t.frames.ShotCuts(), opts.SegmentDuration, len(segments[i]), complexity)
		}

		if placed[i] == nil {
			return false
		}
	}

	for i := range titles {
		if placed[i] != nil {
			segments[i] = placed[i]
		}
	}

	return true
}

// programComplexity is the complexity of the digest of a program against
// its videos': the averages of the videos', each weighing the same. It is
// nil unless every video was analysed.
func programComplexity(
	titles []DigestTitle,
) *DigestComplexity {
	out := &DigestComplexity{}

	for _, t := range titles {
		if t.Complexity == nil {
			return nil
		}

		out.TitleSI += t.Complexity.TitleSI / float64(len(titles))
		out.TitleTI += t.Complexity.TitleTI / float64(len(titles))
		out.SI += t.Complexity.SI / float64(len(titles))
		out.TI += t.Complexity.TI / float64(len(titles))
	}

	return out
}

// titleMeasurements reads a measurement of an encode of the digest video by
// video: the bitrate of the frames of each, and the VMAF of those scored
// among them (every one in an exact measurement).
func (b *build) titleMeasurements(
	cmp *analysis.Comparison,
) []TitleMeasurement {
	if !b.isProgram() || cmp.Distorted == nil || cmp.Distorted.Bitstream == nil || cmp.VMAF == nil {
		return nil
	}

	sizes := cmp.Distorted.Bitstream.FrameSizes
	rate := b.video.AvgFrameRate.Float()
	out := make([]TitleMeasurement, len(b.titleSpans))

	for i, span := range b.titleSpans {
		bytes, frames := 0, 0

		for f := span[0]; f < span[1] && f < len(sizes); f++ {
			bytes += sizes[f]
			frames++
		}

		if frames > 0 {
			out[i].Bitrate = int64(math.Round(float64(bytes*bitsPerByte) * rate / float64(frames)))
		}

		var sum float64

		for _, f := range cmp.VMAF.Frames {
			if f.Index >= span[0] && f.Index < span[1] {
				sum += f.Score
				out[i].ScoredFrames++
			}
		}

		if out[i].ScoredFrames > 0 {
			out[i].VMAF = sum / float64(out[i].ScoredFrames)
		}
	}

	return out
}
