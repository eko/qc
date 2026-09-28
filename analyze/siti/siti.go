// Package siti computes spatial and temporal perceptual information (SI/TI)
// as defined by ITU-T P.910.
//
// SI is the standard deviation of the Sobel gradient magnitude of the luma
// plane (one-pixel border excluded), TI the standard deviation of the pixel
// difference with the previous frame. Values are on the 8-bit code value scale.
// Frames are independent, so they are measured concurrently.
package siti

import (
	"cmp"
	"runtime"
	"slices"
	"sync"

	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/stats"
)

// Result holds per-frame SI/TI and their summaries. P.910 (2023) recommends
// the mean as the representative value.
type Result struct {
	SI        []float64     `json:"-"`
	TI        []float64     `json:"-"`
	SISummary stats.Summary `json:"si"`
	// TISummary excludes the first frame, whose TI is undefined.
	TISummary stats.Summary `json:"ti"`
}

// job is one frame to measure. It holds a reference on cur and, except for
// the first frame, on prev; the worker releases both.
type job struct {
	prev, cur *frame.Frame
}

// sample is the measurement of one frame. Workers finish out of order, so
// samples carry their frame index and are sorted at Close.
type sample struct {
	index  int
	si, ti float64
}

// Analyzer implements analyze.Analyzer and analyze.Forker. A sequential
// pass (Consume) measures frames on a pool of workers; forks measure the
// frames of their run as they come, runs being already concurrent.
type Analyzer struct {
	workers int
	series  analyze.Series[sample]

	jobs    chan job
	wg      sync.WaitGroup
	mu      sync.Mutex
	samples []sample
	prev    *frame.Frame
}

// New returns an analyzer whose sequential pass uses the given number of
// workers (0 = NumCPU), started with the first frame.
func New(
	workers int,
) *Analyzer {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	return &Analyzer{workers: workers}
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	if a.jobs == nil {
		a.jobs = make(chan job, a.workers)
		for range a.workers {
			a.wg.Go(a.work)
		}
	}

	f.Retain() // held as a job input
	f.Retain() // held as the next frame's previous

	a.jobs <- job{prev: a.prev, cur: f}
	a.prev = f

	return nil
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	if a.jobs == nil {
		return nil
	}

	close(a.jobs)
	a.wg.Wait()

	if a.prev != nil {
		a.prev.Release()
		a.prev = nil
	}

	slices.SortFunc(a.samples, func(x, y sample) int { return cmp.Compare(x.index, y.index) })

	if len(a.samples) > 0 {
		a.series.Add(a.samples[0].index, a.samples)
	}

	return nil
}

// Fork implements analyze.Forker.
func (a *Analyzer) Fork() analyze.Analyzer {
	return &run{series: &a.series}
}

// Result returns the computed values. It must be called after Close.
func (a *Analyzer) Result() Result {
	samples := a.series.Merge()
	si := make([]float64, len(samples))
	ti := make([]float64, len(samples))

	for i, smp := range samples {
		si[i], ti[i] = smp.si, smp.ti
	}

	res := Result{SI: si, TI: ti, SISummary: stats.Summarize(si)}
	if len(ti) > 1 {
		res.TISummary = stats.Summarize(ti[1:])
	}

	return res
}

// work measures jobs until the queue is closed.
func (a *Analyzer) work() {
	for j := range a.jobs {
		smp := measure(j.prev, j.cur)
		if j.prev != nil {
			j.prev.Release()
		}

		a.store(smp)
		j.cur.Release()
	}
}

// store records a sample; it is called concurrently by the workers.
func (a *Analyzer) store(
	smp sample,
) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.samples = append(a.samples, smp)
}

// measure returns the SI of cur and its TI with prev (0 without prev).
func measure(
	prev, cur *frame.Frame,
) sample {
	smp := sample{index: cur.Index, si: SpatialInformation(&cur.Luma)}
	if prev != nil {
		smp.ti = TemporalInformation(&prev.Luma, &cur.Luma)
	}

	return smp
}

// run measures the frames of one run of a fork, as they come.
type run struct {
	series  *analyze.Series[sample]
	prev    *frame.Frame
	samples []sample
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	r.samples = append(r.samples, measure(r.prev, f))

	if r.prev != nil {
		r.prev.Release()
	}

	f.Retain()
	r.prev = f

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	if r.prev != nil {
		r.prev.Release()
		r.prev = nil
	}

	if len(r.samples) > 0 {
		r.series.Add(r.samples[0].index, r.samples)
	}

	return nil
}
