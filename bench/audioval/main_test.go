package main

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/audiotest"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestRun(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	clip := testutil.Generate(t, testutil.Clip{Seconds: 4, Audio: true})

	var stdout, stderr bytes.Buffer

	// Two cases keep the test short: go run ./bench/audioval runs them all.
	code := run([]string{"-aac", "-run", "^(3341-1|silence)$", clip}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String()+stdout.String())

	out := stdout.String()
	assert.Contains(t, out, "## EBU Tech 3341 / 3342 conformance")
	assert.Contains(t, out, "| 3341-1 | stereo 1 kHz, -23 dBFS, 20 s | I | -23 ±0.1 | -22.99 | -23.00 | ✓ |")
	assert.Contains(t, out, "| silence | silence 20.0–23.0 s | PCM | 20.000–23.000 s | ✓ |")
	assert.Contains(t, out, "AAC 256k")
	assert.Contains(t, out, "| clip.mp4 | 1 |")
	assert.NotContains(t, out, "✗")
}

func TestRunErrors(
	t *testing.T,
) {
	var stdout, stderr bytes.Buffer

	assert.Equal(t, 2, run([]string{"-unknown"}, &stdout, &stderr))
	assert.Equal(t, 2, run([]string{"-run", "("}, &stdout, &stderr))

	missing := filepath.Join(t.TempDir(), "none", "ffmpeg")
	assert.Equal(t, 1, run([]string{"-ffmpeg", missing}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "audioval:")
}

func TestValidateErrors(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	dir := t.TempDir()
	var out bytes.Buffer

	// An unwritable work directory fails the first WAV, of either section.
	none := filepath.Join(dir, "none")
	_, err := validate(t.Context(), config{dir: none, ffmpeg: "ffmpeg", ffprobe: "ffprobe", filter: regexp.MustCompile("^3342-1$")}, &out)
	require.Error(t, err)

	_, err = validate(t.Context(), config{dir: none, ffmpeg: "ffmpeg", ffprobe: "ffprobe", filter: regexp.MustCompile("^clean$")}, &out)
	require.Error(t, err)

	// A failing AAC encode or decoder.
	_, err = defects(t.Context(), config{dir: dir, ffmpeg: "ffmpeg", ffprobe: "ffprobe", aac: true, filter: regexp.MustCompile("^clean$")}, &out)
	require.NoError(t, err)

	_, err = defects(t.Context(), config{dir: dir, ffmpeg: "ffmpeg", ffprobe: filepath.Join(dir, "no-ffprobe"), filter: regexp.MustCompile("^clean$")}, &out)
	require.Error(t, err)

	// A file without audio is skipped, a missing one fails.
	clip := testutil.Generate(t, testutil.Clip{Seconds: 1})
	require.NoError(t, crossCheck(t.Context(), config{ffmpeg: "ffmpeg", ffprobe: "ffprobe", files: []string{clip}}, &out))
	require.Error(t, crossCheck(t.Context(), config{ffmpeg: "ffmpeg", ffprobe: "ffprobe", files: []string{filepath.Join(dir, "x.mp4")}}, &out))

	_, err = analyse(t.Context(), config{ffmpeg: "ffmpeg", ffprobe: "ffprobe"}, clip)
	require.ErrorContains(t, err, "no audio stream")

	_, err = analyse(t.Context(), config{ffmpeg: "ffmpeg", ffprobe: "ffprobe"}, filepath.Join(dir, "x.wav"))
	require.Error(t, err)

	require.Error(t, encodeAAC(t.Context(), "ffmpeg", filepath.Join(dir, "x.wav"), filepath.Join(dir, "x.m4a")))

	_, err = loudnorm(t.Context(), "ffmpeg", filepath.Join(dir, "x.wav"), "0:0")
	require.Error(t, err)
}

func TestChecksFail(
	t *testing.T,
) {
	// A track where nothing was inserted fails every check but clean's.
	nothing := &audio.Track{
		Channels: []string{"FL", "FR"},
		Defects: defect.Result{
			Channels: []defect.Channel{{}, {}},
			Pairs:    []defect.Pair{{Left: 0, Right: 1}},
		},
	}

	for _, c := range defectCases() {
		if c.name == "clean" || c.name == "lfe" {
			continue
		}

		_, pass := c.check(nothing, edgeTolerance)
		assert.False(t, pass, c.name)
	}

	dirty := &audio.Track{
		Channels: []string{"FL", "FR"},
		Defects: defect.Result{
			Silence:  []media.Interval{{Start: 0, End: 1}},
			Channels: []defect.Channel{{Muted: true}, {}},
			Pairs:    []defect.Pair{{Left: 0, Right: 1, Inverted: true}},
		},
	}
	detected, pass := clean(dirty, 0)
	assert.False(t, pass)
	assert.Equal(t, "silence, FL, phase", detected)
}

func TestFFmpegParsing(
	t *testing.T,
) {
	assert.True(t, strings.HasPrefix(tail(strings.Repeat("x", 1000)), "x"))
	assert.Len(t, tail(strings.Repeat("x", 1000)), tailLength)
	assert.True(t, isNaN(number("-inf")))
	assert.True(t, isNaN(last(readingIntegrated, "nothing")))
	assert.InDelta(t, -23.5, last(readingIntegrated, "lavfi.r128.I=-20\nlavfi.r128.I=-23.5"), 1e-9)
	assert.Equal(t, "–", referenceReading(reference{integrated: nan()}, audiotest.Integrated))
	assert.Equal(t, "–", referenceReading(reference{}, audiotest.Momentary))
	assert.Equal(t, "– / 1.00 / –", triple(reference{integrated: nan(), lra: 1, truePeak: nan()}))
	assert.Zero(t, delta(loudness.Result{Integrated: loudness.Floor}, reference{integrated: -70, lra: nan(), truePeak: nan()}))
}

func isNaN(
	v float64,
) bool {
	return v != v
}

func nan() float64 {
	return number("nan?")
}
