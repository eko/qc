// Package siti computes spatial and temporal perceptual information (SI/TI)
// as defined by ITU-T P.910.
//
// SI is the standard deviation of the Sobel gradient magnitude of the luma
// plane (one-pixel border excluded), TI the standard deviation of the pixel
// difference with the previous frame. Values are on the 8-bit code value scale.
// Frames are independent, so they are processed by a pool of workers.
package siti

import (
	"math"
	"runtime"
	"sync"

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
// samples carry their frame index and are sorted by Result.
type sample struct {
	index  int
	si, ti float64
}

// Analyzer implements analyze.Analyzer.
type Analyzer struct {
	jobs    chan job
	wg      sync.WaitGroup
	mu      sync.Mutex
	samples []sample
	prev    *frame.Frame
}

// New starts an analyzer with the given number of workers (0 = NumCPU).
func New(
	workers int,
) *Analyzer {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	a := &Analyzer{jobs: make(chan job, workers)}

	for range workers {
		a.wg.Go(a.work)
	}

	return a
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	f.Retain() // held as a job input
	f.Retain() // held as the next frame's previous

	a.jobs <- job{prev: a.prev, cur: f}
	a.prev = f

	return nil
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	close(a.jobs)
	a.wg.Wait()

	if a.prev != nil {
		a.prev.Release()
		a.prev = nil
	}

	return nil
}

// Result returns the computed values. It must be called after Close.
func (a *Analyzer) Result() Result {
	si := make([]float64, len(a.samples))
	ti := make([]float64, len(a.samples))

	for _, smp := range a.samples {
		si[smp.index], ti[smp.index] = smp.si, smp.ti
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
		si := SpatialInformation(&j.cur.Luma)

		ti := 0.0
		if j.prev != nil {
			ti = TemporalInformation(&j.prev.Luma, &j.cur.Luma)
			j.prev.Release()
		}

		a.store(j.cur.Index, si, ti)
		j.cur.Release()
	}
}

// store records a sample; it is called concurrently by the workers.
func (a *Analyzer) store(
	index int,
	si, ti float64,
) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.samples = append(a.samples, sample{index: index, si: si, ti: ti})
}

// SpatialInformation returns the standard deviation of the Sobel magnitude.
func SpatialInformation(
	p *frame.Plane,
) float64 {
	if p.Width < 3 || p.Height < 3 {
		return 0
	}

	var (
		sum   float64
		sumSq int64
	)

	for y := 1; y < p.Height-1; y++ {
		above, row, below := p.Row(y-1), p.Row(y), p.Row(y+1)
		rowSum, rowSq := sobelRow(above, row, below)
		sum += rowSum
		sumSq += rowSq
	}

	n := float64((p.Width - 2) * (p.Height - 2))

	return stats.StdDev(sum, float64(sumSq), n)
}

// sobelRow returns the sum of Sobel magnitudes and squared magnitudes over the
// inner pixels of a row.
func sobelRow(
	above, row, below []byte,
) (float64, int64) {
	var (
		sum   float64
		sumSq int64
	)

	for x := 0; x+2 < len(row); x++ {
		a, r, b := above[x:x+3:x+3], row[x:x+3:x+3], below[x:x+3:x+3]

		sq := sobel(a[0], a[1], a[2], r[0], r[2], b[0], b[1], b[2])
		sumSq += int64(sq)
		sum += math.Sqrt(float64(sq))
	}

	return sum, sumSq
}

// sobel returns gx² + gy² for the 3×3 neighbourhood (a: above, l/r: left and
// right of the centre, b: below).
func sobel(
	a0, a1, a2, l, r, b0, b1, b2 byte,
) int32 {
	gx := int32(a2) + 2*int32(r) + int32(b2) - int32(a0) - 2*int32(l) - int32(b0)
	gy := int32(b0) + 2*int32(b1) + int32(b2) - int32(a0) - 2*int32(a1) - int32(a2)

	return gx*gx + gy*gy
}

// TemporalInformation returns the standard deviation of cur - prev.
func TemporalInformation(
	prev, cur *frame.Plane,
) float64 {
	var sum, sumSq int64

	for y := range cur.Height {
		a, b := prev.Row(y), cur.Row(y)
		a = a[:len(b)]

		var rowSum, rowSq int64

		for x := range b {
			d := int64(b[x]) - int64(a[x])
			rowSum += d
			rowSq += d * d
		}

		sum += rowSum
		sumSq += rowSq
	}

	return stats.StdDev(float64(sum), float64(sumSq), float64(cur.Width*cur.Height))
}
