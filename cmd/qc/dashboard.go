package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/spf13/cobra"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/overlay"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
)

// job is the work run behind the dashboard, on the services of the command.
type job func(ctx context.Context, svc services, e *tui.Emitter) error

// runDashboard builds the application of config (checking the GPU first)
// and runs fn behind the live dashboard (drawn on stderr in a terminal),
// between the start and the stop of the application.
func runDashboard(
	cmd *cobra.Command,
	env environment,
	config Config,
	name, subject string,
	labels []string,
	fn job,
) (err error) {
	ctx := cmd.Context()

	var svc services

	app := newApp(ctx, config, &svc)
	if err := app.Err(); err != nil {
		return err //nolint:wrapcheck // newApp documents its errors, returned as is
	}

	if err := app.Start(ctx); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	defer func() {
		// Stop even when the run was cancelled, to flush the profile.
		if stopErr := app.Stop(context.WithoutCancel(ctx)); stopErr != nil && err == nil {
			err = fmt.Errorf("stop: %w", stopErr)
		}
	}()

	return tui.RunDashboard(ctx, cmd.ErrOrStderr(), env.dashboard, gpuTitle(name, gpuSettingsOf(config)), subject, labels,
		func(ctx context.Context, e *tui.Emitter) error {
			return fn(ctx, svc, e)
		})
}

// executePipeline runs opts behind the live dashboard and returns the report.
func executePipeline(
	cmd *cobra.Command,
	env environment,
	config Config,
	name string,
	opts pipeline.Options,
) (*pipeline.Report, error) {
	stages := pipeline.Stages(opts)

	labels := make([]string, len(stages))
	for i, s := range stages {
		labels[i] = s.Label
	}

	var report *pipeline.Report

	err := runDashboard(cmd, env, config, name, subject(opts), labels,
		func(ctx context.Context, svc services, e *tui.Emitter) error {
			var err error
			report, err = svc.runner.Run(ctx, opts, hooks(e, stages, 1+len(opts.Program)))

			return err
		})

	return report, err
}

// subject names what the pipeline works on: the source, and how many videos
// share its ladder.
func subject(
	opts pipeline.Options,
) string {
	name := filepath.Base(opts.Source)
	if len(opts.Program) > 0 {
		return fmt.Sprintf("%s + %d more", name, len(opts.Program))
	}

	return name
}

// executeInspection runs the container and bitstream inspection only (no
// frame decoding) behind the live dashboard.
func executeInspection(
	cmd *cobra.Command,
	env environment,
	config Config,
	source string,
	opts analysis.Options,
) (*analysis.Report, error) {
	var report *analysis.Report

	err := runDashboard(cmd, env, config, "analyze", filepath.Base(source), []string{"Inspect"},
		func(ctx context.Context, svc services, e *tui.Emitter) error {
			e.Start(0)

			opts.SkipVideo = true

			var err error
			if report, err = svc.analyzer.Analyze(ctx, source, opts); err != nil {
				return fmt.Errorf("inspect: %w", err)
			}

			e.Done(0, tui.InspectionSummary(report))

			return nil
		})

	return report, err
}

// hooks maps pipeline events to dashboard updates; videos is the number of
// videos the ladders are built for.
func hooks(
	e *tui.Emitter,
	stages []pipeline.Stage,
	videos int,
) pipeline.Hooks {
	var (
		mu     sync.Mutex
		probes = map[int][]ladder.Probe{}
		rungs  = map[int][]ladder.Rung{}
	)

	return pipeline.Hooks{
		Start: func(i int) {
			if stages[i].Kind == pipeline.KindLadder {
				e.Unit(i, "encodes")
			}

			e.Start(i)
		},
		Done: func(i int, r pipeline.StageResult) { e.Done(i, tui.StageSummary(r)) },
		Analysis: func(i int, p analysis.Progress) {
			if p.Stage == analysis.StageDecode {
				e.Progress(i, p.Done, p.Total, "")
			}
		},
		Quality: func(i int, p quality.Progress) { qualityProgress(e, i, p) },
		Overlay: func(i int, p overlay.Progress) { e.Progress(i, p.Done, p.Total, "") },
		Renditions: func(i int, p ladder.RenditionProgress) {
			detail := ""
			if p.Rendition != nil {
				detail = p.Rendition.Name()
			}

			e.Progress(i, p.Done, p.Total, detail)
		},
		Ladder: func(i int, p ladder.Progress) {
			mu.Lock()
			defer mu.Unlock()

			// Anchors are encodes at another preset than the probes': they
			// would not sit on the probes' curves.
			if p.Probe != nil && p.Stage != ladder.StageAnchor {
				probes[i] = append(probes[i], *p.Probe)
			}

			if p.Rung != nil {
				rungs[i] = append(rungs[i], *p.Rung)
			}

			e.Progress(i, p.Done, p.Total, ladderStageLabel(p.Stage))
			e.Panel(i, tui.LadderPanel{
				Stage:  p.Stage,
				Done:   p.Done,
				Total:  p.Total,
				Probes: append([]ladder.Probe(nil), probes[i]...),
				Rungs:  append([]ladder.Rung(nil), rungs[i]...),
				Videos: videos,
			})
		},
	}
}

// qualityProgress shows the progress of VMAF stage i, and its estimate once
// there is one.
func qualityProgress(
	e *tui.Emitter,
	i int,
	p quality.Progress,
) {
	// A fixed budget is a single round, named in the stage label.
	detail := ""
	if p.Mode == quality.ModeSampled && p.Sample.IsZero() && p.Round > 0 {
		detail = fmt.Sprintf("round %d", p.Round)
	}

	e.Progress(i, p.FramesScored, p.FramesTotal, detail)

	if p.Estimated {
		e.Panel(i, tui.VMAFPanel{Progress: p})
	}
}

// ladderStageLabel is the dashboard label of a ladder stage.
func ladderStageLabel(
	stage string,
) string {
	switch stage {
	case ladder.StageAnalysis:
		return "source analysis"
	case ladder.StageDigest:
		return "digest"
	case ladder.StageProbe:
		return "probe encodes"
	case ladder.StageAnchor:
		return "anchoring"
	case ladder.StageVerify:
		return "verification"
	case ladder.StageShots:
		return "per-shot"
	case ladder.StageGrain:
		return "film grain"
	}

	return ""
}
