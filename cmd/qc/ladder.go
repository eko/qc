package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/eko/qc/internal/htmlreport"
	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/pipeline"
)

func newLadderCommand(
	env environment,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ladder <source>...",
		Short: "Build a per-title streaming ladder, or one ladder for several videos",
		Long: "Build a per-title adaptive streaming ladder, for one codec or several (-c h264,av1).\n\n" +
			"A digest of segments spread over the title is encoded at several resolutions and CRFs; the\n" +
			"upper envelope of the rate-quality curves gives the rungs, each verified by a real\n" +
			"encode of the digest with its final settings.\n\n" +
			"Several videos get one ladder for all of them (the episodes of a programme): the digest\n" +
			"takes as many segments in each, whatever its length, and every rung is also read video\n" +
			"by video. They must share their resolution, frame rate, bit depth and dynamic range.\n\n" +
			"Several codecs share the analysis and the digest, and are compared at equal quality; the\n" +
			"JSON report then holds the ladders of all of them (\"ladders\"), as qc run writes them.",
		Example: "  qc ladder mezzanine.mov\n" +
			"  qc ladder mezzanine.mov -c av1 --encode-bit-depth 10\n" +
			"  qc ladder mezzanine.mov -c hevc --heights 1080,720,480 --commands\n" +
			"  qc ladder mezzanine.mov -c av1 --encode-ladder renditions/\n" +
			"  qc ladder episode-01.mov episode-02.mov episode-03.mov -c h264,av1",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLadder(cmd, env, args)
		},
	}

	addOutputFlags(cmd)
	addModelFlag(cmd)
	addLadderFlags(cmd)
	addMetricFlags(cmd)
	addGPUFlags(cmd, gpuDecode|gpuEncode|gpuVMAF)
	cmd.Flags().StringP("codec", "c", "h264", "target codec: h264, hevc or av1, or several separated by commas (h264,av1)")

	return cmd
}

// runLadder builds the ladder configured by the flags of cmd for sources,
// one video or several, and prints it.
func runLadder(
	cmd *cobra.Command,
	env environment,
	sources []string,
) error {
	config, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	report, err := executePipeline(cmd, env, config, "ladder", pipeline.Options{
		Source:       sources[0],
		Program:      sources[1:],
		SkipAnalysis: true,
		Codecs:       config.ladderCodecs(),
		Ladder:       ladderOptions(config),
		Renditions:   renditionOptions(config),
	})
	if err != nil {
		return err
	}

	// Several codecs are reported together, as a run reports them.
	if len(report.Ladders) > 1 {
		return emit(cmd.OutOrStdout(), config.Output, report, func(w io.Writer) error {
			return renderLadders(w, report, env.width, config.Ladder.Commands)
		}, htmlreport.RenderRun)
	}

	res := report.Ladders[0]

	return emit(cmd.OutOrStdout(), config.Output, res, func(w io.Writer) error {
		return tui.RenderLadder(w, res, env.width, "", config.Ladder.Commands)
	}, htmlreport.RenderLadder)
}
