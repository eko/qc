package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrangeAt(
	t *testing.T,
) {
	testCases := []struct {
		name string
		t    float64
		want string
	}{
		{name: "light end", t: 0, want: "#ffc56b"},
		{name: "middle stop", t: 0.5, want: "#ff8a1f"},
		{name: "deep end", t: 1, want: "#f2530d"},
		{name: "between stops", t: 0.25, want: "#ffa845"},
		{name: "clamped below", t: -1, want: "#ffc56b"},
		{name: "clamped above", t: 2, want: "#f2530d"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, orangeAt(testCase.t))
		})
	}
}

func TestLogo(
	t *testing.T,
) {
	testCases := []struct {
		name string
		text []string
		want []string
	}{
		{
			name: "pixels only",
			want: []string{
				"▄▀▀▀▀▀▀▀▄ ▄▀▀▀▀▀▀",
				"▀▀  ▄  ▀▀ ▀▀",
				"▀▀  ▀▀ ▀▀ ▀▀",
				"▀▀     ▀▀ ▀▀",
				" ▀▀▀▀▀▀▀▀  ▀▀▀▀▀▀",
				"       ▀▀",
			},
		},
		{
			name: "text beside the first rows",
			text: []string{"qc", "fast"},
			want: []string{
				"▄▀▀▀▀▀▀▀▄ ▄▀▀▀▀▀▀   qc",
				"▀▀  ▄  ▀▀ ▀▀        fast",
			},
		},
		{
			name: "empty line leaves its row alone",
			text: []string{"", "fast"},
			want: []string{
				"▄▀▀▀▀▀▀▀▄ ▄▀▀▀▀▀▀",
				"▀▀  ▄  ▀▀ ▀▀        fast",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := strings.Split(ansi.Strip(Logo(testCase.text...)), "\n")

			assert.Len(t, lines, len(logoRows)/2)

			for _, want := range testCase.want {
				assert.True(t, containsTrimmed(lines, want), "missing line %q in\n%s", want, strings.Join(lines, "\n"))
			}
		})
	}
}

func TestLogoColours(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		r, c  int
		sweep float64
		want  string
	}{
		{name: "no pixel", r: 0, c: 0, sweep: noSweep, want: ""},
		{name: "hole of the q", r: 3, c: 6, sweep: noSweep, want: ""},
		{name: "light top-left", r: 1, c: 0, sweep: noSweep, want: "#ffc368"},
		{name: "play button in orange", r: 4, c: 5, sweep: noSweep, want: "#ff8a1f"},
		{name: "deep end of the c", r: 0, c: 16, sweep: noSweep, want: "#f76914"},
		{name: "glint lights a pixel", r: 1, c: 0, sweep: 0.25, want: "#ffe7c1"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, pixelColor(testCase.r, testCase.c, testCase.sweep))
		})
	}
}

func TestAnimateLogo(
	t *testing.T,
) {
	var out bytes.Buffer
	require.NoError(t, AnimateLogo(&out, "qc"))

	frames := strings.Split(out.String(), "\x1b[5A\r")
	require.Len(t, frames, sweepFrames+1)
	assert.Equal(t, Logo("qc"), frames[sweepFrames], "the last frame is the logo at rest")
	assert.NotEqual(t, frames[sweepFrames], frames[sweepFrames/2], "a glint crosses the logo")

	for _, frame := range frames {
		assert.Equal(t, ansi.Strip(Logo("qc")), ansi.Strip(frame), "only colours change between frames")
	}
}

func TestAnimateLogoWriteError(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		budget int
	}{
		{name: "first frame", budget: 0},
		{name: "cursor move", budget: 1},
		{name: "later frame", budget: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := AnimateLogo(&budgetWriter{budget: testCase.budget})

			require.ErrorIs(t, err, errWrite)
			assert.ErrorContains(t, err, "animate logo")
		})
	}
}

var errWrite = errors.New("write failed")

// budgetWriter accepts budget writes, then fails.
type budgetWriter struct {
	budget int
}

func (w *budgetWriter) Write(
	p []byte,
) (int, error) {
	if w.budget == 0 {
		return 0, errWrite
	}

	w.budget--

	return len(p), nil
}

// containsTrimmed reports whether one of lines equals want, ignoring
// trailing spaces.
func containsTrimmed(
	lines []string,
	want string,
) bool {
	for _, l := range lines {
		if strings.TrimRight(l, " ") == want {
			return true
		}
	}

	return false
}
