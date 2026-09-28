package main

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// reviewRow is a line of the review: an answer and, below it, what is
// known of it (the metadata of a picked video).
type reviewRow struct {
	key, value, detail string
}

// reviewSection is the review of a section; skipped says why a section
// has nothing to review.
type reviewSection struct {
	section section
	rows    []reviewRow
	skipped string
}

// reviewSections review every answer of a, section by section.
func (a *wizardAnswers) reviewSections(
	ctx wizardContext,
) []reviewSection {
	sections := []reviewSection{
		{section: sectionSource, rows: []reviewRow{a.videoRow("Video", a.Source, ctx)}},
		{section: sectionAnalysis, rows: []reviewRow{{key: "Compute", value: a.actionsLabel()}}},
		{section: sectionQuality, rows: a.qualityRows(ctx)},
		{section: sectionLadder, rows: a.ladderRows()},
		{section: sectionOutputs, rows: a.outputRows()},
	}

	if a.metricsHidden() {
		sections[sectionQuality].skipped = "no VMAF measured"
	}

	if a.ladderHidden() {
		sections[sectionLadder].skipped = "no ladder requested"
	}

	return sections
}

// videoRow reviews a picked video with its metadata.
func (a *wizardAnswers) videoRow(
	key, path string,
	ctx wizardContext,
) reviewRow {
	row := reviewRow{key: key, value: path}

	if ctx.videos != nil && path != "" {
		if sum, ok := ctx.videos.cached(path); ok && sum.hasVideo {
			row.detail = sum.line()
		}
	}

	return row
}

// actionLabels name the actions in the review.
var actionLabels = map[string]string{
	actionAnalysis: "Technical analysis",
	actionVMAF:     "VMAF",
	actionLadder:   "Per-title ladder",
}

// actionsLabel lists the chosen actions, in the order of the form.
func (a *wizardAnswers) actionsLabel() string {
	var names []string

	for _, action := range []string{actionAnalysis, actionVMAF, actionLadder} {
		if a.wants(action) {
			names = append(names, actionLabels[action])
		}
	}

	return strings.Join(names, ", ")
}

// qualityRows review the reference, the VMAF mode, the metrics and the HDR
// scoring.
func (a *wizardAnswers) qualityRows(
	ctx wizardContext,
) []reviewRow {
	if a.metricsHidden() {
		return nil
	}

	var rows []reviewRow

	if a.wants(actionVMAF) {
		rows = append(rows, a.videoRow("Reference", a.Reference, ctx), reviewRow{key: "VMAF", value: a.vmafModeLabel()})
	}

	rows = append(rows, reviewRow{key: "Metrics", value: a.metricsLabel()})

	if len(a.Devices) > 0 {
		rows = append(rows, reviewRow{key: "Devices", value: devicesLabel(a.Devices)})
	}

	if dr := a.hdrDetected(ctx.detectHDR); dr != "" {
		rows = append(rows, reviewRow{key: "HDR", value: dr + ", " + hdrMetricLabel(a.HDRMetric)})
	}

	return rows
}

// vmafModeLabel describes the VMAF mode and its value.
func (a *wizardAnswers) vmafModeLabel() string {
	switch a.VMAFMode {
	case vmafExact:
		return "exact, every frame"
	case vmafShare:
		return percentAnswer(a.Share) + "% of the frames, fixed budget"
	case vmafPerScene:
		return strings.TrimSpace(a.PerScene) + " clips per scene, fixed budget"
	}

	return "± " + strings.TrimSpace(a.Precision) + " at 95% confidence, adaptive"
}

// metricsLabel lists the metrics measured next to VMAF (nil metrics are
// the defaults, as for metricArgs).
func (a *wizardAnswers) metricsLabel() string {
	metrics := a.Metrics
	if metrics == nil {
		metrics = defaultMetrics
	}

	if len(metrics) == 0 {
		return "VMAF only"
	}

	var names []string

	for _, o := range metricOptions() {
		if slices.Contains(metrics, o.Value) {
			name, _, _ := strings.Cut(o.Key, " ")
			names = append(names, name)
		}
	}

	return "VMAF + " + strings.Join(names, ", ")
}

// deviceLabels name the viewing devices.
var deviceLabels = map[string]string{vmaf.DevicePhone: "phone", vmaf.DeviceTV: "TV", vmaf.Device4K: "4K TV"}

// devicesLabel lists the viewing devices.
func devicesLabel(
	devices []string,
) string {
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = deviceLabels[d]
	}

	return strings.Join(names, ", ")
}

// hdrMetricLabel says how VMAF scores an HDR video.
func hdrMetricLabel(
	metric string,
) string {
	if metric == string(quality.HDRMetricToneMap) {
		return "scored on an SDR tone mapping"
	}

	return "scored on the HDR signal"
}

// codecLabels name the ladder codecs.
var codecLabels = map[string]string{"h264": "H.264", "hevc": "HEVC", av1Codec: "AV1"}

// ladderRows review the codecs and, when customised, the ladder settings.
func (a *wizardAnswers) ladderRows() []reviewRow {
	if a.ladderHidden() {
		return nil
	}

	codecs := make([]string, len(a.Codecs))
	for i, c := range a.Codecs {
		codecs[i] = codecLabels[c]
	}

	rows := []reviewRow{{key: "Codecs", value: strings.Join(codecs, ", ")}}
	if !a.Advanced {
		return append(rows, reviewRow{key: "Settings", value: "automatic"})
	}

	rows = append(rows,
		reviewRow{key: "Shape", value: a.shapeLabel()},
		reviewRow{key: "Quality", value: "VMAF " + strings.TrimSpace(a.MinVMAF) + " to " + strings.TrimSpace(a.TopVMAF)},
		reviewRow{key: "Encoding", value: a.encodingLabel()},
		reviewRow{key: "Rungs", value: a.rungsLabel()},
	)

	if !a.filmGrainHidden() && changed(a.FilmGrain, defaultFilmGrain) {
		rows = append(rows, reviewRow{key: "Film grain", value: a.FilmGrain})
	}

	return rows
}

// shapeLabel describes the ladder shape.
func (a *wizardAnswers) shapeLabel() string {
	switch a.Shape {
	case shapeCount:
		return strings.TrimSpace(a.RungCount) + " rungs"
	case shapeResolutions:
		heights, _ := parseHeights(a.Resolutions)

		return strings.Join(heights, ", ")
	}

	return "automatic, one quality step at a time"
}

// encodingLabel describes the bitrate cap, the preset and the bit depth.
func (a *wizardAnswers) encodingLabel() string {
	parts := []string{"no bitrate cap"}
	if kbps := strings.TrimSpace(a.MaxBitrate); kbps != "" {
		parts[0] = "cap " + kbps + " kb/s"
	}

	preset := "default preset"
	if p := strings.TrimSpace(a.Preset); p != "" {
		preset = "preset " + p
	}

	return strings.Join(append(parts, preset, a.BitDepth+"-bit"), ", ")
}

// rungsLabel describes the verification, the probes and the per-shot
// rungs.
func (a *wizardAnswers) rungsLabel() string {
	parts := []string{"verified"}
	if a.SkipVerify {
		parts[0] = "not verified"
	}

	parts = append(parts, a.Probing+" probes")
	if a.PerShot {
		parts = append(parts, "per-shot")
	}

	return strings.Join(parts, ", ")
}

// outputRows review the HTML report, the annotated video and the hardware.
func (a *wizardAnswers) outputRows() []reviewRow {
	rows := []reviewRow{{key: "Report", value: "terminal" + optionalFile(", HTML ", a.HTML)}}

	if !a.overlayHidden() {
		annotated := "none"
		if path := strings.TrimSpace(a.OverlayPath); a.Overlay && path != "" {
			annotated = path
		}

		rows = append(rows, reviewRow{key: "Annotated", value: annotated})
	}

	return append(rows, reviewRow{key: "Hardware", value: wizardHardware(a.GPU, runtime.GOOS)})
}

// wizardHardware names the hardware a wizard run uses on the operating
// system goos, in the words of the HTML report ("Apple", "NVIDIA"): the
// NVIDIA GPU with --gpu; otherwise the default --hwaccel auto, which is
// Apple's VideoToolbox on macOS (the concurrent decodes of the frame
// analysis and VMAF, and the annotated video) and the CPU elsewhere.
func wizardHardware(
	gpu bool,
	goos string,
) string {
	switch {
	case gpu:
		return "NVIDIA GPU (NVDEC, NVENC, CUDA VMAF)"
	case decode.HWAccelAuto.Resolve(goos) == decode.HWAccelAuto:
		return "Apple VideoToolbox (auto)"
	}

	return "CPU"
}

// optionalFile is prefix and the trimmed path, or "" without a path.
func optionalFile(
	prefix, path string,
) string {
	if path = strings.TrimSpace(path); path == "" {
		return ""
	}

	return prefix + path
}

// plainReview is the review as plain text, for the accessible mode.
func plainReview(
	a *wizardAnswers,
	ctx wizardContext,
) string {
	var b strings.Builder

	b.WriteString("Review\n")

	for _, s := range a.reviewSections(ctx) {
		b.WriteString("\n" + strconv.Itoa(int(s.section)+1) + ". " + s.section.String() + "\n")

		if s.skipped != "" {
			b.WriteString("   " + s.skipped + "\n")
		}

		for _, r := range s.rows {
			fmt.Fprintf(&b, "   %-10s %s\n", r.key, r.value)

			if r.detail != "" {
				fmt.Fprintf(&b, "   %-10s %s\n", "", r.detail)
			}
		}
	}

	b.WriteString("\nCommand: " + commandLine(append([]string{"qc", "run"}, a.runArgs()...)))

	return b.String()
}
