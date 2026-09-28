package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/pipeline"
)

func TestParseAudioTracks(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		input       string
		wantDefault bool
		wantTracks  []int
		wantErr     bool
	}{
		{name: "empty", input: ""},
		{name: "all", input: " ALL "},
		{name: "default", input: "default", wantDefault: true},
		{name: "numbers", input: "0, 2", wantTracks: []int{0, 2}},
		{name: "negative", input: "-1", wantErr: true},
		{name: "not a number", input: "0,first", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			defaultTrack, tracks, err := parseAudioTracks(testCase.input)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidAudioTracks)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantDefault, defaultTrack)
			assert.Equal(t, testCase.wantTracks, tracks)
		})
	}
}

func TestAudioOptions(
	t *testing.T,
) {
	got := audioOptions(AnalysisConfig{
		Audio: true, NoAudio: true, LoudnessTarget: "atsc", AudioTracks: "1",
		SilenceThreshold: -50, SilenceDuration: 3 * time.Second,
	})

	atsc, err := loudness.ParseTarget("atsc")
	require.NoError(t, err)
	assert.Equal(t, analysis.AudioOptions{
		Skip: true, WithInspection: true, Tracks: []int{1}, Target: atsc,
		Defect: defect.Options{SilenceThreshold: -50, SilenceDuration: media.Seconds(3)},
	}, got)
}

func TestValidateAudio(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		config  AnalysisConfig
		wantErr error
	}{
		{name: "valid", config: AnalysisConfig{LoudnessTarget: "-16", AudioTracks: "default", SilenceThreshold: -70}},
		{name: "target", config: AnalysisConfig{LoudnessTarget: "loud"}, wantErr: loudness.ErrInvalidTarget},
		{name: "tracks", config: AnalysisConfig{AudioTracks: "x"}, wantErr: ErrInvalidAudioTracks},
		{name: "threshold", config: AnalysisConfig{SilenceThreshold: 6}, wantErr: ErrInvalidSilenceThreshold},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := Config{Tools: ToolsConfig{LogLevel: "warn"}, Analysis: testCase.config}.validate()
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestAudioCommands(
	t *testing.T,
) {
	source := testutil.Generate(t, testutil.Clip{GOP: 25, Seconds: 3, Audio: true, Name: "audio.mp4"})

	report := func(t *testing.T, stdout string) *analysis.Report {
		t.Helper()

		var r analysis.Report
		require.NoError(t, json.Unmarshal([]byte(stdout), &r))

		return &r
	}

	testCases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr string
		check      func(t *testing.T, stdout string)
	}{
		{
			name:       "analyze as text",
			args:       []string{"analyze", "--no-motion", source},
			wantStdout: []string{"Audio", "target ebu -23 LUFS ±0.5", "#1  aac mono 48 kHz", "LUFS"},
		},
		{
			name: "fast without audio",
			args: []string{"analyze", "--fast", "-f", "json", source},
			check: func(t *testing.T, stdout string) {
				assert.Nil(t, report(t, stdout).Audio)
			},
		},
		{
			name: "fast with audio, another target",
			args: []string{"analyze", "--fast", "--audio", "--loudness-target", "streaming", "-f", "json", source},
			check: func(t *testing.T, stdout string) {
				r := report(t, stdout)
				assert.Nil(t, r.Video)
				require.NotNil(t, r.Audio)
				assert.Equal(t, loudness.TargetStreaming, r.Audio.Target.Name)
				assert.Len(t, r.Audio.Tracks, 1)
			},
		},
		{
			name: "no audio",
			args: []string{"analyze", "--no-audio", "--no-motion", "-f", "json", source},
			check: func(t *testing.T, stdout string) {
				assert.Nil(t, report(t, stdout).Audio)
			},
		},
		{
			name: "run with the default track",
			args: []string{"run", "--codecs=", "--audio-tracks", "default", "-f", "json", source},
			check: func(t *testing.T, stdout string) {
				var r pipeline.Report
				require.NoError(t, json.Unmarshal([]byte(stdout), &r))
				require.NotNil(t, r.Analysis.Audio)
				assert.Len(t, r.Analysis.Audio.Tracks, 1)
			},
		},
		{
			name:       "invalid target",
			args:       []string{"analyze", "--loudness-target", "loud", source},
			wantCode:   1,
			wantStderr: "invalid --loudness-target",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			code, stdout, stderr := execute(t, testEnv, testCase.args...)

			require.Equal(t, testCase.wantCode, code, stderr)
			assert.Contains(t, stderr, testCase.wantStderr)

			for _, want := range testCase.wantStdout {
				assert.Contains(t, stdout, want)
			}

			if testCase.check != nil {
				testCase.check(t, stdout)
			}
		})
	}
}
