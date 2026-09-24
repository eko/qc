package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// writeComparison writes cmp as JSON in a temporary file and returns its path.
func writeComparison(
	t *testing.T,
	cmp any,
) string {
	t.Helper()

	data, err := json.Marshal(cmp)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "exact.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	return path
}

// exactComparison is an exact measurement of frames at 25 fps with a
// keyframe every second.
func exactComparison(
	frames int,
) analysis.Comparison {
	res := &quality.Result{Mode: quality.ModeExact, BitDepth: 8, Model: vmaf.ModelSpec{Width: 1920, Height: 1080}}
	keyframes := []media.Duration{}

	for i := range frames {
		pts := media.Seconds(float64(i) / 25)
		metrics := map[string]float64{
			quality.SeriesPSNRY:  40 + 2*math.Cos(float64(i)/5),
			quality.SeriesXPSNRY: 36 + math.Sin(float64(i)/3),
		}
		res.Frames = append(res.Frames, quality.FrameScore{Index: i, PTS: pts, Score: 80 + 10*math.Sin(float64(i)/7), Metrics: metrics})

		if i%25 == 0 {
			keyframes = append(keyframes, pts)
		}
	}

	return analysis.Comparison{
		VMAF:      res,
		Distorted: &analysis.Report{Bitstream: &bitstream.Report{Keyframes: keyframes}},
	}
}

// writeAnalysis writes a technical analysis report with shots starting at
// the given seconds and returns its path.
func writeAnalysis(
	t *testing.T,
	starts ...float64,
) string {
	t.Helper()

	report := analysis.Report{Video: &analysis.VideoReport{}}

	for _, start := range starts {
		var shot analysis.ShotReport
		shot.Start = media.Seconds(start)
		report.Video.Shots = append(report.Video.Shots, shot)
	}

	return writeComparison(t, report)
}

func TestRun(
	t *testing.T,
) {
	exact := writeComparison(t, exactComparison(500))
	shots := writeAnalysis(t, 0, 3.3, 11)

	testCases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr string
	}{
		{
			name:       "simulates every precision",
			args:       []string{"-runs", "5", "-precisions", "0.5, 1", exact},
			wantCode:   0,
			wantStdout: []string{"coverage", "±0.50", "±1.00", "500"},
		},
		{
			name:       "simulates series",
			args:       []string{"-runs", "5", "-precisions", "0.5", "-series", "psnr_y, xpsnr_y", exact},
			wantCode:   0,
			wantStdout: []string{"series", "psnr_y", "xpsnr_y"},
		},
		{
			name:       "simulates fixed budgets only",
			args:       []string{"-runs", "5", "-precisions=", "-samples", "5%, 1/scene", exact},
			wantCode:   0,
			wantStdout: []string{"mean |err|", "5%", "1/scene"},
		},
		{
			name:       "fixed budgets on shots",
			args:       []string{"-runs", "5", "-precisions=", "-samples", "2/scene", "-shots", shots, exact},
			wantCode:   0,
			wantStdout: []string{"2/scene"},
		},
		{
			name:       "one analysis per measurement",
			args:       []string{"-runs", "5", "-samples", "2/scene", "-shots", shots + "," + shots, exact},
			wantCode:   2,
			wantStderr: ErrShotsCount.Error(),
		},
		{
			name:       "analysis without shots",
			args:       []string{"-runs", "5", "-samples", "2/scene", "-shots", writeAnalysis(t), exact},
			wantCode:   1,
			wantStderr: ErrNoShots.Error(),
		},
		{
			name:       "invalid budget",
			args:       []string{"-samples", "5", exact},
			wantCode:   2,
			wantStderr: "invalid budget",
		},
		{
			name:       "nothing to simulate",
			args:       []string{"-precisions=", exact},
			wantCode:   2,
			wantStderr: ErrNoConfig.Error(),
		},
		{
			name:       "unknown series",
			args:       []string{"-runs", "5", "-series", "ssim", exact},
			wantCode:   1,
			wantStderr: ErrNoSeries.Error(),
		},
		{
			name:       "no file",
			args:       []string{"-runs", "5"},
			wantCode:   2,
			wantStderr: "usage: vmafsim",
		},
		{
			name:       "unknown flag",
			args:       []string{"-nope", exact},
			wantCode:   2,
			wantStderr: "flag provided but not defined",
		},
		{
			name:       "invalid precision",
			args:       []string{"-precisions", "0.5,abc", exact},
			wantCode:   2,
			wantStderr: `invalid precision "abc"`,
		},
		{
			name:       "invalid runs",
			args:       []string{"-runs", "0", exact},
			wantCode:   2,
			wantStderr: "-runs must be positive",
		},
		{
			name:       "missing file",
			args:       []string{filepath.Join(t.TempDir(), "missing.json")},
			wantCode:   1,
			wantStderr: "read ",
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

			assert.Contains(t, stderr.String(), testCase.wantStderr)
		})
	}
}

func TestReadMeasurement(
	t *testing.T,
) {
	noDistorted := exactComparison(10)
	noDistorted.Distorted = nil

	sampled := exactComparison(10)
	sampled.VMAF.Mode = quality.ModeSampled

	empty := exactComparison(0)

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte("{"), 0o600))

	testCases := []struct {
		name          string
		path          string
		series        []string
		wantFrames    int
		wantKeyframes int
		wantSeries    []string
		wantErr       string
	}{
		{
			name:          "exact measurement",
			path:          writeComparison(t, exactComparison(60)),
			wantFrames:    60,
			wantKeyframes: 3,
		},
		{
			name:          "every series",
			path:          writeComparison(t, exactComparison(60)),
			series:        []string{"all"},
			wantFrames:    60,
			wantKeyframes: 3,
			wantSeries:    []string{quality.SeriesPSNRY, quality.SeriesXPSNRY},
		},
		{
			name:    "missing series",
			path:    writeComparison(t, exactComparison(60)),
			series:  []string{quality.SeriesCAMBI},
			wantErr: ErrNoSeries.Error(),
		},
		{
			name:       "without distorted report",
			path:       writeComparison(t, noDistorted),
			wantFrames: 10,
		},
		{
			name:    "sampled measurement",
			path:    writeComparison(t, sampled),
			wantErr: ErrNotExact.Error(),
		},
		{
			name:    "no frames",
			path:    writeComparison(t, empty),
			wantErr: ErrNotExact.Error(),
		},
		{
			name:    "no vmaf",
			path:    writeComparison(t, analysis.Comparison{}),
			wantErr: ErrNotExact.Error(),
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
			m, err := readMeasurement(testCase.path, testCase.series)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Len(t, m.scores, testCase.wantFrames)
			assert.Len(t, m.pts, testCase.wantFrames)
			assert.Len(t, m.keyframes, testCase.wantKeyframes)
			assert.Len(t, m.series, len(testCase.wantSeries))

			for _, name := range testCase.wantSeries {
				assert.Len(t, m.series[name], testCase.wantFrames)
			}
		})
	}
}

func TestReadCuts(
	t *testing.T,
) {
	invalid := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte("{"), 0o600))

	testCases := []struct {
		name    string
		path    string
		want    []media.Duration
		wantErr string
	}{
		{
			name: "every shot start but the first",
			path: writeAnalysis(t, 0, 2.5, 7),
			want: []media.Duration{media.Seconds(2.5), media.Seconds(7)},
		},
		{
			name: "a single shot has no cut",
			path: writeAnalysis(t, 0),
			want: []media.Duration{},
		},
		{
			name:    "no frame analysis",
			path:    writeComparison(t, analysis.Report{}),
			wantErr: ErrNoShots.Error(),
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
			got, err := readCuts(testCase.path)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestSplitList(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "empty", input: "", want: nil},
		{name: "list with spaces and empty fields", input: " a, ,b ,", want: []string{"a", "b"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, splitList(testCase.input))
		})
	}
}

func TestSimulateAllWriteError(
	t *testing.T,
) {
	path := writeComparison(t, exactComparison(100))

	err := simulateAll(failingWriter{}, []string{path}, nil, 2, []config{{label: "±1.00", opts: quality.Options{Precision: 1}}}, nil)

	require.ErrorContains(t, err, "write table")
}

func TestShortName(
	t *testing.T,
) {
	testCases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "short path kept",
			path: "exact.json",
			want: "exact.json",
		},
		{
			name: "long path truncated",
			path: "/data/measurements/drama/exact-720p.json",
			want: "…ments/drama/exact-720p.json",
		},
		{
			name: "multi-byte characters kept whole",
			path: strings.Repeat("é", 30),
			want: "…" + strings.Repeat("é", 27),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, shortName(testCase.path))
		})
	}
}

func TestParseFloats(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    []float64
		wantErr bool
	}{
		{
			name:  "list",
			input: "0.25,0.5,1",
			want:  []float64{0.25, 0.5, 1},
		},
		{
			name:  "spaces",
			input: " 0.5 , 2",
			want:  []float64{0.5, 2},
		},
		{
			name:    "not a number",
			input:   "0.5,abc",
			wantErr: true,
		},
		{
			name:    "zero",
			input:   "0",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := parseFloats(testCase.input)

			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrClosed
}
