package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
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
				"  ████████████    ████████████▓▓▒▒░░ ·",
				"████      ████  ████",
				"          ████",
			},
		},
		{
			name: "text beside the first rows",
			text: []string{"qc", "fast"},
			want: []string{
				"  ████████████    ████████████▓▓▒▒░░ ·   qc",
				"████      ████  ████                     fast",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := strings.Split(ansi.Strip(Logo(testCase.text...)), "\n")

			assert.Len(t, lines, len(logoRows))

			for _, want := range testCase.want {
				assert.True(t, containsTrimmed(lines, want), "missing line %q in\n%s", want, strings.Join(lines, "\n"))
			}
		})
	}
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
