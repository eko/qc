// Command audioval validates the audio analysis (packages audio,
// audio/loudness, audio/defect):
//
//   - conformance: the synthetic signals of EBU Tech 3341 and 3342, measured
//     by qc and by ffmpeg's ebur128 filter, against the readings and
//     tolerances of the specifications;
//
//   - defects: programme-like signals with defects inserted at known times
//     (silence, a muted channel, a dropout, clipping, inverted phase, mono
//     as stereo, DC offset, an empty LFE, leading and trailing silence) and
//     a clean one, decoded by ffmpeg from WAV, and from AAC with -aac;
//
//   - cross-check: every audio track of the files given, measured by qc and
//     by ffmpeg's ebur128 and loudnorm filters, with the time each takes.
//
// Usage:
//
//	go run ./bench/audioval [-aac] [-dir /tmp/audioval] [-run 3341|silence] [file ...]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// config are the command-line settings.
type config struct {
	dir             string
	ffmpeg, ffprobe string
	aac             bool
	files           []string
	// filter selects the conformance and defect cases by name (nil: all).
	filter *regexp.Regexp
}

// selected reports whether the case named name runs.
func (c config) selected(
	name string,
) bool {
	return c.filter == nil || c.filter.MatchString(name)
}

// run executes audioval with args and returns the process exit code: 1
// when a check fails or a tool errs.
func run(
	args []string,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("audioval", flag.ContinueOnError)
	flags.SetOutput(stderr)

	dir := flags.String("dir", "", "directory for the synthesised files (default: a temporary one)")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	ffprobe := flags.String("ffprobe", "ffprobe", "ffprobe binary")
	aac := flags.Bool("aac", false, "also run the defect cases on an AAC encode of each signal")
	filter := flags.String("run", "", "only the conformance and defect cases whose name matches this regular expression")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	cfg := config{dir: *dir, ffmpeg: *ffmpeg, ffprobe: *ffprobe, aac: *aac, files: flags.Args()}

	if *filter != "" {
		re, err := regexp.Compile(*filter)
		if err != nil {
			fmt.Fprintln(stderr, "audioval: -run:", err)

			return 2
		}

		cfg.filter = re
	}

	ok, err := validate(context.Background(), cfg, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "audioval:", err)

		return 1
	}

	if !ok {
		return 1
	}

	return 0
}

// validate runs every section; ok is false when a check failed.
func validate(
	ctx context.Context,
	cfg config,
	w io.Writer,
) (bool, error) {
	if cfg.dir == "" {
		dir, err := os.MkdirTemp("", "audioval")
		if err != nil {
			return false, fmt.Errorf("work directory: %w", err)
		}

		defer os.RemoveAll(dir)

		cfg.dir = dir
	}

	conformanceOK, err := conformance(ctx, cfg, w)
	if err != nil {
		return false, err
	}

	defectsOK, err := defects(ctx, cfg, w)
	if err != nil {
		return false, err
	}

	if err := crossCheck(ctx, cfg, w); err != nil {
		return false, err
	}

	return conformanceOK && defectsOK, nil
}
