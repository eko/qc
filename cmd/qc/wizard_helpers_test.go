package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

// fakeVideos are the files the fake prober knows, by name.
var fakeVideos = map[string]*media.Info{
	"source.mp4": {
		Duration: media.Duration(20 * time.Second),
		Video: []media.VideoStream{{
			Codec: "h264", Width: 1920, Height: 1080, BitDepth: 8,
			AvgFrameRate: media.Rational{Num: 25, Den: 1},
		}},
		Audio: []media.AudioStream{{Codec: "aac"}},
	},
	"ref.mov": {
		Duration: media.Duration(65 * time.Second),
		Video: []media.VideoStream{{
			Codec: "hevc", Width: 3840, Height: 2160, BitDepth: 10,
			AvgFrameRate: media.Rational{Num: 24000, Den: 1001},
			Color:        media.Color{Transfer: media.TransferPQ},
			HDR:          media.HDR{DynamicRange: media.DynamicRangeHDR10},
		}},
		Audio: []media.AudioStream{{Codec: "aac"}, {Codec: "ac3"}},
	},
	"audio.m4v": {Duration: media.Duration(3 * time.Second), Audio: []media.AudioStream{{Codec: "aac"}}},
}

// errUnreadable is the error of the fake prober for unknown files.
var errUnreadable = errors.New("invalid data found when processing input")

// fakeProbe probes from fakeVideos, counting its calls.
func fakeProbe(
	calls *atomic.Int32,
) probeFunc {
	return func(_ context.Context, path string) (*media.Info, error) {
		if calls != nil {
			calls.Add(1)
		}

		if info, ok := fakeVideos[filepath.Base(path)]; ok {
			return info, nil
		}

		return nil, errUnreadable
	}
}

// wizardDir creates a folder of videos for the pickers and moves into it:
// source.mp4, ref.mov, audio.m4v (no video stream), broken.mp4 (unreadable),
// a text file and a hidden video (never listed), and clips/nested.mp4.
func wizardDir(
	t *testing.T,
) string {
	t.Helper()

	dir := t.TempDir()
	for _, name := range []string{"source.mp4", "ref.mov", "audio.m4v", "broken.mp4", "notes.txt", ".hidden.mp4", "clips/nested.mp4"} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	}

	t.Chdir(dir)

	return dir
}

// testWizardContext is the context of a wizard in a test folder, with the
// fake prober and the Unicode style.
func testWizardContext(
	t *testing.T,
	offerGPU bool,
) wizardContext {
	t.Helper()

	videos := newVideoCacheWith(t.Context(), fakeProbe(nil))

	return wizardContext{
		offerGPU:  offerGPU,
		detectHDR: videos.dynamicRange,
		videos:    videos,
		layout:    &wizardLayout{width: 100, height: 20},
		style:     newWizardStyle(func(string) string { return "" }, termenv.TrueColor),
	}
}

// Keys sent to the models.
var (
	keyEnter    = tea.KeyMsg{Type: tea.KeyEnter}
	keyEsc      = tea.KeyMsg{Type: tea.KeyEsc}
	keyDown     = tea.KeyMsg{Type: tea.KeyDown}
	keyUp       = tea.KeyMsg{Type: tea.KeyUp}
	keyLeft     = tea.KeyMsg{Type: tea.KeyLeft}
	keyRight    = tea.KeyMsg{Type: tea.KeyRight}
	keyTab      = tea.KeyMsg{Type: tea.KeyTab}
	keyShiftTab = tea.KeyMsg{Type: tea.KeyShiftTab}
	keyBack     = tea.KeyMsg{Type: tea.KeyBackspace}
	keyCtrlC    = tea.KeyMsg{Type: tea.KeyCtrlC}
)

// runes types s.
func runes(
	s string,
) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// send delivers msgs to m one by one, each followed by the messages of the
// commands it returns, like a program would. Commands that do not return
// at once (ticks, cursor blinks) are dropped. It reports whether a command
// quit.
func send(
	m tea.Model,
	msgs ...tea.Msg,
) bool {
	quit := false

	for _, msg := range msgs {
		queue := []tea.Msg{msg}

		for steps := 0; len(queue) > 0 && steps < maxSteps; steps++ {
			next := queue[0]
			queue = queue[1:]

			if _, ok := next.(tea.QuitMsg); ok {
				quit = true

				continue
			}

			_, cmd := m.Update(next)
			queue = append(queue, runCmd(cmd)...)
		}
	}

	return quit
}

// maxSteps bounds the messages of a send.
const maxSteps = 2000

// runCmd runs cmd and returns its messages, batches and sequences
// flattened.
func runCmd(
	cmd tea.Cmd,
) []tea.Msg {
	if cmd == nil {
		return nil
	}

	done := make(chan tea.Msg, 1)

	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		return flatten(msg)
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

// flatten runs the commands of batches and sequences (tea's sequenceMsg is
// unexported: a slice of commands).
func flatten(
	msg tea.Msg,
) []tea.Msg {
	if msg == nil {
		return nil
	}

	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return []tea.Msg{msg}
	}

	var msgs []tea.Msg

	for i := range v.Len() {
		if cmd, ok := v.Index(i).Interface().(tea.Cmd); ok {
			msgs = append(msgs, runCmd(cmd)...)
		}
	}

	return msgs
}

// lineReader returns one line per read, like a terminal: every accessible
// prompt reads its own line.
type lineReader struct {
	lines []string
}

// Read implements io.Reader.
func (r *lineReader) Read(
	p []byte,
) (int, error) {
	if len(r.lines) == 0 {
		return 0, io.EOF
	}

	n := copy(p, r.lines[0]+"\n")
	r.lines = r.lines[1:]

	return n, nil
}
