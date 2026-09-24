package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// Build metadata, stamped at link time by release builds:
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=… -X main.date=…"
//
// They must be package variables for -X to reach them. Left empty (go
// install, go build), they are read from the module build info instead.
var (
	version string
	commit  string
	date    string
)

// devVersion is the version of a build without release metadata.
const devVersion = "dev"

// shortCommitLength is the length of the commits shown, as git abbreviates
// them in large repositories.
const shortCommitLength = 12

// requiredEncoders are the ffmpeg encoders of the ladder codecs (h264, hevc
// and av1, see encode.Lookup).
var requiredEncoders = []string{"libx264", "libx265", "libsvtav1"}

// nvencEncoders are NVIDIA's hardware encoders: optional, reported for GPU
// bug reports.
var nvencEncoders = []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc"}

// Probe resolution of the VMAF model check: a 1080p25 source resolves to
// the default VMAF v1 model (vmaf_v1.0.16_3d0h), the one libvmaf 3.2.0
// cannot load.
const (
	checkModelHeight = 1080
	checkModelFPS    = 25
)

// Names of the environment checks.
const (
	checkFFmpeg    = "ffmpeg"
	checkFFprobe   = "ffprobe"
	checkNVENC     = "nvenc"
	checkVMAFModel = "vmaf model"
)

// ErrEnvironment is returned by qc version --check when a requirement is
// missing.
var ErrEnvironment = errors.New("environment check failed")

// buildInfo describes the qc binary and the libraries linked into it.
type buildInfo struct {
	Version  string `json:"version"`
	Commit   string `json:"commit,omitempty"`
	Date     string `json:"date,omitempty"`
	Go       string `json:"go"`
	Platform string `json:"platform"`
	Libvmaf  string `json:"libvmaf"`
}

// envCheck is the result of one environment check.
type envCheck struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	// Optional checks never fail qc version --check.
	Optional bool   `json:"optional,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// versionReport is what qc version prints.
type versionReport struct {
	Build  buildInfo  `json:"build"`
	Checks []envCheck `json:"checks,omitempty"`
}

// failed lists the required checks that did not pass.
func (r versionReport) failed() []string {
	var names []string

	for _, c := range r.Checks {
		if !c.OK && !c.Optional {
			names = append(names, c.Name)
		}
	}

	return names
}

func newVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of qc and its libraries, and check the environment",
		Long: "Print the version of qc, of Go and of the libvmaf it is linked against.\n\n" +
			"--check also checks what qc needs at run time: ffmpeg and ffprobe, the libx264,\n" +
			"libx265 and libsvtav1 encoders, NVENC (optional) and a loadable VMAF v1 model.\n" +
			"It exits with an error when a requirement is missing: paste its output in bug\n" +
			"reports.",
		Example: "  qc version\n" +
			"  qc version --check\n" +
			"  qc version --check -f json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			report := versionReport{Build: currentBuild()}

			if check, _ := cmd.Flags().GetBool("check"); check {
				d := doctor{output: ffexec.Output, loadModel: loadDefaultModel}
				report.Checks = d.check(cmd.Context(), config)
			}

			if err := printVersion(cmd.OutOrStdout(), config.Output.Format, report); err != nil {
				return err
			}

			if failed := report.failed(); len(failed) > 0 {
				return fmt.Errorf("%w: %s (see docs/install.md)", ErrEnvironment, strings.Join(failed, ", "))
			}

			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringP("format", "f", formatText, "stdout format: text or json")
	flags.Bool("check", false, "also check ffmpeg, ffprobe, the encoders and the VMAF models")
	flags.StringSlice("model-dir", vmaf.DefaultModelDirs(), "directories searched for model files")

	return cmd
}

// currentBuild describes the running binary.
func currentBuild() buildInfo {
	// ReadBuildInfo returns nil without build info (a binary not built in
	// module mode).
	info, _ := debug.ReadBuildInfo()

	build := resolveBuild(version, commit, date, info)
	build.Libvmaf = libvmaf.Version()

	return build
}

// resolveBuild merges the link-time metadata with the module build info
// (nil when unavailable): go install …@v0.1.0 records the module version,
// go build in a checkout the VCS revision and time.
func resolveBuild(
	ldVersion, ldCommit, ldDate string,
	info *debug.BuildInfo,
) buildInfo {
	build := buildInfo{
		Version:  ldVersion,
		Commit:   ldCommit,
		Date:     ldDate,
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}

	if info != nil {
		if build.Version == "" && info.Main.Version != "(devel)" {
			build.Version = info.Main.Version
		}

		settings := buildSettings(info)
		if build.Commit == "" {
			build.Commit = shortCommit(settings["vcs.revision"], settings["vcs.modified"] == "true")
		}

		if build.Date == "" {
			build.Date = settings["vcs.time"]
		}
	}

	if build.Version == "" {
		build.Version = devVersion
	}

	return build
}

// buildSettings indexes the build settings by key.
func buildSettings(
	info *debug.BuildInfo,
) map[string]string {
	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}

	return settings
}

// shortCommit abbreviates a revision, marked dirty when the working tree had
// uncommitted changes.
func shortCommit(
	revision string,
	modified bool,
) string {
	if revision == "" {
		return ""
	}

	if len(revision) > shortCommitLength {
		revision = revision[:shortCommitLength]
	}

	if modified {
		revision += "-dirty"
	}

	return revision
}

// doctor checks the run-time environment of qc.
type doctor struct {
	// output runs a binary and returns its stdout (ffexec.Output).
	output func(ctx context.Context, bin string, args []string) ([]byte, error)
	// loadModel loads the default VMAF model from dirs and returns its path.
	loadModel func(dirs []string) (string, error)
}

// check runs every environment check, in display order.
func (d doctor) check(
	ctx context.Context,
	config Config,
) []envCheck {
	ffmpeg := d.toolCheck(ctx, checkFFmpeg, config.Tools.FFmpeg)
	checks := []envCheck{ffmpeg, d.toolCheck(ctx, checkFFprobe, config.Tools.FFprobe)}
	checks = append(checks, d.encoderChecks(ctx, config.Tools.FFmpeg, ffmpeg.OK)...)

	path, err := d.loadModel(config.Quality.ModelDir)
	if err != nil {
		return append(checks, envCheck{Name: checkVMAFModel, Detail: err.Error()})
	}

	return append(checks, envCheck{Name: checkVMAFModel, OK: true, Detail: path})
}

// toolCheck reports the version of an ffmpeg tool, from the first line of
// -version ("ffmpeg version 7.1.1 Copyright…").
func (d doctor) toolCheck(
	ctx context.Context,
	name, bin string,
) envCheck {
	out, err := d.output(ctx, bin, []string{"-hide_banner", "-version"})
	if err != nil {
		return envCheck{Name: name, Detail: err.Error()}
	}

	firstLine, _, _ := strings.Cut(string(out), "\n")

	fields := strings.Fields(firstLine)
	if len(fields) < 3 || fields[1] != "version" {
		return envCheck{Name: name, OK: true, Detail: "unknown version"}
	}

	return envCheck{Name: name, OK: true, Detail: fields[2]}
}

// encoderChecks reports the required encoders, then NVENC. Without a
// working ffmpeg every encoder is missing.
func (d doctor) encoderChecks(
	ctx context.Context,
	bin string,
	ffmpegOK bool,
) []envCheck {
	var (
		encoders map[string]bool
		reason   = "ffmpeg unavailable"
	)

	if ffmpegOK {
		out, err := d.output(ctx, bin, []string{"-hide_banner", "-encoders"})
		if err == nil {
			encoders = parseEncoders(out)
			reason = "not built into ffmpeg"
		} else {
			reason = err.Error()
		}
	}

	checks := make([]envCheck, 0, len(requiredEncoders)+1)

	for _, name := range requiredEncoders {
		if encoders[name] {
			checks = append(checks, envCheck{Name: name, OK: true})
		} else {
			checks = append(checks, envCheck{Name: name, Detail: reason})
		}
	}

	var nvenc []string

	for _, name := range nvencEncoders {
		if encoders[name] {
			nvenc = append(nvenc, name)
		}
	}

	if len(nvenc) == 0 {
		return append(checks, envCheck{Name: checkNVENC, Optional: true, Detail: "not available (optional)"})
	}

	// ffmpeg lists NVENC encoders it was built with, GPU or not.
	detail := strings.Join(nvenc, ", ") + " (built in; needs an NVIDIA GPU and driver)"

	return append(checks, envCheck{Name: checkNVENC, OK: true, Optional: true, Detail: detail})
}

// parseEncoders reads the video encoders of ffmpeg -encoders, listed after
// the legend as " V....D libx264  …".
func parseEncoders(
	out []byte,
) map[string]bool {
	encoders := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "V") || fields[1] == "=" {
			continue
		}

		encoders[fields[1]] = true
	}

	return encoders
}

// loadDefaultModel loads the default VMAF v1 model from dirs, as a 1080p
// comparison would, and returns its path.
func loadDefaultModel(
	dirs []string,
) (string, error) {
	spec, err := vmaf.ResolveModel("", checkModelHeight, checkModelFPS, dirs)
	if err != nil {
		return "", err
	}

	if !spec.FromPath {
		return "", fmt.Errorf("%w: %s.json in none of %s", vmaf.ErrModelNotFound, spec.Name, strings.Join(dirs, ", "))
	}

	model, err := libvmaf.LoadModel(spec)
	if err != nil {
		return "", fmt.Errorf("load %s (libvmaf ≥ 3.2.1 is required): %w", spec.Source, err)
	}

	model.Close()

	return spec.Source, nil
}

// printVersion writes the report as text or JSON.
func printVersion(
	w io.Writer,
	format string,
	report versionReport,
) error {
	if format == formatJSON {
		return writeJSON(w, report)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	build := report.Build

	rows := [][2]string{
		{"qc", build.Version},
		{"commit", build.Commit},
		{"built", build.Date},
		{"go", build.Go + " " + build.Platform},
		{"libvmaf", build.Libvmaf},
	}

	for _, row := range rows {
		if row[1] != "" {
			fmt.Fprintf(tw, "%s\t%s\n", row[0], row[1])
		}
	}

	if len(report.Checks) > 0 {
		fmt.Fprintln(tw)
	}

	for _, c := range report.Checks {
		fmt.Fprintf(tw, "%s %s\t%s\n", checkMark(c), c.Name, c.Detail)
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	return nil
}

// checkMark is ✓ for a passing check, ✗ for a failing requirement and - for
// a missing option.
func checkMark(
	c envCheck,
) string {
	switch {
	case c.OK:
		return "✓"
	case c.Optional:
		return "-"
	default:
		return "✗"
	}
}
