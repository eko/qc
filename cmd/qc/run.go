package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport"
	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/pipeline"
)

func newRunCommand(
	env environment,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <source>",
		Short: "Run everything: technical analysis, VMAF against a reference, ladders",
		Long: "Run everything on one source, behind one live dashboard: the technical analysis,\n" +
			"the VMAF of the source against a reference (with -r) and one ladder per codec.\n" +
			"-o and --html write one combined report; --overlay a copy of the source with the\n" +
			"analysis (and the VMAF of each scored frame) burnt in.",
		Example: "  qc run mezzanine.mov --codecs h264,av1 --html report.html\n" +
			"  qc run encode.mp4 --reference mezzanine.mov --codecs=\n" +
			"  qc run mezzanine.mov --skip-analysis --codecs hevc -o ladders.json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEverything(cmd, env, args[0])
		},
	}

	addOutputFlags(cmd)
	addQualityFlags(cmd)
	addLadderFlags(cmd)
	addOverlayFlags(cmd)
	addAudioFlags(cmd)
	addGPUFlags(cmd, gpuDecode|gpuEncode|gpuVMAF)

	flags := cmd.Flags()
	flags.StringP("reference", "r", "", "reference video: also measure the VMAF of the source against it")
	flags.StringSlice("codecs", []string{"h264"}, "ladders to build: h264, hevc, av1 (--codecs= to skip)")
	flags.Bool("skip-analysis", false, "skip the frame analysis (the container and bitstream are still inspected)")

	return cmd
}

// runEverything runs the pipeline configured by the flags of cmd on source
// and prints the report.
func runEverything(
	cmd *cobra.Command,
	env environment,
	source string,
) error {
	config, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	opts, err := runOptions(config, source)
	if err != nil {
		return err
	}

	report, err := executePipeline(cmd, env, config, "run", opts)
	if err != nil {
		return err
	}

	return emit(cmd.OutOrStdout(), config.Output, report, func(w io.Writer) error {
		return renderRun(w, report, env.width, config.Ladder.Commands)
	}, htmlreport.RenderRun)
}

// runOptions maps the run flags to pipeline options.
func runOptions(
	config Config,
	source string,
) (pipeline.Options, error) {
	overlayOpts, err := overlayOptions(config, source)
	if err != nil {
		return pipeline.Options{}, err
	}

	return pipeline.Options{
		Source:       source,
		Reference:    config.Run.Reference,
		SkipAnalysis: config.Run.SkipAnalysis,
		Codecs:       config.Run.Codecs,
		Analysis:     analysis.Options{Audio: audioOptions(config.Analysis)},
		Quality:      qualityOptions(config),
		Ladder:       ladderOptions(config),
		Overlay:      overlayOpts,
		Renditions:   renditionOptions(config),
	}, nil
}

// renderRun prints every result of a run as text.
func renderRun(
	w io.Writer,
	report *pipeline.Report,
	width int,
	commands bool,
) error {
	var sections []func() error

	if report.Analysis != nil {
		sections = append(sections, func() error { return tui.RenderReport(w, report.Analysis, width, "") })
	}

	if report.Comparison != nil {
		sections = append(sections, func() error { return tui.RenderComparison(w, report.Comparison, width, "") })
	}

	return renderSections(w, append(sections, ladderSections(w, report, width, commands)...))
}

// renderLadders prints the ladders of several codecs as text, and how they
// compare.
func renderLadders(
	w io.Writer,
	report *pipeline.Report,
	width int,
	commands bool,
) error {
	return renderSections(w, ladderSections(w, report, width, commands))
}

// ladderSections print every ladder of a report, then how the codecs
// compare at equal quality when there are several.
func ladderSections(
	w io.Writer,
	report *pipeline.Report,
	width int,
	commands bool,
) []func() error {
	var sections []func() error

	for _, l := range report.Ladders {
		sections = append(sections, func() error { return tui.RenderLadder(w, l, width, "", commands) })
	}

	if len(findings.Codecs(report.Ladders)) > 0 {
		sections = append(sections, func() error { return tui.RenderCodecs(w, report.Ladders) })
	}

	return sections
}

// renderSections prints sections separated by a blank line.
func renderSections(
	w io.Writer,
	sections []func() error,
) error {
	for i, render := range sections {
		if i > 0 {
			fmt.Fprintln(w)
		}

		if err := render(); err != nil {
			return err
		}
	}

	return nil
}
