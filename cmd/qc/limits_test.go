package main

import (
	"testing"
	"testing/fstest"

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

func TestMemoryLimit(
	t *testing.T,
) {
	cgroup := fstest.MapFS{"memory.max": {Data: []byte("3221225472\n")}}
	unlimited := fstest.MapFS{"memory.max": {Data: []byte("max\n")}}
	proc := fstest.MapFS{"meminfo": {Data: []byte("MemTotal: 8039596 kB\nMemAvailable: 4718592 kB\n")}}

	testCases := []struct {
		name   string
		asked  string
		cgroup fstest.MapFS
		proc   fstest.MapFS
		want   int64
	}{
		{name: "--memory first", asked: "2g", cgroup: cgroup, proc: proc, want: 2 << 30},
		{name: "then the container's limit", cgroup: cgroup, proc: proc, want: 3 << 30},
		{name: "then the available memory", cgroup: unlimited, proc: proc, want: 4718592 << 10},
		{name: "nothing known", cgroup: fstest.MapFS{}, proc: fstest.MapFS{}, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, memoryLimit(testCase.asked, testCase.cgroup, testCase.proc))
		})
	}
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
