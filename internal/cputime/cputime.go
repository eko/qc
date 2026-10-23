// Package cputime counts the CPU time of the processes a piece of work
// starts: an encode is a process of its own, and its CPU time is what its
// energy follows. A Meter rides on the context of the work; the code
// starting processes adds their time to it (see internal/ffexec).
package cputime

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Meter accumulates CPU time. It is safe for concurrent use.
type Meter struct {
	mu    sync.Mutex
	total time.Duration
}

// Add counts d.
func (m *Meter) Add(
	d time.Duration,
) {
	m.mu.Lock()
	m.total += d
	m.mu.Unlock()
}

// Seconds is the CPU time counted, in seconds.
func (m *Meter) Seconds() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.total.Seconds()
}

type meterKey struct{}

// With returns ctx carrying m: the processes started under it count their
// CPU time in m, as well as in the meters ctx already carried.
func With(
	ctx context.Context,
	m *Meter,
) context.Context {
	meters := append(slices.Clone(from(ctx)), m)

	return context.WithValue(ctx, meterKey{}, meters)
}

// Add counts d in every meter ctx carries.
func Add(
	ctx context.Context,
	d time.Duration,
) {
	for _, m := range from(ctx) {
		m.Add(d)
	}
}

func from(
	ctx context.Context,
) []*Meter {
	meters, _ := ctx.Value(meterKey{}).([]*Meter)

	return meters
}
