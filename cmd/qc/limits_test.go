package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/limits"
)

func TestResourceLimits(
	t *testing.T,
) {
	got := resourceLimits(ToolsConfig{CPUs: 4, Memory: "4g"})

	assert.Equal(t, limits.Limits{CPUs: 4, Memory: 4 << 30}, got)
	assert.Equal(t, 4, toolThreads(got))
}

func TestToolsConfigValidateLimits(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		tools   ToolsConfig
		wantErr error
	}{
		{name: "no limit", tools: ToolsConfig{LogLevel: "warn"}},
		{name: "limits", tools: ToolsConfig{LogLevel: "warn", CPUs: 4, Memory: "512m"}},
		{name: "negative cpus", tools: ToolsConfig{LogLevel: "warn", CPUs: -1}, wantErr: ErrInvalidCPUs},
		{name: "malformed memory", tools: ToolsConfig{LogLevel: "warn", Memory: "lots"}, wantErr: limits.ErrInvalidMemory},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.tools.validate()

			if testCase.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}
