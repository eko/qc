package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectRungsCapBelowProbes(
	t *testing.T,
) {
	hull := []HullPoint{{Bitrate: 200_000, VMAF: 40, Height: 360}, {Bitrate: 4_000_000, VMAF: 96, Height: 1080}}

	testCases := []struct {
		name      string
		max       int64
		wantRungs bool
	}{
		{name: "cap below the lowest probe", max: 150_000, wantRungs: false},
		{name: "cap inside the probed range", max: 1_000_000, wantRungs: true},
		{name: "no cap", max: 0, wantRungs: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rungs := SelectRungs(hull, Constraints{MaxBitrate: testCase.max})
			assert.Equal(t, testCase.wantRungs, len(rungs) > 0)
		})
	}
}

func TestBuildMaxBitrateBelowProbes(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, sourceReport(640, 360, 8, 25, 10))

	_, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec:       "h264",
		SkipVerify:  true,
		Constraints: Constraints{MaxBitrate: 1},
	})

	require.ErrorIs(t, err, ErrNoRungs)
	require.ErrorContains(t, err, "below the lowest probed bitrate")
}
