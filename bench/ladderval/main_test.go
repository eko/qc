package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf/libvmaf"
)

var errMeasure = errors.New("measure failed")

// writeJSON writes v in a temporary file and returns its path.
func writeJSON(
	t *testing.T,
	v any,
) string {
	t.Helper()

	data, err := json.Marshal(v)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "ladder.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	return path
}

// codecOf returns the CPU codec name, which the tests know to exist.
func codecOf(
	name string,
) encode.Codec {
	codec, err := encode.Lookup(name)
	if err != nil {
		panic(err)
	}

	return codec
}

// fakeLadder is a two-rung ladder of a 25 fps source.
func fakeLadder() *ladder.Result {
	return &ladder.Result{
		Source: &analysis.Report{Info: &media.Info{
			Path:  "source.mp4",
			Video: []media.VideoStream{{Width: 1280, Height: 720, AvgFrameRate: media.Rational{Num: 25, Den: 1}}},
		}},
		Codec: codecOf("h264"),
		Probes: []ladder.Probe{
			{Width: 1280, Height: 720, CRF: 20},
			{Width: 1280, Height: 720, CRF: 27},
			{Width: 640, Height: 360, CRF: 20},
		},
		Rungs: []ladder.Rung{
			{Width: 1280, Height: 720, CRF: 22, Bitrate: 3_000_000, PredictedVMAF: 94, Measured: &ladder.Measurement{VMAF: 93.5}},
			{Width: 640, Height: 360, CRF: 26, Bitrate: 800_000, PredictedVMAF: 80},
		},
	}
}

// fakeMeasure models quality as a function of CRF and resolution.
func fakeMeasure(
	p encode.Params,
) (ladder.Probe, error) {
	bitrate := int64(float64(p.Width*p.Height) * 10 * math.Exp(-(p.CRF-20)/6))

	return ladder.Probe{
		Width:   p.Width,
		Height:  p.Height,
		CRF:     p.CRF,
		Bitrate: bitrate,
		VMAF:    100 - p.CRF - 200/float64(p.Height)*10,
	}, nil
}

// failAfter returns a measure failing after n successful calls.
func failAfter(
	n int,
) measureFunc {
	return func(p encode.Params) (ladder.Probe, error) {
		if n == 0 {
			return ladder.Probe{}, errMeasure
		}

		n--

		return fakeMeasure(p)
	}
}

func TestParseArgs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		args    []string
		want    options
		wantErr string
	}{
		{
			name: "defaults",
			args: []string{"ladder.json"},
			want: options{path: "ladder.json", crfStep: 3, shotSpan: 6, shotStep: 1, ffmpeg: "ffmpeg", ffprobe: "ffprobe"},
		},
		{
			name: "every flag",
			args: []string{
				"-crf-step", "2", "-rungs-only", "-precision", "0.25", "-cache", "c.json", "-per-shot", "-shot-rungs", "3", "-shot-optimum", "2",
				"-shot-span", "4", "-shot-step", "0.5", "-ffmpeg", "ff", "-ffprobe", "fp", "l.json",
			},
			want: options{
				path: "l.json", crfStep: 2, rungsOnly: true, precision: 0.25, cache: "c.json", perShot: true, shotRungs: 3, optimum: 2,
				shotSpan: 4, shotStep: 0.5, ffmpeg: "ff", ffprobe: "fp",
			},
		},
		{
			name:    "no file",
			args:    nil,
			wantErr: "expected exactly one ladder.json",
		},
		{
			name:    "zero crf step",
			args:    []string{"-crf-step", "0", "ladder.json"},
			wantErr: "-crf-step must be positive",
		},
		{
			name:    "negative precision",
			args:    []string{"-precision", "-1", "ladder.json"},
			wantErr: "-precision must not be negative",
		},
		{
			name:    "zero shot step",
			args:    []string{"-shot-step", "0", "ladder.json"},
			wantErr: "-shot-step must be positive",
		},
		{
			name:    "unknown flag",
			args:    []string{"-nope", "ladder.json"},
			wantErr: "flag provided but not defined",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var stderr bytes.Buffer

			got, err := parseArgs(testCase.args, &stderr)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				assert.Contains(t, stderr.String(), "usage: ladderval")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestReadLadder(
	t *testing.T,
) {
	noRungs := fakeLadder()
	noRungs.Rungs = nil

	noVideo := fakeLadder()
	noVideo.Source.Info.Video = nil

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte("["), 0o600))

	testCases := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name: "ladder",
			path: writeJSON(t, fakeLadder()),
		},
		{
			name:    "not a ladder",
			path:    writeJSON(t, analysis.Comparison{}),
			wantErr: ErrNotLadder.Error(),
		},
		{
			name:    "no rungs",
			path:    writeJSON(t, noRungs),
			wantErr: ErrNotLadder.Error(),
		},
		{
			name:    "no video stream",
			path:    writeJSON(t, noVideo),
			wantErr: "no video stream",
		},
		{
			name:    "invalid json",
			path:    invalid,
			wantErr: "decode ",
		},
		{
			name:    "missing file",
			path:    filepath.Join(t.TempDir(), "missing.json"),
			wantErr: "read ",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := readLadder(testCase.path)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Len(t, got.Rungs, 2)
		})
	}
}

func TestGridCRFs(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		codec string
		step  float64
		want  []float64
	}{
		{
			name:  "h264 every 3",
			codec: "h264",
			step:  3,
			want:  []float64{17, 20, 23, 26, 29, 32, 35},
		},
		{
			name:  "clamped to the codec range",
			codec: "h264",
			step:  15,
			want:  []float64{20, 35},
		},
		{
			name:  "av1",
			codec: "av1",
			step:  12,
			want:  []float64{16, 28, 40, 52},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := encode.Lookup(testCase.codec)
			require.NoError(t, err)

			assert.Equal(t, testCase.want, gridCRFs(codec, testCase.step))
		})
	}
}

func TestExhaustiveEnvelope(
	t *testing.T,
) {
	noProbes := fakeLadder()
	noProbes.Probes = nil

	testCases := []struct {
		name      string
		fast      *ladder.Result
		measure   measureFunc
		wantCalls int
		wantErr   error
		wantLog   string
	}{
		{
			name:    "one curve per probed height",
			fast:    fakeLadder(),
			measure: fakeMeasure,
			wantLog: "grid 360p crf 30",
		},
		{
			name:    "measure error",
			fast:    fakeLadder(),
			measure: failAfter(1),
			wantErr: errMeasure,
		},
		{
			name:    "no probes",
			fast:    noProbes,
			measure: fakeMeasure,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var log bytes.Buffer

			hull, err := exhaustiveEnvelope(&log, testCase.fast, []float64{20, 25, 30}, testCase.measure)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			if testCase.wantLog == "" {
				require.ErrorContains(t, err, "empty envelope")

				return
			}

			require.NoError(t, err)
			assert.Len(t, hull, envelopePoints)
			assert.Contains(t, log.String(), testCase.wantLog)
			assert.Contains(t, log.String(), "grid 720p crf 20")
		})
	}
}

func TestCheckOptimum(
	t *testing.T,
) {
	var log bytes.Buffer

	hull, err := exhaustiveEnvelope(&log, fakeLadder(), []float64{16, 20, 24, 28, 32}, fakeMeasure)
	require.NoError(t, err)

	testCases := []struct {
		name    string
		measure measureFunc
		writer  io.Writer
		want    []string
		wantErr string
	}{
		{
			name:    "rungs on the envelope",
			measure: fakeMeasure,
			want:    []string{"optimum VMAF", "720p", "360p", "mean ΔVMAF to the optimum: "},
		},
		{
			name:    "measure error",
			measure: failAfter(1),
			wantErr: errMeasure.Error(),
		},
		{
			name:    "write error",
			measure: fakeMeasure,
			writer:  failingWriter{},
			wantErr: "write table",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer

			w := testCase.writer
			if w == nil {
				w = &out
			}

			err := checkOptimum(w, fakeLadder(), hull, testCase.measure)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)

			for _, want := range testCase.want {
				assert.Contains(t, out.String(), want)
			}

			assert.NotContains(t, out.String(), "NaN")
		})
	}
}

func TestCheckRungs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		measure measureFunc
		writer  io.Writer
		want    []string
		wantErr string
	}{
		{
			name:    "prediction against the full title",
			measure: fakeMeasure,
			want:    []string{"digest VMAF", "93.50", "NaN", "mean |predicted − full title| VMAF: "},
		},
		{
			name:    "measure error",
			measure: failAfter(0),
			wantErr: errMeasure.Error(),
		},
		{
			name:    "write error",
			measure: fakeMeasure,
			writer:  failingWriter{},
			wantErr: "write table",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer

			w := testCase.writer
			if w == nil {
				w = &out
			}

			err := checkRungs(w, fakeLadder(), testCase.measure)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)

			for _, want := range testCase.want {
				assert.Contains(t, out.String(), want)
			}
		})
	}
}

func TestHullAt(
	t *testing.T,
) {
	hull := []ladder.HullPoint{
		{Bitrate: 100_000, VMAF: 40, Height: 360},
		{Bitrate: 400_000, VMAF: 60, Height: 540},
		{Bitrate: 1_600_000, VMAF: 90, Height: 1080},
	}

	testCases := []struct {
		name       string
		bitrate    float64
		wantVMAF   float64
		wantHeight int
	}{
		{
			name:       "below the envelope",
			bitrate:    50_000,
			wantVMAF:   40,
			wantHeight: 360,
		},
		{
			name:       "log interpolation",
			bitrate:    200_000,
			wantVMAF:   50,
			wantHeight: 540,
		},
		{
			name:       "on a point",
			bitrate:    1_600_000,
			wantVMAF:   90,
			wantHeight: 1080,
		},
		{
			name:       "above the envelope",
			bitrate:    5_000_000,
			wantVMAF:   90,
			wantHeight: 1080,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			vmaf, height := hullAt(hull, testCase.bitrate)

			assert.InDelta(t, testCase.wantVMAF, vmaf, 1e-9)
			assert.Equal(t, testCase.wantHeight, height)
		})
	}
}

func TestBitrateFor(
	t *testing.T,
) {
	hull := []ladder.HullPoint{
		{Bitrate: 100_000, VMAF: 40},
		{Bitrate: 400_000, VMAF: 60},
	}

	testCases := []struct {
		name string
		vmaf float64
		want float64
	}{
		{
			name: "below the envelope",
			vmaf: 30,
			want: 100_000,
		},
		{
			name: "log interpolation",
			vmaf: 50,
			want: 200_000,
		},
		{
			name: "out of reach",
			vmaf: 99,
			want: 400_000,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, bitrateFor(hull, testCase.vmaf), 1e-6)
		})
	}
}

func TestMean(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		want   float64
	}{
		{
			name:   "values",
			values: []float64{1, 2, 6},
			want:   3,
		},
		{
			name:   "empty",
			values: nil,
			want:   math.NaN(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := mean(testCase.values)

			if math.IsNaN(testCase.want) {
				assert.True(t, math.IsNaN(got))

				return
			}

			assert.InDelta(t, testCase.want, got, 1e-9)
		})
	}
}

// realLadder writes a one-rung ladder of a tiny generated clip.
func realLadder(
	t *testing.T,
) string {
	t.Helper()

	return realLadderWith(t, testutil.Clip{GOP: 50}, "h264", nil)
}

// realLadderWith writes a one-rung ladder of a real clip of codec, changed
// by mutate when set.
func realLadderWith(
	t *testing.T,
	c testutil.Clip,
	codec string,
	mutate func(*ladder.Result),
) string {
	t.Helper()

	clip := testutil.Generate(t, c)

	dec := decode.NewFFmpeg("ffmpeg", 0)
	analyzer := analysis.New(slog.New(slog.DiscardHandler),
		probe.NewFFprobe("ffprobe"), bitstream.NewFFprobeReader("ffprobe"), dec, quality.NewMeter(dec, libvmaf.NewEngine()))

	source, err := analyzer.Analyze(t.Context(), clip, analysis.Options{SkipVideo: true})
	require.NoError(t, err)

	res := ladder.Result{
		Source: source,
		Codec:  codecOf(codec),
		Preset: map[string]string{"h264": "ultrafast", "av1": "12"}[codec],
		Probes: []ladder.Probe{{Width: 320, Height: 180, CRF: 27}},
		Rungs:  []ladder.Rung{{Width: 320, Height: 180, CRF: 30, PredictedVMAF: 80}},
	}

	if mutate != nil {
		mutate(&res)
	}

	return writeJSON(t, res)
}

func TestRun(
	t *testing.T,
) {
	path := realLadder(t)

	badCodec := fakeLadder()
	badCodec.Codec.Name = "mpeg2"

	testCases := []struct {
		name       string
		args       []string
		tmpDir     string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "exhaustive optimum",
			args:       []string{"-crf-step", "20", path},
			wantCode:   0,
			wantStdout: "mean ΔVMAF to the optimum",
			wantStderr: "grid 180p crf 20",
		},
		{
			name:       "rungs only, sampled",
			args:       []string{"-rungs-only", "-precision", "1", path},
			wantCode:   0,
			wantStdout: "mean |predicted − full title| VMAF",
		},
		{
			name:       "encode error",
			args:       []string{"-rungs-only", "-ffmpeg", filepath.Join(t.TempDir(), "no-ffmpeg"), path},
			wantCode:   1,
			wantStderr: "encode 180p crf 30",
		},
		{
			name:       "grid error",
			args:       []string{"-ffmpeg", filepath.Join(t.TempDir(), "no-ffmpeg"), path},
			wantCode:   1,
			wantStderr: "encode 180p crf 17",
		},
		{
			name:       "no temporary directory",
			args:       []string{path},
			tmpDir:     filepath.Join(t.TempDir(), "missing"),
			wantCode:   1,
			wantStderr: "temp dir",
		},
		{
			name:       "measure error",
			args:       []string{"-rungs-only", "-ffprobe", filepath.Join(t.TempDir(), "no-ffprobe"), path},
			wantCode:   1,
			wantStderr: "measure 180p crf 30",
		},
		{
			name:       "unknown codec",
			args:       []string{writeJSON(t, badCodec)},
			wantCode:   1,
			wantStderr: "mpeg2",
		},
		{
			name:       "not a ladder",
			args:       []string{writeJSON(t, analysis.Comparison{})},
			wantCode:   1,
			wantStderr: ErrNotLadder.Error(),
		},
		{
			name:       "usage",
			args:       nil,
			wantCode:   2,
			wantStderr: "usage: ladderval",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.tmpDir != "" {
				t.Setenv("TMPDIR", testCase.tmpDir)
			}

			var stdout, stderr bytes.Buffer

			code := run(t.Context(), testCase.args, &stdout, &stderr)

			assert.Equal(t, testCase.wantCode, code, stderr.String())
			assert.Contains(t, stdout.String(), testCase.wantStdout)
			assert.Contains(t, stderr.String(), testCase.wantStderr)
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrClosed
}
