// Package features describes a video by what its analysis says of it,
// segment by segment: the numbers a ladder model reads to predict how the
// video encodes without encoding it (package predict).
//
// Five sources, all cheap next to the encodes of a ladder:
//
//   - the frame analysis: spatial and temporal information (ITU-T P.910),
//     luma, and the camera's move (pan, tilt, zoom, shake);
//   - the source's own encode: the quantiser it was coded with (Spec
//     .Quantisers). A source is an encode already made, and what its
//     encoder had to do to hold its bitrate tells how hard the video is:
//     on 40 titles it brought the bitrate error of a prediction from 7.5%
//     to 6.4%, and from 12.2% to 9.3% without the encodes below. It means
//     what it meant in the corpus: sources encoded alike;
//   - a statistics pass of x264 at its fastest preset and a low
//     resolution, at two CRFs: what the encoder spends on residuals and on
//     motion, how many blocks it skips or codes intra, and the PSNR it
//     reaches;
//   - the loss of a round trip through each lower resolution (PSNR): what
//     a rendition at that resolution can no longer show, however many bits
//     it gets. On 40 titles it added nothing to the others (1.19 against
//     1.20 VMAF): it is computed for the models trained with it, when
//     asked by name (Spec.Names);
//   - VMAF itself, where it is cheap: the two encodes of the statistics
//     pass, scored against the video. The other features tell what the
//     encoder makes of the video; these tell what VMAF makes of it, which
//     is what a model misses most: on three titles they brought the error
//     of a prediction without anchor from 3.6 to 1.2 VMAF. Two things
//     were tried with them and left out: the VMAF of a lossless copy at
//     each lower resolution (its ceiling), which added nothing, and the
//     same encodes scored against a lossless copy at their resolution
//     (the loss of the encoding alone), which brought 1.2 to 1.0 for
//     twice the time.
//
// Not every build can read every source (see Usable): the quantisers need
// a decoder exporting them, and a build asked not to encode leaves the
// statistics pass out. A model holds a design for each case (see Sets).
package features

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/quality"
)

// ErrFrames is returned when the analysis or the statistics of a video do
// not hold the frames of its segments.
var ErrFrames = errors.New("features do not cover the segments of the video")

// Version is the version of the set of features Extract returns: it grows
// when features are added or change meaning, and corpora described by an
// older one are described again.
const Version = 7

// SourceQP is the feature of the source's own encode: the mean quantiser
// of the frames of a segment.
const SourceQP = "src_qp"

// Prefixes of the names of the features, by what they are read from.
const (
	encodePrefix = "x264_"
	scalePrefix  = "scale_"
	sourcePrefix = "src_"
	scoreSuffix  = "_vmaf"
)

// NeedsEncode tells whether a feature is read from the encodes of the
// statistics pass.
func NeedsEncode(
	name string,
) bool {
	return strings.HasPrefix(name, encodePrefix)
}

// fromSource tells whether a feature is read from the source's own encode.
func fromSource(
	name string,
) bool {
	return strings.HasPrefix(name, sourcePrefix)
}

// retired tells whether a feature is no longer part of the set (the scale
// loss): Extract still computes it for a model that reads it.
func retired(
	name string,
) bool {
	return strings.HasPrefix(name, scalePrefix)
}

// Usable returns what tells the features a build can read: those of the
// statistics pass when it may encode, those of the source's encode when
// its quantisers are known.
func Usable(
	encodes, quantisers bool,
) func(name string) bool {
	return func(name string) bool {
		return (encodes || !NeedsEncode(name)) && (quantisers || !fromSource(name))
	}
}

// Sets returns, of the features names of a corpus, the sets a model fits
// a design without anchor on, one for each thing a build can read of a
// title: everything; everything but the source's encode (a source whose
// decoder does not tell its quantisers); what needs no encode; and that
// without the source's encode. Retired features are in none.
func Sets(
	names []string,
) [][]string {
	var sets [][]string

	for _, usable := range []func(string) bool{Usable(true, true), Usable(true, false), Usable(false, true), Usable(false, false)} {
		var set []string

		for _, name := range names {
			if usable(name) && !retired(name) {
				set = append(set, name)
			}
		}

		sets = append(sets, set)
	}

	return sets
}

// Settings of the statistics pass and of the scale loss.
const (
	statsHeight = 360
	statsPreset = "ultrafast"
	// scaleStep keeps one frame in ten for the scale loss: it moves
	// slowly within a shot.
	scaleStep = 10
)

// statsCRFs are the CRFs of the statistics pass: a good and a poor
// quality, whose difference tells how fast the video's cost falls.
var statsCRFs = []float64{23, 35}

// Inspector analyses the frames of a video and scores one against
// another.
type Inspector interface {
	Analyze(
		ctx context.Context,
		path string,
		opts analysis.Options,
	) (*analysis.Report, error)
	Compare(
		ctx context.Context,
		refPath, distPath string,
		opts analysis.CompareOptions,
	) (*analysis.Comparison, error)
}

// Encoder takes the cheap looks of an encoder at a video.
type Encoder interface {
	Stats(
		ctx context.Context,
		spec encode.StatsSpec,
	) ([][]encode.GOPStats, error)
	ScaleLoss(
		ctx context.Context,
		spec encode.ScaleLossSpec,
	) ([][]float64, error)
}

// Extractor computes the features of videos.
type Extractor struct {
	inspector Inspector
	encoder   Encoder
}

// NewExtractor returns an Extractor analysing with inspector and encoding
// with encoder.
func NewExtractor(
	inspector Inspector,
	encoder Encoder,
) *Extractor {
	return &Extractor{inspector: inspector, encoder: encoder}
}

// Spec describes the video whose features are extracted: a file made of
// segments of Frames frames each (an excerpt, a digest), as raw or
// lossless video.
type Spec struct {
	Path          string
	Width, Height int
	// Rate is its frame rate and Frames the length of a segment.
	Rate   float64
	Frames int
	// Heights are the resolutions of the ladder; those under Height get a
	// scale loss when Names asks for it.
	Heights []int
	// Names are the features wanted; nil for all those of Version. Only
	// their sources are read: a build without the encodes of the
	// statistics pass asks for none of theirs.
	Names []string
	// Quantisers are the quantisers of the source's own encode, one per
	// frame of the file (see encode.DigestSpec.Quantisers); nil when they
	// are not known, and SourceQP then cannot be asked.
	Quantisers []float64
	// Inspection is the inspection of the file, when already made: it is
	// then not made again for each score.
	Inspection *analysis.Report
	// Precise scores every frame where a ladder build scores clips (see
	// scorePass): what a corpus asks for.
	Precise bool
	// WorkDir receives temporary files.
	WorkDir string
}

// Set holds the features of the segments of a video: Values[name][i] is
// the feature name of segment i.
type Set struct {
	Segments int
	Values   map[string][]float64
}

// Names returns the names of the features, sorted.
func (s Set) Names() []string {
	names := make([]string, 0, len(s.Values))
	for name := range s.Values {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// Mean returns the features of the video as a whole: the mean of those of
// its segments.
func (s Set) Mean() map[string]float64 {
	out := make(map[string]float64, len(s.Values))

	for name, values := range s.Values {
		sum := 0.0
		for _, v := range values {
			sum += v
		}

		out[name] = sum / float64(max(len(values), 1))
	}

	return out
}

// Extract returns the features of every segment of the video spec
// describes: those of Spec.Names. Their sources are read at once: the
// frame analysis, the statistics pass followed by the scores of its
// encodes, the scale loss.
func (e *Extractor) Extract(
	ctx context.Context,
	spec Spec,
) (Set, error) {
	if spec.Frames <= 0 || spec.Rate <= 0 || spec.Width <= 0 || spec.Height <= 0 {
		return Set{}, fmt.Errorf("features of %s: %w: no geometry", spec.Path, ErrFrames)
	}

	want := wanted(spec)

	pass := encode.StatsSpec{
		Source: spec.Path, Width: evenWidth(statsHeight, spec.Width, spec.Height), Height: statsHeight,
		Preset: statsPreset, CRFs: statsCRFs, GOP: spec.Frames, WorkDir: spec.WorkDir, Keep: want.scores,
	}

	defer func() {
		for i := range pass.CRFs {
			_ = os.Remove(pass.Output(i))
		}
	}()

	lower := lowerHeights(spec, want)

	read, err := e.read(ctx, spec, pass, lower, want)
	if err != nil {
		return Set{}, fmt.Errorf("features of %s: %w", spec.Path, err)
	}

	set := Set{Values: map[string][]float64{}}

	err = set.addFrames(read.report.Frames, spec.Frames)
	if err == nil && want.quantisers {
		err = set.addQuantisers(spec.Quantisers, spec.Frames)
	}

	if err == nil && want.encodes {
		err = set.addStats(read.stats, spec)
	}

	if err == nil {
		err = set.addScaleLoss(read.loss, lower, spec.Frames)
	}

	if err == nil && want.scores {
		err = set.addScores(read.scores, spec.Frames)
	}

	if err != nil {
		return Set{}, fmt.Errorf("features of %s: %w", spec.Path, err)
	}

	if spec.Names != nil {
		maps.DeleteFunc(set.Values, func(name string, _ []float64) bool { return !slices.Contains(spec.Names, name) })
	}

	return set, nil
}

// lowerHeights are the resolutions under that of the video whose scale
// loss is asked, each once.
func lowerHeights(
	spec Spec,
	want wants,
) []int {
	var lower []int

	for _, h := range spec.Heights {
		if want.scale && h < spec.Height && !slices.Contains(lower, h) {
			lower = append(lower, h)
		}
	}

	return lower
}

// wants is what the features asked of a video are read from, next to its
// frame analysis.
type wants struct {
	// encodes is the statistics pass, scores the VMAF of its encodes.
	encodes, scores bool
	// scale is the scale loss, quantisers the source's own encode.
	scale, quantisers bool
}

// wanted tells what the features spec asks for are read from: every
// source of the features of Version when it names none, the quantisers
// when they are known.
func wanted(
	spec Spec,
) wants {
	if spec.Names == nil {
		return wants{encodes: true, scores: true, quantisers: spec.Quantisers != nil}
	}

	var w wants

	for _, name := range spec.Names {
		switch {
		case NeedsEncode(name):
			w.encodes = true
			w.scores = w.scores || strings.HasSuffix(name, scoreSuffix)
		case retired(name):
			w.scale = true
		case fromSource(name):
			w.quantisers = true
		}
	}

	return w
}

// sources is what the features of a video are computed from.
type sources struct {
	report *analysis.Report
	stats  [][]encode.GOPStats
	scores [][]scored
	loss   [][]float64
}

// read reads the sources of the features at once: the frame analysis, the
// scale loss at the heights lower, and the statistics pass followed by
// the scores of its encodes, those want asks for.
func (e *Extractor) read(
	ctx context.Context,
	spec Spec,
	pass encode.StatsSpec,
	lower []int,
	want wants,
) (sources, error) {
	var out sources

	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(concurrency())

	group.Go(func() (err error) {
		out.report, err = e.inspector.Analyze(gctx, spec.Path, analysis.Options{Audio: analysis.AudioOptions{Skip: true}})

		return err
	})

	if want.encodes {
		group.Go(func() (err error) {
			if out.stats, err = e.encoder.Stats(gctx, pass); err != nil || !want.scores {
				return err
			}

			out.scores, err = e.scorePass(gctx, spec, pass)

			return err
		})
	}

	if len(lower) > 0 {
		group.Go(func() (err error) {
			out.loss, err = e.encoder.ScaleLoss(gctx, encode.ScaleLossSpec{
				Source: spec.Path, Width: spec.Width, Height: spec.Height, Heights: lower, Step: scaleStep, WorkDir: spec.WorkDir,
			})

			return err
		})
	}

	return out, group.Wait()
}

// sharedBelow is the number of CPUs under which the sources of the
// features are read one after the other, each with all of them: run
// together, each sized for every CPU, they took 3 to 6.5 cores under a cap
// of 2 (--cpus, or a container's quota, which GOMAXPROCS follows).
const sharedBelow = 6

// concurrency is how many sources of the features are read at once.
func concurrency() int {
	if runtime.GOMAXPROCS(0) < sharedBelow {
		return 1
	}

	return -1
}

// segmentFrames is the number of frames of each segment a ladder build
// scores: single frames spread evenly in the segment (quality.Sample
// .Even). The quality of an encode falls along each of its GOPs (81.8 on
// the keyframe, 77.7 after it, 74 at the end, on a quiet dialogue), and a
// model fitted on scores of every frame reads a draw of a few clips as
// another encode: one or two clips of four frames drawn in each segment
// gave scores 3 to 6 VMAF points too high on that dialogue, three still
// up to 1.4. Four frames spread evenly read the scores of every frame
// within 0.1 on that dialogue and on a reality show, and 0.5 to 1.1 too
// high on a cartoon, for a third fewer frames to score than three clips;
// three are kept, which the scores of an exact run put within 0.8 at
// worst and 0.3 on average: scoring is most of the time of the features,
// and an instant ladder is asked for in five seconds.
const segmentFrames = 3

// scored is the VMAF of one frame of an encode.
type scored struct {
	frame int
	vmaf  float64
}

// scorePass scores each encode of the statistics pass against the video,
// at once: VMAF as a viewer of the video's resolution gets it.
//
// A corpus scores every frame (Spec.Precise): its segments are what a
// model is fitted on. A ladder build scores segmentFrames frames of each
// segment: every frame would double the time of the features.
func (e *Extractor) scorePass(
	ctx context.Context,
	spec Spec,
	pass encode.StatsSpec,
) ([][]scored, error) {
	opts := quality.Options{Sample: quality.Sample{PerScene: segmentFrames, Even: true}, ClipFrames: 1}
	if spec.Precise {
		opts = quality.Options{Exact: true}
	}

	out := make([][]scored, len(pass.CRFs))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(concurrency())

	for i := range pass.CRFs {
		group.Go(func() error {
			cmp, err := e.inspector.Compare(gctx, spec.Path, pass.Output(i), analysis.CompareOptions{Reference: spec.Inspection, Quality: opts})
			if err != nil {
				return fmt.Errorf("score the statistics pass: %w", err)
			}

			if cmp.VMAF == nil || len(cmp.VMAF.Frames) == 0 {
				return fmt.Errorf("score the statistics pass: %w: no frame scored", ErrFrames)
			}

			for _, frame := range cmp.VMAF.Frames {
				out[i] = append(out[i], scored{frame: frame.Index, vmaf: frame.Score})
			}

			return nil
		})
	}

	return out, group.Wait()
}

// addScores adds, per CRF of the statistics pass, the VMAF of each segment
// on the logistic scale models predict on: the mean of its scored frames.
// A segment none of whose frames was scored takes the mean of them all.
func (s *Set) addScores(
	scores [][]scored,
	perSegment int,
) error {
	for c, frames := range scores {
		sums, counts := make([]float64, s.Segments), make([]int, s.Segments)
		total := 0.0

		for _, f := range frames {
			total += f.vmaf

			if segment := f.frame / perSegment; f.frame >= 0 && segment < s.Segments {
				sums[segment] += f.vmaf
				counts[segment]++
			}
		}

		values := make([]float64, s.Segments)
		for i := range values {
			mean := total / float64(len(frames))
			if counts[i] > 0 {
				mean = sums[i] / float64(counts[i])
			}

			values[i] = link(mean)
		}

		s.Values["x264_"+strconv.FormatFloat(statsCRFs[c], 'f', -1, 64)+"_vmaf"] = values
	}

	return nil
}

// link puts a VMAF on the logistic scale of package predict: the
// logarithm of v/(100−v), v kept half a point from 0 and 100.
func link(
	v float64,
) float64 {
	v = min(max(v, 0.5), 99.5)

	return math.Log(v / (100 - v))
}

// addQuantisers adds the feature of the source's own encode: per segment,
// the mean of the quantisers of its frames.
func (s *Set) addQuantisers(
	quantisers []float64,
	perSegment int,
) error {
	if len(quantisers) < s.Segments*perSegment {
		return fmt.Errorf("%w: the quantisers of %d frames of the source for %d segments", ErrFrames, len(quantisers), s.Segments)
	}

	values := make([]float64, s.Segments)

	for i := range values {
		for _, qp := range quantisers[i*perSegment : (i+1)*perSegment] {
			values[i] += qp / float64(perSegment)
		}
	}

	s.Values[SourceQP] = values

	return nil
}

// addFrames adds the features of the frame analysis: per segment, the
// means of the spatial and temporal information (and of their logarithms:
// costs are multiplicative), of the luma and of the camera's speed. The
// first frame of a segment is left out of the temporal features: in an
// excerpt it follows a frame of another part of the title.
func (s *Set) addFrames(
	frames *analysis.FrameSeries,
	perSegment int,
) error {
	if frames == nil || len(frames.SI) < perSegment || len(frames.TI) != len(frames.SI) {
		return fmt.Errorf("%w: no frame analysis", ErrFrames)
	}

	s.Segments = len(frames.SI) / perSegment

	within := func(values []float64, from int, transform func(float64) float64) []float64 {
		out := make([]float64, s.Segments)

		if len(values) < s.Segments*perSegment {
			return out
		}

		for i := range out {
			segment := values[i*perSegment+from : (i+1)*perSegment]
			for _, v := range segment {
				out[i] += transform(v) / float64(len(segment))
			}
		}

		return out
	}

	identity := func(v float64) float64 { return v }

	s.Values["si"] = within(frames.SI, 0, identity)
	s.Values["si_log"] = within(frames.SI, 0, math.Log1p)
	s.Values["ti"] = within(frames.TI, 1, identity)
	s.Values["ti_log"] = within(frames.TI, 1, math.Log1p)
	s.Values["luma"] = within(frames.LumaMean, 0, identity)
	s.Values["motion_pan"] = within(frames.MotionPan, 1, math.Abs)
	s.Values["motion_tilt"] = within(frames.MotionTilt, 1, math.Abs)
	s.Values["motion_zoom"] = within(frames.MotionZoom, 1, math.Abs)
	s.Values["motion_shake"] = within(frames.MotionShake, 1, identity)

	return nil
}

// addStats adds the features of the statistics pass, per segment and CRF:
// the logarithms of the bits, of the residual bits and of the motion bits
// per pixel and per second, the PSNR, and the shares of skipped and intra
// blocks.
func (s *Set) addStats(
	stats [][]encode.GOPStats,
	spec Spec,
) error {
	pixels := float64(evenWidth(statsHeight, spec.Width, spec.Height) * statsHeight)

	for c, gops := range stats {
		if len(gops) < s.Segments {
			return fmt.Errorf("%w: %d GOPs in the statistics for %d segments", ErrFrames, len(gops), s.Segments)
		}

		prefix := "x264_" + strconv.FormatFloat(statsCRFs[c], 'f', -1, 64) + "_"
		add := func(name string, value func(g encode.GOPStats) float64) {
			values := make([]float64, s.Segments)
			for i := range values {
				values[i] = value(gops[i])
			}

			s.Values[prefix+name] = values
		}

		perPixelSecond := func(bits int64, g encode.GOPStats) float64 {
			seconds := float64(g.Frames) / spec.Rate

			return math.Log((float64(bits) + 1) / seconds / pixels)
		}

		add("bits", func(g encode.GOPStats) float64 { return perPixelSecond(g.Bits, g) })
		add("texture", func(g encode.GOPStats) float64 { return perPixelSecond(g.Texture, g) })
		add("motion", func(g encode.GOPStats) float64 { return perPixelSecond(g.Motion, g) })
		add("psnr", func(g encode.GOPStats) float64 { return g.PSNR })
		add("skip", func(g encode.GOPStats) float64 { return g.Skip })
		add("intra", func(g encode.GOPStats) float64 { return g.Intra })
	}

	return nil
}

// addScaleLoss adds, per lower resolution, the mean PSNR of the round
// trip through it over the frames kept of each segment.
func (s *Set) addScaleLoss(
	loss [][]float64,
	heights []int,
	perSegment int,
) error {
	if len(heights) == 0 {
		return nil
	}

	for h, frames := range loss {
		values, counts := make([]float64, s.Segments), make([]int, s.Segments)

		for k, psnr := range frames {
			if segment := k * scaleStep / perSegment; segment < s.Segments {
				// A round trip that loses nothing has no finite PSNR.
				values[segment] += min(psnr, maxPSNR)
				counts[segment]++
			}
		}

		for i, n := range counts {
			if n == 0 {
				return fmt.Errorf("%w: no frame of segment %d in the scale loss at %dp", ErrFrames, i, heights[h])
			}

			values[i] /= float64(n)
		}

		s.Values["scale_"+strconv.Itoa(heights[h])] = values
	}

	return nil
}

// maxPSNR caps the PSNR of frames a round trip leaves untouched.
const maxPSNR = 60

// evenWidth is the width of a frame of height keeping the aspect ratio of
// sourceWidth×sourceHeight, rounded to even.
func evenWidth(
	height, sourceWidth, sourceHeight int,
) int {
	return int(math.Round(float64(height)*float64(sourceWidth)/float64(sourceHeight)/2)) * 2
}
