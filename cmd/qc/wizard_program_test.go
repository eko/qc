package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/pipeline"
)

// severalPicker is a picker of the program folder that takes several
// videos, focused.
func severalPicker(
	t *testing.T,
) (*videoPicker, *string, *[]string) {
	t.Helper()

	programDir(t)

	value, more := new(string), new([]string)
	f := newVideoPicker("Video to analyse", "The source.", value, testWizardContext(t, false)).Several(more)
	f.Focus()

	return f, value, more
}

// mark presses space on the file called name, and waits for its probe.
func mark(
	t *testing.T,
	f *videoPicker,
	name string,
) {
	t.Helper()

	f.moveTo(name)
	require.Equal(t, name, f.visible[f.cursor].name)

	if msg := pickerCmd(f.handleKey(runes(" "))); msg != nil {
		f.Update(msg)
	}
}

func TestVideoPickerSeveral(
	t *testing.T,
) {
	f, value, more := severalPicker(t)
	dir := f.dir

	assert.Contains(t, ansi.Strip(f.View()), "space selects several videos")

	// Space selects the highlighted video once probed, and again takes it
	// out.
	f.moveTo("source.mp4")
	msg := pickerCmd(f.handleKey(runes(" ")))
	require.IsType(t, probedMsg{}, msg)
	assert.Contains(t, ansi.Strip(f.View()), "checking source.mp4")
	assert.Empty(t, f.marked, "not before the probe tells its format")

	_, cmd := f.Update(msg)
	assert.Nil(t, cmd, "selecting does not leave the page")
	assert.Equal(t, []string{filepath.Join(dir, "source.mp4")}, f.marked)
	assert.Contains(t, ansi.Strip(f.View()), "1 video selected · space adds another")

	mark(t, f, "source.mp4")
	assert.Empty(t, f.marked)

	// A folder is not selected; what cannot share the ladder is refused.
	f.moveTo("clips")
	assert.Nil(t, f.handleKey(runes(" ")))

	mark(t, f, "source.mp4")

	for name, want := range map[string]string{
		"ref.mov":    "not 1920×1080 like source.mp4: one ladder needs videos of one format",
		"broken.mp4": "cannot read this file",
		"audio.m4v":  "no video stream",
	} {
		mark(t, f, name)
		require.ErrorContains(t, f.Error(), want, name)
		assert.Contains(t, ansi.Strip(f.View()), want, "told under the list")
		assert.Len(t, f.marked, 1, name)
	}

	// Selected videos are ticked; enter moves on with them, whatever is
	// highlighted.
	mark(t, f, "episode.mp4")
	require.NoError(t, f.Error())

	screen := ansi.Strip(f.View())
	assert.Contains(t, screen, "✓ episode.mp4")
	assert.Contains(t, screen, "✓ source.mp4")
	assert.NotContains(t, screen, "✓ ref.mov")
	assert.Contains(t, screen, "2 videos selected · one ladder for all of them · enter continues")

	f.moveTo("ref.mov")
	assert.IsType(t, huh.NextField(), f.handleKey(keyEnter)())
	assert.Equal(t, "source.mp4", *value)
	assert.Equal(t, []string{"episode.mp4"}, *more)

	// Blurred, the picker tells there are more.
	f.Blur()
	assert.Contains(t, ansi.Strip(f.View()), "+ 1 more")
	f.Focus()

	// Tab moves on with the selection too.
	*more = nil
	assert.IsType(t, huh.NextField(), f.handleKey(keyTab)())
	assert.Equal(t, []string{"episode.mp4"}, *more)

	// Back to one video: it alone is picked.
	mark(t, f, "episode.mp4")
	assert.IsType(t, huh.NextField(), f.handleKey(keyEnter)())
	assert.Equal(t, "source.mp4", *value)
	assert.Empty(t, *more)

	// Without a selection, enter picks the highlighted video alone.
	mark(t, f, "source.mp4")
	*more = []string{"episode.mp4"}
	f.moveTo("episode.mp4")
	f.handleKey(keyEnter)
	assert.Equal(t, "episode.mp4", *value)
	assert.Nil(t, *more)
}

func TestVideoPickerSeveralKeys(
	t *testing.T,
) {
	help := func(f *videoPicker) map[string]string {
		hints := map[string]string{}

		for _, b := range f.KeyBinds() {
			if b.Enabled() {
				hints[b.Help().Key] = b.Help().Desc
			}
		}

		return hints
	}

	f, _, _ := severalPicker(t)
	assert.Equal(t, "select", help(f)["space"])
	assert.Equal(t, "open/pick", help(f)["enter"])
	assert.NotContains(t, help(f), "tab")

	mark(t, f, "source.mp4")
	assert.Equal(t, "add/remove", help(f)["space"])
	assert.Equal(t, "continue", help(f)["enter"])
	assert.Contains(t, help(f), "tab", "the selection can be kept")

	// A picker of one video types its spaces in the filter.
	single, _ := focusedPicker(t)
	assert.NotContains(t, help(single), "space")

	single.handleKey(runes("a"))
	single.handleKey(runes(" "))
	assert.Equal(t, "a ", single.filter.Value())
	assert.Empty(t, single.marked)
}

func TestVideoPickerRestoresSelection(
	t *testing.T,
) {
	dir := programDir(t)
	ctx := testWizardContext(t, false)

	value, more := "source.mp4", []string{"episode.mp4"}
	f := newVideoPicker("Video", "", &value, ctx).Several(&more)
	f.Focus()

	assert.Equal(t, []string{filepath.Join(dir, "source.mp4"), filepath.Join(dir, "episode.mp4")}, f.marked)
	assert.True(t, f.isMarked(filepath.Join(dir, "episode.mp4")))

	// One video picked before is highlighted, not selected: enter on
	// another one must pick that one.
	more = nil
	f = newVideoPicker("Video", "", &value, ctx).Several(&more)
	f.Focus()
	assert.Empty(t, f.marked)
}

func TestVideoPickerSeveralAccessible(
	t *testing.T,
) {
	f, value, more := severalPicker(t)

	var out strings.Builder

	input := strings.Join([]string{"source.mp4", "missing.mp4", "ref.mov", "episode.mp4", "", ""}, "\n")
	require.NoError(t, f.RunAccessible(&out, strings.NewReader(input)))
	assert.Equal(t, "source.mp4", *value)
	assert.Equal(t, []string{"episode.mp4"}, *more)

	for _, want := range []string{"Another video for the same ladder", "no such file", "not 1920×1080 like source.mp4"} {
		assert.Contains(t, out.String(), want)
	}

	// Asked again, the videos typed replace those picked before; the end
	// of the input stops the prompt.
	require.NoError(t, f.RunAccessible(io.Discard, strings.NewReader("\n\n")))
	assert.Equal(t, "source.mp4", *value)
	assert.Nil(t, *more)
	require.ErrorIs(t, f.RunAccessible(io.Discard, strings.NewReader("source.mp4\n")), io.ErrUnexpectedEOF)
}

func TestWizardProgramCommand(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		answers wizardAnswers
		want    []string
	}{
		{
			name: "defaults",
			answers: wizardAnswers{
				Source: "ep1.mov", Program: []string{"ep2.mov", "-ep3.mov"}, Codecs: []string{"h264"},
				// What was answered for one video is not asked of several.
				Actions: []string{actionAnalysis, actionVMAF}, Reference: "ref.mov",
				Digest: defaultDigest,
			},
			want: []string{"qc", "ladder", "ep1.mov", "ep2.mov", "./-ep3.mov", "-c", "h264"},
		},
		{
			name: "everything",
			answers: wizardAnswers{
				Source: "ep1.mov", Program: []string{"ep2.mov"}, Codecs: []string{"h264", av1Codec},
				Metrics: []string{}, Devices: []string{"phone"},
				Digest: "top", Advanced: true, Shape: shapeCount, RungCount: "5", PerShot: true, FilmGrain: "auto",
				HDRMetric: "tonemap", GPU: true, HTML: " report.html ",
				Renditions: true, RenditionsDir: "out", Overlay: true, OverlayPath: "annotated.mp4",
			},
			want: []string{
				"qc", "ladder", "ep1.mov", "ep2.mov", "-c", "h264,av1", "--metrics=", "--devices=phone", "--digest", "top",
				"--rungs", "5", "--film-grain", "auto", "--hdr-metric", "tonemap", "--gpu", "--html", "report.html",
				"--encode-ladder", "out",
			},
		},
		{
			name:    "one video runs everything",
			answers: wizardAnswers{Source: "ep1.mov", Actions: []string{actionLadder}, Codecs: []string{"h264"}},
			want:    []string{"qc", "run", "ep1.mov", "--codecs=h264", "--skip-analysis"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.answers.command())

			if !testCase.answers.isProgram() {
				return
			}

			// The flags are those of qc ladder.
			cmd := newLadderCommand(testEnv)
			require.NoError(t, cmd.ParseFlags(testCase.want[2:]))
			assert.Equal(t, append([]string{testCase.answers.Source}, testCase.answers.Program...), unflag(cmd.Flags().Args()))
		})
	}
}

// unflag strips the ./ that keeps a path from reading as a flag.
func unflag(
	paths []string,
) []string {
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = strings.TrimPrefix(path, "./")
	}

	return out
}

func TestWizardProgramSteps(
	t *testing.T,
) {
	one := wizardAnswers{Source: "ep1.mov", Actions: []string{actionAnalysis, actionVMAF, actionLadder}, Codecs: []string{av1Codec}, Advanced: true, PerShot: true}
	several := one
	several.Program = []string{"ep2.mov"}

	assert.False(t, one.isProgram())
	assert.True(t, one.wants(actionVMAF))
	assert.True(t, one.perShot())
	assert.False(t, one.codecsHidden())
	assert.True(t, one.programHidden())
	assert.False(t, one.encodingHidden(false)())
	assert.True(t, one.encodingHidden(true)())

	assert.True(t, several.isProgram())
	assert.False(t, several.wants(actionAnalysis), "several videos get a ladder and nothing else")
	assert.False(t, several.wants(actionVMAF))
	assert.True(t, several.wants(actionLadder))
	assert.False(t, several.perShot())
	assert.True(t, several.codecsHidden())
	assert.False(t, several.programHidden())
	assert.True(t, several.encodingHidden(false)())
	assert.False(t, several.encodingHidden(true)())
	assert.True(t, several.vmafHidden())
	assert.True(t, several.overlayHidden())
	assert.False(t, several.renditionsHidden())
	assert.False(t, several.filmGrainHidden(), "an AV1 ladder without per-shot rungs")

	// Per-shot rungs are asked of one title only.
	assert.Len(t, several.encodingFields(true), len(several.encodingFields(false))+1)

	// Without a ladder asked for one video, nothing of it is asked.
	one.Actions = []string{actionAnalysis}
	assert.True(t, one.codecsHidden())
	assert.True(t, one.encodingHidden(false)())
}

func TestWizardProgramReview(
	t *testing.T,
) {
	programDir(t)

	ctx := testWizardContext(t, false)
	ctx.videos.inspect("source.mp4")

	a := newWizardAnswers()
	a.Source, a.Program, a.Codecs = "source.mp4", []string{"episode.mp4"}, []string{"hevc", av1Codec}
	a.Advanced, a.PerShot = true, true

	values := reviewValues(a.reviewSections(ctx))
	assert.Equal(t, []string{"Video 1: source.mp4", "Video 2: episode.mp4"}, values[sectionSource])
	assert.Equal(t, []string{"Compute: One ladder per codec for the 2 videos"}, values[sectionAnalysis])
	assert.Contains(t, values[sectionLadder], "Codecs: HEVC, AV1")
	assert.Contains(t, values[sectionLadder], "Digest: shared equally, balanced on each video")
	assert.Contains(t, values[sectionLadder], "Rungs: verified, fixed probes", "no per-shot rungs for several videos")
	assert.NotContains(t, strings.Join(values[sectionOutputs], "\n"), "Annotated")

	assert.Equal(t, "H.264 · 1920×1080 · 25 fps · 0:20 · SDR · 1 audio track", a.sourceRows(ctx)[0].detail)
	assert.Contains(t, plainReview(a, ctx), "Command: qc ladder source.mp4 episode.mp4 -c hevc,av1")
}

func TestWizardModelProgram(
	t *testing.T,
) {
	programDir(t)

	m := newWizardModel(newWizardAnswers(), testWizardContext(t, false))
	send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	send(m, runCmd(m.Init())...)

	// Two videos selected: the wizard goes straight to the ladder.
	send(m, runes("source"), runes(" "), keyEsc, runes("episode"), runes(" "), keyEnter)
	require.Equal(t, []string{"episode.mp4"}, m.answers.Program, view(m))
	require.Equal(t, sectionQuality, m.current(), view(m))
	assert.Contains(t, view(m), "Metrics next to VMAF")

	// Metrics, devices, codec, digest, customisation, HTML, renditions.
	for range 7 {
		send(m, keyEnter)
	}

	require.Equal(t, phaseReview, m.phase, view(m))

	screen := view(m)
	for _, want := range []string{"Video 2", "One ladder per codec for the 2 videos", "qc ladder source.mp4 episode.mp4 -c h264"} {
		assert.Contains(t, screen, want)
	}

	assert.NotContains(t, m.editable(), sectionAnalysis, "nothing to choose for several videos")
}

func TestWizardProgramLadderPageFits(
	t *testing.T,
) {
	programDir(t)

	// The smallest terminal the wizard is made for shows the whole page:
	// the three questions, down to the buttons of the last one.
	m := newWizardModel(newWizardAnswers(), testWizardContext(t, false))
	send(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	send(m, runCmd(m.Init())...)
	send(m, runes("source"), runes(" "), keyEsc, runes("episode"), runes(" "), keyEnter, keyEnter, keyEnter)
	require.Equal(t, sectionLadder, m.current(), view(m))

	for range 3 {
		screen := view(m)
		for _, want := range []string{"Ladder codecs", "AV1", "Digest the ladder is estimated on", "Uniform", "Customise the ladder?", "No, automatic"} {
			assert.Contains(t, screen, want)
		}

		send(m, keyEnter)
	}
}

func TestFitHints(
	t *testing.T,
) {
	hint := func(k, desc string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc)) }

	off := hint("x", "disabled")
	off.SetEnabled(false)

	// "a one" is 5 cells, "bb two" 6 after a gap of 2.
	hints := []key.Binding{hint("a", "one"), off, hint("bb", "two"), hint("c", "three")}

	assert.Len(t, fitHints(hints, 100), 4)
	assert.Len(t, fitHints(hints, 13), 3, "up to the first that does not fit")
	assert.Len(t, fitHints(hints, 12), 2, "the first and the disabled one, which takes no room")
	assert.Empty(t, fitHints(hints, 4))
}

func TestSubject(
	t *testing.T,
) {
	assert.Equal(t, "episode-01.mov", subject(pipeline.Options{Source: "/titles/episode-01.mov"}))
	assert.Equal(t, "episode-01.mov + 2 more", subject(pipeline.Options{Source: "/titles/episode-01.mov", Program: []string{"b.mov", "c.mov"}}))
}
