package quality

import (
	"context"
	"fmt"

	"golang.org/x/sync/semaphore"
)

// memoryBudget bounds the memory of the workers scoring at once, over
// every measurement of a Meter: a ladder runs several measurements at a
// time, each with its own workers. The zero value sets no bound.
type memoryBudget struct {
	bytes   int64
	workers *semaphore.Weighted
}

func newMemoryBudget(
	bytes int64,
) memoryBudget {
	if bytes <= 0 {
		return memoryBudget{}
	}

	return memoryBudget{bytes: bytes, workers: semaphore.NewWeighted(bytes)}
}

// fit is the number of units costing cost bytes each the budget holds: at
// most wanted, at least one. A measurement must run, even over a budget
// too small for it.
func (b memoryBudget) fit(
	wanted int,
	cost int64,
) int {
	if b.workers == nil || cost <= 0 {
		return wanted
	}

	return max(1, min(wanted, int(b.bytes/cost)))
}

// acquire waits for cost bytes of the budget and returns what gives them
// back. A cost above the whole budget takes all of it: that worker then
// runs alone.
func (b memoryBudget) acquire(
	ctx context.Context,
	cost int64,
) (func(), error) {
	if b.workers == nil || cost <= 0 {
		return func() {}, nil
	}

	cost = min(cost, b.bytes)
	if err := b.workers.Acquire(ctx, cost); err != nil {
		return nil, fmt.Errorf("wait for memory: %w", err)
	}

	return func() { b.workers.Release(cost) }, nil
}

// threadBytesPerPixel is the memory of a scoring worker per libvmaf thread
// and per pixel of the evaluation resolution, at 8 bits: libvmaf's buffers
// grow with its threads, and the frame pairs queued for a worker are
// counted with them. Measured on ladder builds at 1080p in the Linux image:
// about 165 MiB per thread, whether 2 or 24 of them run (docs/install.md).
const threadBytesPerPixel = 80

// threadCost is the memory one libvmaf thread of the run is counted for.
// 10-bit scoring doubles the frames, not libvmaf's own buffers.
func (r *run) threadCost() int64 {
	cost := int64(r.spec.Width) * int64(r.spec.Height) * threadBytesPerPixel
	if r.bitDepth > 8 {
		cost += cost / 2
	}

	return cost
}

// fitMemory lowers workers, each scoring on threads libvmaf threads, to
// what the Meter's memory budget holds; a single worker gets fewer threads
// when even it does not fit. Without a budget, both are returned as is.
func (r *run) fitMemory(
	workers, threads int,
) (int, int) {
	budget, cost := r.meter.memory, r.threadCost()

	if workers = budget.fit(workers, cost*int64(threads)); workers == 1 {
		threads = budget.fit(threads, cost)
	}

	return workers, threads
}
