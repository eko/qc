// Package levels measures per-frame luma statistics on the full-resolution
// plane: average, extremes and share of samples outside the legal range.
package levels

import (
	"math"

	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
)

// Result holds per-frame luma series and their summaries.
type Result struct {
	Levels media.Levels `json:"levels"`
	Mean   []float64    `json:"-"`
	Min    []float64    `json:"-"`
	Max    []float64    `json:"-"`
	// OutOfRange is the per-frame share of samples below black or above white.
	OutOfRange []float64 `json:"-"`

	MeanSummary       stats.Summary `json:"mean"`
	OutOfRangeSummary stats.Summary `json:"outOfRange"`
	GlobalMin         float64       `json:"globalMin"`
	GlobalMax         float64       `json:"globalMax"`
}

// Analyzer implements analyze.Analyzer and analyze.Forker.
type Analyzer struct {
	levels media.Levels
	series analyze.Series[stat]
	// seq is the run of a sequential pass (Consume).
	seq *run
}

// stat is the measure of one frame.
type stat struct {
	mean, lo, hi, outside float64
}

// New returns an Analyzer for a signal with the given nominal levels.
func New(
	levels media.Levels,
) *Analyzer {
	return &Analyzer{levels: levels}
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	if a.seq == nil {
		a.seq = a.fork()
	}

	return a.seq.Consume(f)
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	if a.seq != nil {
		return a.seq.Close()
	}

	return nil
}

// Fork implements analyze.Forker.
func (a *Analyzer) Fork() analyze.Analyzer {
	return a.fork()
}

func (a *Analyzer) fork() *run {
	return &run{parent: a, first: -1}
}

// Result returns the computed values. It must be called after Close.
func (a *Analyzer) Result() Result {
	// GlobalMin starts above every 8-bit value so the first frame sets it.
	res := Result{Levels: a.levels, GlobalMin: math.MaxUint8}

	for _, st := range a.series.Merge() {
		res.Mean = append(res.Mean, st.mean)
		res.Min = append(res.Min, st.lo)
		res.Max = append(res.Max, st.hi)
		res.OutOfRange = append(res.OutOfRange, st.outside)
		res.GlobalMin = min(res.GlobalMin, st.lo)
		res.GlobalMax = max(res.GlobalMax, st.hi)
	}

	res.MeanSummary = stats.Summarize(res.Mean)
	res.OutOfRangeSummary = stats.Summarize(res.OutOfRange)

	if len(res.Mean) == 0 {
		res.GlobalMin = 0
	}

	return res
}

// run measures the frames of one run.
type run struct {
	parent *Analyzer
	first  int
	frames []stat
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	if r.first < 0 {
		r.first = f.Index
	}

	st := planeStats(&f.Luma, r.parent.levels)
	total := float64(f.Luma.Width * f.Luma.Height)

	r.frames = append(r.frames, stat{
		mean: float64(st.sum) / total, lo: float64(st.lo), hi: float64(st.hi),
		outside: float64(st.below+st.above) / total,
	})

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	r.parent.series.Add(r.first, r.frames)

	return nil
}

// planeStats returns the statistics of the samples of an 8-bit plane.
// Their sums are integers: the order they are accumulated in (vector
// lanes, histograms) does not change them.
func planeStats(
	p *frame.Plane,
	lv media.Levels,
) rowStats {
	st := rowStats{lo: math.MaxUint8}
	black := byte(min(max(lv.Black, 0), math.MaxUint8)) //nolint:gosec // clamped to a byte
	white := byte(min(max(lv.White, 0), math.MaxUint8)) //nolint:gosec // clamped to a byte

	st.addPlane(p, black, white)

	return st
}

// rowStats accumulates the sum, extremes and out-of-range counts (below
// black, above white) of 8-bit samples. Its layout is read by the vector
// loop (stats_arm64.s).
type rowStats struct {
	sum, below, above uint64
	lo, hi            byte
}
