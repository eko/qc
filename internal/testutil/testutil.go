// Package testutil provides helpers for tests that need real media files.
package testutil

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// RequireFFmpeg skips the test when ffmpeg or ffprobe is not installed.
func RequireFFmpeg(
	t testing.TB,
) {
	t.Helper()

	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
}

// Clip describes a synthetic clip generated with ffmpeg's lavfi sources.
type Clip struct {
	// Source is a lavfi source graph (default testsrc2).
	Source string
	// Width, Height and Rate default to 320×180 at 25 fps.
	Width, Height int
	Rate          int
	// Seconds defaults to 2.
	Seconds float64
	// Codec is an ffmpeg encoder (default libx264).
	Codec string
	// PixelFormat defaults to yuv420p.
	PixelFormat string
	// GOP sets a fixed keyframe interval in frames (0 = encoder default).
	GOP int
	// Filter is an extra video filter chain applied before encoding.
	Filter string
	// Name is the output file name (default clip.mp4).
	Name string
	// Args are extra output arguments.
	Args []string
}

// Generate encodes the clip into t.TempDir and returns its path. It skips
// the test when ffmpeg is missing.
func Generate(
	t testing.TB,
	c Clip,
) string {
	t.Helper()
	RequireFFmpeg(t)

	c = c.withDefaults()
	path := filepath.Join(t.TempDir(), c.Name)

	source := c.Source + "=size=" + strconv.Itoa(c.Width) + "x" + strconv.Itoa(c.Height) +
		":rate=" + strconv.Itoa(c.Rate) + ":duration=" + strconv.FormatFloat(c.Seconds, 'f', -1, 64)

	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", source}

	filter := "format=" + c.PixelFormat
	if c.Filter != "" {
		filter = c.Filter + "," + filter
	}

	args = append(args, "-vf", filter, "-c:v", c.Codec)

	if c.GOP > 0 {
		gop := strconv.Itoa(c.GOP)
		args = append(args, "-g", gop, "-keyint_min", gop)

		if c.Codec == "libx264" {
			args = append(args, "-sc_threshold", "0")
		}
	}

	if c.Codec == "libx264" {
		args = append(args, "-preset", "ultrafast")
	}

	args = append(args, c.Args...)
	args = append(args, path)

	out, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput()
	require.NoError(t, err, string(out))

	return path
}

func (c Clip) withDefaults() Clip {
	if c.Source == "" {
		c.Source = "testsrc2"
	}

	if c.Width == 0 {
		c.Width, c.Height = 320, 180
	}

	if c.Rate == 0 {
		c.Rate = 25
	}

	if c.Seconds == 0 {
		c.Seconds = 2
	}

	if c.Codec == "" {
		c.Codec = "libx264"
	}

	if c.PixelFormat == "" {
		c.PixelFormat = "yuv420p"
	}

	if c.Name == "" {
		c.Name = "clip.mp4"
	}

	return c
}
