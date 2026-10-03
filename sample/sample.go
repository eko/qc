// Package sample extracts a sample from one or several videos: a file made
// of scenes of each, chosen on their spatial and temporal information (the
// most complex ones, representative ones, a mix of both, or the easiest),
// copied from the sources without re-encoding.
//
// The scenes are whole GOPs: a stream is only copied from a keyframe to the
// next. The sample is therefore about as long as asked, not exactly, and
// its frames are the sources' own, bit for bit.
package sample

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
)

var (
	// ErrNoSource is returned when no video is given.
	ErrNoSource = errors.New("no source")
	// ErrInvalidSource is returned for a source without a usable video.
	ErrInvalidSource = errors.New("no usable video stream")
	// ErrFormat is returned when the sources cannot be copied into one
	// stream: another codec, resolution, frame rate, bit depth or dynamic
	// range.
	ErrFormat = errors.New("the videos of a sample must share their codec and format (they are copied, not re-encoded)")
	// ErrOptions is returned for options out of range.
	ErrOptions = errors.New("invalid sample options")
	// ErrDestination is returned when the sample would overwrite a source.
	ErrDestination = errors.New("the sample cannot be written over a source")
	// ErrNoKeyframe is returned for a source whose frames show no keyframe
	// to cut at.
	ErrNoKeyframe = errors.New("no keyframe to cut at")
)

// Scenes is which scenes of the videos a sample takes.
type Scenes string

const (
	// ScenesTop takes the most complex scenes: the highest spatial ×
	// temporal information, what costs an encoder most.
	ScenesTop Scenes = "top"
	// ScenesMixed takes the most complex scenes for a share of the sample
	// (Options.TopShare) and representative scenes for the rest.
	ScenesMixed Scenes = "mixed"
	// ScenesAverage takes scenes spread over each video that together
	// have its spatial and temporal information: what the video is on
	// average.
	ScenesAverage Scenes = "average"
	// ScenesEasy takes the easiest scenes: the lowest spatial × temporal
	// information.
	ScenesEasy Scenes = "easy"
)

// AllScenes lists the choices of scenes, in the order they are offered.
func AllScenes() []Scenes {
	return []Scenes{ScenesTop, ScenesMixed, ScenesAverage, ScenesEasy}
}

// Defaults of Options.
const (
	DefaultDuration = 60 * media.Duration(time.Second)
	DefaultPiece    = 2 * media.Duration(time.Second)
	DefaultTopShare = 0.5
)

// Stages reported through Options.Progress.
const (
	StageInspect  = "inspect"
	StageAnalysis = "analysis"
	StageExtract  = "extract"
	StageVerify   = "verify"
)

// Progress reports the advancement of an extraction: Done out of Total
// units of Stage (frames analysed, scenes copied).
type Progress struct {
	Stage       string
	Done, Total int
}

// Options configures Engine.Extract. The zero value takes a minute of
// mixed scenes.
type Options struct {
	// Duration is the length asked of the sample, shared equally between
	// the videos. Default one minute.
	Duration media.Duration
	// Scenes is which scenes are taken. Default ScenesMixed.
	Scenes Scenes
	// TopShare is the share of a mixed sample given to the most complex
	// scenes, between 0 and 1 excluded. Default a half.
	TopShare float64
	// Piece is the least length of a scene taken: GOPs are joined until
	// they reach it. Default 2 s.
	Piece media.Duration
	// Progress is called as the extraction advances, never concurrently.
	Progress func(Progress)
}

// withDefaults fills the unset options and checks the others.
func (o Options) withDefaults() (Options, error) {
	if o.Duration == 0 {
		o.Duration = DefaultDuration
	}

	if o.Scenes == "" {
		o.Scenes = ScenesMixed
	}

	if o.TopShare == 0 {
		o.TopShare = DefaultTopShare
	}

	if o.Piece == 0 {
		o.Piece = DefaultPiece
	}

	switch {
	case o.Duration < 0 || o.Piece < 0:
		return o, fmt.Errorf("sample: %w: durations must be positive", ErrOptions)
	case o.TopShare <= 0 || o.TopShare >= 1 || math.IsNaN(o.TopShare):
		return o, fmt.Errorf("sample: %w: the share of top scenes must be between 0 and 1", ErrOptions)
	}

	for _, known := range AllScenes() {
		if o.Scenes == known {
			return o, nil
		}
	}

	return o, fmt.Errorf("sample: %w: unknown scenes %q (top, mixed, average or easy)", ErrOptions, o.Scenes)
}

// report calls Progress when set.
func (o Options) report(
	p Progress,
) {
	if o.Progress != nil {
		o.Progress(p)
	}
}

// Inspector inspects and analyses the sources (*analysis.Analyzer).
type Inspector interface {
	Analyze(
		ctx context.Context,
		path string,
		opts analysis.Options,
	) (*analysis.Report, error)
}

// Cutter copies scenes of videos into one file, without re-encoding, and
// checks that a file decodes (*encode.FFmpeg).
type Cutter interface {
	Copy(
		ctx context.Context,
		spec encode.CopySpec,
	) error
	Decodes(
		ctx context.Context,
		path string,
	) error
}

// Engine extracts samples.
type Engine struct {
	inspector Inspector
	cutter    Cutter
}

// NewEngine returns an Engine reading the sources with inspector and
// writing samples with cutter.
func NewEngine(
	inspector Inspector,
	cutter Cutter,
) *Engine {
	return &Engine{inspector: inspector, cutter: cutter}
}

// source is a video of the sample.
type source struct {
	path       string
	inspection *analysis.Report
	video      media.VideoStream
	duration   media.Duration
}

// Extract writes to destination a sample of sources, as opts ask: every
// video gives the same length, in scenes that are whole GOPs, copied as
// they are, the most complex first when complex scenes are asked for (see
// Result.Order). A video shorter than its share is taken whole. The sources must
// share their codec and format. The sample is then read back: its frame
// count against the scenes taken, and a decode of every frame.
func (e *Engine) Extract(
	ctx context.Context,
	sources []string,
	destination string,
	opts Options,
) (*Result, error) {
	started := time.Now()

	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}

	if len(sources) == 0 {
		return nil, fmt.Errorf("sample: %w", ErrNoSource)
	}

	if err := distinct(sources, destination); err != nil {
		return nil, err
	}

	videos, err := e.inspect(ctx, sources, opts)
	if err != nil {
		return nil, err
	}

	res := &Result{
		SchemaVersion: analysis.SchemaVersion,
		GeneratedAt:   started.UTC(),
		Path:          destination,
		Scenes:        opts.Scenes,
		Asked:         opts.Duration,
	}

	if opts.Scenes == ScenesMixed {
		res.TopShare = opts.TopShare
	}

	if err := e.plan(ctx, videos, opts, res); err != nil {
		return nil, err
	}

	if err := e.write(ctx, videos, opts, res); err != nil {
		return nil, err
	}

	res.Elapsed = media.Duration(time.Since(started))

	return res, nil
}

// distinct refuses a destination that is one of the sources.
func distinct(
	sources []string,
	destination string,
) error {
	target, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("sample: %w", err)
	}

	for _, s := range sources {
		if abs, err := filepath.Abs(s); err == nil && abs == target {
			return fmt.Errorf("sample: %w: %s", ErrDestination, destination)
		}
	}

	return nil
}

// inspect inspects the sources and checks that they can share a stream.
func (e *Engine) inspect(
	ctx context.Context,
	sources []string,
	opts Options,
) ([]source, error) {
	videos := make([]source, 0, len(sources))

	for i, path := range sources {
		opts.report(Progress{Stage: StageInspect, Done: i, Total: len(sources)})

		inspection, err := e.inspector.Analyze(ctx, path, analysis.Options{SkipVideo: true})
		if err != nil {
			return nil, fmt.Errorf("sample: inspect %s: %w", path, err)
		}

		video, duration, err := usableVideo(inspection)
		if err != nil {
			return nil, fmt.Errorf("sample: %w (%s)", err, path)
		}

		if i > 0 {
			if diff := formatDifference(videos[0].video, video); diff != "" {
				return nil, fmt.Errorf("sample: %w: %s is %s, %s is not", ErrFormat, videos[0].path, diff, path)
			}
		}

		videos = append(videos, source{path: path, inspection: inspection, video: video, duration: duration})
	}

	return videos, nil
}

// usableVideo returns the video stream of an inspection, with its frame
// rate and the duration of the file.
func usableVideo(
	report *analysis.Report,
) (media.VideoStream, media.Duration, error) {
	if report == nil || report.Info == nil {
		return media.VideoStream{}, 0, ErrInvalidSource
	}

	video, ok := report.Info.PrimaryVideo()
	if !ok || video.Width <= 0 || video.Height <= 0 {
		return media.VideoStream{}, 0, ErrInvalidSource
	}

	if video.AvgFrameRate.Float() <= 0 {
		video.AvgFrameRate = video.FrameRate
	}

	duration := max(report.Info.Duration, video.Duration)
	if video.AvgFrameRate.Float() <= 0 || duration <= 0 {
		return media.VideoStream{}, 0, fmt.Errorf("%w: unknown frame rate or duration", ErrInvalidSource)
	}

	return video, duration, nil
}

// formatDifference names what the first video is that the other is not,
// among what a copied stream needs alike: the codec, and what one ladder
// for both needs (ladder.FormatDifference).
func formatDifference(
	first, other media.VideoStream,
) string {
	if first.Codec != other.Codec {
		return first.Codec
	}

	return ladder.FormatDifference(first, other)
}
