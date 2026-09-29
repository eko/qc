package main

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/pipeline"
)

var errRender = errors.New("render failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, os.ErrClosed
}

func TestEmit(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		config     Config
		renderText func(w io.Writer) error
		want       string
		wantErr    error
	}{
		{
			name:       "text",
			config:     Config{Output: OutputConfig{Format: formatText}},
			renderText: func(w io.Writer) error { _, err := io.WriteString(w, "report\n"); return err },
			want:       "report\n",
		},
		{
			name:    "json",
			config:  Config{Output: OutputConfig{Format: formatJSON}},
			want:    "{\n  \"score\": 1\n}\n",
			wantErr: nil,
		},
		{
			name:       "render error",
			config:     Config{Output: OutputConfig{Format: formatText}},
			renderText: func(io.Writer) error { return errRender },
			wantErr:    errRender,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer

			err := emit(&out, testCase.config.Output, map[string]int{"score": 1}, testCase.renderText, nil)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, out.String())
		})
	}
}

func TestEmitWrittenFiles(
	t *testing.T,
) {
	report := filepath.Join(t.TempDir(), "report.json")
	quiet := func(io.Writer) error { return nil }

	testCases := []struct {
		name    string
		output  OutputConfig
		w       io.Writer
		want    string
		wantErr error
	}{
		{name: "annotated copy", output: OutputConfig{Overlay: "a.mp4"}, w: &bytes.Buffer{}, want: "annotated video written to a.mp4"},
		{name: "report line failure", output: OutputConfig{Output: report}, w: failingWriter{}, wantErr: os.ErrClosed},
		{name: "annotated copy line failure", output: OutputConfig{Overlay: "a.mp4"}, w: failingWriter{}, wantErr: os.ErrClosed},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := emit(testCase.w, testCase.output, map[string]int{"score": 1}, quiet, nil)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Contains(t, testCase.w.(*bytes.Buffer).String(), testCase.want)
		})
	}
}

func TestWriteFile(
	t *testing.T,
) {
	dir := t.TempDir()

	testCases := []struct {
		name    string
		path    string
		write   func(w io.Writer) error
		want    string
		wantErr string
	}{
		{
			name:  "written",
			path:  filepath.Join(dir, "ok.json"),
			write: func(w io.Writer) error { return writeJSON(w, []int{1}) },
			want:  "[\n  1\n]\n",
		},
		{
			name:    "write error",
			path:    filepath.Join(dir, "bad.json"),
			write:   func(w io.Writer) error { return writeJSON(w, math.NaN()) },
			wantErr: "write " + filepath.Join(dir, "bad.json") + ": encode json report",
		},
		{
			name:    "create error",
			path:    filepath.Join(dir, "missing", "report.json"),
			write:   func(io.Writer) error { return nil },
			wantErr: "create ",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := writeFile(testCase.path, testCase.write)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)

			data, err := os.ReadFile(testCase.path)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, string(data))
		})
	}
}

func TestCPUProfile(
	t *testing.T,
) {
	dir := t.TempDir()

	first := &cpuProfile{path: filepath.Join(dir, "first.pprof")}
	require.NoError(t, first.start())

	second := &cpuProfile{path: filepath.Join(dir, "second.pprof")}
	require.ErrorContains(t, second.start(), "start cpu profile")

	require.NoError(t, first.stop())
	require.ErrorContains(t, first.stop(), "close cpu profile")

	missing := &cpuProfile{path: filepath.Join(dir, "missing", "cpu.pprof")}
	require.ErrorContains(t, missing.start(), "create cpu profile")
}

func TestRenderRunError(
	t *testing.T,
) {
	clip := testutil.Generate(t, testutil.Clip{})

	svc := newTestServices(t, Config{Tools: ToolsConfig{LogLevel: "warn", FFprobe: "ffprobe", FFmpeg: "ffmpeg"}})

	report, err := svc.analyzer.Analyze(t.Context(), clip, analysis.Options{SkipVideo: true})
	require.NoError(t, err)

	err = renderRun(failingWriter{}, &pipeline.Report{Analysis: report}, defaultWidth, false)
	require.Error(t, err)
}

func TestLadderStageLabel(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		stage string
		want  string
	}{
		{
			name:  "digest",
			stage: ladder.StageDigest,
			want:  "digest",
		},
		{
			name:  "probes",
			stage: ladder.StageProbe,
			want:  "probe encodes",
		},
		{
			name:  "verification",
			stage: ladder.StageVerify,
			want:  "verification",
		},
		{
			name:  "per-shot",
			stage: ladder.StageShots,
			want:  "per-shot",
		},
		{
			name:  "film grain",
			stage: ladder.StageGrain,
			want:  "film grain",
		},
		{
			name:  "anchoring",
			stage: ladder.StageAnchor,
			want:  "anchoring",
		},
		{
			name:  "unknown",
			stage: "other",
			want:  "",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, ladderStageLabel(testCase.stage))
		})
	}
}
