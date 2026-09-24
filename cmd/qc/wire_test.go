package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/nvidia"
	"github.com/eko/qc/pipeline"
)

// newTestServices builds the services of config, failing the test when the
// application cannot be built.
func newTestServices(
	t *testing.T,
	config Config,
) services {
	t.Helper()

	var svc services

	require.NoError(t, newApp(t.Context(), config, &svc).Err())

	return svc
}

// testTools locates the real tools, quietly.
var testTools = ToolsConfig{FFprobe: "ffprobe", FFmpeg: "ffmpeg", LogLevel: "warn"}

func TestModuleProvidesEveryPort(
	t *testing.T,
) {
	var (
		analyzer  *analysis.Analyzer
		inspector ladder.Inspector
		stages    pipeline.Analyzer
		encoder   ladder.Encoder
		digester  ladder.Digester
		lab       ladder.GrainLab
		builder   pipeline.LadderBuilder
		source    decode.Source
		runner    *pipeline.Runner
	)

	app := fx.New(
		fx.NopLogger,
		module(t.Context(), Config{Tools: testTools}),
		fx.Populate(&analyzer, &inspector, &stages, &encoder, &digester, &lab, &builder, &source, &runner),
	)
	require.NoError(t, app.Err())

	assert.NotNil(t, runner)
	assert.IsType(t, (*ladder.Engine)(nil), builder)
	assert.Same(t, analyzer, inspector, "one analyzer behind every port")
	assert.Same(t, analyzer, stages)
	assert.Same(t, encoder, digester, "one ffmpeg encoder behind every port")
	assert.Same(t, encoder, lab)

	reporter, ok := source.(decode.HWAccelReporter)
	require.True(t, ok)
	assert.Equal(t, decode.HWAccelNone, reporter.HWAccel(), "no GPU asked, none probed")
}

func TestModuleChecksTheGPUFirst(
	t *testing.T,
) {
	noCUDA := testutil.FakeFFmpeg(t, `printf 'Hardware acceleration methods:\n'`)
	profile := filepath.Join(t.TempDir(), "cpu.pprof")

	var svc services

	app := newApp(t.Context(), Config{
		Tools:  ToolsConfig{FFprobe: "ffprobe", FFmpeg: noCUDA, LogLevel: "warn"},
		Output: OutputConfig{CPUProfile: profile},
		GPU:    GPUConfig{HWAccel: "cuda"},
	}, &svc)

	err := app.Err()
	require.ErrorIs(t, err, nvidia.ErrMissing)
	assert.Equal(t, "--hwaccel cuda: ", err.Error()[:len("--hwaccel cuda: ")], "the preflight error is returned as is")
	assert.NoFileExists(t, profile, "nothing started")
}

func TestModuleProfilesWhileRunning(
	t *testing.T,
) {
	profile := filepath.Join(t.TempDir(), "cpu.pprof")

	var svc services

	app := newApp(t.Context(), Config{Tools: testTools, Output: OutputConfig{CPUProfile: profile}}, &svc)
	require.NoError(t, app.Err())

	require.NoError(t, app.Start(t.Context()))
	assert.FileExists(t, profile)
	require.NoError(t, app.Stop(context.WithoutCancel(t.Context())))

	info, err := os.Stat(profile)
	require.NoError(t, err)
	assert.Positive(t, info.Size(), "the profile is flushed on stop")
}

func TestEventLoggerIsQuiet(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		level    slog.Level
		wantLogs bool
	}{
		{name: "regular run", level: slog.LevelWarn},
		{name: "debug", level: slog.LevelDebug, wantLogs: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buf bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: testCase.level}))
			events := newEventLogger(logger)

			events.LogEvent(&fxevent.Invoked{FunctionName: "checkGPU", Err: nvidia.ErrDevice})
			events.LogEvent(&fxevent.Started{})

			assert.Equal(t, testCase.wantLogs, buf.Len() > 0, buf.String())
		})
	}
}

// TestProfileStartFailure stops a command before any work when its
// application cannot start.
func TestProfileStartFailure(
	t *testing.T,
) {
	source, _ := clips(t)
	profile := filepath.Join(t.TempDir(), "missing", "cpu.pprof")

	code, _, stderr := execute(t, testEnv, "analyze", source, "--fast", "--cpuprofile", profile)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "qc: start: create cpu profile")
}
