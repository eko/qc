// Package testutil provides helpers for tests that need real media files.
package testutil

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// RequireFilter skips the test when ffmpeg is not installed or lacks the
// named filter (subtitles needs an ffmpeg built with libass).
func RequireFilter(
	t testing.TB,
	name string,
) {
	t.Helper()
	RequireFFmpeg(t)

	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-filters").Output()
	require.NoError(t, err)

	for line := range strings.Lines(string(out)) {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[1] == name {
			return
		}
	}

	t.Skipf("ffmpeg has no %s filter", name)
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
	// Audio adds a sine tone as an AAC audio stream.
	Audio bool
	// VideoDelay starts the video that many seconds after the audio (with
	// Audio): the container's timeline then starts before the first frame
	// of the video, as in some concatenations.
	VideoDelay float64
	// AudioLead starts the audio that many seconds before the video, which
	// stays at 0 (with Audio): the container's timeline then starts before
	// 0, as with an audio track keeping its encoder priming. The clip is
	// remuxed to Matroska, which keeps negative timestamps.
	AudioLead float64
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
	if c.Audio {
		tone := "sine=frequency=440:sample_rate=48000:duration=" + strconv.FormatFloat(c.Seconds, 'f', -1, 64)
		args = append(args, "-f", "lavfi", "-i", tone, "-c:a", "aac")
	}

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

	if c.VideoDelay > 0 {
		return delayVideo(t, path, c.VideoDelay)
	}

	if c.AudioLead > 0 {
		return leadAudio(t, path, c.AudioLead)
	}

	return path
}

// leadAudio remuxes path to Matroska with its audio shifted lead seconds
// earlier, before the start of the timeline, and returns the new file.
func leadAudio(
	t testing.TB,
	path string,
	lead float64,
) string {
	t.Helper()

	base := filepath.Base(path)
	led := filepath.Join(t.TempDir(), strings.TrimSuffix(base, filepath.Ext(base))+".mkv")
	args := []string{
		"-v", "error", "-y", "-i", path, "-itsoffset", strconv.FormatFloat(-lead, 'f', -1, 64), "-i", path,
		"-map", "0:v", "-map", "1:a", "-c", "copy", "-avoid_negative_ts", "disabled", led,
	}

	out, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput()
	require.NoError(t, err, string(out))

	return led
}

// delayVideo remuxes path with its video shifted by delay seconds and its
// other streams in place, and returns the new file.
func delayVideo(
	t testing.TB,
	path string,
	delay float64,
) string {
	t.Helper()

	delayed := filepath.Join(t.TempDir(), filepath.Base(path))
	args := []string{
		"-v", "error", "-y", "-itsoffset", strconv.FormatFloat(delay, 'f', -1, 64), "-i", path, "-i", path,
		"-map", "0:v", "-map", "1:a?", "-c", "copy", delayed,
	}

	out, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput()
	require.NoError(t, err, string(out))

	return delayed
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

// Metadata of the synthetic HDR10 clips of HDRClip: a P3 D65 mastering
// display of 1000 cd/m² (x265's units: 0.00002 for chromaticities, 0.0001
// cd/m² for luminances), MaxCLL 1000 and MaxFALL 400 cd/m².
const (
	hdrMasterDisplay = "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,50)"
	hdrMaxCLL        = "1000,400"
)

// HDRClip returns a 10-bit HEVC clip tagged with BT.2020 primaries and
// matrix and transfer (smpte2084 or arib-std-b67): its code values are
// testsrc2's, labelled as HDR, which is all signalling and metric tests
// need. PQ clips also carry HDR10 metadata (hdrMasterDisplay, hdrMaxCLL)
// in their SEI. The colours are set on the frames (setparams): ffmpeg's
// encoders take them from the frames, and ignore -color_trc when the
// frames say otherwise.
func HDRClip(
	transfer string,
) Clip {
	params := "log-level=error"
	if transfer == "smpte2084" {
		params += ":hdr10=1:master-display=" + hdrMasterDisplay + ":max-cll=" + hdrMaxCLL
	}

	return Clip{
		Codec:       "libx265",
		PixelFormat: "yuv420p10le",
		Filter:      "format=yuv420p10le,setparams=color_primaries=bt2020:color_trc=" + transfer + ":colorspace=bt2020nc:range=tv",
		Args:        []string{"-preset", "ultrafast", "-x265-params", params},
	}
}
