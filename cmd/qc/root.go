package main

import (
	"github.com/spf13/cobra"
)

func newRootCommand(
	env environment,
) *cobra.Command {
	commands := wizardCommands{run: newRunCommand(env), ladder: newLadderCommand(env), sample: newSampleCommand(env)}

	root := &cobra.Command{
		Use:   "qc",
		Short: "Fast video analysis: technical metrics, VMAF and per-title streaming ladders",
		Long: "qc · fast video quality analysis: technical metrics, VMAF and per-title streaming ladders.\n\n" +
			"Run qc without arguments in a terminal for an interactive wizard. Every flag can\n" +
			"also be set through a QC_ environment variable (QC_FFMPEG, QC_PRECISION...) or\n" +
			"in a YAML, TOML or JSON file given with --config (or QC_CONFIG), keyed by flag\n" +
			"name; flags win over the environment, which wins over the file.",
		Example: "  qc run mezzanine.mov --codecs h264,av1 --html report.html\n" +
			"  qc analyze video.mp4 --fast\n" +
			"  qc vmaf reference.mov encode.mp4\n" +
			"  qc ladder mezzanine.mov -c av1\n" +
			"  qc sample episode-01.mov episode-02.mov --to sample.mkv --duration 60",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !env.wizard {
				return cmd.Help()
			}

			return runWizard(cmd, commands, env)
		},
	}

	flags := root.PersistentFlags()
	flags.String("ffprobe", "ffprobe", "ffprobe binary")
	flags.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	flags.String("log-level", "warn", "log level: debug, info, warn or error")
	flags.String(configFlag, "", "configuration file (YAML, TOML or JSON) keyed by flag name")
	flags.Int("cpus", 0, "CPUs used by qc and the ffmpeg processes it starts (0 = all those of the machine, or of the container's CPU quota)")
	flags.String("memory", "", "memory qc and its ffmpeg processes may use (4g, 512m): concurrency is lowered to fit (default: the container's memory limit, if any)")

	root.AddCommand(
		commands.run,
		newAnalyzeCommand(env),
		newVMAFCommand(env),
		commands.ladder,
		commands.sample,
		newVersionCommand(),
	)

	return root
}
