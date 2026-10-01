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
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// testEnv is a non-interactive environment.
var testEnv = environment{width: defaultWidth}

// execute runs qc with args and returns its exit code and outputs.
func execute(
	t *testing.T,
	env environment,
	args ...string,
) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	code := run(t.Context(), args, &stdout, &stderr, env)

	return code, stdout.String(), stderr.String()
}

// clips generates a tiny source and a degraded encode of it.
func clips(
	t *testing.T,
) (string, string) {
	t.Helper()

	source := testutil.Generate(t, testutil.Clip{GOP: 25, Name: "source.mp4"})
	encoded := testutil.Generate(t, testutil.Clip{GOP: 25, Name: "encode.mp4", Args: []string{"-crf", "40"}})

	return source, encoded
}

func TestCommands(
	t *testing.T,
) {
	source, encoded := clips(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.mp4")
	noDir := filepath.Join(dir, "no-such-dir", "file")
	ladderArgs := []string{"--heights", "180", "--max-rungs", "2", "--no-verify"}

	testCases := []struct {
		name       string
		args       []string
		env        map[string]string
		wantCode   int
		wantStdout []string
		wantStderr string
		wantFiles  []string
		check      func(t *testing.T, stdout string)
	}{
		{
			name:       "analyze as text",
			args:       []string{"analyze", source},
			wantStdout: []string{"320×180", "Bitrate"},
		},
		{
			name: "analyze fast as json",
			args: []string{"analyze", "--fast", "-f", "json", source},
			check: func(t *testing.T, stdout string) {
				var report analysis.Report
				require.NoError(t, json.Unmarshal([]byte(stdout), &report))
				assert.NotNil(t, report.Info)
				assert.NotNil(t, report.Bitstream)
				assert.Nil(t, report.Video)
			},
		},
		{
			name:       "analyze fast writes reports",
			args:       []string{"analyze", "--fast", "-o", filepath.Join(dir, "a.json"), "--html", filepath.Join(dir, "a.html"), source},
			wantStdout: []string{"a.json", "a.html"},
			wantFiles:  []string{filepath.Join(dir, "a.json"), filepath.Join(dir, "a.html")},
		},
		{
			name:       "analyze fast of a missing file",
			args:       []string{"analyze", "--fast", missing},
			wantCode:   1,
			wantStderr: "qc: inspect: ",
		},
		{
			name:       "analyze a missing file",
			args:       []string{"analyze", missing},
			wantCode:   1,
			wantStderr: "qc: Inspect: ",
		},
		{
			name: "vmaf sampled as json",
			args: []string{"vmaf", "-f", "json", source, encoded},
			check: func(t *testing.T, stdout string) {
				var cmp analysis.Comparison
				require.NoError(t, json.Unmarshal([]byte(stdout), &cmp))
				require.NotNil(t, cmp.VMAF)
				assert.Less(t, cmp.VMAF.Mean, 100.0)
			},
		},
		{
			name: "vmaf with a fixed budget as json",
			args: []string{"vmaf", "-f", "json", "--sample", "1/scene", source, encoded},
			check: func(t *testing.T, stdout string) {
				var cmp analysis.Comparison
				require.NoError(t, json.Unmarshal([]byte(stdout), &cmp))
				require.NotNil(t, cmp.VMAF)
				require.NotNil(t, cmp.VMAF.Sample)
				assert.Equal(t, quality.ModeSampled, cmp.VMAF.Mode)
				assert.Equal(t, 1, cmp.VMAF.Sample.PerScene)
			},
		},
		{
			name:       "vmaf with a budget and a precision",
			args:       []string{"vmaf", "--sample", "5%", "--precision", "0.25", source, encoded},
			wantCode:   1,
			wantStderr: ErrSampleConflict.Error(),
		},
		{
			name:       "vmaf exact as text",
			args:       []string{"vmaf", "--exact", source, encoded},
			wantStdout: []string{"VMAF", "reference", "distorted"},
		},
		{
			name:       "vmaf needs two files",
			args:       []string{"vmaf", source},
			wantCode:   1,
			wantStderr: "accepts 2 arg(s)",
		},
		{
			name:       "ladder verified, as text with commands",
			args:       []string{"ladder", "--commands", "--heights", "180", "--max-rungs", "2", source},
			wantStdout: []string{"180p", "ffmpeg"},
		},
		{
			name:       "ladder with an unknown codec",
			args:       []string{"ladder", "-c", "mpeg2", source},
			wantCode:   1,
			wantStderr: `unknown codec "mpeg2"`,
		},
		{
			name: "run everything as text",
			args: append([]string{
				"run", "-r", source, "--codecs", "h264",
				"-o", filepath.Join(dir, "run.json"), "--html", filepath.Join(dir, "run.html"),
				"--cpuprofile", filepath.Join(dir, "cpu.pprof"), encoded,
			}, ladderArgs...),
			wantStdout: []string{"Bitrate", "VMAF", "180p", "run.json", "run.html"},
			wantFiles:  []string{filepath.Join(dir, "run.json"), filepath.Join(dir, "run.html"), filepath.Join(dir, "cpu.pprof")},
			check: func(t *testing.T, _ string) {
				data, err := os.ReadFile(filepath.Join(dir, "run.json"))
				require.NoError(t, err)

				var report pipeline.Report
				require.NoError(t, json.Unmarshal(data, &report))
				assert.NotNil(t, report.Analysis)
				assert.NotNil(t, report.Comparison)
				require.Len(t, report.Ladders, 1)
				assert.NotEmpty(t, report.Ladders[0].Rungs)
			},
		},
		{
			name: "run vmaf only as json",
			args: []string{"run", "-r", source, "--skip-analysis", "--codecs=", "-f", "json", encoded},
			check: func(t *testing.T, stdout string) {
				var report pipeline.Report
				require.NoError(t, json.Unmarshal([]byte(stdout), &report))
				assert.NotNil(t, report.Analysis)
				assert.Nil(t, report.Analysis.Video)
				assert.NotNil(t, report.Comparison)
				assert.Empty(t, report.Ladders)
			},
		},
		{
			name:       "run with a missing reference",
			args:       []string{"run", "-r", missing, "--skip-analysis", "--codecs=", source},
			wantCode:   1,
			wantStderr: "qc: VMAF: ",
		},
		{
			name:       "vmaf with an invalid format",
			args:       []string{"vmaf", "-f", "xml", source, encoded},
			wantCode:   1,
			wantStderr: ErrInvalidFormat.Error(),
		},
		{
			name:       "vmaf of a missing file",
			args:       []string{"vmaf", source, missing},
			wantCode:   1,
			wantStderr: "qc: Inspect: ",
		},
		{
			name:       "ladder of a missing file",
			args:       []string{"ladder", missing},
			wantCode:   1,
			wantStderr: "qc: Inspect: ",
		},
		{
			name:       "run with nothing to do",
			args:       []string{"run", "--skip-analysis", "--codecs=", source},
			wantCode:   1,
			wantStderr: pipeline.ErrNothingToDo.Error(),
		},
		{
			name:       "run with an unknown codec",
			args:       []string{"run", "--codecs", "h264,vp9", source},
			wantCode:   1,
			wantStderr: `unknown codec "vp9"`,
		},
		{
			name:       "invalid format",
			args:       []string{"analyze", "-f", "xml", source},
			wantCode:   1,
			wantStderr: `invalid output format "xml"`,
		},
		{
			name:       "invalid log level",
			args:       []string{"analyze", "--log-level", "loud", source},
			wantCode:   1,
			wantStderr: `invalid log level "loud"`,
		},
		{
			name:     "config from the environment",
			args:     []string{"analyze", "--fast", source},
			env:      map[string]string{"QC_FORMAT": "json"},
			wantCode: 0,
			check: func(t *testing.T, stdout string) {
				assert.True(t, json.Valid([]byte(stdout)))
			},
		},
		{
			name:       "invalid config from the environment",
			args:       []string{"analyze", source},
			env:        map[string]string{"QC_FAST": "maybe"},
			wantCode:   1,
			wantStderr: `invalid QC_FAST: invalid argument "maybe"`,
		},
		{
			name:       "unwritable json report",
			args:       []string{"analyze", "--fast", "-o", noDir, source},
			wantCode:   1,
			wantStderr: "create " + noDir,
		},
		{
			name:       "unwritable html report",
			args:       []string{"analyze", "--fast", "--html", noDir, source},
			wantCode:   1,
			wantStderr: "create " + noDir,
		},
		{
			name:       "unwritable cpu profile",
			args:       []string{"analyze", "--fast", "--cpuprofile", noDir, source},
			wantCode:   1,
			wantStderr: "create cpu profile",
		},
		{
			name:       "help without a terminal",
			args:       nil,
			wantStdout: []string{"Usage:", "analyze", "ladder", "run", "vmaf"},
		},
		{
			name:       "unknown command",
			args:       []string{"encode"},
			wantCode:   1,
			wantStderr: `unknown command "encode"`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for key, value := range testCase.env {
				t.Setenv(key, value)
			}

			code, stdout, stderr := execute(t, testEnv, testCase.args...)

			require.Equal(t, testCase.wantCode, code, stderr)
			assert.Contains(t, stderr, testCase.wantStderr)

			for _, want := range testCase.wantStdout {
				assert.Contains(t, stdout, want)
			}

			for _, path := range testCase.wantFiles {
				info, err := os.Stat(path)
				require.NoError(t, err)
				assert.Positive(t, info.Size(), path)
			}

			if testCase.check != nil {
				testCase.check(t, stdout)
			}
		})
	}
}

func TestLoadConfigFromEnvironment(
	t *testing.T,
) {
	t.Setenv("QC_PRECISION", "0.25")
	t.Setenv("QC_CODECS", "hevc,av1")
	t.Setenv("QC_HEIGHTS", "720,360")
	t.Setenv("QC_FFMPEG", "/opt/ffmpeg")
	t.Setenv("QC_NO_VERIFY", "true")
	t.Setenv("QC_PROBING", "adaptive")
	t.Setenv("QC_DEVICES", "phone,4k")

	root := newRootCommand(testEnv)
	cmd, _, err := root.Find([]string{"run"})
	require.NoError(t, err)

	config, err := loadConfig(cmd)
	require.NoError(t, err)

	assert.InDelta(t, 0.25, config.Quality.Precision, 1e-9)
	assert.Equal(t, []string{"hevc", "av1"}, config.Run.Codecs)
	assert.Equal(t, []int{720, 360}, config.Ladder.Heights)
	assert.Equal(t, "/opt/ffmpeg", config.Tools.FFmpeg)
	assert.True(t, config.Ladder.NoVerify)
	assert.Equal(t, formatText, config.Output.Format)

	opts, err := runOptions(config, "source.mov")
	require.NoError(t, err)
	assert.Equal(t, "source.mov", opts.Source)
	assert.Equal(t, ladder.Options{
		Constraints: ladder.Constraints{TopVMAF: 95, MinVMAF: 30, Step: 6, MaxRungs: 8, MinBitrate: 145_000},
		Heights:     []int{720, 360},
		SkipVerify:  true,
		Model:       "auto",
		ModelDirs:   vmaf.DefaultModelDirs(),
		Metrics:     []string{quality.MetricXPSNR, quality.MetricCAMBI, quality.MetricPSNR},
		Devices:     []string{vmaf.DevicePhone, vmaf.Device4K},
		Parallel:    2,
		BitDepth:    8,
		Probing:     ladder.ProbingAdaptive,
		HDRMetric:   quality.HDRMetricPQ,

		DigestSampling: ladder.DigestBalanced,
	}, opts.Ladder, "the rungs get the metrics and devices of the comparison")
	assert.Equal(t, quality.Options{
		Model:     "auto",
		ModelDirs: config.Quality.ModelDir,
		Precision: 0.25,
		MaxShare:  0.4,
		Metrics:   []string{quality.MetricXPSNR, quality.MetricCAMBI, quality.MetricPSNR},
		Devices:   []string{vmaf.DevicePhone, vmaf.Device4K},
		HDRMetric: quality.HDRMetricPQ,
	}, opts.Quality)
}

func TestConfigValidate(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:   "valid",
			config: Config{Tools: ToolsConfig{LogLevel: "debug"}, Output: OutputConfig{Format: formatJSON}, Ladder: LadderConfig{Codec: "av1"}, Run: RunConfig{Codecs: []string{"h264", "HEVC"}}},
		},
		{
			name:   "flags not defined by the command",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}},
		},
		{
			name:    "log level",
			config:  Config{Tools: ToolsConfig{LogLevel: "verbose"}},
			wantErr: "invalid log level",
		},
		{
			name:    "format",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Output: OutputConfig{Format: "yaml"}},
			wantErr: ErrInvalidFormat.Error(),
		},
		{
			name:    "codec",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Codec: "vp9"}},
			wantErr: "vp9",
		},
		{
			name:    "codecs",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Run: RunConfig{Codecs: []string{"h264", "vvc"}}},
			wantErr: "vvc",
		},
		{
			name:    "probing",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Probing: "random"}},
			wantErr: ladder.ErrInvalidProbing.Error(),
		},
		{
			name:    "digest sampling",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Digest: "random"}},
			wantErr: ladder.ErrInvalidDigestSampling.Error(),
		},
		{
			name:   "sample",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}, Quality: QualityConfig{Sample: "2/scene", Precision: 0.5}},
		},
		{
			name:    "malformed sample",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Quality: QualityConfig{Sample: "5"}},
			wantErr: quality.ErrInvalidSample.Error(),
		},
		{
			name:    "sample with exact",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Quality: QualityConfig{Sample: "5%", Exact: true}},
			wantErr: ErrSampleConflict.Error() + " with --exact",
		},
		{
			name:    "sample with an explicit precision",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Quality: QualityConfig{Sample: "5%", precisionSet: true}},
			wantErr: ErrSampleConflict.Error() + " with --precision",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.validate()

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNewLogger(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{
			name:  "valid level",
			level: "info",
		},
		{
			name:    "invalid level",
			level:   "chatty",
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			logger, err := newLogger(ToolsConfig{LogLevel: testCase.level})

			if testCase.wantErr {
				require.Error(t, err)

				var svc services
				require.ErrorContains(t, newApp(t.Context(), Config{Tools: ToolsConfig{LogLevel: testCase.level}}, &svc).Err(), "invalid log level")

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, logger)
		})
	}
}

func TestDetectEnvironment(
	t *testing.T,
) {
	env := detectEnvironment()

	assert.Positive(t, env.width)
	assert.NotNil(t, env.askWizard)
	assert.NotNil(t, env.gpuAvailable)
}
