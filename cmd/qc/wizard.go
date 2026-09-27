package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
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
		options[i] = huh.NewOption(l.label, l.name).Selected(slices.Contains(defaultMetrics, l.name))
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

// runWizard asks what to compute, prints the equivalent qc run command and
// runs it with runCmd, so that the wizard behaves exactly like the command it
// shows.
func runWizard(
	cmd, runCmd *cobra.Command,
	env environment,
) error {
	stderr := cmd.ErrOrStderr()
	fmt.Fprintln(stderr, wizardBanner())

	offerGPU, err := wizardOffersGPU(cmd, env)
	if err != nil {
		return err
	}

	config, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	answers, err := env.askWizard(wizardContext{
		offerGPU:  offerGPU,
		detectHDR: hdrDetector(cmd.Context(), config.Tools.FFprobe),
	})
	if errors.Is(err, huh.ErrUserAborted) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("wizard: %w", err)
	}

	args := answers.runArgs()
	fmt.Fprintln(stderr, subtle("  equivalent command: ")+commandLine(append([]string{"qc", "run"}, args...)))

	if err := runCmd.ParseFlags(args); err != nil {
		return fmt.Errorf("wizard: %w", err)
	}

	runCmd.SetContext(cmd.Context())

	return runEverything(runCmd, env, runCmd.Flags().Arg(0))
}

// wizardOffersGPU reports whether the wizard asks about the GPU: whether
// the ffmpeg of the configuration (the wizard has no flags: QC_FFMPEG, the
// configuration file or PATH) can use an NVIDIA GPU. The run then checks
// everything again (checkGPU).
func wizardOffersGPU(
	cmd *cobra.Command,
	env environment,
) (bool, error) {
	if env.gpuAvailable == nil {
		return false, nil
	}

	config, err := loadConfig(cmd)
	if err != nil {
		return false, err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), wizardProbeTimeout)
	defer cancel()

	return env.gpuAvailable(ctx, config.Tools.FFmpeg), nil
}

// wizardContext is what the wizard knows before asking: whether to offer
// the GPU, and how to tell the dynamic range of the picked video.
type wizardContext struct {
	offerGPU bool
	// detectHDR returns the dynamic range of an HDR video ("HDR10",
	// "HLG"...), "" for SDR or when unknown; nil detects nothing.
	detectHDR func(path string) string
}

// hdrDetector probes a video with ffprobe for its dynamic range, once per
// path: the form re-evaluates its hidden pages on every key stroke.
func hdrDetector(
	ctx context.Context,
	ffprobe string,
) func(path string) string {
	prober := probe.NewFFprobe(ffprobe)
	seen := map[string]string{}

	return func(path string) string {
		if path == "" {
			return ""
		}

		if dr, ok := seen[path]; ok {
			return dr
		}

		probeCtx, cancel := context.WithTimeout(ctx, wizardProbeTimeout)
		defer cancel()

		dr := ""
		if info, err := prober.Probe(probeCtx, path); err == nil {
			if v, ok := info.PrimaryVideo(); ok && v.Color.IsHDR() {
				dr = string(v.HDR.DynamicRange)
			}
		}

		seen[path] = dr

		return dr
	}
}

// askWizard runs the interactive form.
func askWizard(
	ctx wizardContext,
) (wizardAnswers, error) {
	answers := newWizardAnswers()

	form := huh.NewForm(answers.formGroups(ctx)...).WithTheme(wizardTheme())
	if err := form.Run(); err != nil {
		return wizardAnswers{}, fmt.Errorf("run form: %w", err)
	}

	return *answers, nil
}

// newWizardAnswers are the answers before the form: the defaults it shows.
func newWizardAnswers() *wizardAnswers {
	return &wizardAnswers{
		Actions:   []string{actionAnalysis, actionLadder},
		Codecs:    []string{"h264"},
		VMAFMode:  vmafPrecision,
		Precision: defaultPrecision,
		Share:     defaultShare,
		PerScene:  defaultPerScene,
		Metrics:   slices.Clone(defaultMetrics),
		Shape:     shapeAuto,
		TopVMAF:   defaultTopVMAF,
		MinVMAF:   defaultMinVMAF,
		BitDepth:  defaultBitDepth,
		Probing:   defaultProbing,
		FilmGrain: defaultFilmGrain,
		HDRMetric: string(quality.HDRMetricPQ),
	}
}

// formGroups are the pages of the wizard form, in order. Each writes its
// answers into a, and hides itself according to the answers given before
// it.
func (a *wizardAnswers) formGroups(
	ctx wizardContext,
) []*huh.Group {
	return []*huh.Group{
		a.sourceGroup(),
		a.actionsGroup(),
		a.referenceGroup(),
		a.precisionGroup(),
		a.shareGroup(),
		a.perSceneGroup(),
		a.metricsGroup(),
		a.hdrGroup(ctx.detectHDR),
		a.codecsGroup(),
		a.shapeGroup(),
		a.rungCountGroup(),
		a.resolutionsGroup(),
		a.encodingGroup(),
		a.filmGrainGroup(),
		a.gpuGroup(ctx.offerGPU),
		a.htmlGroup(),
	}
}

// wants reports whether action was picked.
func (a *wizardAnswers) wants(
	action string,
) bool {
	return slices.Contains(a.Actions, action)
}

// vmafHidden hides the VMAF settings unless VMAF was picked.
func (a *wizardAnswers) vmafHidden() bool {
	return !a.wants(actionVMAF)
}

// ladderHidden hides the ladder settings unless a ladder was picked.
func (a *wizardAnswers) ladderHidden() bool {
	return !a.wants(actionLadder)
}

// metricsHidden hides the metrics unless something measures VMAF: a
// comparison or the verification of ladder rungs.
func (a *wizardAnswers) metricsHidden() bool {
	return a.vmafHidden() && a.ladderHidden()
}

// shapeHidden hides the settings of a ladder shape other than the chosen
// one.
func (a *wizardAnswers) shapeHidden(
	shape string,
) func() bool {
	return func() bool { return a.advancedHidden() || a.Shape != shape }
}

// filmGrainHidden hides film grain synthesis without an AV1 ladder, or with
// per-shot rungs, which exclude it.
func (a *wizardAnswers) filmGrainHidden() bool {
	return a.advancedHidden() || !slices.Contains(a.Codecs, av1Codec) || a.PerShot
}

// advancedHidden hides the ladder customisation unless it was asked for.
func (a *wizardAnswers) advancedHidden() bool {
	return a.ladderHidden() || !a.Advanced
}

// vmafModeHidden hides the settings of a VMAF mode other than the chosen
// one.
func (a *wizardAnswers) vmafModeHidden(
	mode string,
) func() bool {
	return func() bool { return a.vmafHidden() || a.VMAFMode != mode }
}

// sourceGroup asks for the video to analyse.
func (a *wizardAnswers) sourceGroup() *huh.Group {
	return huh.NewGroup(
		videoPicker("Video to analyse", "The source (a mezzanine for ladders, an encode to compare for VMAF).").
			Value(&a.Source),
	)
}

// actionsGroup asks what to compute.
func (a *wizardAnswers) actionsGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("What should I compute?").
			Options(
				huh.NewOption("Technical analysis · bitrate, GOP, SI/TI, shots, black/freeze, crop", actionAnalysis).Selected(true),
				huh.NewOption("VMAF against a reference", actionVMAF),
				huh.NewOption("Per-title streaming ladder", actionLadder).Selected(true),
			).
			Validate(requireOne("pick at least one")).
			Value(&a.Actions),
	)
}

// referenceGroup asks for the reference and the VMAF mode.
func (a *wizardAnswers) referenceGroup() *huh.Group {
	return huh.NewGroup(
		videoPicker("Reference video", "The pristine version the source is compared to.").
			Value(&a.Reference),
		huh.NewSelect[string]().
			Title("VMAF mode").
			Options(
				huh.NewOption("Target precision (± VMAF, adaptive)", vmafPrecision),
				huh.NewOption("Fixed budget · share of frames", vmafShare),
				huh.NewOption("Fixed budget · clips per scene", vmafPerScene),
				huh.NewOption("Exact (every frame)", vmafExact),
			).
			Value(&a.VMAFMode),
	).WithHideFunc(a.vmafHidden)
}

// precisionGroup asks for the target precision of the adaptive mode.
func (a *wizardAnswers) precisionGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Target precision (± VMAF, 95% confidence)").
			Description("Clips are scored until the interval is this narrow.").
			Validate(validateRange(0.05, 5, false)).
			Value(&a.Precision),
	).WithHideFunc(a.vmafModeHidden(vmafPrecision))
}

// shareGroup asks for the share of frames of a fixed budget.
func (a *wizardAnswers) shareGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Share of frames to score (%)").
			Description("One round over the whole video; the report gives the 95% interval it reaches.").
			Validate(validateShare).
			Value(&a.Share),
	).WithHideFunc(a.vmafModeHidden(vmafShare))
}

// perSceneGroup asks for the clips per scene of a fixed budget.
func (a *wizardAnswers) perSceneGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Clips per scene").
			Description("4-frame clips in every scene (shot or GOP); 1 gives a wider, conservative interval.").
			Validate(validatePerScene).
			Value(&a.PerScene),
	).WithHideFunc(a.vmafModeHidden(vmafPerScene))
}

// metricsGroup asks for the metrics and devices measured next to VMAF, on
// the comparison and on the verified ladder rungs.
func (a *wizardAnswers) metricsGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Metrics next to VMAF").
			Description("Measured on the same frames as VMAF: the comparison, and the verified ladder rungs. Costs are extra CPU relative to VMAF.").
			Options(metricOptions()...).
			Value(&a.Metrics),
		huh.NewMultiSelect[string]().
			Title("VMAF per viewing device (optional)").
			Options(
				huh.NewOption("Phone · VMAF v1 5d0h (+65%)", vmaf.DevicePhone),
				huh.NewOption("TV · VMAF v1 3d0h", vmaf.DeviceTV),
				huh.NewOption("4K TV · VMAF v1 3d0h_2160 (extra 2160p pass, ≈ 2.5×)", vmaf.Device4K),
			).
			Value(&a.Devices),
	).WithHideFunc(a.metricsHidden)
}

// hdrGroup asks how VMAF scores an HDR source, only when the picked source
// (or reference) is HDR and something measures VMAF.
func (a *wizardAnswers) hdrGroup(
	detect func(path string) string,
) *huh.Group {
	var detected string

	return huh.NewGroup(
		huh.NewSelect[string]().
			TitleFunc(func() string { return detected + " source: how should VMAF score it?" }, &a.Source).
			Description("VMAF has no HDR model. HDR metrics (wPSNR, ΔE ITP) and light levels are measured either way.").
			Options(
				huh.NewOption("On the HDR signal · fast, ranks encodes, not HDR-calibrated", string(quality.HDRMetricPQ)),
				huh.NewOption("On an SDR tone mapping · slower, what SDR screens show", string(quality.HDRMetricToneMap)),
			).
			Value(&a.HDRMetric),
	).WithHideFunc(func() bool {
		detected = a.hdrDetected(detect)

		return detected == ""
	})
}

// hdrDetected is the dynamic range of the HDR video VMAF would score (the
// reference, else the source), "" when nothing measures VMAF, the videos
// are SDR or nothing can tell.
func (a *wizardAnswers) hdrDetected(
	detect func(path string) string,
) string {
	if detect == nil || a.metricsHidden() {
		return ""
	}

	if a.wants(actionVMAF) {
		if dr := detect(a.Reference); dr != "" {
			return dr
		}
	}

	return detect(a.Source)
}

// codecsGroup asks for the ladder codecs, and whether to customise the
// ladders.
func (a *wizardAnswers) codecsGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Ladder codecs").
			Options(
				huh.NewOption("H.264 · libx264", "h264"),
				huh.NewOption("HEVC · libx265", "hevc"),
				huh.NewOption("AV1 · SVT-AV1", av1Codec),
			).
			Validate(requireOne("pick at least one codec")).
			Value(&a.Codecs),
		huh.NewConfirm().
			Title("Customise the ladder?").
			Description("Rung count or resolutions, quality range, bitrate cap, preset, bit depth.").
			Affirmative("Yes").
			Negative("No, automatic").
			Value(&a.Advanced),
	).WithHideFunc(a.ladderHidden)
}

// shapeGroup asks for the ladder shape and its quality range.
func (a *wizardAnswers) shapeGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Ladder shape").
			Options(
				huh.NewOption("Automatic · one quality step (6 VMAF) at a time", shapeAuto),
				huh.NewOption("Fixed number of rungs · resolutions chosen automatically", shapeCount),
				huh.NewOption("Fixed resolutions · one rung per resolution", shapeResolutions),
			).
			Value(&a.Shape),
		huh.NewInput().
			Title("Top VMAF").
			Description("Quality of the highest rung.").
			Validate(validateRange(1, 110, false)).
			Value(&a.TopVMAF),
		huh.NewInput().
			Title("Minimum VMAF").
			Description("Lowest acceptable rung quality.").
			Validate(validateRange(0, 110, false)).
			Value(&a.MinVMAF),
	).WithHideFunc(a.advancedHidden)
}

// rungCountGroup asks for the rung count of a fixed-count shape.
func (a *wizardAnswers) rungCountGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Number of rungs").
			Validate(validateCount).
			Value(&a.RungCount),
	).WithHideFunc(a.shapeHidden(shapeCount))
}

// resolutionsGroup asks for the rung resolutions of an imposed shape.
func (a *wizardAnswers) resolutionsGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Rung resolutions, top first").
			Description("Heights separated by commas; a height may repeat.").
			Placeholder("1080,720,720,540,360").
			Validate(validateHeights).
			Value(&a.Resolutions),
	).WithHideFunc(a.shapeHidden(shapeResolutions))
}

// encodingGroup asks for the encoding settings, the verification, the
// probe placement and the per-shot rungs.
func (a *wizardAnswers) encodingGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Maximum bitrate (kb/s)").
			Description("Cap of the top rung. Leave empty for no cap.").
			Validate(validateRange(1, 1_000_000, true)).
			Value(&a.MaxBitrate),
		huh.NewInput().
			Title("Encoder preset").
			Description("Leave empty for the codec's fast default.").
			Value(&a.Preset),
		huh.NewSelect[string]().
			Title("Bit depth").
			Options(
				huh.NewOption("8-bit", "8"),
				huh.NewOption("10-bit (Main10, best for HEVC/AV1)", "10"),
			).
			Value(&a.BitDepth),
		huh.NewConfirm().
			Title("Skip the verification encodes?").
			Description("Each rung is normally encoded and measured to confirm its prediction.").
			Affirmative("Yes, faster").
			Negative("No, verify every rung").
			Value(&a.SkipVerify),
		huh.NewSelect[string]().
			Title("Probe placement").
			Options(
				huh.NewOption("Fixed · three CRFs per resolution", "fixed"),
				huh.NewOption("Adaptive · probes where the rungs are uncertain", "adaptive"),
			).
			Value(&a.Probing),
		huh.NewConfirm().
			Title("Add per-shot rungs?").
			Description("One CRF per shot at equal rate-quality slope; costs exact probes and a verification per rung. Excludes AV1 film grain synthesis.").
			Affirmative("Yes").
			Negative("No").
			Value(&a.PerShot),
	).WithHideFunc(a.advancedHidden)
}

// filmGrainGroup asks for AV1 film grain synthesis, which per-shot rungs
// exclude.
func (a *wizardAnswers) filmGrainGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("AV1 film grain synthesis").
			Options(
				huh.NewOption("Off", "off"),
				huh.NewOption("Auto · detect grain and calibrate the level", "auto"),
			).
			Value(&a.FilmGrain),
	).WithHideFunc(a.filmGrainHidden)
}

// gpuGroup asks whether to use the GPU, only when one is usable.
func (a *wizardAnswers) gpuGroup(
	offerGPU bool,
) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title("Use the NVIDIA GPU?").
			Description("NVDEC decoding, NVENC ladder encodes, CUDA VMAF when the model allows it (--gpu).").
			Affirmative("Yes").
			Negative("No, CPU only").
			Value(&a.GPU),
	).WithHideFunc(func() bool { return !offerGPU })
}

// htmlGroup asks where to write the HTML report.
func (a *wizardAnswers) htmlGroup() *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("HTML report").
			Description("Leave empty to skip.").
			Placeholder("report.html").
			Value(&a.HTML),
	)
}

// videoPicker is a file picker restricted to video files.
func videoPicker(
	title, description string,
) *huh.FilePicker {
	return huh.NewFilePicker().
		Title(title).
		Description(description).
		AllowedTypes(videoExtensions).
		CurrentDirectory(".").
		Height(12).
		Picking(true).
		ShowPermissions(false).
		ShowSize(true).
		Validate(requirePath)
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

// subtle renders dimmed text.
func subtle(
	s string,
) string {
	return lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6C6C6C"}).Render(s)
}
