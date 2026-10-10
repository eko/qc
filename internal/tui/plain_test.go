package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunPlain(
	t *testing.T,
) {
	var out bytes.Buffer

	err := RunPlain(t.Context(), &out, "ladder", "source.mp4", []string{"Inspect", "Ladder · h264"},
		func(_ context.Context, e *Emitter) error {
			e.Start(0)
			e.Done(0, "h264 · 1920×1080")
			e.Start(1)
			e.Unit(1, "encodes")
			e.Progress(1, 1, 15, "probe encodes")
			e.Done(1, "\x1b[32m6 rungs\x1b[0m")

			return nil
		})
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 6)

	assert.Equal(t, "qc ladder · source.mp4", lines[0])
	assert.Equal(t, "[1/2] Inspect: started", lines[1])
	assert.Regexp(t, `^\[1/2\] Inspect: done in \d+\.\ds · h264 · 1920×1080$`, lines[2])
	assert.Equal(t, "[2/2] Ladder · h264: started", lines[3])
	assert.Regexp(t, `^\[2/2\] Ladder · h264: done in \d+\.\ds · 6 rungs$`, lines[4], "colours are left out, and progress under ten seconds old too")
	assert.Regexp(t, `^qc ladder: done in \d+\.\ds$`, lines[5])
	assert.NotContains(t, out.String(), "\x1b")
}

func TestRunPlainReportsAFailure(
	t *testing.T,
) {
	var out bytes.Buffer

	failure := errors.New("no rung")

	err := RunPlain(t.Context(), &out, "ladder", "source.mp4", []string{"Ladder"},
		func(_ context.Context, e *Emitter) error {
			e.Start(0)
			e.Fail(0, "failed")

			return failure
		})
	require.ErrorIs(t, err, failure)

	assert.Regexp(t, `\[1/1\] Ladder: failed after \d+\.\ds · failed\n`, out.String())
	assert.Regexp(t, `qc ladder: failed after \d+\.\ds: no rung\n$`, out.String())
}

func TestPlainPrinterProgress(
	t *testing.T,
) {
	var out bytes.Buffer

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	p := &plainPrinter{out: &out, labels: []string{"Analysis", "Ladder"}, now: func() time.Time { return now }, stages: make([]plainStage, 2)}

	send := func(after time.Duration, msg tea.Msg) string {
		now = now.Add(after)

		out.Reset()
		p.handle(msg)

		return strings.TrimSpace(out.String())
	}

	assert.Equal(t, "[1/2] Analysis: started", send(0, startMsg{i: 0}))
	assert.Empty(t, send(4*time.Second, progressMsg{i: 0, done: 100, total: 1000}), "too soon after the last line")
	assert.Equal(t, "[1/2] Analysis: 300/1000 frames (30%) after 12.0s", send(8*time.Second, progressMsg{i: 0, done: 300, total: 1000}))
	assert.Empty(t, send(5*time.Second, progressMsg{i: 0, done: 500, total: 1000}))
	assert.Empty(t, send(10*time.Second, progressMsg{i: 0, done: 1000, total: 1000}), "the end is said by done")
	assert.Equal(t, "[1/2] Analysis: done in 1m30s · 12 shots", send(63*time.Second, doneMsg{i: 0, summary: "12 shots"}))

	// A stage counted in its own unit, then one that only says what it does.
	send(0, startMsg{i: 1})
	send(0, unitMsg{i: 1, unit: "encodes"})
	assert.Equal(t, "[2/2] Ladder: 2/15 encodes (13%) after 11.0s · probe encodes", send(11*time.Second, progressMsg{i: 1, done: 2, total: 15, detail: "probe encodes"}))
	assert.Equal(t, "[2/2] Ladder: digest after 22.0s", send(11*time.Second, progressMsg{i: 1, detail: "digest"}))
	assert.Empty(t, send(11*time.Second, progressMsg{i: 1, detail: "digest"}), "the same thing is not said twice")
	assert.Equal(t, "[2/2] Ladder: failed after 40.0s", send(7*time.Second, failMsg{i: 1}))

	// Events of stages that do not exist are ignored.
	assert.Empty(t, send(0, startMsg{i: 5}))
	assert.Empty(t, send(time.Minute, progressMsg{i: -1, done: 1, total: 2}))
	assert.Empty(t, send(0, doneMsg{i: 2}))
	assert.Empty(t, send(0, panelMsg{i: 0}))
}
