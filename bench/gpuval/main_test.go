package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

var errFake = errors.New("fake failure")

// fakeRunner answers commands like a GPU machine would.
type fakeRunner func(name string, args []string) ([]byte, error)

func (f fakeRunner) run(
	_ context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	return f(name, args)
}

// has reports whether args contain every word.
func has(
	args []string,
	words ...string,
) bool {
	for _, w := range words {
		if !slices.Contains(args, w) {
			return false
		}
	}

	return true
}

// hasPair reports whether args hold flag followed by value.
func hasPair(
	args []string,
	flag, value string,
) bool {
	i := slices.Index(args, flag)

	return i >= 0 && i+1 < len(args) && args[i+1] == value
}

// comparison is a qc vmaf report of 4 frames around mean.
func comparison(
	mean, bitrate float64,
) []byte {
	res := &quality.Result{Mean: mean, HalfWidth: 0.3}
	for i := range 4 {
		res.Frames = append(res.Frames, quality.FrameScore{Index: i, Score: mean + float64(i%2)*0.5})
	}

	cmp := analysis.Comparison{VMAF: res, Distorted: &analysis.Report{Bitstream: &bitstream.Report{AverageBitrate: int64(bitrate)}}}
	out, _ := json.Marshal(cmp)

	return out
}

// ladderReport is a qc ladder report: CPU rungs, and a lower envelope for
// NVENC (it needs more bitrate for the same quality).
func ladderReport(
	encoder string,
) []byte {
	res := ladder.Result{Codec: encode.Codec{Name: "h264", Encoder: encoder}}
	scale := 1.0

	if strings.HasSuffix(encoder, "_nvenc") {
		scale = 1.25
		res.Probes = []ladder.Probe{{Height: 1080, CRF: 21, Bitrate: 6_000_000, VMAF: 96}}
	}

	for i, b := range []float64{500_000, 1_000_000, 2_000_000, 4_000_000, 8_000_000} {
		res.Hull = append(res.Hull, ladder.HullPoint{Bitrate: int64(b * scale), VMAF: 50 + 10*float64(i), Height: 1080})
	}

	res.Rungs = []ladder.Rung{
		{Height: 1080, Bitrate: 4_000_000, PredictedVMAF: 80, Measured: &ladder.Measurement{Bitrate: 4_000_000, VMAF: 80}},
		{Height: 720, Bitrate: 1_400_000, PredictedVMAF: 65},
		{Height: 360, Bitrate: 200_000, PredictedVMAF: 30},
	}

	out, _ := json.Marshal(res)

	return out
}

var calibName = regexp.MustCompile(`calib-\w+-(\d+)p-cq([\d.]+)\.mp4$`)

// gpuMachine fakes a machine where everything works; fail makes the
// commands it matches fail.
func gpuMachine(
	fail func(name string, args []string) bool,
) fakeRunner {
	return func(name string, args []string) ([]byte, error) {
		if fail != nil && fail(name, args) {
			return nil, fmt.Errorf("%w\nsecond line", errFake)
		}

		switch {
		case name == "nvidia-smi":
			return []byte("NVIDIA L4, 570.86, 23034 MiB\n"), nil
		case name == "ffprobe":
			return []byte("1920,1080\n"), nil
		case name == "qc" && args[0] == "version":
			return []byte("\nqc dev\n"), nil
		case name == "qc" && args[0] == "analyze":
			return []byte("{}"), nil
		case name == "qc" && args[0] == "ladder":
			encoder := "libx264"
			if has(args, "--gpu") {
				encoder = "h264_nvenc"
			}

			return ladderReport(encoder), nil
		case name == "qc" && args[0] == "vmaf":
			return qcVMAFAnswer(args), nil
		case has(args, "framemd5"):
			return []byte("#format: frame checksums\n0, 0, 0, 1, 3110400, aaa\n0, 1, 1, 1, 3110400, bbb\n"), nil
		case slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "libvmaf_cuda") }):
			return nil, writeLibvmafLog(args)
		}

		return nil, nil
	}
}

// qcVMAFAnswer answers qc vmaf: calibration encodes lose quality with the
// CQ and the resolution, CUDA shifts scores slightly.
func qcVMAFAnswer(
	args []string,
) []byte {
	if m := calibName.FindStringSubmatch(args[2]); m != nil {
		height, _ := strconv.Atoi(m[1])
		cq, _ := strconv.ParseFloat(m[2], 64)
		quality := 130 - 2*cq - 0.02*float64(1080-height)

		return comparison(quality, 1e8/cq)
	}

	mean := 90.0
	if hasPair(args, "--vmaf-backend", "cuda") {
		mean += 0.001
	}

	return comparison(mean, 3e6)
}

// writeLibvmafLog writes the JSON log ffmpeg's libvmaf_cuda would.
func writeLibvmafLog(
	args []string,
) error {
	for _, a := range args {
		if i := strings.Index(a, "log_path="); i >= 0 {
			return os.WriteFile(a[i+len("log_path="):], []byte(`{"pooled_metrics": {"vmaf": {"mean": 89.5}}}`), 0o600)
		}
	}

	return errFake
}

func TestRun(
	t *testing.T,
) {
	dir := t.TempDir()

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), []string{"-dir", dir, "-codecs", "h264"}, &stdout, &stderr, gpuMachine(nil))
	require.Equal(t, 0, code, stderr.String())

	text := stdout.String()
	saved, err := os.ReadFile(filepath.Join(dir, reportName))
	require.NoError(t, err)
	assert.Equal(t, text, string(saved))

	for _, want := range []string{
		"# qc GPU validation report",
		"- **GPU**: NVIDIA L4, 570.86, 23034 MiB",
		"- **qc**: qc dev",
		"Synthetic, generated with ffmpeg lavfi",
		"## (a) NVDEC decoding",
		"| H.264 1080p (source) | 2 |",
		"| yes | 2 / 2 |",
		"| cuda-scale |",
		"## (b) CUDA VMAF",
		"| 90.0000 | 90.0010 | 0.001000 | +0.001000 | 0.001000 |",
		"mean 89.5000",
		"| --gpu |",
		"## (c) NVENC ladders vs CPU ladders",
		"| 1080p | 4000 kb/s | 80.0 | 5000 kb/s | 1.25 |",
		"| 360p | 200 kb/s | 30.0 | outside the NVENC envelope |",
		"1.26 (2 rungs)",
		"1080p CQ 21 → 6000 kb/s, 96.0",
		"## (d) NVENC CQ calibration",
		"| h264_nvenc | {21, 28, 35} | {17, 28, 38} |",
	} {
		assert.Contains(t, text, want)
	}

	assert.Contains(t, stderr.String(), "gpuval: (d) NVENC CQ calibration")
}

func TestRunFailures(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		args     []string
		fail     func(name string, args []string) bool
		wantCode int
		want     []string
	}{
		{name: "bad flag", args: []string{"-nope"}, wantCode: 2},
		{
			name:     "no content",
			fail:     func(name string, args []string) bool { return name == "ffmpeg" && has(args, "-filter_complex") },
			wantCode: 1,
		},
		{
			name:     "no geometry",
			fail:     func(name string, _ []string) bool { return name == "ffprobe" },
			wantCode: 1,
		},
		{
			name:     "an encode fails",
			fail:     func(_ string, args []string) bool { return has(args, "libx265") },
			wantCode: 1,
		},
		{
			name: "missing tools and failing steps",
			args: []string{"-source", "public.mp4", "-codecs", "av1", "-skip-calibration"},
			fail: func(name string, args []string) bool {
				return name == "nvidia-smi" || has(args, "framemd5", "-hwaccel") ||
					(has(args, "analyze", "cuda-scale")) || (has(args, "vmaf") && hasPair(args, "--vmaf-backend", "cuda")) ||
					(has(args, "ladder", "--gpu")) || has(args, "hwdownload,format=nv12")
			},
			want: []string{
				"- **GPU**: unavailable: fake failure second line",
				"Public clip `public.mp4` (1920×1080).",
				"| no hashes |",
				"no (software fallback)",
				"| cuda-scale | failed: fake failure second line |",
				"| failed: qc vmaf: fake failure second line |",
				"| av1 | ",
				"| failed: qc ladder: fake failure second line |",
			},
		},
		{
			name: "every measurement fails",
			args: []string{"-skip-ladders"},
			fail: func(name string, args []string) bool {
				return (name == "qc" && args[0] == "vmaf") || has(args, "-f", "null") || has(args, "framemd5")
			},
			want: []string{
				"| H.264 1080p (source) | failed: framemd5: fake failure second line |",
				"| none | 0.0 s | failed: qc vmaf",
				"| H.264 720p, 8-bit | failed: qc vmaf",
				"failed: `fake failure second line`",
				"| CPU | failed: qc vmaf",
				"| h264_nvenc | {21, 28, 35} | failed: qc vmaf: fake failure second line |",
			},
		},
		{
			name: "cpu ladder and bottom sweep fail",
			args: []string{"-codecs", "hevc"},
			fail: func(_ string, args []string) bool {
				return (has(args, "ladder") && !has(args, "--gpu")) || slices.ContainsFunc(args, func(a string) bool {
					return strings.Contains(a, "calib-hevc-360p")
				})
			},
			want: []string{
				"| hevc | failed: qc ladder",
				"| hevc_nvenc | {23, 30, 37} | failed: hevc_nvenc CQ 15: fake failure second line |",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			args := append([]string{"-dir", t.TempDir()}, testCase.args...)
			code := run(t.Context(), args, &stdout, &stderr, gpuMachine(testCase.fail))
			require.Equal(t, testCase.wantCode, code, stderr.String())

			for _, want := range testCase.want {
				assert.Contains(t, stdout.String(), want)
			}
		})
	}
}

func TestRunUnwritableDir(
	t *testing.T,
) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	var stdout, stderr bytes.Buffer

	assert.Equal(t, 1, run(t.Context(), []string{"-dir", filepath.Join(file, "sub")}, &stdout, &stderr, gpuMachine(nil)))
}

func TestRunReportUnwritable(
	t *testing.T,
) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, reportName), 0o750))

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), []string{"-dir", dir, "-skip-ladders", "-skip-calibration"}, &stdout, &stderr, gpuMachine(nil))
	assert.Equal(t, 1, code)
}

func TestRunCancelled(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(t.Context())

	var stdout, stderr bytes.Buffer

	fail := func(name string, args []string) bool {
		if name == "qc" && args[0] == "analyze" {
			cancel()

			return true
		}

		return false
	}

	code := run(ctx, []string{"-dir", t.TempDir()}, &stdout, &stderr, gpuMachine(fail))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "context canceled")
}

func TestLibvmafCUDAFailures(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		runner fakeRunner
		want   string
	}{
		{
			name:   "no log written",
			runner: func(string, []string) ([]byte, error) { return nil, nil },
			want:   "failed: `open",
		},
		{
			name: "invalid log",
			runner: func(_ string, args []string) ([]byte, error) {
				for _, a := range args {
					if i := strings.Index(a, "log_path="); i >= 0 {
						return nil, os.WriteFile(a[i+len("log_path="):], []byte("{"), 0o600)
					}
				}

				return nil, nil
			},
			want: "failed: `unexpected end of JSON input`",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := &validator{opts: options{dir: t.TempDir(), ffmpeg: "ffmpeg"}, run: testCase.runner, report: &report{}}

			assert.Contains(t, v.ffmpegCUDAVMAF(t.Context(), content{width: 1920, height: 1080}), testCase.want)
		})
	}
}

func TestQCVMAFErrors(
	t *testing.T,
) {
	testCases := []struct {
		name string
		out  string
	}{
		{name: "invalid json", out: "{"},
		{name: "no vmaf", out: "{}"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := &validator{opts: options{qc: "qc"}, run: fakeRunner(func(string, []string) ([]byte, error) {
				return []byte(testCase.out), nil
			})}

			_, _, err := v.qcVMAF(t.Context(), "a", "b")
			require.Error(t, err)
		})
	}
}

func TestQCLadderInvalidJSON(
	t *testing.T,
) {
	v := &validator{opts: options{qc: "qc"}, run: fakeRunner(func(string, []string) ([]byte, error) {
		return []byte("{"), nil
	})}

	_, _, err := v.qcLadder(t.Context(), "a", "h264")
	require.Error(t, err)
}

func TestGeometryInvalid(
	t *testing.T,
) {
	v := &validator{opts: options{ffprobe: "ffprobe"}, run: fakeRunner(func(string, []string) ([]byte, error) {
		return []byte("N/A"), nil
	})}

	_, _, err := v.geometry(t.Context(), "a.mp4")
	require.Error(t, err)
}

func TestDecodeRowWithoutFrames(
	t *testing.T,
) {
	v := &validator{opts: options{ffmpeg: "ffmpeg"}, run: fakeRunner(func(string, []string) ([]byte, error) {
		return []byte("#only comments\n\n"), nil
	})}

	_, err := v.decodeRow(t.Context(), clip{label: "x", path: "x.mp4"})
	require.ErrorIs(t, err, errNoFrames)
}

func TestCalibrationUnknownCodec(
	t *testing.T,
) {
	v := &validator{opts: options{codecs: []string{"vp9"}}, run: gpuMachine(nil), report: &report{}, log: &bytes.Buffer{}}

	require.ErrorIs(t, v.calibration(t.Context(), content{}), encode.ErrUnknownCodec)
}
