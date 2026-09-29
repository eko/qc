// Package pipeline chains every analysis of a title: technical analysis,
// VMAF against a reference and streaming ladders, reporting each stage.
package pipeline

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/overlay"
	"github.com/eko/qc/quality"
)

// ErrNothingToDo is returned when every stage is disabled.
var ErrNothingToDo = errors.New("pipeline: nothing to do")

// ErrNoOverlayer is returned when an overlay is requested from a Runner
// built without an Overlayer (see WithOverlayer).
var ErrNoOverlayer = errors.New("pipeline: no overlayer configured")

// Options selects the stages. The zero value analyses the source only.
type Options struct {
	// Source is the video analysed, the distorted side of the VMAF
	// comparison, and the source of the ladders unless LadderSource is set.
	Source string
	// Reference, when set, measures VMAF of Source against it.
	Reference string
	// LadderSource, when set, is the video the ladders are built from
	// instead of Source: typically the mezzanine (the Reference) when
	// Source is an encode under test. It is not analysed by the run.
	LadderSource string
	// SkipAnalysis skips the decoded-frame analysis (the container and
	// bitstream inspection always runs).
	SkipAnalysis bool
	// Codecs builds one ladder per codec (h264, hevc, av1).
	Codecs   []string
	Analysis analysis.Options
	Quality  quality.Options
	Ladder   ladder.Options
	// Overlay, when its Output is set, writes an annotated copy of Source
	// once it is analysed and compared: see OverlayOptions.
	Overlay OverlayOptions
}

// OverlayOptions configures the annotated copy of the source: the frame
// analysis (or the inspection alone, with SkipAnalysis) and the comparison
// burnt into its frames as a debug overlay (see package overlay).
type OverlayOptions struct {
	// Output is the file written; empty writes no copy.
	Output string
	// Render tunes the overlay and its encode.
	Render overlay.RenderOptions
}

// Report gathers the results of a run.
type Report struct {
	SchemaVersion int                  `json:"schemaVersion"`
	GeneratedAt   time.Time            `json:"generatedAt"`
	Analysis      *analysis.Report     `json:"analysis"`
	Comparison    *analysis.Comparison `json:"comparison,omitempty"`
	Ladders       []*ladder.Result     `json:"ladders,omitempty"`
	Elapsed       string               `json:"elapsed"`
}

// StageResult is what a stage produced, for the Done hook: the domain
// result of the stage's kind, the other fields nil. Presenters word it (the
// CLI's dashboard shows a one-line summary).
type StageResult struct {
	Stage Stage
	// Analysis is the inspection (KindInspect) or the frame analysis
	// (KindAnalysis) of the source.
	Analysis *analysis.Report
	// Comparison is the VMAF measurement (KindVMAF).
	Comparison *analysis.Comparison
	// Ladder is the ladder built (KindLadder), with at least one rung.
	Ladder *ladder.Result
	// Overlay is the annotated copy written (KindOverlay).
	Overlay string
}

// Stage identifies a step of the pipeline.
type Stage struct {
	Kind  string
	Label string
	// Codec is set on ladder stages.
	Codec string
}

// Stage kinds.
const (
	KindInspect  = "inspect"
	KindAnalysis = "analysis"
	KindVMAF     = "vmaf"
	KindLadder   = "ladder"
	KindOverlay  = "overlay"
)

// Hooks receive the pipeline events. Every field is optional; i is the
// index of the stage in Stages(opts). Progress hooks may be called from
// worker goroutines and must be fast.
type Hooks struct {
	// Start is called when stage i starts.
	Start func(i int)
	// Done is called when stage i succeeds, with its result.
	Done func(i int, r StageResult)
	// Analysis reports the progress of the frame analysis.
	Analysis func(i int, p analysis.Progress)
	// Quality reports the progress of the VMAF measurement.
	Quality func(i int, p quality.Progress)
	// Ladder reports the progress of a ladder.
	Ladder func(i int, p ladder.Progress)
	// Overlay reports the progress of the annotated copy.
	Overlay func(i int, p overlay.Progress)
}

// Analyzer inspects, analyses and compares files. *analysis.Analyzer
// implements it.
type Analyzer interface {
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

// LadderBuilder builds streaming ladders. *ladder.Engine implements it.
type LadderBuilder interface {
	Build(
		ctx context.Context,
		source string,
		opts ladder.Options,
	) (*ladder.Result, error)
}

// Overlayer writes annotated copies of videos. *overlay.Renderer implements
// it.
type Overlayer interface {
	Render(
		ctx context.Context,
		source, output string,
		in overlay.Input,
		opts overlay.RenderOptions,
	) error
}

// Runner runs pipelines.
type Runner struct {
	analyzer Analyzer
	ladders  LadderBuilder
	// overlayer is nil unless WithOverlayer set it.
	overlayer Overlayer
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithOverlayer lets the Runner write annotated copies
// (Options.Overlay).
func WithOverlayer(
	o Overlayer,
) RunnerOption {
	return func(r *Runner) { r.overlayer = o }
}

// NewRunner returns a Runner. ladders is only used when ladders are
// requested.
func NewRunner(
	analyzer Analyzer,
	ladders LadderBuilder,
	opts ...RunnerOption,
) *Runner {
	r := &Runner{analyzer: analyzer, ladders: ladders}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Stages lists the stages opts will run, in order.
func Stages(
	opts Options,
) []Stage {
	stages := []Stage{{Kind: KindInspect, Label: "Inspect"}}

	if !opts.SkipAnalysis {
		stages = append(stages, Stage{Kind: KindAnalysis, Label: "Frame analysis"})
	}

	if opts.Reference != "" {
		label := "VMAF"
		if sample := opts.Quality.Sample; !sample.IsZero() {
			label += " · " + sample.String()
		}

		stages = append(stages, Stage{Kind: KindVMAF, Label: label})
	}

	// The copy shows the analysis and the comparison, and comes before the
	// ladders, which take longer.
	if opts.Overlay.Output != "" {
		stages = append(stages, Stage{Kind: KindOverlay, Label: "Overlay"})
	}

	for _, codec := range opts.Codecs {
		stages = append(stages, Stage{Kind: KindLadder, Label: "Ladder · " + codec, Codec: codec})
	}

	return stages
}

// elapsedPrecision rounds Report.Elapsed: finer digits are noise for a run
// that takes seconds to minutes.
const elapsedPrecision = 10 * time.Millisecond

// errNoRungs fails a ladder stage whose ladder has no rung: there is
// nothing to encode, and a run must not end as if it succeeded.
var errNoRungs = errors.New("ladder has no rungs")

// Run executes the stages of opts, calling hooks as they start and finish.
// On failure it returns the partial report along with the error, prefixed
// with the label of the failed stage.
func (r *Runner) Run(
	ctx context.Context,
	opts Options,
	hooks Hooks,
) (*Report, error) {
	started := time.Now()
	stages := Stages(opts)

	// The inspection alone is only a preamble of the other stages.
	if len(stages) == 1 {
		return nil, ErrNothingToDo
	}

	rep := &Report{SchemaVersion: analysis.SchemaVersion, GeneratedAt: started.UTC()}

	for i, stage := range stages {
		hooks.start(i)

		result, err := r.runStage(ctx, i, stage, opts, hooks, rep)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", stage.Label, err)
		}

		result.Stage = stage
		hooks.done(i, result)
	}

	rep.Elapsed = time.Since(started).Round(elapsedPrecision).String()

	return rep, nil
}

// runStage runs stage i, stores its result in rep and returns it for the
// Done hook.
func (r *Runner) runStage(
	ctx context.Context,
	i int,
	stage Stage,
	opts Options,
	hooks Hooks,
	rep *Report,
) (StageResult, error) {
	switch stage.Kind {
	case KindInspect:
		return r.inspect(ctx, opts, rep)
	case KindAnalysis:
		return r.analyze(ctx, opts, rep, func(p analysis.Progress) { hooks.analysis(i, p) })
	case KindVMAF:
		return r.compare(ctx, opts, rep, func(p quality.Progress) { hooks.quality(i, p) })
	case KindLadder:
		return r.ladder(ctx, stage.Codec, opts, rep, func(p ladder.Progress) { hooks.ladder(i, p) })
	case KindOverlay:
		return r.overlay(ctx, opts, rep, func(p overlay.Progress) { hooks.overlay(i, p) })
	default:
		return StageResult{}, fmt.Errorf("unknown stage %q", stage.Kind)
	}
}

// inspect reads the container and bitstream of the source (no decoding).
func (r *Runner) inspect(
	ctx context.Context,
	opts Options,
	rep *Report,
) (StageResult, error) {
	aopts := opts.Analysis
	aopts.SkipVideo = true
	// The frame analysis that follows reads the first frame's HDR metadata
	// while it decodes, and analyses the audio: the inspection need not
	// wait for the first, nor do the second.
	aopts.DeferHDRMetadata = !opts.SkipAnalysis
	aopts.Audio.WithInspection = opts.Analysis.Audio.WithInspection && opts.SkipAnalysis

	report, err := r.analyzer.Analyze(ctx, opts.Source, aopts)
	if err != nil {
		return StageResult{}, err
	}

	rep.Analysis = report

	return StageResult{Analysis: report}, nil
}

// analyze runs the full technical analysis of the source; its report
// replaces the inspection's.
func (r *Runner) analyze(
	ctx context.Context,
	opts Options,
	rep *Report,
	progress func(analysis.Progress),
) (StageResult, error) {
	aopts := opts.Analysis
	// This stage is the frame analysis: Options.SkipAnalysis disables it.
	aopts.SkipVideo = false
	aopts.Progress = progress

	report, err := r.analyzer.Analyze(ctx, opts.Source, aopts)
	if err != nil {
		return StageResult{}, err
	}

	rep.Analysis = report

	return StageResult{Analysis: report}, nil
}

// compare measures VMAF of the source against the reference.
func (r *Runner) compare(
	ctx context.Context,
	opts Options,
	rep *Report,
	progress func(quality.Progress),
) (StageResult, error) {
	qopts := opts.Quality
	qopts.Progress = progress

	// The source is already inspected (and analysed, unless skipped): the
	// comparison reuses it, and takes its shot cuts for scene budgets.
	comparison, err := r.analyzer.Compare(ctx, opts.Reference, opts.Source, analysis.CompareOptions{
		Bitstream: opts.Analysis.Bitstream,
		Quality:   qopts,
		Distorted: rep.Analysis,
	})
	if err != nil {
		return StageResult{}, err
	}

	rep.Comparison = comparison

	return StageResult{Comparison: comparison}, nil
}

// ladder builds the ladder for codec, from the ladder source.
func (r *Runner) ladder(
	ctx context.Context,
	codec string,
	opts Options,
	rep *Report,
	progress func(ladder.Progress),
) (StageResult, error) {
	lopts := opts.Ladder
	lopts.Codec = codec
	lopts.Progress = progress

	if opts.LadderSource == "" {
		// The ladder is built on the analysed source: its per-shot rungs
		// read the shots of that analysis rather than decoding it again.
		lopts.Analysis = rep.Analysis

		if lopts.ContentLight == nil {
			lopts.ContentLight = measuredLight(rep.Analysis)
		}
	}

	res, err := r.ladders.Build(ctx, cmp.Or(opts.LadderSource, opts.Source), lopts)
	if err != nil {
		return StageResult{}, err
	}

	if len(res.Rungs) == 0 {
		return StageResult{}, errNoRungs
	}

	rep.Ladders = append(rep.Ladders, res)

	return StageResult{Ladder: res}, nil
}

// overlay writes the annotated copy of the source: its analysis, and its
// comparison when one ran.
func (r *Runner) overlay(
	ctx context.Context,
	opts Options,
	rep *Report,
	progress func(overlay.Progress),
) (StageResult, error) {
	if r.overlayer == nil {
		return StageResult{}, ErrNoOverlayer
	}

	in := overlay.Input{Report: rep.Analysis}
	if rep.Comparison != nil {
		in.Quality = rep.Comparison.VMAF
	}

	ropts := opts.Overlay.Render
	ropts.Progress = progress

	if err := r.overlayer.Render(ctx, opts.Source, opts.Overlay.Output, in, ropts); err != nil {
		return StageResult{}, err
	}

	return StageResult{Overlay: opts.Overlay.Output}, nil
}

// measuredLight is the content light level the frame analysis measured on
// an HDR source, for its encodes to carry when the source signals none:
// the robust MaxCLL (the strict maximum of a 4:2:0 source overshoots, see
// light.RobustPercentile) and MaxFALL. It is nil without a light analysis.
func measuredLight(
	report *analysis.Report,
) *media.ContentLightLevel {
	if report == nil || report.Video == nil || report.Video.Light == nil {
		return nil
	}

	l := report.Video.Light

	return &media.ContentLightLevel{MaxCLL: int(math.Round(l.MaxCLLRobust)), MaxFALL: int(math.Round(l.MaxFALL))}
}

// The helpers below call a hook when it is set.

func (h Hooks) start(i int) {
	if h.Start != nil {
		h.Start(i)
	}
}

func (h Hooks) done(i int, r StageResult) {
	if h.Done != nil {
		h.Done(i, r)
	}
}

func (h Hooks) analysis(i int, p analysis.Progress) {
	if h.Analysis != nil {
		h.Analysis(i, p)
	}
}

func (h Hooks) quality(i int, p quality.Progress) {
	if h.Quality != nil {
		h.Quality(i, p)
	}
}

func (h Hooks) ladder(i int, p ladder.Progress) {
	if h.Ladder != nil {
		h.Ladder(i, p)
	}
}

func (h Hooks) overlay(i int, p overlay.Progress) {
	if h.Overlay != nil {
		h.Overlay(i, p)
	}
}
