package shotalloc

import "math"

// Allocation search settings.
const (
	// lambdaLo and lambdaHi bound the rate-quality slope (VMAF per Mb/s).
	lambdaLo = 1e-4
	lambdaHi = 1e4
	// bisections of λ: 60 halvings of the log range reach 1e-16 relative.
	bisections = 60
	// megabit turns bits/s into Mb/s, the unit λ is expressed in.
	megabit = 1e6
)

// Option is one setting a unit (shot or digest piece) may take: a
// resolution and a CRF, with the bitrate (b/s) and VMAF its model gives.
type Option struct {
	Height int
	CRF    float64
	Rate   float64
	VMAF   float64
}

// Menu lists, for every unit, the options of its model at one resolution
// (height): every CRF of grid.
func Menu(
	models []Model,
	height int,
	grid []float64,
) [][]Option {
	out := make([][]Option, len(models))

	for i, m := range models {
		out[i] = make([]Option, len(grid))

		for c, crf := range grid {
			rate, vmaf := m.At(crf)
			out[i][c] = Option{Height: height, CRF: crf, Rate: rate, VMAF: vmaf}
		}
	}

	return out
}

// JoinMenus puts the options of the same units at several resolutions
// together: each unit then picks its resolution too.
func JoinMenus(
	menus ...[][]Option,
) [][]Option {
	out := make([][]Option, len(menus[0]))

	for _, menu := range menus {
		for i, options := range menu {
			out[i] = append(out[i], options...)
		}
	}

	return out
}

// Allocation is the option of every unit at one slope λ, with the pooled
// (frame-weighted) VMAF and bitrate it gives.
type Allocation struct {
	// Lambda is the common slope dVMAF/dbitrate, in VMAF per Mb/s.
	Lambda  float64
	Picks   []Option
	VMAF    float64
	Bitrate float64
}

// Allocate gives every unit the option of its menu maximising
// VMAF − λ·bitrate (Mb/s): at the optimum every unit sits at the same slope
// dVMAF/dbitrate. Equal slope in bitrate, not in log bitrate, is what
// maximises the pooled VMAF at a given total size. With options at several
// resolutions, this is the per-shot convex hull over (resolution, CRF) of
// the Dynamic Optimizer. weights are the units' frame shares.
func Allocate(
	menus [][]Option,
	weights []float64,
	lambda float64,
) Allocation {
	a := Allocation{Lambda: lambda, Picks: make([]Option, len(menus))}

	for i, options := range menus {
		best, bestValue := options[0], math.Inf(-1)

		for _, o := range options {
			if value := o.VMAF - lambda*o.Rate/megabit; value > bestValue {
				best, bestValue = o, value
			}
		}

		a.Picks[i] = best
		a.VMAF += weights[i] * best.VMAF
		a.Bitrate += weights[i] * best.Rate
	}

	return a
}

// SolveLambda finds by bisection the largest slope λ (the cheapest
// allocation) whose pooled VMAF still reaches target. Pooled VMAF falls as λ
// grows. When no λ reaches it, the highest-quality allocation is returned.
func SolveLambda(
	menus [][]Option,
	weights []float64,
	target float64,
) Allocation {
	lo, hi := math.Log(lambdaLo), math.Log(lambdaHi)
	best := Allocate(menus, weights, lambdaLo)

	if best.VMAF < target {
		return best
	}

	for range bisections {
		mid := (lo + hi) / 2

		if a := Allocate(menus, weights, math.Exp(mid)); a.VMAF >= target {
			best, lo = a, mid
		} else {
			hi = mid
		}
	}

	return best
}
