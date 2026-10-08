package limits

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMemory(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		input string
		want  int64
		fails bool
	}{
		{name: "empty", input: "", want: 0},
		{name: "bytes", input: "1048576", want: 1 << 20},
		{name: "gigabytes", input: "4g", want: 4 << 30},
		{name: "upper case", input: "4G", want: 4 << 30},
		{name: "megabytes", input: "512m", want: 512 << 20},
		{name: "kilobytes", input: "2048k", want: 2 << 20},
		{name: "fraction", input: "1.5g", want: 3 << 29},
		{name: "gb suffix", input: "4gb", want: 4 << 30},
		{name: "gib suffix", input: "4GiB", want: 4 << 30},
		{name: "spaces", input: " 2 g ", want: 2 << 30},
		{name: "zero", input: "0", fails: true},
		{name: "negative", input: "-1g", fails: true},
		{name: "unknown unit", input: "4t", fails: true},
		{name: "no number", input: "g", fails: true},
		{name: "words", input: "lots", fails: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseMemory(testCase.input)

			if testCase.fails {
				require.ErrorIs(t, err, ErrInvalidMemory)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestContainerMemory(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		files fstest.MapFS
		want  int64
	}{
		{name: "no cgroup", files: fstest.MapFS{}, want: 0},
		{name: "v2 limit", files: fstest.MapFS{"memory.max": {Data: []byte("4294967296\n")}}, want: 4 << 30},
		{name: "v2 unlimited", files: fstest.MapFS{"memory.max": {Data: []byte("max\n")}}, want: 0},
		{name: "v1 limit", files: fstest.MapFS{"memory/memory.limit_in_bytes": {Data: []byte("2147483648\n")}}, want: 2 << 30},
		{name: "v1 unlimited", files: fstest.MapFS{"memory/memory.limit_in_bytes": {Data: []byte("9223372036854771712\n")}}, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, ContainerMemory(testCase.files))
		})
	}
}

func TestFormatMemory(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		bytes int64
		want  string
	}{
		{name: "gibibytes", bytes: 4 << 30, want: "4.0 GiB"},
		{name: "fraction", bytes: 3 << 29, want: "1.5 GiB"},
		{name: "mebibytes", bytes: 512 << 20, want: "512 MiB"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, FormatMemory(testCase.bytes))
		})
	}
}

func TestThreads(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		limits       Limits
		procs, cores int
		want         int
	}{
		{name: "whole machine", procs: 12, cores: 12, want: 0},
		{name: "container quota", procs: 4, cores: 12, want: 4},
		{name: "explicit", limits: Limits{CPUs: 2}, procs: 12, cores: 12, want: 2},
		{name: "explicit wins over quota", limits: Limits{CPUs: 2}, procs: 4, cores: 12, want: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.limits.Threads(testCase.procs, testCase.cores))
		})
	}
}
