package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eko/qc/internal/tui"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
)

// videoSummary is what the wizard shows of a video file: enough to tell the
// right file from its neighbours, read by ffprobe without decoding.
type videoSummary struct {
	// err is set when ffprobe cannot read the file.
	err      error
	hasVideo bool
	codec    string
	width    int
	height   int
	fps      float64
	bitDepth int
	duration media.Duration
	// dynamicRange is the HDR format ("HDR10", "HLG"...), "" for SDR.
	dynamicRange string
	audioTracks  int
	// stream is the video stream, compared between the videos of one ladder.
	stream media.VideoStream
}

// summarize extracts the summary of a probed file.
func summarize(
	info *media.Info,
) videoSummary {
	s := videoSummary{duration: info.Duration, audioTracks: len(info.Audio)}

	v, ok := info.PrimaryVideo()
	if !ok {
		return s
	}

	s.hasVideo, s.stream = true, v
	s.codec, s.width, s.height, s.bitDepth = v.Codec, v.Width, v.Height, v.BitDepth
	s.fps = v.AvgFrameRate.Float()

	if v.Color.IsHDR() {
		s.dynamicRange = string(v.HDR.DynamicRange)
	}

	return s
}

// validate rejects a file the run could not use.
func (s videoSummary) validate() error {
	if s.err != nil {
		return errors.New("cannot read this file (ffprobe failed): pick another one")
	}

	if !s.hasVideo {
		return errors.New("no video stream in this file")
	}

	return nil
}

// codecNames are the usual names of common video codecs.
var codecNames = map[string]string{
	"h264": "H.264", "hevc": "HEVC", "av1": "AV1", "vp9": "VP9", "vp8": "VP8",
	"prores": "ProRes", "mpeg2video": "MPEG-2", "mpeg4": "MPEG-4", "dnxhd": "DNxHD",
	"rawvideo": "raw", "ffv1": "FFV1", "mjpeg": "MJPEG", "jpeg2000": "JPEG 2000",
}

// codecName is the usual name of a codec, or its ffprobe name in capitals.
func codecName(
	codec string,
) string {
	if name, ok := codecNames[codec]; ok {
		return name
	}

	return strings.ToUpper(codec)
}

// format is the picture of the stream, e.g. "H.264 · 1920×1080 · 25 fps".
func (s videoSummary) format() string {
	parts := []string{codecName(s.codec), fmt.Sprintf("%d×%d", s.width, s.height)}

	if s.fps > 0 {
		parts = append(parts, strings.TrimSuffix(fmt.Sprintf("%.3f", s.fps), ".000")+" fps")
	}

	if s.bitDepth > 8 {
		parts = append(parts, fmt.Sprintf("%d-bit", s.bitDepth))
	}

	return strings.Join(parts, " · ")
}

// timing is the duration, the dynamic range and the audio, e.g.
// "0:20 · SDR · 1 audio track".
func (s videoSummary) timing() string {
	dr := s.dynamicRange
	if dr == "" {
		dr = "SDR"
	}

	return tui.Clock(s.duration, true) + " · " + dr + " · " + audioTracksLabel(s.audioTracks)
}

// line is the whole summary on one line.
func (s videoSummary) line() string {
	return s.format() + " · " + s.timing()
}

// short is a compact label for file lists, e.g. "1080p · 0:20".
func (s videoSummary) short() string {
	return fmt.Sprintf("%dp · %s", s.height, tui.Clock(s.duration, true))
}

// audioTracksLabel counts the audio tracks.
func audioTracksLabel(
	n int,
) string {
	switch n {
	case 0:
		return "no audio"
	case 1:
		return "1 audio track"
	}

	return fmt.Sprintf("%d audio tracks", n)
}

// probeFunc reads a media file (probe.FFprobe.Probe in production).
type probeFunc func(ctx context.Context, path string) (*media.Info, error)

// videoCache probes each file once: the pickers probe what they highlight,
// the form asks the dynamic range of the chosen files on every key stroke.
// It is safe for concurrent use: previews are probed in the background.
type videoCache struct {
	// ctx bounds the probes: the form has no context of its own.
	ctx   context.Context
	probe probeFunc

	mu      sync.Mutex
	entries map[string]videoSummary
}

// newVideoCache probes with ffprobe.
func newVideoCache(
	ctx context.Context,
	ffprobe string,
) *videoCache {
	return newVideoCacheWith(ctx, probe.NewFFprobe(ffprobe).Probe)
}

// newVideoCacheWith probes with fn.
func newVideoCacheWith(
	ctx context.Context,
	fn probeFunc,
) *videoCache {
	return &videoCache{ctx: ctx, probe: fn, entries: map[string]videoSummary{}}
}

// cached returns the summary of path if it was probed.
func (c *videoCache) cached(
	path string,
) (videoSummary, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.entries[cacheKey(path)]

	return s, ok
}

// inspect returns the summary of path, probing it on first use.
func (c *videoCache) inspect(
	path string,
) videoSummary {
	if s, ok := c.cached(path); ok {
		return s
	}

	ctx, cancel := context.WithTimeout(c.ctx, wizardProbeTimeout)
	defer cancel()

	info, err := c.probe(ctx, path)

	s := videoSummary{err: err}
	if err == nil {
		s = summarize(info)
	}

	c.mu.Lock()
	c.entries[cacheKey(path)] = s
	c.mu.Unlock()

	return s
}

// cacheKey is the absolute path: the form holds relative paths, the pickers
// absolute ones.
func cacheKey(
	path string,
) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}

	return path
}

// dynamicRange is the HDR format of a video, "" for SDR, for an unreadable
// file or without a path.
func (c *videoCache) dynamicRange(
	path string,
) string {
	if path == "" {
		return ""
	}

	return c.inspect(path).dynamicRange
}

// previewDelay lets the highlight of a file list settle before probing:
// scrolling through a folder must not start one ffprobe per line.
const previewDelay = 120 * time.Millisecond
