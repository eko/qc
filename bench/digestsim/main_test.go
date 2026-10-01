package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

const (
	frameRate = 25
	// slotFrames is the share of the title of each of the 20 segments of a
	// 40 s digest of a 600 s title.
	slotFrames = 750
	// burstFrames is how long every slot starts busy: evenly spaced
	// segments, centred in their slots, never see it.
	burstFrames = 150
)

// busy reports whether a frame of the title is in a burst.
func busy(
	frame int,
) bool {
	return frame%slotFrames < burstFrames
}

// writeJSON writes v as JSON in a temporary file and returns its path.
func writeJSON(
	t *testing.T,
	name string,
	v any,
) string {
	t.Helper()

	data, err := json.Marshal(v)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	return path
}

// sourceAnalysis is the frame analysis of a 25 fps title whose temporal
// information bursts at the start of every slot.
func sourceAnalysis(
	seconds int,
) analysis.Report {
	frames := seconds * frameRate
	series := &analysis.FrameSeries{
		PTS: make([]media.Duration, frames), SI: make([]float64, frames), TI: make([]float64, frames),
	}

	for i := range frames {
		series.PTS[i] = media.Seconds(float64(i) / frameRate)
		series.SI[i] = 40

		series.TI[i] = 4
		if busy(i) {
			series.TI[i] = 24
		}
	}

	rate := media.Rational{Num: frameRate, Den: 1}

	return analysis.Report{
		Info: &media.Info{
			Duration: media.Seconds(float64(seconds)),
			Video:    []media.VideoStream{{Width: 1920, Height: 1080, FrameRate: rate, AvgFrameRate: rate}},
		},
		Frames: series,
	}
}

// exactEncode is an exact measurement of an encode of that title: its
// bursts score 70 and cost 5 Mb/s, the rest 90 and 1 Mb/s. Over the title,
// VMAF 86 at 1.8 Mb/s.
func exactEncode(
	seconds int,
) analysis.Comparison {
	res := &quality.Result{Mode: quality.ModeExact, Mean: 86}
	bs := &bitstream.Report{
		Interval: media.Seconds(1), Duration: media.Seconds(float64(seconds)), AverageBitrate: 1_800_000,
	}

	for i := range seconds * frameRate {
		score := 90.0
		if busy(i) {
			score = 70
		}

		res.Frames = append(res.Frames, quality.FrameScore{Index: i, PTS: media.Seconds(float64(i) / frameRate), Score: score})
	}

	for s := range seconds {
		bitrate := int64(1_000_000)
		if busy(s * frameRate) {
			bitrate = 5_000_000
		}

		bs.Bitrate = append(bs.Bitrate, bitstream.BitratePoint{Start: media.Seconds(float64(s)), Bitrate: bitrate})
	}

	return analysis.Comparison{VMAF: res, Distorted: &analysis.Report{Bitstream: bs}}
}

func TestRun(
	t *testing.T,
) {
	source := writeJSON(t, "analysis.json", sourceAnalysis(600))
	exact := writeJSON(t, "exact.json", exactEncode(600))

	sampled := exactEncode(600)
	sampled.VMAF.Mode = quality.ModeSampled

	noBitrate := exactEncode(600)
	noBitrate.Distorted.Bitstream.Bitrate = nil

	inspection := sourceAnalysis(600)
	inspection.Frames = nil

	holed := sourceAnalysis(600)
	holed.Frames.PTS = append(holed.Frames.PTS[:2500:2500], holed.Frames.PTS[5500:]...)
	holed.Frames.SI = append(holed.Frames.SI[:2500:2500], holed.Frames.SI[5500:]...)
	holed.Frames.TI = append(holed.Frames.TI[:2500:2500], holed.Frames.TI[5500:]...)

	noVideo := sourceAnalysis(600)
	noVideo.Info.Video = nil

	testCases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr string
	}{
		{
			name:     "compares the digests on every measurement",
			args:     []string{"-source", source, exact, exact},
			wantCode: 0,
			wantStdout: []string{
				"digest 40s, 20 segments (title SI 40.00 TI 8.00; balanced 40.00 8.00; uniform 40.00 4.00; top 40.00 24.00)",
				"every phase: rms ΔVMAF",
				// The balanced digest is the title; the uniform one misses
				// every burst: 90 for 86, 1 Mb/s for 1.8.
				"exact  86.00  1800           +0.00     +0.0%          +4.00    -44.4%",
				"rms                         0.00      0.0%           4.00     44.4%",
				// The top digest holds bursts only: 70 for 86, and 5 Mb/s for
				// 1.8 but for the quarter of a frame segments start early.
				"-16.00   +177.4%",
			},
		},
		{
			name:       "replays every digest length",
			args:       []string{"-source", source, "-digests", "40, 80", "-segment", "4", "-phases", "10", exact},
			wantCode:   0,
			wantStdout: []string{"digest 40s, 10 segments", "digest 1m20s, 20 segments"},
		},
		{
			name:       "no measurement",
			args:       []string{"-source", source},
			wantCode:   2,
			wantStderr: "usage: digestsim",
		},
		{
			name:       "no source",
			args:       []string{exact},
			wantCode:   2,
			wantStderr: ErrNoSource.Error(),
		},
		{
			name:       "unknown flag",
			args:       []string{"-nope"},
			wantCode:   2,
			wantStderr: "flag provided but not defined",
		},
		{
			name:       "invalid digest length",
			args:       []string{"-source", source, "-digests", "40,x", exact},
			wantCode:   2,
			wantStderr: `invalid digest length "x"`,
		},
		{
			name:       "negative digest length",
			args:       []string{"-source", source, "-digests", "-40", exact},
			wantCode:   2,
			wantStderr: `invalid digest length "-40"`,
		},
		{
			name:       "no phase",
			args:       []string{"-source", source, "-phases", "0", exact},
			wantCode:   2,
			wantStderr: "-segment and -phases must be positive",
		},
		{
			name:       "missing source file",
			args:       []string{"-source", filepath.Join(t.TempDir(), "missing.json"), exact},
			wantCode:   1,
			wantStderr: "read ",
		},
		{
			name:       "source that is not JSON",
			args:       []string{"-source", writeText(t, "{"), exact},
			wantCode:   1,
			wantStderr: "parse ",
		},
		{
			name:       "source without a frame analysis",
			args:       []string{"-source", writeJSON(t, "inspection.json", inspection), exact},
			wantCode:   1,
			wantStderr: ErrNoSource.Error(),
		},
		{
			name:       "source without video",
			args:       []string{"-source", writeJSON(t, "novideo.json", noVideo), exact},
			wantCode:   1,
			wantStderr: ladder.ErrInvalidSource.Error(),
		},
		{
			name:       "source whose digest cannot be balanced",
			args:       []string{"-source", writeJSON(t, "holed.json", holed), exact},
			wantCode:   1,
			wantStderr: ErrNoSource.Error(),
		},
		{
			name:       "digest holding the whole title",
			args:       []string{"-source", source, "-digests", "600", exact},
			wantCode:   1,
			wantStderr: ErrNotSampled.Error(),
		},
		{
			name:       "missing measurement",
			args:       []string{"-source", source, filepath.Join(t.TempDir(), "missing.json")},
			wantCode:   1,
			wantStderr: "read ",
		},
		{
			name:       "sampled measurement",
			args:       []string{"-source", source, writeJSON(t, "sampled.json", sampled)},
			wantCode:   1,
			wantStderr: ErrNotExact.Error(),
		},
		{
			name:       "measurement without the bitrate of the encode",
			args:       []string{"-source", source, writeJSON(t, "nobitrate.json", noBitrate)},
			wantCode:   1,
			wantStderr: ErrNoBitrate.Error(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(testCase.args, &stdout, &stderr)
			assert.Equal(t, testCase.wantCode, code, stderr.String())

			for _, want := range testCase.wantStdout {
				assert.Contains(t, stdout.String(), want)
			}

			if testCase.wantStderr != "" {
				assert.Contains(t, stderr.String(), testCase.wantStderr)
			}
		})
	}
}

// writeText writes text in a temporary file and returns its path.
func writeText(
	t *testing.T,
	text string,
) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "text.json")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))

	return path
}

func TestRunWriteError(
	t *testing.T,
) {
	source := writeJSON(t, "analysis.json", sourceAnalysis(600))
	exact := writeJSON(t, "exact.json", exactEncode(600))

	var stderr bytes.Buffer

	code := run([]string{"-source", source, exact}, failingWriter{}, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "write table")
}

// failingWriter refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrClosed
}

func TestEncodeGap(
	t *testing.T,
) {
	read := func(t *testing.T) encode {
		t.Helper()

		e, err := readEncode(writeJSON(t, "exact.json", exactEncode(600)))
		require.NoError(t, err)

		return e
	}

	testCases := []struct {
		name        string
		segments    []media.Interval
		wantVMAF    float64
		wantBitrate float64
	}{
		{
			name:        "calm frames only",
			segments:    []media.Interval{{Start: media.Seconds(14), End: media.Seconds(16)}},
			wantVMAF:    4,
			wantBitrate: 1/1.8 - 1,
		},
		{
			name:        "a burst only",
			segments:    []media.Interval{{Start: media.Seconds(30), End: media.Seconds(32)}},
			wantVMAF:    -16,
			wantBitrate: 5/1.8 - 1,
		},
		{
			name: "half a second of a burst, between buckets",
			// 12 of its 50 frames (5.52 s to 5.96 s) are busy.
			segments:    []media.Interval{{Start: media.Seconds(5.5), End: media.Seconds(7.5)}},
			wantVMAF:    90 - 20*12.0/50 - 86,
			wantBitrate: (0.5*5+1.5*1)/2/1.8 - 1,
		},
		{
			name: "the title's share of bursts",
			segments: []media.Interval{
				{Start: media.Seconds(0), End: media.Seconds(2)},
				{Start: media.Seconds(10), End: media.Seconds(18)},
			},
			wantVMAF:    0,
			wantBitrate: 0,
		},
		{
			name:        "a segment cut by the end of the title",
			segments:    []media.Interval{{Start: media.Seconds(599), End: media.Seconds(601)}},
			wantVMAF:    4,
			wantBitrate: 1/1.8 - 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := read(t).gap(testCase.segments)

			assert.InDelta(t, testCase.wantVMAF, got.vmaf, 1e-9)
			assert.InDelta(t, testCase.wantBitrate, got.bitrate, 1e-9)
		})
	}
}

func TestEncodeShifted(
	t *testing.T,
) {
	e, err := readEncode(writeJSON(t, "exact.json", exactEncode(600)))
	require.NoError(t, err)

	// One phase is the first position: every segment in a burst.
	first := e.shifted(media.Seconds(600), media.Seconds(2), 20, 1)
	assert.InDelta(t, 16, first.vmaf, 1e-9)
	assert.InDelta(t, 5/1.8-1, first.bitrate, 1e-9)

	// Over every position, segments are wholly in a burst a seventh of the
	// time (starts 0 to 4 s of 0 to 28 s), partly in it another fourteenth
	// and out of it otherwise: √((16²·4 + 4160/30 + 4²·22) / 28) VMAF, and
	// likewise for the bitrate.
	every := e.shifted(media.Seconds(600), media.Seconds(2), 20, 141)
	assert.InDelta(t, 7.36, every.vmaf, 0.05)
	assert.InDelta(t, 0.817, every.bitrate, 0.01)
}
