package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/eko/qc/quality"
)

// videoExtensions are offered by the file pickers.
var videoExtensions = []string{".mp4", ".mov", ".mkv", ".mxf", ".ts", ".m2ts", ".webm", ".y4m", ".avi", ".m4v"}

// Wizard actions.
const (
	actionAnalysis = "analysis"
	actionVMAF     = "vmaf"
	actionLadder   = "ladder"
)

// VMAF modes offered by the wizard.
const (
	vmafPrecision = "precision"
	vmafShare     = "share"
	vmafPerScene  = "per-scene"
	vmafExact     = "exact"
)

// Ladder shapes offered by the wizard.
const (
	shapeAuto        = "auto"
	shapeCount       = "count"
	shapeResolutions = "resolutions"
)

// Defaults of the advanced answers: a flag is only emitted when an answer
// differs from them, which keeps the printed command short.
const (
	defaultTopVMAF   = "95"
	defaultMinVMAF   = "30"
	defaultPrecision = "0.5"
	defaultShare     = "5"
	defaultPerScene  = "2"
	defaultBitDepth  = "8"
	defaultProbing   = "fixed"
	defaultFilmGrain = "off"
)

// wizardAnswers are the choices made in the wizard. Numeric answers are kept
// as typed text: they are validated by the form and passed on as flags.
type wizardAnswers struct {
	Source    string
	Reference string
	Actions   []string
	Codecs    []string
	VMAFMode  string
	Precision string
	Share     string // percent of the frames, fixed-budget mode
	PerScene  string // clips per scene, fixed-budget mode
	Metrics   []string
	Devices   []string
	HTML      string
	// Overlay asks for an annotated copy of the source, written to
	// OverlayPath; both are asked only when the source is analysed or
	// compared (see overlayHidden).
	Overlay     bool
	OverlayPath string
	// Renditions asks for the ladders encoded on the whole title, into
	// RenditionsDir; both are asked only with a ladder (see
	// renditionsHidden).
	Renditions    bool
	RenditionsDir string
	// GPU is asked only when an NVIDIA GPU is usable (see wizardOffersGPU).
	GPU bool
	// HDRMetric is asked only for an HDR source: how VMAF scores it.
	HDRMetric string

	// Advanced ladder options, asked only when Advanced is set.
	Advanced    bool
	Shape       string
	RungCount   string
	Resolutions string
	TopVMAF     string
	MinVMAF     string
	MaxBitrate  string // kb/s, empty for no cap
	Preset      string
	BitDepth    string
	SkipVerify  bool
	Probing     string
	PerShot     bool
	FilmGrain   string
}

// runArgs are the arguments of the qc run invocation equivalent to a.
func (a wizardAnswers) runArgs() []string {
	args := []string{notFlag(a.Source)}

	if slices.Contains(a.Actions, actionVMAF) {
		args = append(args, "-r", notFlag(a.Reference))
		args = append(args, a.vmafModeArgs()...)
	}

	var codecs []string
	if slices.Contains(a.Actions, actionLadder) {
		codecs = a.Codecs
	}

	// The metrics apply to the comparison and to the verified ladder rungs.
	if slices.Contains(a.Actions, actionVMAF) || len(codecs) > 0 {
		args = append(args, a.metricArgs()...)
	}

	args = append(args, "--codecs="+strings.Join(codecs, ","))

	if len(codecs) > 0 && a.Advanced {
		args = append(args, a.ladderArgs()...)
	}

	if !slices.Contains(a.Actions, actionAnalysis) {
		args = append(args, "--skip-analysis")
	}

	if a.HDRMetric == string(quality.HDRMetricToneMap) && (slices.Contains(a.Actions, actionVMAF) || len(codecs) > 0) {
		args = append(args, "--hdr-metric", a.HDRMetric)
	}

	if a.GPU {
		args = append(args, "--gpu")
	}

	if html := strings.TrimSpace(a.HTML); html != "" {
		args = append(args, "--html", notFlag(html))
	}

	if path := strings.TrimSpace(a.OverlayPath); a.Overlay && path != "" && !a.overlayHidden() {
		args = append(args, "--overlay", notFlag(path))
	}

	if dir := strings.TrimSpace(a.RenditionsDir); a.Renditions && dir != "" && !a.renditionsHidden() {
		args = append(args, "--encode-ladder", notFlag(dir))
	}

	return args
}

// vmafModeArgs are the flags of the VMAF mode: --exact, a fixed --sample
// budget, or --precision only when it differs from the default.
func (a wizardAnswers) vmafModeArgs() []string {
	switch a.VMAFMode {
	case vmafExact:
		return []string{"--exact"}
	case vmafShare:
		return []string{"--sample", percentAnswer(a.Share) + "%"}
	case vmafPerScene:
		return []string{"--sample", strings.TrimSpace(a.PerScene) + "/scene"}
	}

	if changed(a.Precision, defaultPrecision) {
		return []string{"--precision", strings.TrimSpace(a.Precision)}
	}

	return nil
}

// percentAnswer is a typed percentage without its optional "%" sign.
func percentAnswer(
	s string,
) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
}

// validateShare accepts a percentage of the frames in (0, 100], with or
// without its "%" sign.
func validateShare(
	s string,
) error {
	if v, err := strconv.ParseFloat(percentAnswer(s), 64); err != nil || v <= 0 || v > percentMax {
		return errors.New("enter a percentage above 0 and at most 100")
	}

	return nil
}

// validatePerScene accepts a positive number of clips per scene.
func validatePerScene(
	s string,
) error {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 1 {
		return errors.New("enter a positive number of clips")
	}

	return nil
}

// percentMax is the largest share of the frames: all of them.
const percentMax = 100

// metricArgs are the --metrics and --devices flags, only when the chosen
// metrics differ from the defaults or devices are asked. Nil metrics mean the
// defaults; an empty selection (never nil from the form) means VMAF only.
func (a wizardAnswers) metricArgs() []string {
	var args []string

	if a.Metrics != nil && !sameSet(a.Metrics, defaultMetrics) {
		args = append(args, "--metrics="+strings.Join(a.Metrics, ","))
	}

	if len(a.Devices) > 0 {
		args = append(args, "--devices="+strings.Join(a.Devices, ","))
	}

	return args
}

// sameSet reports whether a and b hold the same names, in any order.
func sameSet(
	a, b []string,
) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(s string) bool { return !slices.Contains(b, s) })
}

// metricOptions are the metrics offered by the wizard, with their CPU cost
// next to VMAF (1080p, measured) so the user can trade detail for speed.
// The answers tick them: the defaults first, the choice made when a section
// is edited.
func metricOptions() []huh.Option[string] {
	labels := []struct{ name, label string }{
		{quality.MetricXPSNR, "XPSNR · perceptual PSNR (+10% CPU)"},
		{quality.MetricCAMBI, "CAMBI · banding (free)"},
		{quality.MetricPSNR, "PSNR · Y, Cb, Cr (+2%)"},
		{quality.MetricSSIM, "SSIM (+25%)"},
		{quality.MetricPSNRHVS, "PSNR-HVS (+75%)"},
		{quality.MetricMSSSIM, "MS-SSIM (≈ 4× VMAF, slow)"},
		{quality.MetricCIEDE2000, "CIEDE2000 · colour (≈ 9× VMAF, very slow)"},
	}

	options := make([]huh.Option[string], len(labels))
	for i, l := range labels {
		options[i] = huh.NewOption(l.label, l.name)
	}

	return options
}

// ladderArgs are the flags of the advanced ladder answers that differ from
// the defaults.
func (a wizardAnswers) ladderArgs() []string {
	var args []string

	switch a.Shape {
	case shapeCount:
		args = append(args, "--rungs", strings.TrimSpace(a.RungCount))
	case shapeResolutions:
		// A "p" suffix keeps a single height from being read as a count.
		heights, _ := parseHeights(a.Resolutions)
		args = append(args, "--rungs", strings.Join(heights, ","))
	}

	if changed(a.TopVMAF, defaultTopVMAF) {
		args = append(args, "--top-vmaf", strings.TrimSpace(a.TopVMAF))
	}

	if changed(a.MinVMAF, defaultMinVMAF) {
		args = append(args, "--min-vmaf", strings.TrimSpace(a.MinVMAF))
	}

	if kbps := strings.TrimSpace(a.MaxBitrate); kbps != "" {
		value, _ := strconv.ParseFloat(kbps, 64)
		args = append(args, "--max-bitrate", strconv.FormatInt(int64(value*kilo), 10))
	}

	if preset := strings.TrimSpace(a.Preset); preset != "" {
		args = append(args, "--preset", preset)
	}

	if changed(a.BitDepth, defaultBitDepth) {
		args = append(args, "--encode-bit-depth", a.BitDepth)
	}

	if a.SkipVerify {
		args = append(args, "--no-verify")
	}

	return append(args, a.innovationArgs()...)
}

// innovationArgs are the flags of the probing, per-shot and film grain
// answers that differ from the defaults.
func (a wizardAnswers) innovationArgs() []string {
	var args []string

	if changed(a.Probing, defaultProbing) {
		args = append(args, "--probing", a.Probing)
	}

	if a.PerShot {
		args = append(args, "--per-shot")
	}

	// Film grain synthesis is an AV1 tool, which per-shot rungs exclude.
	if changed(a.FilmGrain, defaultFilmGrain) && slices.Contains(a.Codecs, av1Codec) && !a.PerShot {
		args = append(args, "--film-grain", a.FilmGrain)
	}

	return args
}

// kilo converts the kb/s typed in the wizard to the bits/s of the flags.
const kilo = 1000

// changed reports whether a typed answer differs from its default (an empty
// answer keeps the default).
func changed(
	answer, fallback string,
) bool {
	answer = strings.TrimSpace(answer)

	return answer != "" && answer != fallback
}

// parseHeights reads "1080, 720p,540" into ["1080p", "720p", "540p"].
func parseHeights(
	s string,
) ([]string, error) {
	var heights []string

	for field := range strings.SplitSeq(s, ",") {
		h, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(strings.ToLower(field)), "p"))
		if err != nil || h < 1 {
			return nil, fmt.Errorf("%q is not a resolution height (e.g. 1080,720,540)", strings.TrimSpace(field))
		}

		heights = append(heights, strconv.Itoa(h)+"p")
	}

	return heights, nil
}

// validateHeights accepts a comma-separated list of heights.
func validateHeights(
	s string,
) error {
	_, err := parseHeights(s)

	return err
}

// validateCount accepts a positive number of rungs.
func validateCount(
	s string,
) error {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 1 {
		return errors.New("enter a positive number of rungs")
	}

	return nil
}

// validateRange returns a validator accepting a number in [lo, hi], or an
// empty answer when optional.
func validateRange(
	lo, hi float64,
	optional bool,
) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" && optional {
			return nil
		}

		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < lo || v > hi {
			return fmt.Errorf("enter a number between %g and %g", lo, hi)
		}

		return nil
	}
}

// notFlag prefixes a relative path starting with a dash with ./ so that it is
// not read as a flag.
func notFlag(
	path string,
) string {
	if strings.HasPrefix(path, "-") {
		return "./" + path
	}

	return path
}

// requirePath rejects an empty file selection.
func requirePath(
	path string,
) error {
	if path == "" {
		return errors.New("pick a video file")
	}

	return nil
}

// requireOne returns a validator rejecting an empty selection with message.
func requireOne(
	message string,
) func([]string) error {
	return func(values []string) error {
		if len(values) == 0 {
			return errors.New(message)
		}

		return nil
	}
}

// commandLine renders args as a shell command line.
func commandLine(
	args []string,
) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quoteArg(arg)
	}

	return strings.Join(quoted, " ")
}

// quoteArg single-quotes s when a POSIX shell would not read it verbatim.
func quoteArg(
	s string,
) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./,:=@%+") == "" {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
