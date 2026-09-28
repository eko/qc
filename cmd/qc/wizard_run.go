package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/eko/qc/quality"
)

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

	videos := newVideoCache(cmd.Context(), config.Tools.FFprobe)

	answers, err := env.askWizard(wizardContext{
		offerGPU:  offerGPU,
		detectHDR: videos.dynamicRange,
		videos:    videos,
	})
	if errors.Is(err, huh.ErrUserAborted) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("wizard: %w", err)
	}

	args := answers.runArgs()
	fmt.Fprintln(stderr, handoff(args, newWizardStyle(os.Getenv, lipgloss.ColorProfile())))

	if err := runCmd.ParseFlags(args); err != nil {
		return fmt.Errorf("wizard: %w", err)
	}

	runCmd.SetContext(cmd.Context())

	return runEverything(runCmd, env, runCmd.Flags().Arg(0))
}

// handoff introduces the dashboard: the command the wizard runs, to run it
// again without the wizard.
func handoff(
	args []string,
	s wizardStyle,
) string {
	command := commandLine(append([]string{"qc", "run"}, args...))

	return "  " + s.faint.Render("equivalent command "+s.g.dot+" run it again without the wizard") + "\n" +
		"  " + s.accent.Render("$ ") + s.text.Render(command)
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

// wizardContext is what the wizard knows before asking, and what its pages
// share.
type wizardContext struct {
	offerGPU bool
	// detectHDR returns the dynamic range of an HDR video ("HDR10",
	// "HLG"...), "" for SDR or when unknown; nil detects nothing.
	detectHDR func(path string) string
	// videos describes the files the pickers highlight and pick.
	videos *videoCache
	// layout is the room the page gives the form; style its look.
	layout *wizardLayout
	style  wizardStyle
}

// askWizard runs the wizard in the terminal: the full-screen form and its
// review, or plain prompts in accessible mode (ACCESSIBLE set, or TERM=dumb)
// for screen readers.
func askWizard(
	ctx wizardContext,
) (wizardAnswers, error) {
	return askWizardWith(ctx, os.Getenv, nil, os.Stderr)
}

// askWizardWith runs the wizard reading in (the terminal when nil) and
// drawing on out.
func askWizardWith(
	ctx wizardContext,
	getenv func(string) string,
	in io.Reader,
	out io.Writer,
) (wizardAnswers, error) {
	// Adaptive colours query the terminal background: once, before the form
	// reads the terminal.
	lipgloss.HasDarkBackground()

	ctx.style = newWizardStyle(getenv, lipgloss.ColorProfile())
	ctx.layout = &wizardLayout{}
	answers := newWizardAnswers()

	if accessibleMode(getenv) {
		err := runAccessible(ctx, answers, out, cmp.Or[io.Reader](in, os.Stdin))

		return *answers, err
	}

	options := []tea.ProgramOption{tea.WithAltScreen(), tea.WithOutput(out)}
	if in != nil {
		options = append(options, tea.WithInput(in))
	}

	final, err := tea.NewProgram(newWizardModel(answers, ctx), options...).Run()

	return wizardOutcome(answers, final, err)
}

// wizardOutcome is the result of the wizard program: the answers once run
// was chosen, huh.ErrUserAborted when cancelled.
func wizardOutcome(
	answers *wizardAnswers,
	final tea.Model,
	err error,
) (wizardAnswers, error) {
	if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
		return wizardAnswers{}, huh.ErrUserAborted
	}

	if err != nil {
		return wizardAnswers{}, fmt.Errorf("run wizard: %w", err)
	}

	if m, ok := final.(*wizardModel); !ok || m.phase != phaseDone {
		return wizardAnswers{}, huh.ErrUserAborted
	}

	return *answers, nil
}

// accessibleMode reports whether to ask with plain prompts: ACCESSIBLE is
// set (the Charm convention) or the terminal is dumb.
func accessibleMode(
	getenv func(string) string,
) bool {
	return getenv("ACCESSIBLE") != "" || getenv("TERM") == "dumb"
}

// runAccessible asks the pages one by one with plain prompts, skipping
// those the answers make irrelevant, then offers to run, edit a section or
// cancel.
func runAccessible(
	ctx wizardContext,
	a *wizardAnswers,
	w io.Writer,
	r io.Reader,
) error {
	theme := wizardTheme(ctx.style)
	steps := a.formSteps(ctx)

	for {
		if err := askAccessibly(steps, theme, w, r); err != nil {
			return err
		}

		if requirePath(a.Source) != nil {
			return huh.ErrUserAborted
		}

		fmt.Fprintln(w, "\n"+plainReview(a, ctx))

		target, err := askReviewAction(visibleSections(a.formSteps(ctx)), theme, w, r)
		if err != nil {
			return err
		}

		if target == sectionReview {
			return nil
		}

		steps = editSteps(a.formSteps(ctx), target)
	}
}

// askAccessibly asks the visible pages of steps.
func askAccessibly(
	steps []wizardStep,
	theme *huh.Theme,
	w io.Writer,
	r io.Reader,
) error {
	for _, s := range steps {
		if s.isHidden() {
			continue
		}

		form := huh.NewForm(huh.NewGroup(s.fields...)).WithTheme(theme).WithAccessible(true).WithInput(r).WithOutput(w)
		if err := form.Run(); err != nil {
			return fmt.Errorf("ask: %w", err)
		}
	}

	return nil
}

// Review actions of the accessible mode.
const (
	reviewRun    = "run"
	reviewEdit   = "edit"
	reviewCancel = "cancel"
)

// askReviewAction asks what to do after the review: sectionReview to run,
// the section to edit, or huh.ErrUserAborted to cancel.
func askReviewAction(
	visible map[section]bool,
	theme *huh.Theme,
	w io.Writer,
	r io.Reader,
) (section, error) {
	action := reviewRun

	fmt.Fprintln(w)

	_ = huh.NewSelect[string]().Title("Run this command?").Options(
		huh.NewOption("Run", reviewRun),
		huh.NewOption("Edit a section", reviewEdit),
		huh.NewOption("Cancel", reviewCancel),
	).Value(&action).WithTheme(theme).RunAccessible(w, r)

	switch action {
	case reviewCancel:
		return sectionReview, huh.ErrUserAborted
	case reviewRun:
		return sectionReview, nil
	}

	var options []huh.Option[section]

	for s := sectionSource; s < sectionReview; s++ {
		if visible[s] {
			options = append(options, huh.NewOption(s.String(), s))
		}
	}

	target := sectionSource
	_ = huh.NewSelect[section]().Title("Edit which section?").Options(options...).Value(&target).WithTheme(theme).RunAccessible(w, r)

	return target, nil
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
