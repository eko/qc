package quality

import (
	"context"
	"math"

	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
)

// Simulation summarises repeated sampled measurements replayed on known
// per-frame scores: it checks that the confidence intervals are honest.
type Simulation struct {
	Runs int     `json:"runs"`
	True float64 `json:"true"`
	RMSE float64 `json:"rmse"`
	// MeanAbsErr is the mean absolute error of the estimate.
	MeanAbsErr float64 `json:"meanAbsError"`
	MeanHalf   float64 `json:"meanHalfWidth"`
	Coverage   float64 `json:"coverage"`
	// SampledCoverage excludes runs that fell back to exact scoring.
	SampledCoverage float64 `json:"sampledCoverage"`
	MeanShare       float64 `json:"meanShare"`
	Fallbacks       int     `json:"fallbacks"`
	Strata          int     `json:"strata"`
}

// Simulate replays the production sampling loop runs times (different seeds)
// on exact per-frame scores, with strata built from the distorted keyframes
// (or opts.Cuts with a fixed budget, as in a real measurement). Runs that
// fall back to exact scoring count as exact (share 1, no error).
// The share of frames counts scored frames only, not the warm-up frames a
// real measurement also decodes and scores.
func Simulate(
	scores []float64,
	pts, keyframes []media.Duration,
	opts Options,
	runs int,
) Simulation {
	sim, _ := SimulateSeries(scores, nil, pts, keyframes, opts, runs)

	return sim
}

// SimulateSeries is Simulate with other per-frame series (e.g. XPSNR or
// PSNR, as returned by RawSeries) measured on the clips the VMAF scores
// select, as in a real measurement. It also returns, per series, how its
// estimate and interval behave: VMAF alone drives the sampling, so the
// other series get no precision target, but their intervals must still
// cover their true mean ~95% of the time.
func SimulateSeries(
	scores []float64,
	series map[string][]float64,
	pts, keyframes []media.Duration,
	opts Options,
	runs int,
) (Simulation, map[string]Simulation) {
	opts = opts.withDefaults()
	n := len(scores)

	sim := newCoverage(runs, stats.Mean(scores))
	others := make(map[string]*coverage, len(series))

	for name, values := range series {
		others[name] = newCoverage(runs, stats.Mean(values))
	}

	if runs > 0 && n > 0 {
		replay := func(_ context.Context, clips []clip, _ int) ([]clipResult, error) {
			results := make([]clipResult, len(clips))
			for j, c := range clips {
				values := make(map[string][]float64, len(series))
				for name, v := range series {
					values[name] = v[c.from:c.to]
				}

				results[j] = clipResult{clip: c, scores: scores[c.from:c.to], values: values}
			}

			return results, nil
		}

		for i := range runs {
			opts.Seed = uint64(i + 1)

			// replay never fails.
			strata, out, _ := sampling(context.Background(), n, pts, keyframes, opts, replay, nil)
			share := float64(out.scored) / float64(n)

			sim.add(out.est, out.fallback, share)

			for name, c := range others {
				c.add(budgetEstimate(seriesStrata(strata, out.results, name), opts), out.fallback, share)
			}

			sim.Strata = len(strata)
		}
	}

	out := make(map[string]Simulation, len(others))
	for name, c := range others {
		c.Strata = sim.Strata
		out[name] = c.summary()
	}

	return sim.summary(), out
}

// coverage accumulates replayed estimates of one series.
type coverage struct {
	Simulation

	sqErr, absErr, halfSum, shareSum float64
	covered, sampledCovered          int
}

func newCoverage(
	runs int,
	truth float64,
) *coverage {
	return &coverage{Simulation: Simulation{Runs: runs, True: truth}}
}

// add records one replayed run. A run that fell back to exact scoring
// counts as exact: no error, share 1.
func (c *coverage) add(
	est estimate,
	fallback bool,
	share float64,
) {
	mean, half := est.mean, est.halfWidth
	if fallback {
		c.Fallbacks++
		mean, half, share = c.True, 0, 1
	}

	c.sqErr += (mean - c.True) * (mean - c.True)
	c.absErr += math.Abs(mean - c.True)
	c.halfSum += half
	c.shareSum += share

	if math.Abs(mean-c.True) <= half+1e-9 {
		c.covered++

		if !fallback {
			c.sampledCovered++
		}
	}
}

// summary turns the sums into rates.
func (c *coverage) summary() Simulation {
	sim := c.Simulation
	if sim.Runs <= 0 {
		return sim
	}

	runs := float64(sim.Runs)
	sim.RMSE = math.Sqrt(c.sqErr / runs)
	sim.MeanAbsErr = c.absErr / runs
	sim.MeanHalf = c.halfSum / runs
	sim.Coverage = float64(c.covered) / runs
	sim.MeanShare = c.shareSum / runs

	if sampled := sim.Runs - sim.Fallbacks; sampled > 0 {
		sim.SampledCoverage = float64(c.sampledCovered) / float64(sampled)
	}

	return sim
}
