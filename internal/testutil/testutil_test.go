package testutil

import (
	"os"
	"strings"
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
		{name: "with audio", clip: Clip{Seconds: 0.5, Audio: true}},
		{name: "video starting after the audio", clip: Clip{Seconds: 0.5, Audio: true, VideoDelay: 0.2}},
		{name: "audio starting before the timeline", clip: Clip{Seconds: 0.5, Audio: true, AudioLead: 0.05}},
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

func TestRequireFilter(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		filter   string
		wantSkip bool
	}{
		{name: "known filter", filter: "scale"},
		{name: "unknown filter", filter: "qc-no-such-filter", wantSkip: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			RequireFFmpeg(t)

			skipped := true

			// The check runs in its own test, whose skip is observed here.
			ok := t.Run("check", func(t *testing.T) {
				RequireFilter(t, testCase.filter)

				skipped = false
			})

			assert.True(t, ok)
			assert.Equal(t, testCase.wantSkip, skipped)
		})
	}
}

func TestHDRClip(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		transfer string
		hdr10    bool
	}{
		{name: "pq carries hdr10 metadata", transfer: "smpte2084", hdr10: true},
		{name: "hlg", transfer: "arib-std-b67"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			clip := HDRClip(testCase.transfer)

			assert.Equal(t, "libx265", clip.Codec)
			assert.Equal(t, "yuv420p10le", clip.PixelFormat)
			assert.Contains(t, clip.Filter, "color_trc="+testCase.transfer)
			assert.Equal(t, testCase.hdr10, strings.Contains(strings.Join(clip.Args, " "), "master-display"))
		})
	}
}
