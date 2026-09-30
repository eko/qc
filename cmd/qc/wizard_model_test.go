package main

import (
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestWizard starts the wizard in the test folder, sized width × height.
func newTestWizard(
	t *testing.T,
	width, height int,
) *wizardModel {
	t.Helper()

	wizardDir(t)

	m := newWizardModel(newWizardAnswers(), testWizardContext(t, false))
	send(m, tea.WindowSizeMsg{Width: width, Height: height})
	send(m, runCmd(m.Init())...)

	return m
}

// view is the screen without its styles.
func view(
	m *wizardModel,
) string {
	return ansi.Strip(m.View())
}

// toReview answers the default wizard: the source, then enter on every
// page (analysis and an H.264 ladder).
func toReview(
	t *testing.T,
	m *wizardModel,
) {
	t.Helper()

	send(m, runes("source"), keyEnter)
	require.Equal(t, sectionAnalysis, m.current(), view(m))

	// Analysis, metrics, devices, codecs, customisation, HTML, annotated
	// video, renditions.
	for range 8 {
		send(m, keyEnter)
	}

	require.Equal(t, phaseReview, m.phase, view(m))
}

func TestWizardModelDefaults(
	t *testing.T,
) {
	m := newTestWizard(t, 80, 24)

	screen := view(m)
	assert.Contains(t, screen, "◆ qc   ● Source › ○ Analysis › ○ Quality › ○ Ladder › ○ Outputs › ○ Review")
	assert.Contains(t, screen, "SOURCE  ·  the video to check")
	assert.Contains(t, screen, "ctrl+c quit")

	toReview(t, m)

	screen = view(m)
	assert.Contains(t, screen, "✓ Source › ✓ Analysis › ✓ Quality › ✓ Ladder › ✓ Outputs › ● Review")
	assert.Contains(t, screen, "$ qc run source.mp4 --codecs=h264")
	assert.Contains(t, screen, "H.264 · 1920×1080 · 25 fps · 0:20 · SDR · 1 aud…", "the metadata of the source")
	assert.Contains(t, screen, "Hardware   "+wizardHardware(false, runtime.GOOS))
	assert.Equal(t, []string{"source.mp4", "--codecs=h264"}, m.answers.runArgs())

	assert.True(t, send(m, runes("r")), "run quits")
	assert.Equal(t, phaseDone, m.phase)
	assert.Empty(t, m.View())
}

func TestWizardModelBack(
	t *testing.T,
) {
	m := newTestWizard(t, 80, 24)

	send(m, runes("source"), keyEnter)
	require.Equal(t, sectionAnalysis, m.current())

	send(m, keyEsc)
	assert.Equal(t, sectionSource, m.current(), "esc goes back a page")

	// esc first clears the filter of a picker.
	send(m, runes("ref"), keyEsc)
	assert.Equal(t, sectionSource, m.current())
	assert.Contains(t, view(m), "clips/")

	assert.True(t, send(m, keyCtrlC))
	assert.Equal(t, phaseAborted, m.phase)
}

func TestWizardModelReviewActions(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		keys      []tea.Msg
		wantQuit  bool
		wantPhase wizardPhase
		wantShown section
	}{
		{name: "run with enter", keys: []tea.Msg{keyEnter}, wantQuit: true, wantPhase: phaseDone},
		{name: "cancel with q", keys: []tea.Msg{runes("q")}, wantQuit: true, wantPhase: phaseAborted},
		{name: "cancel button", keys: []tea.Msg{keyLeft, keyEnter}, wantQuit: true, wantPhase: phaseAborted},
		{name: "edit button", keys: []tea.Msg{keyRight, keyEnter, keyRight, keyEnter}, wantPhase: phaseForm, wantShown: sectionAnalysis},
		{name: "chooser wraps", keys: []tea.Msg{runes("e"), keyLeft, keyEnter}, wantPhase: phaseForm, wantShown: sectionOutputs},
		{name: "chooser digit", keys: []tea.Msg{runes("e"), runes("4")}, wantPhase: phaseForm, wantShown: sectionLadder},
		{name: "chooser closed", keys: []tea.Msg{runes("e"), keyEsc, keyTab, keyTab, keyEnter}, wantQuit: true, wantPhase: phaseAborted},
		{name: "digit", keys: []tea.Msg{runes("1")}, wantPhase: phaseForm, wantShown: sectionSource},
		{name: "no page to edit", keys: []tea.Msg{runes("3"), runes("9")}, wantPhase: phaseForm, wantShown: sectionQuality},
		{name: "esc edits the outputs", keys: []tea.Msg{keyEsc}, wantPhase: phaseForm, wantShown: sectionOutputs},
		{name: "other messages", keys: []tea.Msg{tea.FocusMsg{}, runes("z")}, wantPhase: phaseReview, wantShown: sectionReview},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			m := newTestWizard(t, 110, 40)
			toReview(t, m)

			quit := send(m, testCase.keys...)

			assert.Equal(t, testCase.wantQuit, quit)
			assert.Equal(t, testCase.wantPhase, m.phase)

			if !quit {
				assert.Equal(t, testCase.wantShown, m.current())
			}
		})
	}
}

func TestWizardModelChooser(
	t *testing.T,
) {
	m := newTestWizard(t, 110, 40)
	toReview(t, m)

	send(m, runes("e"))
	screen := view(m)
	assert.Contains(t, screen, "Edit which section?")
	assert.Contains(t, screen, "5 Outputs")
	assert.Contains(t, screen, "enter edit")
	assert.Contains(t, screen, "esc cancel edit")
}

func TestWizardModelEditAddsVMAF(
	t *testing.T,
) {
	m := newTestWizard(t, 110, 40)
	toReview(t, m)

	// Adding VMAF asks the reference, skipped so far, then its HDR scoring.
	send(m, runes("2"), keyDown, runes("x"), keyEnter)
	require.Equal(t, sectionQuality, m.current(), view(m))
	assert.Contains(t, view(m), "Reference video")

	send(m, runes("ref"), keyEnter, keyEnter, keyEnter)
	assert.Contains(t, view(m), "HDR10 source: how should VMAF score it?")

	send(m, keyDown, keyEnter)
	require.Equal(t, phaseReview, m.phase, view(m))

	screen := view(m)
	assert.Contains(t, screen, "HDR10, scored on an SDR tone mapping")
	assert.Contains(t, screen, "$ qc run source.mp4 -r ref.mov --codecs=h264 --hdr-metric tonemap")
}

func TestWizardModelLayouts(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		width      int
		height     int
		want       []string
		notWant    []string
		wantHeight int
	}{
		{
			name: "80×24", width: 80, height: 24,
			want:    []string{"● Source", "/ type to filter", "source.mp4"},
			notWant: []string{"YOUR RUN"},
		},
		{
			name: "wide terminals have a summary panel", width: 160, height: 50,
			want: []string{"YOUR RUN", "Your answers show up here.", "╭", "reading…"},
		},
		{
			name: "narrow terminals number the steps", width: 60, height: 20,
			want:    []string{"Step 1 of 6 · Source"},
			notWant: []string{"Analysis ›"},
		},
		{
			name: "too small", width: 40, height: 10,
			want: []string{"The terminal is 40×10: the wizard", "needs 56×16."},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			m := newTestWizard(t, testCase.width, testCase.height)
			send(m, keyDown, keyDown)

			screen := view(m)
			for _, want := range testCase.want {
				assert.Contains(t, screen, want)
			}

			for _, notWant := range testCase.notWant {
				assert.NotContains(t, screen, notWant)
			}

			for i, line := range strings.Split(screen, "\n") {
				assert.LessOrEqual(t, ansi.StringWidth(line), testCase.width, "line %d fits: %q", i, line)
			}

			if testCase.width >= minPageWidth {
				assert.Len(t, strings.Split(screen, "\n"), testCase.height, "the page fills the terminal")
			}
		})
	}
}

func TestWizardModelPanel(
	t *testing.T,
) {
	m := newTestWizard(t, 160, 50)
	send(m, runes("source"), keyEnter)

	screen := view(m)
	assert.Contains(t, screen, "YOUR RUN")
	assert.Contains(t, screen, "source.mp4")
	assert.NotContains(t, screen, "Compute", "only the sections answered")
}

func TestWizardModelScroll(
	t *testing.T,
) {
	m := newTestWizard(t, 80, 24)
	toReview(t, m)

	// A customised ladder does not fit 80×24: the card scrolls.
	m.answers.Advanced, m.answers.Actions = true, []string{actionAnalysis, actionVMAF, actionLadder}

	screen := view(m)
	require.True(t, m.scrollable)
	assert.Contains(t, screen, "↑↓ scroll")
	assert.Contains(t, screen, "1  Source")

	for range 30 {
		send(m, keyDown)
	}

	screen = view(m)
	assert.Contains(t, screen, "↑ scroll back")
	assert.NotContains(t, screen, "1  Source")

	send(m, keyUp)
	assert.Contains(t, view(m), "1 more line")
}

func TestWizardModelFieldError(
	t *testing.T,
) {
	m := newTestWizard(t, 80, 24)
	send(m, runes("source"), keyEnter, keyDown, runes("x"), keyEnter, runes("ref"), keyEnter, keyEnter)
	require.Contains(t, view(m), "Target precision", view(m))

	send(m, tea.KeyMsg{Type: tea.KeyCtrlU}, runes("abc"), keyEnter)
	assert.Contains(t, view(m), "✗ enter a number between 0.05 and 5")
}

func TestWizardModelASCII(
	t *testing.T,
) {
	wizardDir(t)

	ctx := testWizardContext(t, true)
	ctx.style = newWizardStyle(func(name string) string { return map[string]string{"NO_COLOR": "1"}[name] }, termenv.Ascii)

	m := newWizardModel(newWizardAnswers(), ctx)
	send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	send(m, runCmd(m.Init())...)
	send(m, runes("source"), keyEnter)

	screen := m.View()
	assert.Contains(t, screen, "qc   + Source > * Analysis > o Quality")
	assert.Contains(t, screen, "==")

	for _, r := range screen {
		assert.Less(t, r, rune(128), "ASCII only: %q", string(r))
	}
}

func TestWizardOutcome(
	t *testing.T,
) {
	answers := &wizardAnswers{Source: "source.mp4"}

	testCases := []struct {
		name    string
		final   tea.Model
		err     error
		want    wizardAnswers
		wantErr error
	}{
		{name: "run", final: &wizardModel{phase: phaseDone}, want: *answers},
		{name: "cancelled", final: &wizardModel{phase: phaseAborted}, wantErr: huh.ErrUserAborted},
		{name: "interrupted", err: tea.ErrInterrupted, wantErr: huh.ErrUserAborted},
		{name: "killed", err: tea.ErrProgramKilled, wantErr: huh.ErrUserAborted},
		{name: "terminal error", err: errUnreadable, wantErr: errUnreadable},
		{name: "unknown model", final: nil, wantErr: huh.ErrUserAborted},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := wizardOutcome(answers, testCase.final, testCase.err)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestWizardHardware(
	t *testing.T,
) {
	assert.Equal(t, "NVIDIA GPU (NVDEC, NVENC, CUDA VMAF)", wizardHardware(true, "linux"))
	assert.Equal(t, "Apple VideoToolbox (auto)", wizardHardware(false, "darwin"))
	assert.Equal(t, "CPU", wizardHardware(false, "linux"))

	rows := (&wizardAnswers{}).outputRows()
	assert.Equal(t, reviewRow{key: "Hardware", value: wizardHardware(false, runtime.GOOS)}, rows[len(rows)-1])
}
