package testutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerate(
	t *testing.T,
) {
	testCases := []struct {
		name string
		clip Clip
	}{
		{name: "defaults", clip: Clip{}},
		{name: "fixed gop with filter", clip: Clip{GOP: 10, Filter: "hflip", Seconds: 1, Name: "x.mkv"}},
		{name: "other codec", clip: Clip{Codec: "mpeg4", Seconds: 0.5, Args: []string{"-q:v", "5"}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := Generate(t, testCase.clip)

			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Positive(t, info.Size())
		})
	}
}
