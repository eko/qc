// Command motionval checks the camera motion analysis (analyze/motion)
// against synthetic clips of known camera motion: pans, tilts and zooms at
// several speeds, handheld shake, combined moves, moving objects, parallax,
// grain, blur, fades and a cut. Clips are rendered with ffmpeg from a
// still (-texture, e.g. a large landscape photograph) and cached in -dir;
// each is analysed unsmoothed, for the per-frame error of the estimates,
// and with the defaults, for the classification.
//
//	go run ./bench/motionval -texture photo.jpg -dir /tmp/motionval [-run pan]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/probe"
)

var (
	errNoMotion = errors.New("no motion analysis in the report")
	// ErrUsage is returned for missing flags.
	ErrUsage = errors.New("-texture and -dir are required")
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes motionval with args and returns the process exit code.
func run(
	args []string,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("motionval", flag.ContinueOnError)
	flags.SetOutput(stderr)

	texture := flags.String("texture", "", "still image the clips are rendered from (at least 2 MP; scaled to 5496×3091)")
	dir := flags.String("dir", "", "directory caching the texture and the rendered clips")
	filter := flags.String("run", "", "only the clips whose name matches this regular expression")
	ffmpeg := flags.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	ffprobe := flags.String("ffprobe", "ffprobe", "ffprobe binary")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	err := validate(context.Background(), config{texture: *texture, dir: *dir, filter: *filter, ffmpeg: *ffmpeg, ffprobe: *ffprobe}, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "motionval:", err)

		return 1
	}

	return 0
}

// config are the command-line settings.
type config struct {
	texture, dir, filter, ffmpeg, ffprobe string
}

func validate(
	ctx context.Context,
	cfg config,
	w io.Writer,
) error {
	if cfg.texture == "" || cfg.dir == "" {
		return ErrUsage
	}

	match, err := regexp.Compile(cfg.filter)
	if err != nil {
		return fmt.Errorf("-run: %w", err)
	}

	if err := os.MkdirAll(cfg.dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", cfg.dir, err)
	}

	r := renderer{ffmpeg: cfg.ffmpeg, dir: cfg.dir}
	if r.texture, err = r.prepareTexture(ctx, cfg.texture); err != nil {
		return err
	}

	a := analysis.New(slog.New(slog.DiscardHandler), probe.NewFFprobe(cfg.ffprobe),
		bitstream.NewFFprobeReader(cfg.ffprobe), decode.NewFFmpeg(cfg.ffmpeg, 0), nil)

	var outcomes []outcome

	for _, c := range clips() {
		if !match.MatchString(c.name) {
			continue
		}

		path, err := r.render(ctx, c)
		if err != nil {
			return fmt.Errorf("render %s: %w", c.name, err)
		}

		o, err := measure(ctx, a.Analyze, path, c)
		if err != nil {
			return err
		}

		outcomes = append(outcomes, o)
	}

	return report(w, outcomes)
}

// report prints one row per clip, then the classification accuracy.
func report(
	w io.Writer,
	outcomes []outcome,
) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "clip\treliable\tpan bias\tpan rmse\tpx/frame\ttilt rmse\tzoom bias\tzoom rmse\tshake\ttrue shake\tclass\tok\t")

	right := 0

	for _, o := range outcomes {
		if o.ok {
			right++
		}

		fmt.Fprintf(tw, "%s\t%d/%d\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%s\t%s\t\n",
			o.clip.name, o.reliable, o.frames, o.pan.bias, o.pan.rmse, o.pan.rmse*clipWidth/100/float64(o.clip.fps),
			o.tilt.rmse, o.zoom.bias, o.zoom.rmse, o.shake, o.trueShake, classes(o.got), mark(o.ok))
	}

	fmt.Fprintf(tw, "\nclassified right: %d/%d\n", right, len(outcomes))

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

// classes words the classification of each shot.
func classes(
	shots []motion.Shot,
) string {
	parts := make([]string, len(shots))
	for i, s := range shots {
		parts[i] = strings.TrimSpace(fmt.Sprintf("%s %s", s.Class, s.Direction))
		if s.Shaky {
			parts[i] += " (shaky)"
		}
	}

	return strings.Join(parts, " | ")
}

func mark(
	ok bool,
) string {
	if ok {
		return "yes"
	}

	return "NO"
}
