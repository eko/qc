// Command gpuval validates qc on an NVIDIA GPU and writes a Markdown report
// to paste back: NVDEC decoding speed and frame identity, CUDA VMAF against
// CPU VMAF (agreement and speed), NVENC ladders against CPU ladders (time,
// bitrate at equal VMAF), and a CQ sweep calibrating the NVENC probe CQs.
//
// It only uses synthetic content generated with ffmpeg's lavfi sources, or
// a public clip given with -source (see docs/gpu.md). It is meant to run in
// the CUDA image, which ships it:
//
//	docker run --rm --gpus all --user "$(id -u):$(id -g)" -v "$PWD/gpu-validation:/work" \
//	    --entrypoint gpuval ghcr.io/eko/qc:<version>-cuda -dir /work
//
// or make gpu-validate from a checkout. Each step records its failure in the
// report and the next one runs, so a partial GPU (no AV1 NVENC, no CUDA
// VMAF) still yields a useful report.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Defaults of the command-line options.
const (
	defaultSeconds = 8
	defaultCodecs  = "h264,hevc,av1"
	reportName     = "gpu-report.md"
)

// options are the command-line settings.
type options struct {
	dir       string
	source    string
	seconds   int
	codecs    []string
	ladders   bool
	calibrate bool
	qc        string
	ffmpeg    string
	ffprobe   string
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, execRunner{}))
}

// run executes gpuval with args and returns the process exit code.
func run(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	r runner,
) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return 2
	}

	v := &validator{opts: opts, run: r, log: stderr, report: &report{}}

	text, err := v.validate(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)

		return 1
	}

	fmt.Fprint(stdout, text)

	return 0
}

// parseArgs reads the command line.
func parseArgs(
	args []string,
	stderr io.Writer,
) (options, error) {
	fs := flag.NewFlagSet("gpuval", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		opts   options
		codecs string
		skipL  bool
		skipC  bool
	)

	fs.StringVar(&opts.dir, "dir", ".", "working directory: generated clips, encodes and "+reportName)
	fs.StringVar(&opts.source, "source", "", "a public 1080p clip to use instead of the synthetic content (see docs/gpu.md)")
	fs.IntVar(&opts.seconds, "seconds", defaultSeconds, "length of each of the 4 synthetic segments (1080p25)")
	fs.StringVar(&codecs, "codecs", defaultCodecs, "codecs of the ladder comparison and the CQ calibration")
	fs.BoolVar(&skipL, "skip-ladders", false, "skip the ladder comparison")
	fs.BoolVar(&skipC, "skip-calibration", false, "skip the NVENC CQ calibration")
	fs.StringVar(&opts.qc, "qc", "qc", "qc binary (built with -tags cuda for CUDA VMAF)")
	fs.StringVar(&opts.ffmpeg, "ffmpeg", "ffmpeg", "ffmpeg binary with NVDEC/NVENC")
	fs.StringVar(&opts.ffprobe, "ffprobe", "ffprobe", "ffprobe binary")

	if err := fs.Parse(args); err != nil {
		return options{}, fmt.Errorf("gpuval: %w", err)
	}

	opts.codecs = strings.Split(codecs, ",")
	opts.ladders, opts.calibrate = !skipL, !skipC

	return opts, nil
}

// validator runs the steps and fills the report.
type validator struct {
	opts   options
	run    runner
	log    io.Writer
	report *report
}

// step is one part of the validation. Its error is reported and the next
// step runs.
type step struct {
	title string
	fn    func(ctx context.Context, c content) error
}

// validate runs every step and writes the report into the working
// directory. It only fails when no content can be prepared.
func (v *validator) validate(
	ctx context.Context,
) (string, error) {
	if err := os.MkdirAll(v.opts.dir, 0o750); err != nil {
		return "", fmt.Errorf("gpuval: %w", err)
	}

	v.report.title("qc GPU validation report")
	v.environment(ctx)

	c, err := v.prepare(ctx)
	if err != nil {
		return "", err
	}

	steps := []step{
		{title: "(a) NVDEC decoding", fn: v.decoding},
		{title: "(b) CUDA VMAF", fn: v.vmaf},
	}

	if v.opts.ladders {
		steps = append(steps, step{title: "(c) NVENC ladders vs CPU ladders", fn: v.ladders})
	}

	if v.opts.calibrate {
		steps = append(steps, step{title: "(d) NVENC CQ calibration", fn: v.calibration})
	}

	for _, s := range steps {
		v.progress("%s", s.title)
		v.report.section(s.title)

		err := s.fn(ctx, c)
		if ctx.Err() != nil {
			return "", fmt.Errorf("gpuval: %w", ctx.Err())
		}

		if err != nil {
			v.report.line("**Step failed:** `%s`", oneLine(err.Error()))
		}
	}

	text := v.report.String()

	if err := os.WriteFile(filepath.Join(v.opts.dir, reportName), []byte(text), 0o600); err != nil {
		return "", fmt.Errorf("gpuval: %w", err)
	}

	return text, nil
}

// environment records the GPU, driver and tool versions. Missing tools
// (nvidia-smi outside the container toolkit) are reported, not fatal.
func (v *validator) environment(
	ctx context.Context,
) {
	v.report.line("Generated %s.", time.Now().UTC().Format(time.RFC3339))
	v.report.line("")

	probes := []struct {
		label string
		name  string
		args  []string
	}{
		{label: "GPU", name: "nvidia-smi", args: []string{"--query-gpu=name,driver_version,memory.total", "--format=csv,noheader"}},
		{label: "qc", name: v.opts.qc, args: []string{"version"}},
		{label: "ffmpeg", name: v.opts.ffmpeg, args: []string{"-hide_banner", "-version"}},
	}

	for _, p := range probes {
		out, err := v.run.run(ctx, p.name, p.args...)
		value := firstLine(string(out))

		if err != nil {
			value = "unavailable: " + oneLine(err.Error())
		}

		v.report.line("- **%s**: %s", p.label, value)
	}
}

// progress tells the operator what runs: steps take minutes.
func (v *validator) progress(
	format string,
	args ...any,
) {
	fmt.Fprintf(v.log, "gpuval: "+format+"\n", args...)
}

// timed runs a command and returns its output and wall time.
func (v *validator) timed(
	ctx context.Context,
	name string,
	args ...string,
) ([]byte, time.Duration, error) {
	started := time.Now()
	out, err := v.run.run(ctx, name, args...)

	return out, time.Since(started), err
}

// path is a file of the working directory.
func (v *validator) path(
	name string,
) string {
	return filepath.Join(v.opts.dir, name)
}

// errNoFrames is returned when a decode yields no frame.
var errNoFrames = errors.New("no frame decoded")

// firstLine returns the first non-empty line of s.
func firstLine(
	s string,
) string {
	for line := range strings.Lines(s) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// oneLine flattens a multi-line error for a Markdown table or line.
func oneLine(
	s string,
) string {
	return strings.Join(strings.Fields(s), " ")
}
