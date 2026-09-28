package main

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

func TestVideoSummary(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		info      *media.Info
		wantLine  string
		wantShort string
		wantErr   string
	}{
		{
			name:      "sdr with audio",
			info:      fakeVideos["source.mp4"],
			wantLine:  "H.264 · 1920×1080 · 25 fps · 0:20 · SDR · 1 audio track",
			wantShort: "1080p · 0:20",
		},
		{
			name:      "hdr, two tracks",
			info:      fakeVideos["ref.mov"],
			wantLine:  "HEVC · 3840×2160 · 23.976 fps · 10-bit · 1:05 · HDR10 · 2 audio tracks",
			wantShort: "2160p · 1:05",
		},
		{
			name:     "unknown codec, no frame rate, no audio",
			info:     &media.Info{Video: []media.VideoStream{{Codec: "cfhd", Width: 640, Height: 360}}},
			wantLine: "CFHD · 640×360 · 0:00 · SDR · no audio",
		},
		{
			name:    "no video",
			info:    fakeVideos["audio.m4v"],
			wantErr: "no video stream in this file",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			s := summarize(testCase.info)

			if testCase.wantErr != "" {
				require.EqualError(t, s.validate(), testCase.wantErr)

				return
			}

			require.NoError(t, s.validate())
			assert.Equal(t, testCase.wantLine, s.line())

			if testCase.wantShort != "" {
				assert.Equal(t, testCase.wantShort, s.short())
			}
		})
	}

	require.ErrorContains(t, videoSummary{err: errUnreadable}.validate(), "cannot read this file")
}

func TestVideoCache(
	t *testing.T,
) {
	dir := wizardDir(t)

	var calls atomic.Int32

	videos := newVideoCacheWith(t.Context(), fakeProbe(&calls))

	_, known := videos.cached("source.mp4")
	assert.False(t, known)

	assert.True(t, videos.inspect("source.mp4").hasVideo)
	assert.True(t, videos.inspect(filepath.Join(dir, "source.mp4")).hasVideo, "relative and absolute paths share an entry")
	assert.Equal(t, int32(1), calls.Load())

	assert.Equal(t, "HDR10", videos.dynamicRange("ref.mov"))
	assert.Empty(t, videos.dynamicRange(""))
	assert.Equal(t, int32(2), calls.Load())

	require.Error(t, videos.inspect("broken.mp4").validate())

	sum, known := videos.cached("broken.mp4")
	assert.True(t, known, "failures are cached too")
	require.ErrorIs(t, sum.err, errUnreadable)
}

func TestVideoCacheBoundsProbes(
	t *testing.T,
) {
	videos := newVideoCacheWith(t.Context(), func(ctx context.Context, _ string) (*media.Info, error) {
		deadline, ok := ctx.Deadline()
		assert.True(t, ok, "probes are bounded")
		assert.False(t, deadline.IsZero())

		return fakeVideos["source.mp4"], nil
	})

	videos.inspect("clip.mp4")
}
