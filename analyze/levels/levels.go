// Package levels measures per-frame luma statistics on the full-resolution
// plane: average, extremes and share of samples outside the legal range.
package levels

import (
	"math"

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

// Analyzer implements analyze.Analyzer.
type Analyzer struct {
	levels media.Levels
	res    Result
}

// New returns an Analyzer for a signal with the given nominal levels.
func New(
	levels media.Levels,
) *Analyzer {
	// GlobalMin starts above every 8-bit value so the first frame sets it.
	return &Analyzer{levels: levels, res: Result{Levels: levels, GlobalMin: math.MaxUint8}}
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	var (
		hist [256]int64
		p    = &f.Luma
	)

	for y := range p.Height {
		for _, v := range p.Row(y) {
			hist[v]++
		}
	}

	var sum, total, outside int64

	lo, hi := -1, 0

	for v, n := range hist {
		if n == 0 {
			continue
		}

		if lo < 0 {
			lo = v
		}

		hi = v
		sum += int64(v) * n
		total += n

		if v < a.levels.Black || v > a.levels.White {
			outside += n
		}
	}

	a.res.Mean = append(a.res.Mean, float64(sum)/float64(total))
	a.res.Min = append(a.res.Min, float64(lo))
	a.res.Max = append(a.res.Max, float64(hi))
	a.res.OutOfRange = append(a.res.OutOfRange, float64(outside)/float64(total))
	a.res.GlobalMin = min(a.res.GlobalMin, float64(lo))
	a.res.GlobalMax = max(a.res.GlobalMax, float64(hi))

	return nil
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	return nil
}

// Result returns the computed values. It must be called after Close.
func (a *Analyzer) Result() Result {
	res := a.res
	res.MeanSummary = stats.Summarize(res.Mean)
	res.OutOfRangeSummary = stats.Summarize(res.OutOfRange)

	if len(res.Mean) == 0 {
		res.GlobalMin = 0
	}

	return res
}
