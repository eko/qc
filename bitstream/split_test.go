package bitstream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitAtKeyframes(
	t *testing.T,
) {
	keyframes := func(frames, gop int) []bool {
		k := make([]bool, frames)
		for i := 0; i < frames; i += gop {
			k[i] = true
		}

		return k
	}

	testCases := []struct {
		name     string
		keyframe []bool
		count    int
		minGap   int
		want     []int
	}{
		{name: "one run", keyframe: keyframes(900, 25), count: 1, minGap: 10, want: []int{0}},
		{name: "no keyframe to split at", keyframe: keyframes(5000, 10000), count: 4, minGap: 10, want: []int{0}},
		{name: "bounds at the next keyframe", keyframe: keyframes(1000, 30), count: 4, minGap: 100, want: []int{0, 270, 510, 750}},
		{name: "a bound too close to the end is dropped", keyframe: keyframes(1000, 300), count: 4, minGap: 150, want: []int{0, 300, 600}},
		{name: "no frame", keyframe: nil, count: 4, minGap: 1, want: []int{0}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, SplitAtKeyframes(testCase.keyframe, testCase.count, testCase.minGap))
		})
	}
}
