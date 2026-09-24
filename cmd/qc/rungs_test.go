package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRungs(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		in          string
		wantCount   int
		wantHeights []int
		wantErr     bool
	}{
		{name: "empty is automatic", in: ""},
		{name: "auto", in: " Auto "},
		{name: "count", in: "6", wantCount: 6},
		{name: "resolutions", in: "1080,720,720,540,360", wantHeights: []int{1080, 720, 720, 540, 360}},
		{name: "resolutions with suffix and spaces", in: "1080p, 720p", wantHeights: []int{1080, 720}},
		{name: "a single resolution", in: "720p", wantHeights: []int{720}},
		{name: "zero count", in: "0", wantErr: true},
		{name: "not a number", in: "many", wantErr: true},
		{name: "bad resolution", in: "1080,hd", wantErr: true},
		{name: "negative resolution", in: "1080,-720", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			count, heights, err := parseRungs(testCase.in)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidRungs)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantCount, count)
			assert.Equal(t, testCase.wantHeights, heights)
		})
	}
}

func TestLadderOptionsShape(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		rungs       string
		wantCount   int
		wantHeights []int
	}{
		{name: "automatic", rungs: "auto"},
		{name: "count", rungs: "4", wantCount: 4},
		{name: "resolutions", rungs: "1080,540", wantHeights: []int{1080, 540}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			opts := ladderOptions(Config{Ladder: LadderConfig{Rungs: testCase.rungs}})

			assert.Equal(t, testCase.wantCount, opts.Constraints.Rungs)
			assert.Equal(t, testCase.wantHeights, opts.Constraints.Resolutions)
		})
	}
}

func TestValidateRejectsBadRungs(
	t *testing.T,
) {
	err := Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Rungs: "lots"}}.validate()

	require.ErrorIs(t, err, ErrInvalidRungs)
}
