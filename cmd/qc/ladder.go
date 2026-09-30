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
		Use:   "ladder <source>",
		Short: "Build a per-title streaming ladder for one codec",
		Long: "Build a per-title adaptive streaming ladder for one codec.\n\n" +
			"A digest of evenly spaced segments is encoded at several resolutions and CRFs; the\n" +
			"upper envelope of the rate-quality curves gives the rungs, each verified by a real\n" +
			"encode of the digest with its final settings.",
		Example: "  qc ladder mezzanine.mov\n" +
			"  qc ladder mezzanine.mov -c av1 --encode-bit-depth 10\n" +
			"  qc ladder mezzanine.mov -c hevc --heights 1080,720,480 --commands\n" +
			"  qc ladder mezzanine.mov -c av1 --encode-ladder renditions/",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			report, err := executePipeline(cmd, env, config, "ladder", pipeline.Options{
				Source:       args[0],
				SkipAnalysis: true,
				Codecs:       []string{config.Ladder.Codec},
				Ladder:       ladderOptions(config),
				Renditions:   renditionOptions(config),
			})
			if err != nil {
				return err
			}

			res := report.Ladders[0]

			return emit(cmd.OutOrStdout(), config.Output, res, func(w io.Writer) error {
				return tui.RenderLadder(w, res, env.width, "", config.Ladder.Commands)
			}, htmlreport.RenderLadder)
		},
	}

	addOutputFlags(cmd)
	addModelFlag(cmd)
	addLadderFlags(cmd)
	addMetricFlags(cmd)
	addGPUFlags(cmd, gpuDecode|gpuEncode|gpuVMAF)
	cmd.Flags().StringP("codec", "c", "h264", "target codec: h264, hevc or av1")

	return cmd
}
