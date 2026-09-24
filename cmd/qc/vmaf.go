package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/eko/qc/internal/htmlreport"
	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/pipeline"
)

func newVMAFCommand(
	env environment,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vmaf <reference> <distorted>",
		Short: "Measure the VMAF of a distorted video against its reference",
		Long: "Measure the VMAF of a distorted video against its reference.\n\n" +
			"By default short clips are sampled across the timeline until the 95% confidence\n" +
			"interval of the mean is narrower than --precision. --sample fixes the budget\n" +
			"instead (5%: that share of the frames, 2/scene: two clips per scene) and reports\n" +
			"the interval it reaches. --exact scores every frame.\n" +
			"Both videos are scaled to the resolution of the model.",
		Example: "  qc vmaf mezzanine.mov encode.mp4\n" +
			"  qc vmaf mezzanine.mov encode.mp4 --precision 0.25\n" +
			"  qc vmaf mezzanine.mov encode.mp4 --sample 5%\n" +
			"  qc vmaf mezzanine.mov encode.mp4 --exact -f json > exact.json",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			report, err := executePipeline(cmd, env, config, "vmaf", pipeline.Options{
				Source:       args[1],
				Reference:    args[0],
				SkipAnalysis: true,
				Quality:      qualityOptions(config),
			})
			if err != nil {
				return err
			}

			return emit(cmd.OutOrStdout(), config.Output, report.Comparison, func(w io.Writer) error {
				return tui.RenderComparison(w, report.Comparison, env.width, "")
			}, htmlreport.RenderComparison)
		},
	}

	addOutputFlags(cmd)
	addQualityFlags(cmd)
	addGPUFlags(cmd, gpuDecode|gpuVMAF)

	return cmd
}
