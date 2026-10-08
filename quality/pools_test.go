package quality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFramePoolsGet(
	t *testing.T,
) {
	var pools framePools

	first := pools.get(1920, 1080, false)

	testCases := []struct {
		name          string
		width, height int
		highBitDepth  bool
		same          bool
	}{
		{name: "same geometry", width: 1920, height: 1080, same: true},
		{name: "other size", width: 1280, height: 720},
		{name: "other depth", width: 1920, height: 1080, highBitDepth: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := pools.get(testCase.width, testCase.height, testCase.highBitDepth)

			assert.Equal(t, testCase.same, pool == first)
			assert.Equal(t, testCase.width, pool.Width())
			assert.Equal(t, testCase.highBitDepth, pool.HighBitDepth())
			assert.True(t, pool.Chroma())
		})
	}
}
