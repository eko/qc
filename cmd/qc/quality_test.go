package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/quality"
)

func TestQualityMetricFlags(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		args        []string
		wantMetrics []string
		wantDevices []string
		wantErr     string
	}{
		{
			name:        "defaults: the cheap metrics",
			wantMetrics: []string{quality.MetricXPSNR, quality.MetricCAMBI, quality.MetricPSNR},
		},
		{
			name:        "VMAF only",
			args:        []string{"--metrics="},
			wantMetrics: []string{},
		},
		{
			name: "AV2 CTC preset added to the metrics",
			args: []string{"--metrics", "xpsnr", "--av2-ctc", "--devices", "phone,tv"},
			wantMetrics: append([]string{quality.MetricXPSNR},
				quality.AV2CTCMetrics()...),
			wantDevices: []string{"phone", "tv"},
		},
		{
			name:    "unknown metric",
			args:    []string{"--metrics", "vif"},
			wantErr: "invalid --metrics: unknown metric \"vif\"",
		},
		{
			name:    "unknown device",
			args:    []string{"--devices", "watch"},
			wantErr: "invalid --devices",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cmd, _, err := newRootCommand(testEnv).Find([]string{"vmaf"})
			require.NoError(t, err)
			require.NoError(t, cmd.ParseFlags(testCase.args))

			config, err := loadConfig(cmd)
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)

			opts := qualityOptions(config)
			assert.Equal(t, testCase.wantMetrics, opts.Metrics)

			if testCase.wantDevices != nil {
				assert.Equal(t, testCase.wantDevices, opts.Devices)
			}

			_, err = quality.ParseMetrics(opts.Metrics)
			require.NoError(t, err, "the library accepts what the CLI passes")
		})
	}
}

func TestQualitySampleFlag(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		args       []string
		env        map[string]string
		wantSample quality.Sample
		wantErr    error
	}{
		{
			name: "precision-driven by default",
		},
		{
			name:       "share of frames",
			args:       []string{"--sample", "5%"},
			wantSample: quality.Sample{Share: 0.05},
		},
		{
			name:       "clips per scene",
			args:       []string{"--sample", "2/scene"},
			wantSample: quality.Sample{PerScene: 2},
		},
		{
			name:       "from the environment",
			env:        map[string]string{"QC_SAMPLE": "1-per-scene"},
			wantSample: quality.Sample{PerScene: 1},
		},
		{
			name:    "malformed",
			args:    []string{"--sample", "lots"},
			wantErr: quality.ErrInvalidSample,
		},
		{
			name:    "with exact",
			args:    []string{"--sample", "5%", "--exact"},
			wantErr: ErrSampleConflict,
		},
		{
			name:    "with a precision from the environment",
			args:    []string{"--sample", "5%"},
			env:     map[string]string{"QC_PRECISION": "0.25"},
			wantErr: ErrSampleConflict,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for name, value := range testCase.env {
				t.Setenv(name, value)
			}

			cmd, _, err := newRootCommand(testEnv).Find([]string{"vmaf"})
			require.NoError(t, err)
			require.NoError(t, cmd.ParseFlags(testCase.args))

			config, err := loadConfig(cmd)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantSample, qualityOptions(config).Sample)
		})
	}
}
