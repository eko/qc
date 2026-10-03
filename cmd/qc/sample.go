package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/media"
	"github.com/eko/qc/sample"
)

// ErrNoSampleFile is returned when the sample command is not told where
// to write.
var ErrNoSampleFile = errors.New("--to is required: the file the sample is written to")

func newSampleCommand(
	env environment,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sample <source>...",
		Short: "Extract a sample of one or several videos: their most complex, representative or easiest scenes",
		Long: "Extract a sample of one or several videos into one file, without re-encoding.\n\n" +
			"Every video gives the same length of scenes, chosen on its spatial and temporal\n" +
			"information (a frame analysis of each video):\n\n" +
			"  top      the most complex scenes: what costs an encoder most\n" +
			"  mixed    the most complex scenes for a share of the sample (--top-share), and\n" +
			"           representative scenes for the rest\n" +
			"  average  scenes spread over the video that together have its spatial and\n" +
			"           temporal information: what the video is on average\n" +
			"  easy     the easiest scenes\n\n" +
			"The scenes are copied from the sources as they are: whole GOPs, from a keyframe\n" +
			"to the next. The sample is therefore about as long as --duration, and its frames\n" +
			"are the sources' own. The videos must share their codec, resolution, frame rate,\n" +
			"bit depth and dynamic range. The sample holds the video alone, without audio.",
		Example: "  qc sample episode-01.mov episode-02.mov episode-03.mov --to sample.mkv\n" +
			"  qc sample film.mp4 --to hardest.mp4 --duration 30 --scenes top\n" +
			"  qc sample a.mov b.mov --to mix.mov --duration 120 --scenes mixed --top-share 0.3",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSample(cmd, env, args)
		},
	}

	flags := cmd.Flags()
	flags.StringP("format", "f", formatText, "stdout format: text or json")
	flags.StringP("output", "o", "", "also write the full JSON report to this file")
	flags.String("to", "", "the file the sample is written to; its extension picks the container (.mkv takes any codec)")
	flags.Float64("duration", sample.DefaultDuration.Seconds(), "length of the sample in seconds, shared equally between the videos")
	flags.String("scenes", string(sample.ScenesMixed), "scenes taken: top, mixed, average or easy")
	flags.Float64("top-share", sample.DefaultTopShare, "share of a mixed sample given to the most complex scenes (between 0 and 1)")
	flags.Float64("piece", sample.DefaultPiece.Seconds(), "least length of a scene in seconds: GOPs are joined until they reach it")
	addGPUFlags(cmd, gpuDecode)

	return cmd
}

// sampleOptions maps the sample flags to the options of the engine.
func sampleOptions(
	config SampleConfig,
) sample.Options {
	return sample.Options{
		Duration: media.Seconds(config.Duration),
		Scenes:   sample.Scenes(strings.ToLower(strings.TrimSpace(config.Scenes))),
		TopShare: config.TopShare,
		Piece:    media.Seconds(config.Piece),
	}
}

// sampleStages are the dashboard stages of an extraction, in order.
var sampleStages = []string{sample.StageInspect, sample.StageAnalysis, sample.StageExtract, sample.StageVerify}

// sampleLabels name the stages on the dashboard.
var sampleLabels = []string{"Inspect", "Frame analysis", "Copy scenes", "Check the sample"}

// runSample extracts the sample configured by the flags of cmd from
// sources and prints what it took.
func runSample(
	cmd *cobra.Command,
	env environment,
	sources []string,
) error {
	config, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	if strings.TrimSpace(config.Sample.To) == "" {
		return ErrNoSampleFile
	}

	subject := filepath.Base(sources[0])
	if len(sources) > 1 {
		subject = fmt.Sprintf("%s + %d more", subject, len(sources)-1)
	}

	var res *sample.Result

	err = runDashboard(cmd, env, config, "sample", subject, sampleLabels,
		func(ctx context.Context, svc services, e *tui.Emitter) error {
			opts := sampleOptions(config.Sample)
			opts.Progress = sampleProgress(e)

			var err error
			if res, err = svc.sampler.Extract(ctx, sources, config.Sample.To, opts); err != nil {
				return err
			}

			e.Done(len(sampleStages)-1, tui.SampleSummary(res))

			return nil
		})
	if err != nil {
		return err
	}

	return emit(cmd.OutOrStdout(), config.Output, res, func(w io.Writer) error {
		return tui.RenderSample(w, res)
	}, func(io.Writer, *sample.Result) error { return nil })
}

// stageEmitter is what the progress of an extraction drives of the
// dashboard (*tui.Emitter).
type stageEmitter interface {
	Start(i int)
	Done(i int, summary string)
	Progress(i, done, total int, detail string)
}

// sampleProgress maps the progress of an extraction to dashboard updates:
// a stage starts when its first progress arrives, and the stages before it
// are then done.
func sampleProgress(
	e stageEmitter,
) func(sample.Progress) {
	current := -1

	return func(p sample.Progress) {
		stage := 0
		for i, name := range sampleStages {
			if name == p.Stage {
				stage = i
			}
		}

		for current < stage {
			if current >= 0 {
				e.Done(current, "")
			}

			current++
			e.Start(current)
		}

		if p.Total > 0 {
			e.Progress(stage, p.Done, p.Total, "")
		}
	}
}
