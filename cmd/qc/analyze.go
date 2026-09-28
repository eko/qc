package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/htmlreport"
	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/pipeline"
)

func newAnalyzeCommand(
	env environment,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze <file>",
		Short: "Technical analysis of a video file",
		Long: "Technical analysis of a video file.\n\n" +
			"Reads the container and the bitstream (bitrate over time, GOP structure, HDR\n" +
			"metadata), then decodes the frames once to measure SI/TI, shots, black and\n" +
			"frozen segments, letterboxing and luma levels. --fast skips the decoding.\n" +
			"--overlay also writes a copy of the video with the analysis burnt in.",
		Example: "  qc analyze video.mp4\n" +
			"  qc analyze video.mp4 --fast -f json\n" +
			"  qc analyze video.mp4 -o report.json --html report.html\n" +
			"  qc analyze video.mp4 --overlay annotated.mp4 --overlay-height 720",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			report, err := analyze(cmd, env, config, args[0])
			if err != nil {
				return err
			}

			return emit(cmd.OutOrStdout(), config.Output, report, func(w io.Writer) error {
				return tui.RenderReport(w, report, env.width, "")
			}, htmlreport.RenderAnalysis)
		},
	}

	addOutputFlags(cmd)
	addAnalysisFlags(cmd)
	addOverlayFlags(cmd)
	addGPUFlags(cmd, gpuDecode)

	return cmd
}

// analyze runs the technical analysis of source, without decoding in fast
// mode, and writes its annotated copy with --overlay.
func analyze(
	cmd *cobra.Command,
	env environment,
	config Config,
	source string,
) (*analysis.Report, error) {
	overlayOpts, err := overlayOptions(config, source)
	if err != nil {
		return nil, err
	}

	if config.Analysis.Fast && overlayOpts.Output == "" {
		return executeInspection(cmd, env, config, source, analysisOptions(config.Analysis))
	}

	report, err := executePipeline(cmd, env, config, "analyze", pipeline.Options{
		Source:   source,
		Analysis: analysisOptions(config.Analysis),
		// A fast annotated copy shows what the bitstream tells.
		SkipAnalysis: config.Analysis.Fast,
		Overlay:      overlayOpts,
	})
	if err != nil {
		return nil, err
	}

	return report.Analysis, nil
}
