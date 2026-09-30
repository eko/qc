package encode

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/internal/testutil"
)

func TestEncodeRendition(
	t *testing.T,
) {
	src := ChunkSource{Path: testutil.Generate(t, testutil.Clip{Seconds: 4, GOP: 50}), Rate: rate25}

	testCases := []struct {
		name   string
		chunks []Chunk
	}{
		{name: "whole title"},
		{name: "per-shot chunks", chunks: []Chunk{{Start: 0, Frames: 50, CRF: 22}, {Start: 50, Frames: 50, CRF: 32}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "01-180p.mp4")

			var frames atomic.Int64

			err := NewFFmpeg("ffmpeg").EncodeRendition(t.Context(), codecs["h264"], RenditionSpec{
				Source: src, Destination: dst, Chunks: testCase.chunks,
				Params:   Params{Width: 320, Height: 180, CRF: 28, Preset: "ultrafast", GOP: 50},
				Progress: func(n int) { frames.Store(int64(n)) },
			})
			require.NoError(t, err)

			out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
				"-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", dst).Output()
			require.NoError(t, err)
			assert.Equal(t, "100", strings.TrimSpace(string(out)), "every frame of the title")
			assert.Equal(t, int64(100), frames.Load(), "progress reaches the last frame")
		})
	}
}

func TestEncodeRenditionError(
	t *testing.T,
) {
	dst := filepath.Join(t.TempDir(), "01-180p.mp4")

	err := NewFFmpeg("qc-no-such-ffmpeg").EncodeRendition(t.Context(), codecs["h264"], RenditionSpec{
		Source: ChunkSource{Path: "in.mov", Rate: rate25}, Destination: dst,
		Params: Params{Width: 64, Height: 36},
	})

	require.ErrorIs(t, err, ffexec.ErrNotFound)
	assert.NoFileExists(t, dst)
}
