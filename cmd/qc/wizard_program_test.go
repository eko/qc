package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/sample"
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

	assert.Contains(t, ansi.Strip(f.View()), "space selects several videos: one ladder, or one sample, for all of them")

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
	assert.Contains(t, screen, "2 videos selected · one ladder or sample for all of them · enter continues")

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

	for _, want := range []string{"Another video, for one ladder or sample of them all", "no such file", "not 1920×1080 like source.mp4"} {
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

	// Two videos selected: a ladder for both, or a sample of them.
	send(m, runes("source"), runes(" "), keyEsc, runes("episode"), runes(" "), keyEnter)
	require.Equal(t, []string{"episode.mp4"}, m.answers.Program, view(m))
	require.Equal(t, sectionAnalysis, m.current(), view(m))
	assert.Contains(t, view(m), "What should I do with these videos?")

	send(m, keyEnter)
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

	assert.Contains(t, m.editable(), sectionAnalysis)
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
	send(m, runes("source"), runes(" "), keyEsc, runes("episode"), runes(" "), keyEnter, keyEnter, keyEnter, keyEnter)
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

func TestSampleOptions(
	t *testing.T,
) {
	opts := sampleOptions(SampleConfig{Duration: 90, Scenes: " Top ", TopShare: 0.3, Piece: 4})
	assert.Equal(t, sample.Options{Duration: media.Seconds(90), Scenes: sample.ScenesTop, TopShare: 0.3, Piece: media.Seconds(4)}, opts)
}

func TestWizardSampleCommand(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		answers wizardAnswers
		want    []string
	}{
		{
			name: "defaults, one video",
			answers: wizardAnswers{
				Source: "film.mp4", Actions: []string{actionSample}, Codecs: []string{"h264"},
				SampleDuration: "60", SampleScenes: "mixed", SampleTopShare: "50", SampleTo: " sample.mkv ",
				// What a run would write is not asked of a sample.
				HTML: "report.html", Renditions: true, RenditionsDir: "out",
			},
			want: []string{"qc", "sample", "film.mp4", "--to", "sample.mkv"},
		},
		{
			name: "several videos, the most complex scenes",
			answers: wizardAnswers{
				Source: "ep1.mov", Program: []string{"-ep2.mov"}, ProgramAction: actionSample,
				SampleDuration: " 90 ", SampleScenes: "top", SampleTopShare: "30", SampleTo: "-hard.mp4", GPU: true,
			},
			want: []string{"qc", "sample", "ep1.mov", "./-ep2.mov", "--to", "./-hard.mp4", "--duration", "90", "--scenes", "top", "--gpu"},
		},
		{
			name: "a mixed sample with a third of complex scenes",
			answers: wizardAnswers{
				Source: "ep1.mov", Program: []string{"ep2.mov"}, ProgramAction: actionSample,
				SampleDuration: "60", SampleScenes: "mixed", SampleTopShare: "30 %", SampleTo: "mix.mkv",
			},
			want: []string{"qc", "sample", "ep1.mov", "ep2.mov", "--to", "mix.mkv", "--top-share", "0.3"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.True(t, testCase.answers.isSample())
			assert.Equal(t, testCase.want, testCase.answers.command())

			// The flags are those of qc sample.
			cmd := newSampleCommand(testEnv)
			require.NoError(t, cmd.ParseFlags(testCase.want[2:]))
			assert.Len(t, cmd.Flags().Args(), 1+len(testCase.answers.Program))
		})
	}
}

func TestWizardSampleSteps(
	t *testing.T,
) {
	one := wizardAnswers{Source: "film.mp4", Actions: []string{actionSample}, SampleScenes: "top"}
	several := wizardAnswers{Source: "ep1.mov", Program: []string{"ep2.mov"}, ProgramAction: actionSample, SampleScenes: "mixed"}
	ladder := wizardAnswers{Source: "ep1.mov", Program: []string{"ep2.mov"}}

	for _, a := range []*wizardAnswers{&one, &several} {
		assert.True(t, a.isSample())
		assert.False(t, a.sampleHidden())
		assert.True(t, a.ladderHidden(), "a sample is extracted alone")
		assert.True(t, a.metricsHidden())
		assert.True(t, a.codecsHidden())
		assert.True(t, a.programLadderHidden())
		assert.True(t, a.overlayHidden())
		assert.True(t, a.renditionsHidden())
	}

	assert.True(t, one.sampleShareHidden(), "a share of a mixed sample only")
	assert.False(t, several.sampleShareHidden())
	assert.True(t, one.programHidden())
	assert.False(t, several.programHidden())

	// Several videos get a ladder unless told otherwise.
	assert.False(t, ladder.isSample())
	assert.True(t, ladder.wants(actionLadder))
	assert.True(t, ladder.sampleHidden())
	assert.True(t, ladder.sampleShareHidden())
	assert.False(t, ladder.programLadderHidden())
	assert.True(t, ladder.sampleMixed(), "the default scenes")

	// A sample is extracted alone.
	require.NoError(t, validateActions([]string{actionSample}))
	require.NoError(t, validateActions([]string{actionAnalysis, actionLadder}))
	require.ErrorContains(t, validateActions(nil), "pick at least one")
	require.ErrorContains(t, validateActions([]string{actionAnalysis, actionSample}), "a sample is extracted alone")

	for input, valid := range map[string]bool{"50": true, "30 %": true, "0": false, "100": false, "half": false, "": false} {
		assert.Equal(t, valid, validateTopShare(input) == nil, input)
	}
}

func TestWizardSampleReview(
	t *testing.T,
) {
	programDir(t)

	ctx := testWizardContext(t, false)

	a := newWizardAnswers()
	a.Source, a.Program, a.ProgramAction = "source.mp4", []string{"episode.mp4"}, actionSample
	a.SampleDuration, a.SampleTopShare, a.SampleTo, a.HTML = "90", "30", "mix.mkv", "report.html"

	values := reviewValues(a.reviewSections(ctx))
	assert.Equal(t, []string{
		"Compute: A sample of the 2 videos",
		"Sample: 90 s, most complex and representative scenes (30% complex)",
		"File: mix.mkv, video copied without re-encoding",
	}, values[sectionAnalysis])
	assert.Equal(t, []string{"skipped: no ladder requested"}, values[sectionLadder])
	assert.Equal(t, "Report: terminal", values[sectionOutputs][0], "a sample has no HTML report")
	assert.Contains(t, plainReview(a, ctx), "Command: qc sample source.mp4 episode.mp4 --to mix.mkv --duration 90 --top-share 0.3")

	// One video, other scenes: no share to tell.
	a.Program, a.Actions, a.SampleScenes = nil, []string{actionSample}, "easy"
	values = reviewValues(a.reviewSections(ctx))
	assert.Equal(t, "Compute: A sample of the video", values[sectionAnalysis][0])
	assert.Equal(t, "Sample: 90 s, easiest scenes", values[sectionAnalysis][1])
}

func TestWizardModelSample(
	t *testing.T,
) {
	programDir(t)

	m := newWizardModel(newWizardAnswers(), testWizardContext(t, false))
	send(m, tea.WindowSizeMsg{Width: 120, Height: 40})
	send(m, runCmd(m.Init())...)

	// Two videos, then a sample of them rather than a ladder.
	send(m, runes("source"), runes(" "), keyEsc, runes("episode"), runes(" "), keyEnter)
	send(m, keyDown, keyEnter)
	require.Equal(t, actionSample, m.answers.ProgramAction, view(m))
	require.Equal(t, sectionAnalysis, m.current(), view(m))

	screen := view(m)
	for _, want := range []string{"Length of the sample (seconds)", "Scenes", "Sample file"} {
		assert.Contains(t, screen, want)
	}

	// Length, scenes, file, then the share of complex scenes: the review.
	for range 4 {
		send(m, keyEnter)
	}

	require.Equal(t, phaseReview, m.phase, view(m))

	screen = view(m)
	for _, want := range []string{"A sample of the 2 videos", "60 s, most complex and representative scenes (50% complex)", "qc sample source.mp4 episode.mp4 --to sample.mkv"} {
		assert.Contains(t, screen, want)
	}
}

// stageLog records the dashboard updates of an extraction.
type stageLog struct {
	events []string
}

func (l *stageLog) Start(
	i int,
) {
	l.events = append(l.events, "start "+strconv.Itoa(i))
}

func (l *stageLog) Done(
	i int,
	_ string,
) {
	l.events = append(l.events, "done "+strconv.Itoa(i))
}

func (l *stageLog) Progress(
	i, done, total int,
	_ string,
) {
	l.events = append(l.events, fmt.Sprintf("progress %d %d/%d", i, done, total))
}

func TestSampleProgress(
	t *testing.T,
) {
	log := &stageLog{}

	progress := sampleProgress(log)
	progress(sample.Progress{Stage: sample.StageInspect, Done: 0, Total: 2})
	progress(sample.Progress{Stage: sample.StageInspect, Done: 1, Total: 2})
	// The analysis is skipped when every video is taken whole: the stages
	// before the one reported are closed in order.
	progress(sample.Progress{Stage: sample.StageExtract, Done: 1, Total: 3})
	progress(sample.Progress{Stage: sample.StageVerify})

	assert.Equal(t, []string{
		"start 0", "progress 0 0/2", "progress 0 1/2",
		"done 0", "start 1", "done 1", "start 2", "progress 2 1/3",
		"done 2", "start 3",
	}, log.events)
}
