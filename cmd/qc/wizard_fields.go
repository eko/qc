package main

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// sourceFields ask for the video to analyse.
func (a *wizardAnswers) sourceFields(
	ctx wizardContext,
) []huh.Field {
	return []huh.Field{
		newVideoPicker("Video to analyse", "The source: a mezzanine for ladders, or an encode to compare for VMAF.", &a.Source, ctx).
			Several(&a.Program),
	}
}

// actionsFields ask what to compute.
func (a *wizardAnswers) actionsFields() []huh.Field {
	return []huh.Field{
		huh.NewMultiSelect[string]().
			Title("What should I compute?").
			Options(
				huh.NewOption("Technical analysis · bitrate, GOP, SI/TI, shots, black/freeze, crop", actionAnalysis),
				huh.NewOption("VMAF against a reference", actionVMAF),
				huh.NewOption("Per-title streaming ladder", actionLadder),
				huh.NewOption("Extract a sample · scenes of the video copied into a new file (alone)", actionSample),
			).
			Validate(validateActions).
			Value(&a.Actions),
	}
}

// validateActions wants at least one action, and a sample alone: it is
// another command, which computes nothing else.
func validateActions(
	actions []string,
) error {
	if len(actions) == 0 {
		return errors.New("pick at least one")
	}

	if slices.Contains(actions, actionSample) && len(actions) > 1 {
		return errors.New("a sample is extracted alone: untick the rest, or the sample")
	}

	return nil
}

// programActionFields ask what to do with several videos.
func (a *wizardAnswers) programActionFields() []huh.Field {
	return []huh.Field{
		huh.NewSelect[string]().
			Title("What should I do with these videos?").
			Options(
				huh.NewOption("One streaming ladder for all of them", actionLadder),
				huh.NewOption("Extract a sample · scenes of each video copied into one file", actionSample),
			).
			Value(&a.ProgramAction),
	}
}

// sampleFields ask for the length of the sample, its scenes and its file.
func (a *wizardAnswers) sampleFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Length of the sample (seconds)").
			Description("Shared equally between the videos. Scenes are whole GOPs, copied as they are: about that long.").
			Validate(validateRange(1, 86_400, false)).
			Value(&a.SampleDuration),
		huh.NewSelect[string]().
			Title("Scenes").
			Options(
				huh.NewOption("Most complex and representative · a share of each", "mixed"),
				huh.NewOption("Most complex · what costs an encoder most", "top"),
				huh.NewOption("Representative · the SI and TI of each video", "average"),
				huh.NewOption("Easiest · the lowest SI × TI", "easy"),
			).
			Value(&a.SampleScenes),
		huh.NewInput().
			Title("Sample file").
			Description("Its extension picks the container; .mkv takes any codec. Video only.").
			Placeholder(defaultSampleFile).
			Validate(requireName).
			Value(&a.SampleTo),
	}
}

// sampleShareFields ask for the share of complex scenes of a mixed sample.
func (a *wizardAnswers) sampleShareFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Share of the most complex scenes (%)").
			Description("The rest of the sample is representative scenes.").
			Validate(validateTopShare).
			Value(&a.SampleTopShare),
	}
}

// validateTopShare accepts a percentage strictly between 0 and 100.
func validateTopShare(
	s string,
) error {
	if v, err := strconv.ParseFloat(percentAnswer(s), 64); err != nil || v <= 0 || v >= percentMax {
		return errors.New("enter a percentage between 0 and 100, both excluded")
	}

	return nil
}

// referenceFields ask for the reference, browsed from the folder of the
// source, and the VMAF mode.
func (a *wizardAnswers) referenceFields(
	ctx wizardContext,
) []huh.Field {
	return []huh.Field{
		newVideoPicker("Reference video", "The pristine version the source is compared to.", &a.Reference, ctx).
			StartIn(func() string { return a.Source }),
		huh.NewSelect[string]().
			Title("VMAF mode").
			Options(
				huh.NewOption("Target precision (± VMAF, adaptive)", vmafPrecision),
				huh.NewOption("Fixed budget · share of frames", vmafShare),
				huh.NewOption("Fixed budget · clips per scene", vmafPerScene),
				huh.NewOption("Exact (every frame)", vmafExact),
			).
			Value(&a.VMAFMode),
	}
}

// precisionFields ask for the target precision of the adaptive mode.
func (a *wizardAnswers) precisionFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Target precision (± VMAF, 95% confidence)").
			Description("Clips are scored until the interval is this narrow.").
			Validate(validateRange(0.05, 5, false)).
			Value(&a.Precision),
	}
}

// shareFields ask for the share of frames of a fixed budget.
func (a *wizardAnswers) shareFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Share of frames to score (%)").
			Description("One round over the whole video; the report gives the 95% interval it reaches.").
			Validate(validateShare).
			Value(&a.Share),
	}
}

// perSceneFields ask for the clips per scene of a fixed budget.
func (a *wizardAnswers) perSceneFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Clips per scene").
			Description("4-frame clips in every scene (shot or GOP); 1 gives a wider, conservative interval.").
			Validate(validatePerScene).
			Value(&a.PerScene),
	}
}

// metricsFields ask for the metrics and devices measured next to VMAF, on
// the comparison and on the verified ladder rungs.
func (a *wizardAnswers) metricsFields() []huh.Field {
	return []huh.Field{
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
	}
}

// hdrFields ask how VMAF scores an HDR source, only when the picked source
// (or reference) is HDR and something measures VMAF.
func (a *wizardAnswers) hdrStep(
	detect func(path string) string,
) wizardStep {
	var detected string

	hidden := func() bool {
		detected = a.hdrDetected(detect)

		return detected == ""
	}

	return wizardStep{section: sectionQuality, hidden: hidden, fields: []huh.Field{
		huh.NewSelect[string]().
			TitleFunc(func() string { return detected + " source: how should VMAF score it?" }, &a.Source).
			Description("VMAF has no HDR model. HDR metrics (wPSNR, ΔE ITP) and light levels are measured either way.").
			Options(
				huh.NewOption("On the HDR signal · fast, ranks encodes, not HDR-calibrated", string(quality.HDRMetricPQ)),
				huh.NewOption("On an SDR tone mapping · slower, what SDR screens show", string(quality.HDRMetricToneMap)),
			).
			Value(&a.HDRMetric),
	}}
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

// codecOptions are the codecs a ladder is built for.
func codecOptions() []huh.Option[string] {
	return []huh.Option[string]{
		huh.NewOption("H.264 · libx264", "h264"),
		huh.NewOption("HEVC · libx265", "hevc"),
		huh.NewOption("AV1 · SVT-AV1", av1Codec),
	}
}

// codecsFields ask for the ladder codecs, the digest, and whether to
// customise the ladders.
func (a *wizardAnswers) codecsFields() []huh.Field {
	return []huh.Field{
		huh.NewMultiSelect[string]().
			Title("Ladder codecs").
			Options(codecOptions()...).
			Validate(requireOne("pick at least one codec")).
			Value(&a.Codecs),
		huh.NewSelect[string]().
			Title("Digest the ladder is estimated on").
			Description("40 s of the title. Balanced and the most complex scenes read its frame analysis.").
			Options(
				huh.NewOption("Balanced · as busy as the title (its SI and TI)", "balanced"),
				huh.NewOption("Most complex scenes · what the demanding scenes need, not the title", "top"),
				huh.NewOption("Uniform · evenly spaced segments", "uniform"),
			).
			Value(&a.Digest),
		a.customiseField(),
	}
}

// programFields ask for the codecs of the ladders of several videos, their
// digest, and whether to customise them. The page is as tall as that of
// one video (codecsFields), which fills an 80×24 terminal.
func (a *wizardAnswers) programFields() []huh.Field {
	return []huh.Field{
		huh.NewMultiSelect[string]().
			Title("Ladder codecs · one ladder each, for all the videos").
			Options(codecOptions()...).
			Validate(requireOne("pick at least one codec")).
			Value(&a.Codecs),
		huh.NewSelect[string]().
			Title("Digest the ladder is estimated on").
			Description("About 40 s, shared equally between the videos whatever their length.").
			Options(
				huh.NewOption("Balanced · each video's part as busy as the video (its SI and TI)", "balanced"),
				huh.NewOption("Most complex scenes · what the demanding scenes of each video need", "top"),
				huh.NewOption("Uniform · evenly spaced segments", "uniform"),
			).
			Value(&a.Digest),
		a.customiseField(),
	}
}

// customiseField asks whether to customise the ladder.
func (a *wizardAnswers) customiseField() huh.Field {
	return newConfirm().
		Title("Customise the ladder?").
		Description("Rung count or resolutions, quality range, bitrate cap, preset, bit depth.").
		Affirmative("Yes").
		Negative("No, automatic").
		Value(&a.Advanced)
}

// shapeFields ask for the ladder shape and its quality range.
func (a *wizardAnswers) shapeFields() []huh.Field {
	return []huh.Field{
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
	}
}

// rungCountFields ask for the rung count of a fixed-count shape.
func (a *wizardAnswers) rungCountFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Number of rungs").
			Validate(validateCount).
			Value(&a.RungCount),
	}
}

// resolutionsFields ask for the rung resolutions of an imposed shape.
func (a *wizardAnswers) resolutionsFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Rung resolutions, top first").
			Description("Heights separated by commas; a height may repeat.").
			Placeholder("1080,720,720,540,360").
			Validate(validateHeights).
			Value(&a.Resolutions),
	}
}

// encodingFields ask for the encoding settings, the verification, the
// probe placement and, with perShot, the per-shot rungs (of one title: the
// ladder of several videos has none).
func (a *wizardAnswers) encodingFields(
	perShot bool,
) []huh.Field {
	fields := []huh.Field{
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
		newConfirm().
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
	}

	if !perShot {
		return fields
	}

	return append(fields, newConfirm().
		Title("Add per-shot rungs?").
		Description("One CRF per shot at equal rate-quality slope; costs exact probes and a verification per rung. Excludes AV1 film grain synthesis.").
		Affirmative("Yes").
		Negative("No").
		Value(&a.PerShot))
}

// filmGrainFields ask for AV1 film grain synthesis, which per-shot rungs
// exclude.
func (a *wizardAnswers) filmGrainFields() []huh.Field {
	return []huh.Field{
		huh.NewSelect[string]().
			Title("AV1 film grain synthesis").
			Options(
				huh.NewOption("Off", "off"),
				huh.NewOption("Auto · detect grain and calibrate the level", "auto"),
			).
			Value(&a.FilmGrain),
	}
}

// gpuFields ask whether to use the GPU.
func (a *wizardAnswers) gpuFields() []huh.Field {
	return []huh.Field{
		newConfirm().
			Title("Use the NVIDIA GPU?").
			Description("NVDEC decoding, NVENC ladder encodes, CUDA VMAF when the model allows it (--gpu).").
			Affirmative("Yes").
			Negative("No, CPU only").
			Value(&a.GPU),
	}
}

// htmlFields ask where to write the HTML report.
func (a *wizardAnswers) htmlFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("HTML report").
			Description("Leave empty to skip.").
			Placeholder("report.html").
			Value(&a.HTML),
	}
}

// overlayFields ask whether to write an annotated copy of the source.
func (a *wizardAnswers) overlayFields() []huh.Field {
	return []huh.Field{
		newConfirm().
			Title("Produce an annotated video?").
			Description("A copy of the source with the analysis (and the VMAF of each scored frame) burnt in: timecode, " +
				"bitrate, shots, camera motion, SI/TI, levels, timeline. Every frame has a VMAF with the exact mode.").
			Affirmative("Yes").
			Negative("No").
			Value(&a.Overlay),
	}
}

// overlayPathFields ask where to write the annotated copy.
func (a *wizardAnswers) overlayPathFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Annotated video").
			Description("An H.264 file (MP4, MKV or MOV).").
			Placeholder("annotated.mp4").
			Validate(requireName).
			Value(&a.OverlayPath),
	}
}

// renditionsFields ask whether to encode the ladders on the whole title.
func (a *wizardAnswers) renditionsFields() []huh.Field {
	return []huh.Field{
		newConfirm().
			Title("Encode the ladder?").
			Description("Every rung encoded on the whole title (a folder per codec), each measured against the source: " +
				"its bitrate and VMAF on the whole title next to the ladder's prediction. Takes as long as the encodes.").
			Affirmative("Yes").
			Negative("No").
			Value(&a.Renditions),
	}
}

// renditionsDirFields ask where to write the renditions.
func (a *wizardAnswers) renditionsDirFields() []huh.Field {
	return []huh.Field{
		huh.NewInput().
			Title("Renditions folder").
			Description("Created if missing; the renditions of each codec go into a subfolder.").
			Placeholder("renditions").
			Validate(requireName).
			Value(&a.RenditionsDir),
	}
}

// renditionsDirHidden hides the renditions folder unless the renditions
// were asked for.
func (a *wizardAnswers) renditionsDirHidden() bool {
	return a.renditionsHidden() || !a.Renditions
}

// overlayPathHidden hides the path of the annotated copy unless one was
// asked for.
func (a *wizardAnswers) overlayPathHidden() bool {
	return a.overlayHidden() || !a.Overlay
}

// requireName rejects an empty file name.
func requireName(
	name string,
) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("enter a file name")
	}

	return nil
}

// newConfirm is a yes/no question with its buttons under the question,
// aligned with the other fields.
func newConfirm() *huh.Confirm {
	return huh.NewConfirm().WithButtonAlignment(lipgloss.Left)
}
