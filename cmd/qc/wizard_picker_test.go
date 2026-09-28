package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// names are the names of the listed entries.
func names(
	f *videoPicker,
) []string {
	var list []string
	for _, e := range f.visible {
		list = append(list, e.name)
	}

	return list
}

// focusedPicker is a picker of the test folder, focused.
func focusedPicker(
	t *testing.T,
) (*videoPicker, *string) {
	t.Helper()

	wizardDir(t)

	value := new(string)
	f := newVideoPicker("Video to analyse", "The source.", value, testWizardContext(t, false))
	f.Focus()

	return f, value
}

// pickerCmd runs the command of a picker and reports the message it ends
// with.
func pickerCmd(
	cmd tea.Cmd,
) tea.Msg {
	msgs := runCmd(cmd)
	if len(msgs) == 0 {
		return nil
	}

	return msgs[len(msgs)-1]
}

func TestListVideos(
	t *testing.T,
) {
	dir := wizardDir(t)

	entries, err := listVideos(dir)
	require.NoError(t, err)

	var got []string
	for _, e := range entries {
		got = append(got, e.name)
	}

	assert.Equal(t, []string{"clips", "audio.m4v", "broken.mp4", "ref.mov", "source.mp4"}, got, "folders first, videos only, hidden files left out")

	_, err = listVideos(filepath.Join(dir, "missing"))
	require.Error(t, err)
}

func TestVideoPickerBrowse(
	t *testing.T,
) {
	f, _ := focusedPicker(t)

	assert.Equal(t, []string{parentEntry, "clips", "audio.m4v", "broken.mp4", "ref.mov", "source.mp4"}, names(f))
	assert.Equal(t, 1, f.cursor, "the cursor skips the parent folder")

	f.handleKey(keyDown)
	assert.Equal(t, "audio.m4v", f.visible[f.cursor].name)

	for _, k := range []tea.KeyMsg{{Type: tea.KeyEnd}, keyDown} {
		f.handleKey(k)
	}

	assert.Equal(t, "source.mp4", f.visible[f.cursor].name, "the cursor stays in the list")

	for _, k := range []tea.KeyMsg{{Type: tea.KeyHome}, keyUp, {Type: tea.KeyPgDown}, {Type: tea.KeyPgUp}} {
		f.handleKey(k)
	}

	assert.Equal(t, 0, f.cursor)

	// Into a folder, and back up with ←, backspace or the parent entry.
	f.handleKey(keyDown)
	f.handleKey(keyRight)
	assert.Equal(t, "clips", filepath.Base(f.dir))
	assert.Equal(t, []string{parentEntry, "nested.mp4"}, names(f))

	f.handleKey(keyLeft)
	assert.Equal(t, "clips", f.visible[f.cursor].name, "the cursor is on the folder left")

	f.handleKey(keyEnter)
	f.handleKey(keyBack)
	assert.Equal(t, "clips", f.visible[f.cursor].name)

	f.handleKey(keyEnter)
	f.cursor = 0
	f.handleKey(keyEnter)
	assert.Equal(t, "clips", f.visible[f.cursor].name)
}

func TestVideoPickerFilter(
	t *testing.T,
) {
	f, _ := focusedPicker(t)

	f.handleKey(runes("SOU"))
	assert.Equal(t, []string{"source.mp4"}, names(f), "case-insensitive, without the parent folder")
	assert.True(t, usesEsc(f.KeyBinds()), "esc clears the filter")

	f.handleKey(keyBack)
	f.handleKey(keyBack)
	f.handleKey(keyBack)
	assert.Contains(t, names(f), parentEntry)

	f.handleKey(runes("zzz"))
	assert.Empty(t, f.visible)
	assert.Contains(t, ansi.Strip(f.View()), "Nothing matches “zzz”")
	assert.Nil(t, f.handleKey(keyEnter), "nothing to open")

	f.handleKey(keyEsc)
	assert.Empty(t, f.filter.Value())
	assert.False(t, usesEsc(f.KeyBinds()))
}

func TestVideoPickerPick(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		file      string
		wantValue string
		wantErr   string
	}{
		{name: "video", file: "source.mp4", wantValue: "source.mp4"},
		{name: "unreadable", file: "broken.mp4", wantErr: "cannot read this file"},
		{name: "no video stream", file: "audio.m4v", wantErr: "no video stream"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f, value := focusedPicker(t)
			f.moveTo(testCase.file)

			// Not probed yet: the pick waits for the probe.
			msg := pickerCmd(f.handleKey(keyEnter))
			assert.Equal(t, filepath.Join(f.dir, testCase.file), f.pending)
			assert.Contains(t, ansi.Strip(f.View()), "checking "+testCase.file)
			require.IsType(t, probedMsg{}, msg)

			_, cmd := f.Update(msg)

			if testCase.wantErr != "" {
				require.ErrorContains(t, f.Error(), testCase.wantErr)
				assert.Empty(t, *value)
				assert.Nil(t, cmd)

				// Probed already: the pick is decided at once.
				f.handleKey(keyEnter)
				require.ErrorContains(t, f.Error(), testCase.wantErr)

				return
			}

			assert.Equal(t, testCase.wantValue, *value)
			assert.IsType(t, huh.NextField(), cmd())
		})
	}
}

func TestVideoPickerPreview(
	t *testing.T,
) {
	f, _ := focusedPicker(t)
	f.moveTo("ref.mov")

	due, ok := f.highlighted()().(previewDueMsg)
	require.True(t, ok, "the preview waits for the highlight to settle")

	_, stale := f.Update(previewDueMsg{picker: f, seq: due.seq - 1})
	assert.Nil(t, stale, "a later highlight replaces the preview")

	_, probe := f.Update(due)
	assert.Equal(t, probedMsg{path: filepath.Join(f.dir, "ref.mov")}, pickerCmd(probe))

	_, cmd := f.Update(probedMsg{path: "other.mp4"})
	assert.Nil(t, cmd, "only a pending pick moves on")
	assert.Nil(t, f.highlighted(), "known files are not probed again")

	f.width = 100
	view := ansi.Strip(f.View())
	assert.Contains(t, view, "Picture    3840×2160", "wide pages have a preview card")
	assert.Contains(t, view, "HDR10")
	assert.Contains(t, view, "2160p · 1:05", "probed files show their height and duration")

	f.width = 70
	view = ansi.Strip(f.View())
	assert.Contains(t, view, "HEVC · 3840×2160 · 23.976 fps · 10-bit · 1:05 · HDR10 · 2 audio tracks")

	f.moveTo("clips")
	assert.Contains(t, ansi.Strip(f.View()), "folder · enter opens it")
	assert.Nil(t, f.highlighted())

	f.moveTo("broken.mp4")
	f.cache.inspect(f.currentPath())
	assert.Contains(t, ansi.Strip(f.View()), "cannot read this file")

	f.moveTo("audio.m4v")
	assert.Contains(t, ansi.Strip(f.View()), "reading")
}

func TestVideoPickerNavigation(
	t *testing.T,
) {
	f, value := focusedPicker(t)

	assert.False(t, f.KeyBinds()[4].Enabled(), "tab moves on only once a file is picked")
	assert.Nil(t, f.handleKey(keyTab))
	require.EqualError(t, f.Error(), "pick a video file")

	*value = "source.mp4"
	assert.IsType(t, huh.NextField(), f.handleKey(keyTab)())
	assert.IsType(t, huh.PrevField(), f.handleKey(keyShiftTab)())

	_, cmd := f.Update(keyDown)
	assert.NotNil(t, cmd, "focused pickers take keys")

	f.Blur()
	_, cmd = f.Update(keyDown)
	assert.Nil(t, cmd, "blurred pickers ignore keys")
	assert.False(t, f.Zoom())

	view := ansi.Strip(f.View())
	assert.Contains(t, view, "Video to analyse")
	assert.Contains(t, view, "✓ source.mp4")
}

func TestVideoPickerOpen(
	t *testing.T,
) {
	dir := wizardDir(t)
	ctx := testWizardContext(t, false)

	// The reference is browsed from the folder of the source.
	source := "clips/nested.mp4"
	reference := ""
	f := newVideoPicker("Reference video", "", &reference, ctx).StartIn(func() string { return source })
	f.Focus()
	assert.Equal(t, filepath.Join(dir, "clips"), f.dir)

	// A file picked before is highlighted in its folder.
	picked := "ref.mov"
	f = newVideoPicker("Video", "", &picked, ctx)
	f.Focus()
	assert.Equal(t, "ref.mov", f.visible[f.cursor].name)

	// Outside the working directory, paths stay absolute.
	assert.Equal(t, "/elsewhere/clip.mp4", f.relative("/elsewhere/clip.mp4"))
	assert.Equal(t, "clips/nested.mp4", f.relative(filepath.Join(dir, "clips", "nested.mp4")))

	// A folder that cannot be listed keeps the listing.
	f.chdir(filepath.Join(dir, "missing"))
	assert.Equal(t, dir, f.dir)
	assert.Contains(t, ansi.Strip(f.View()), "cannot open this folder")

	// An empty folder says how to leave it.
	empty := t.TempDir()
	f.chdir(empty)
	assert.Contains(t, ansi.Strip(f.View()), "No video here")
	assert.Nil(t, f.activate(false))
}

func TestVideoPickerField(
	t *testing.T,
) {
	f, value := focusedPicker(t)

	assert.Same(t, f, f.WithTheme(nil))
	assert.Same(t, f, f.WithAccessible(true))
	assert.Same(t, f, f.WithKeyMap(nil))
	assert.Same(t, f, f.WithHeight(3))
	assert.Same(t, f, f.WithWidth(90))
	assert.Same(t, f, f.WithPosition(huh.FieldPosition{}))
	assert.False(t, f.Skip())
	assert.True(t, f.Zoom())
	assert.Nil(t, f.Init())
	assert.Empty(t, f.GetKey())

	*value = "ref.mov"
	assert.Equal(t, "ref.mov", f.GetValue())

	// Without a page, a picker keeps a sensible height.
	f.layout = nil
	assert.Equal(t, defaultPickerHeight-f.chromeHeight(), f.rows())
}

func TestVideoPickerAccessible(
	t *testing.T,
) {
	f, value := focusedPicker(t)

	var out strings.Builder

	input := strings.Join([]string{"", "missing.mp4", "clips", "notes.txt", "audio.m4v", "source.mp4"}, "\n")
	require.NoError(t, f.RunAccessible(&out, strings.NewReader(input)))
	assert.Equal(t, "source.mp4", *value)

	for _, want := range []string{"pick a video file", "no such file", "not a video file", "no video stream"} {
		assert.Contains(t, out.String(), want)
	}

	// An empty answer keeps the file picked before; the end of the input
	// stops the prompt.
	require.NoError(t, f.RunAccessible(io.Discard, strings.NewReader("\n")))
	assert.Equal(t, "source.mp4", *value)
	require.ErrorIs(t, f.RunAccessible(io.Discard, strings.NewReader("")), io.ErrUnexpectedEOF)
}

func TestTruncateLeft(
	t *testing.T,
) {
	assert.Equal(t, "…/clips", truncateLeft("~/videos/clips", 7, "…"))
	assert.Equal(t, "~/clips", truncateLeft("~/clips", 7, "…"))
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("~", "a", "b"), homeRelative(filepath.Join(home, "a", "b")))
	assert.Equal(t, "/tmp", homeRelative("/tmp"))
}
