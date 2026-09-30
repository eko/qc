package main

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForeground(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		terminal int
		err      error
		own      int
		want     bool
	}{
		{name: "the foreground job", terminal: 42, own: 42, want: true},
		{name: "a background job", terminal: 42, own: 7},
		{name: "not a terminal", err: errors.New("inappropriate ioctl for device"), own: 7},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := foreground(
				func() (int, error) { return testCase.terminal, testCase.err },
				func() int { return testCase.own },
			)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestInForegroundWithoutTerminal(
	t *testing.T,
) {
	r, w, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	assert.False(t, inForeground(int(w.Fd())), "a pipe has no foreground job")
}
