package main

import (
	"cmp"
	"slices"

	"github.com/charmbracelet/huh"
)

// section is a stage of the wizard, shown in its step indicator.
type section int

// The sections, in order. Review is the page that closes the form.
const (
	sectionSource section = iota
	sectionAnalysis
	sectionQuality
	sectionLadder
	sectionOutputs
	sectionReview
)

// sectionInfo names a section and says what it is about.
var sectionInfo = []struct{ name, blurb string }{
	sectionSource:   {"Source", "the video to check"},
	sectionAnalysis: {"Analysis", "what to compute"},
	sectionQuality:  {"Quality", "how quality is measured"},
	sectionLadder:   {"Ladder", "per-title encoding ladder"},
	sectionOutputs:  {"Outputs", "reports, annotated video, hardware"},
	sectionReview:   {"Review", "check everything, then run"},
}

// String implements fmt.Stringer.
func (s section) String() string {
	return sectionInfo[s].name
}

// wizardStep is a page of the form: its fields, its section, and when it
// is skipped (hidden depends on the answers given before it).
type wizardStep struct {
	section section
	fields  []huh.Field
	hidden  func() bool
}

// isHidden reports whether the page is skipped with the current answers.
func (s wizardStep) isHidden() bool {
	return s.hidden != nil && s.hidden()
}

// formSteps are the pages of the wizard, in order. Each writes its answers
// into a. The annotated video comes last, as asked by its users.
func (a *wizardAnswers) formSteps(
	ctx wizardContext,
) []wizardStep {
	return []wizardStep{
		{section: sectionSource, fields: a.sourceFields(ctx)},
		{section: sectionAnalysis, fields: a.actionsFields(), hidden: a.isProgram},
		{section: sectionAnalysis, fields: a.programActionFields(), hidden: a.programHidden},
		{section: sectionAnalysis, fields: a.sampleFields(), hidden: a.sampleHidden},
		{section: sectionAnalysis, fields: a.sampleShareFields(), hidden: a.sampleShareHidden},
		{section: sectionQuality, fields: a.referenceFields(ctx), hidden: a.vmafHidden},
		{section: sectionQuality, fields: a.precisionFields(), hidden: a.vmafModeHidden(vmafPrecision)},
		{section: sectionQuality, fields: a.shareFields(), hidden: a.vmafModeHidden(vmafShare)},
		{section: sectionQuality, fields: a.perSceneFields(), hidden: a.vmafModeHidden(vmafPerScene)},
		{section: sectionQuality, fields: a.metricsFields(), hidden: a.metricsHidden},
		a.hdrStep(ctx.detectHDR),
		{section: sectionLadder, fields: a.codecsFields(), hidden: a.codecsHidden},
		{section: sectionLadder, fields: a.programFields(), hidden: a.programLadderHidden},
		{section: sectionLadder, fields: a.shapeFields(), hidden: a.advancedHidden},
		{section: sectionLadder, fields: a.rungCountFields(), hidden: a.shapeHidden(shapeCount)},
		{section: sectionLadder, fields: a.resolutionsFields(), hidden: a.shapeHidden(shapeResolutions)},
		{section: sectionLadder, fields: a.encodingFields(true), hidden: a.encodingHidden(false)},
		{section: sectionLadder, fields: a.encodingFields(false), hidden: a.encodingHidden(true)},
		{section: sectionLadder, fields: a.filmGrainFields(), hidden: a.filmGrainHidden},
		{section: sectionOutputs, fields: a.gpuFields(), hidden: func() bool { return !ctx.offerGPU }},
		{section: sectionOutputs, fields: a.htmlFields(), hidden: a.isSample},
		{section: sectionOutputs, fields: a.overlayFields(), hidden: a.overlayHidden},
		{section: sectionOutputs, fields: a.overlayPathFields(), hidden: a.overlayPathHidden},
		{section: sectionOutputs, fields: a.renditionsFields(), hidden: a.renditionsHidden},
		{section: sectionOutputs, fields: a.renditionsDirFields(), hidden: a.renditionsDirHidden},
	}
}

// visibleSections are the sections with a page to show, and Review.
func visibleSections(
	steps []wizardStep,
) map[section]bool {
	visible := map[section]bool{sectionReview: true}

	for _, s := range steps {
		if !s.isHidden() {
			visible[s.section] = true
		}
	}

	return visible
}

// editSteps are the pages to show to edit section target: its own, and
// the later ones skipped so far, which the edit may call for (an added
// VMAF needs a reference). Pages still skipped after the edit are skipped
// again by the form.
func editSteps(
	steps []wizardStep,
	target section,
) []wizardStep {
	var edit []wizardStep

	for _, s := range steps {
		if s.section == target || (s.section > target && s.isHidden()) {
			edit = append(edit, s)
		}
	}

	return edit
}

// wizardForm is a huh form built from steps, with the section of each field
// for the step indicator.
type wizardForm struct {
	form     *huh.Form
	steps    []wizardStep
	sections map[huh.Field]section
}

// newWizardForm builds the form of steps. The page shows the errors (see
// footerView), pickers their own.
func newWizardForm(
	steps []wizardStep,
	theme *huh.Theme,
) wizardForm {
	groups := make([]*huh.Group, len(steps))
	sections := map[huh.Field]section{}

	for i, s := range steps {
		group := huh.NewGroup(s.fields...).WithShowErrors(false)
		if s.hidden != nil {
			group = group.WithHideFunc(s.hidden)
		}

		for _, f := range s.fields {
			sections[f] = s.section
		}

		groups[i] = group
	}

	form := huh.NewForm(groups...).WithTheme(theme).WithShowHelp(false)

	return wizardForm{form: form, steps: steps, sections: sections}
}

// isPicker reports whether f is a video picker.
func isPicker(
	f huh.Field,
) bool {
	_, ok := f.(*videoPicker)

	return ok
}

// current is the section of the focused field.
func (w wizardForm) current() section {
	return w.sections[w.form.GetFocusedField()]
}

// wants reports whether action was picked. Several videos get one thing:
// a ladder, or a sample.
func (a *wizardAnswers) wants(
	action string,
) bool {
	if a.isProgram() {
		return action == cmp.Or(a.ProgramAction, actionLadder)
	}

	return slices.Contains(a.Actions, action)
}

// sampleHidden hides the settings of a sample unless one is extracted.
func (a *wizardAnswers) sampleHidden() bool {
	return !a.isSample()
}

// sampleShareHidden hides the share of complex scenes unless the sample
// mixes them with representative ones.
func (a *wizardAnswers) sampleShareHidden() bool {
	return !a.isSample() || !a.sampleMixed()
}

// programLadderHidden hides the codecs of the ladders of several videos
// unless they get a ladder.
func (a *wizardAnswers) programLadderHidden() bool {
	return !a.isProgram() || a.ladderHidden()
}

// codecsHidden hides the codecs of the ladders of one video.
func (a *wizardAnswers) codecsHidden() bool {
	return a.ladderHidden() || a.isProgram()
}

// programHidden hides what is asked of several videos only.
func (a *wizardAnswers) programHidden() bool {
	return !a.isProgram()
}

// encodingHidden hides the encoding settings unless the ladder is
// customised, and those of the other kind of ladder: of several videos or
// of one.
func (a *wizardAnswers) encodingHidden(
	program bool,
) func() bool {
	return func() bool { return a.advancedHidden() || a.isProgram() != program }
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
	return a.advancedHidden() || !slices.Contains(a.Codecs, av1Codec) || a.perShot()
}

// overlayHidden hides the annotated copy unless the source is analysed or
// compared: it would only show what the bitstream tells.
func (a *wizardAnswers) overlayHidden() bool {
	return !a.wants(actionAnalysis) && !a.wants(actionVMAF)
}

// renditionsHidden hides the encoding of the ladders without a ladder.
func (a *wizardAnswers) renditionsHidden() bool {
	return a.ladderHidden()
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
