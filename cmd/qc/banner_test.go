package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMotionAllowed(
	t *testing.T,
) {
	testCases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "plain terminal", env: map[string]string{"TERM": "xterm-256color"}, want: true},
		{name: "no colours", env: map[string]string{"NO_COLOR": "1"}, want: false},
		{name: "accessible mode", env: map[string]string{"ACCESSIBLE": "1"}, want: false},
		{name: "motion turned off", env: map[string]string{"QC_NO_ANIMATION": "1"}, want: false},
		{name: "dumb terminal", env: map[string]string{"TERM": "dumb"}, want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, motionAllowed(func(name string) string { return testCase.env[name] }))
		})
	}
}

func TestPrintBanner(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		animate    bool
		wantFrames int
	}{
		{name: "still", animate: false, wantFrames: 1},
		{name: "animated", animate: true, wantFrames: 15},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, printBanner(&out, testCase.animate))

			frames := strings.Split(out.String(), "\x1b[5A\r")
			assert.Len(t, frames, testCase.wantFrames)

			last := ansi.Strip(frames[len(frames)-1])
			assert.Contains(t, last, "qc · fast video quality analysis")
			assert.True(t, strings.HasSuffix(last, "\n\n"), "a blank line separates the banner from the wizard")
		})
	}
}

func TestPrintBannerWriteError(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		animate bool
		budget  int
	}{
		{name: "still", animate: false, budget: 0},
		{name: "animated leading line", animate: true, budget: 0},
		{name: "animated logo", animate: true, budget: 1},
		{name: "animated trailing lines", animate: true, budget: 30},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := printBanner(&budgetWriter{budget: testCase.budget}, testCase.animate)

			require.ErrorIs(t, err, os.ErrClosed)
		})
	}
}

// budgetWriter accepts budget writes, then fails like failingWriter.
type budgetWriter struct {
	budget int
}

func (w *budgetWriter) Write(
	p []byte,
) (int, error) {
	if w.budget == 0 {
		return 0, os.ErrClosed
	}

	w.budget--

	return len(p), nil
}
