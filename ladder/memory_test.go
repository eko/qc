package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFitParallel(
	t *testing.T,
) {
	const (
		gib   = int64(1) << 30
		hd    = 1920 * 1080
		ultra = 3840 * 2160
	)

	testCases := []struct {
		name     string
		parallel int
		memory   int64
		pixels   int
		want     int
	}{
		{name: "no limit", parallel: 2, memory: 0, pixels: hd, want: 2},
		{name: "1080p in 8 GiB", parallel: 2, memory: 8 * gib, pixels: hd, want: 2},
		{name: "1080p in 4 GiB", parallel: 2, memory: 4 * gib, pixels: hd, want: 2},
		{name: "1080p in 3 GiB", parallel: 2, memory: 3 * gib, pixels: hd, want: 1},
		{name: "1080p in 1 GiB still runs", parallel: 2, memory: gib, pixels: hd, want: 1},
		{name: "2160p in 8 GiB", parallel: 2, memory: 8 * gib, pixels: ultra, want: 1},
		{name: "never above the request", parallel: 1, memory: 64 * gib, pixels: hd, want: 1},
		{name: "unknown source", parallel: 2, memory: gib, pixels: 0, want: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, fitParallel(testCase.parallel, testCase.memory, testCase.pixels))
		})
	}
}

func TestFitWorkers(
	t *testing.T,
) {
	const (
		gib = int64(1) << 30
		hd  = 1920 * 1080
	)

	testCases := []struct {
		name    string
		workers int
		memory  int64
		pixels  int
		want    int
	}{
		{name: "no limit", workers: 12, pixels: hd, want: 12},
		{name: "1080p in 4 GiB", workers: 12, memory: 4 * gib, pixels: hd, want: 7},
		{name: "1080p in 1 GiB", workers: 12, memory: gib, pixels: hd, want: 1},
		{name: "half a gigabyte still runs", workers: 12, memory: gib / 2, pixels: hd, want: 1},
		{name: "never above the processors", workers: 2, memory: 64 * gib, pixels: hd, want: 2},
		{name: "unknown source", workers: 4, memory: gib, pixels: 0, want: 4},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, fitWorkers(testCase.workers, testCase.memory, testCase.pixels))
		})
	}
}
