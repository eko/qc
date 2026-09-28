package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfigFile writes content to a configuration file named name.
func writeConfigFile(
	t *testing.T,
	name, content string,
) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// loadCommandConfig parses args for the command they name and loads its
// configuration.
func loadCommandConfig(
	t *testing.T,
	args ...string,
) (Config, error) {
	t.Helper()

	root := newRootCommand(testEnv)

	cmd, flags, err := root.Find(args)
	require.NoError(t, err)
	require.NoError(t, cmd.ParseFlags(flags))

	return loadConfig(cmd)
}

func TestLoadConfigFile(
	t *testing.T,
) {
	yaml := writeConfigFile(t, "qc.yaml", `
ffmpeg: /opt/ffmpeg/bin/ffmpeg
precision: 0.25
codecs: [hevc, av1]
heights: [1080, 720]
no-verify: true
bitrate-interval: 2s
max-bitrate: 6000000
# keys of other commands are left to them
fast: true
loudness-target: atsc
silence-duration: 5s
`)
	toml := writeConfigFile(t, "qc.toml", `
precision = 0.3
codecs = ["h264"]
max-bitrate = 4000000
`)
	json := writeConfigFile(t, "qc.json", `{"precision": 0.4, "max-bitrate": 5000000, "model-dir": ["/models, shared", "/more"]}`)

	testCases := []struct {
		name  string
		args  []string
		env   map[string]string
		check func(t *testing.T, config Config)
	}{
		{
			name: "yaml",
			args: []string{"run", "--config", yaml},
			check: func(t *testing.T, config Config) {
				assert.Equal(t, "/opt/ffmpeg/bin/ffmpeg", config.Tools.FFmpeg)
				assert.InDelta(t, 0.25, config.Quality.Precision, 1e-9)
				assert.Equal(t, []string{"hevc", "av1"}, config.Run.Codecs)
				assert.Equal(t, []int{1080, 720}, config.Ladder.Heights)
				assert.True(t, config.Ladder.NoVerify)
				assert.Equal(t, int64(6_000_000), config.Ladder.MaxBitrate)
				assert.False(t, config.Analysis.Fast, "run has no --fast")
				assert.True(t, config.Quality.precisionSet, "a precision in the file is given")
			},
		},
		{
			name: "yaml for another command",
			args: []string{"analyze", "--config", yaml},
			check: func(t *testing.T, config Config) {
				assert.True(t, config.Analysis.Fast)
				assert.Equal(t, 2*time.Second, config.Analysis.BitrateInterval)
				assert.Equal(t, "atsc", config.Analysis.LoudnessTarget)
				assert.Equal(t, 5*time.Second, config.Analysis.SilenceDuration)
				assert.Empty(t, config.Run.Codecs, "analyze has no --codecs")
			},
		},
		{
			name: "toml",
			args: []string{"run", "--config", toml},
			check: func(t *testing.T, config Config) {
				assert.InDelta(t, 0.3, config.Quality.Precision, 1e-9)
				assert.Equal(t, []string{"h264"}, config.Run.Codecs)
				assert.Equal(t, int64(4_000_000), config.Ladder.MaxBitrate)
			},
		},
		{
			name: "json, integers as floats and quoted list items",
			args: []string{"vmaf", "--config", json},
			check: func(t *testing.T, config Config) {
				assert.InDelta(t, 0.4, config.Quality.Precision, 1e-9)
				assert.Equal(t, []string{"/models, shared", "/more"}, config.Quality.ModelDir)
			},
		},
		{
			name: "file from QC_CONFIG",
			args: []string{"run"},
			env:  map[string]string{"QC_CONFIG": toml},
			check: func(t *testing.T, config Config) {
				assert.InDelta(t, 0.3, config.Quality.Precision, 1e-9)
			},
		},
		{
			name: "flags win over the environment, which wins over the file",
			args: []string{"run", "--config", yaml, "--precision", "1"},
			env:  map[string]string{"QC_CODECS": "h264", "QC_PRECISION": "2"},
			check: func(t *testing.T, config Config) {
				assert.InDelta(t, 1.0, config.Quality.Precision, 1e-9)
				assert.Equal(t, []string{"h264"}, config.Run.Codecs)
				assert.Equal(t, []int{1080, 720}, config.Ladder.Heights)
				assert.InDelta(t, 95.0, config.Ladder.TopVMAF, 1e-9, "defaults fill the rest")
			},
		},
		{
			name: "no file",
			args: []string{"run"},
			check: func(t *testing.T, config Config) {
				assert.InDelta(t, 0.5, config.Quality.Precision, 1e-9)
				assert.False(t, config.Quality.precisionSet)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for key, value := range testCase.env {
				t.Setenv(key, value)
			}

			config, err := loadCommandConfig(t, testCase.args...)
			require.NoError(t, err)

			testCase.check(t, config)
		})
	}
}

func TestLoadConfigFileErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		file    string
		content string
		wantErr string
	}{
		{name: "missing file", file: "", wantErr: "no such file"},
		{name: "unsupported format", file: "qc.xml", content: "<qc/>", wantErr: "Unsupported Config Type"},
		{name: "malformed", file: "qc.yaml", content: "precision: [", wantErr: ErrConfigFile.Error()},
		{name: "unknown key", file: "qc.yaml", content: "precison: 0.25", wantErr: `unknown key "precison"`},
		{name: "section instead of keys", file: "qc.yaml", content: "quality:\n  precision: 0.25", wantErr: `unknown key "quality"`},
		{name: "invalid value", file: "qc.yaml", content: "precision: high", wantErr: "invalid precision"},
		{name: "table value", file: "qc.toml", content: "[codecs]\nh264 = true", wantErr: ErrConfigValue.Error()},
		{name: "table in a list", file: "qc.json", content: `{"codecs": [{"h264": true}]}`, wantErr: ErrConfigValue.Error()},
		{name: "invalid value in a valid type", file: "qc.yaml", content: "codecs: [vp9]", wantErr: "vp9"},
		{name: "conflict found by validation", file: "qc.yaml", content: "sample: 5%\nexact: true", wantErr: ErrSampleConflict.Error()},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing.yaml")
			if testCase.file != "" {
				path = writeConfigFile(t, testCase.file, testCase.content)
			}

			_, err := loadCommandConfig(t, "run", "--config", path)
			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

// TestConfigFileRuns checks a configuration file end to end: the values of
// the file reach the command.
func TestConfigFileRuns(
	t *testing.T,
) {
	source, _ := clips(t)
	config := writeConfigFile(t, "qc.yaml", "fast: true\nformat: json\n")

	code, stdout, stderr := execute(t, testEnv, "analyze", source, "--config", config)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, `"schemaVersion"`)
}
