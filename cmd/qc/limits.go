package main

import (
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/limits"
	"github.com/eko/qc/quality"
)

// cgroupRoot is where Linux mounts the control groups of the process: a
// container's memory limit is read there.
const cgroupRoot = "/sys/fs/cgroup"

// Shares of the memory limit, in percent. The libvmaf threads of the
// measurements get scoringShare (quality.WithMemory) and the ffmpeg
// processes of a ladder half of the limit (ladder.Options.Memory); the
// rest is a margin. The Go heap, most of it decoded frames already counted
// in the workers, is told to stay under heapShare.
const (
	scoringShare = 40
	heapShare    = 25
	percent      = 100
)

// resourceLimits are the limits of the run: --cpus, and --memory or, by
// default, the memory limit of the container qc runs in.
func resourceLimits(
	tools ToolsConfig,
) limits.Limits {
	// validate already rejected a malformed --memory.
	memory, _ := limits.ParseMemory(tools.Memory)
	if memory == 0 {
		memory = limits.ContainerMemory(os.DirFS(cgroupRoot))
	}

	return limits.Limits{CPUs: tools.CPUs, Memory: memory}
}

// toolThreads is the thread cap of the ffmpeg processes (0: none).
func toolThreads(
	l limits.Limits,
) int {
	return l.Threads(runtime.GOMAXPROCS(0), runtime.NumCPU())
}

// applyLimits makes the Go runtime follow the limits: its processors, on
// which every worker count of qc is sized, and its heap.
func applyLimits(
	l limits.Limits,
	logger *slog.Logger,
) {
	if l.CPUs > 0 {
		runtime.GOMAXPROCS(l.CPUs)
	}

	if l.Memory > 0 {
		debug.SetMemoryLimit(l.Memory * heapShare / percent)
	}

	logger.Debug("resource limits", "cpus", runtime.GOMAXPROCS(0), "toolThreads", toolThreads(l), "memory", l.Memory)
}

// newMeter gives the measurements their share of the memory limit.
func newMeter(
	decoder decode.Source,
	engine quality.Engine,
	l limits.Limits,
) *quality.Meter {
	return quality.NewMeter(decoder, engine, quality.WithMemory(l.Memory*scoringShare/percent))
}
