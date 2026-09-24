package quality

import (
	"math"
	"slices"

	"github.com/eko/qc/internal/stats"
)

// stratum is a contiguous range of frames [first, last) split into clip
// slots. A slot is the sampling unit: frames inside a clip are strongly
// correlated, so clips (not frames) are what the variance is computed on.
type stratum struct {
	first, last int
	slots       [][2]int // frame ranges [from, to) of each candidate clip
	sampled     []int    // indices into slots, in sampling order
	clipMeans   []float64
	// clipFrames is the frame count of each scored clip: the last slot of a
	// stratum takes the remainder, so clips are not all the same length.
	clipFrames []int
}

func (s *stratum) frames() int {
	return s.last - s.first
}

// addClip records the mean score of a clip of the given length.
func (s *stratum) addClip(
	mean float64,
	frames int,
) {
	s.clipMeans = append(s.clipMeans, mean)
	s.clipFrames = append(s.clipFrames, frames)
}

// mean is the frame-weighted mean of the scored clips. Weighting by length
// makes a fully sampled stratum give exactly its frame mean even though its
// last slot is longer than the others.
func (s *stratum) mean() float64 {
	var sum, frames float64

	for i, m := range s.clipMeans {
		sum += m * float64(s.clipFrames[i])
		frames += float64(s.clipFrames[i])
	}

	return sum / frames
}

// estimate is a stratified estimate of the mean frame score.
type estimate struct {
	mean      float64
	halfWidth float64
	df        float64
	// variance names the variance estimator used (VariancePooled...).
	variance string
}

// estimator estimates the mean of sampled strata and its interval.
type estimator func(strata []*stratum, confidence float64) estimate

// estimatorFor returns the estimator of a sampling design. Strata of the
// precision loop and of a share budget are GOPs grouped to similar lengths:
// their within-stratum variances are alike and pooling them is both stable
// and honest. Scenes of a per-scene budget are never grouped, and their
// lengths span two orders of magnitude: long scenes vary more and weigh more,
// which a pooled variance underestimates, so each scene keeps its own.
func estimatorFor(
	sample Sample,
) estimator {
	if sample.PerScene > 0 {
		return estimateSeparate
	}

	return estimateMean
}

// estimateMean combines per-stratum clip means into the population mean and
// its confidence interval half-width:
//
//	Ȳ = Σ W_h ȳ_h,  Var(Ȳ) = s_p² Σ W_h² (1 − n_h/M_h) / n_h
//
// with W_h the share of frames in stratum h and n_h sampled clips out of M_h.
// s_p² is the within-stratum variance of clip means pooled over all strata.
// In the precision loop, per-stratum variances rest on two or three clips
// each: they are so noisy that intervals built on them are too narrow
// whenever a draw happens to be low, and the loop stops on such draws. The pooled variance was measured to keep the nominal coverage on real
// content (bench/vmafsim). When no stratum holds two clips (a budget of one
// clip per scene), there is no within-stratum variance to pool: adjacent
// strata are collapsed instead (collapsedVariance).
func estimateMean(
	strata []*stratum,
	confidence float64,
) estimate {
	total := totalFrames(strata)

	pooled, df := pooledVariance(strata)
	mean := stratifiedMean(strata, total)

	if df == 0 {
		return estimateCollapsed(strata, confidence)
	}

	var weight float64

	for _, s := range strata {
		if len(s.clipMeans) == 0 {
			continue
		}

		w := float64(s.frames()) / float64(total)
		n := float64(len(s.clipMeans))
		weight += w * w * (1 - n/float64(len(s.slots))) / n
	}

	return estimate{
		mean:      mean,
		halfWidth: tQuantile(0.5+confidence/2, float64(df)) * math.Sqrt(pooled*weight),
		df:        float64(df),
		variance:  VariancePooled,
	}
}

// estimateSeparate is the textbook stratified estimator, each stratum with
// its own variance:
//
//	Var(Ȳ) = Σ W_h² (1 − n_h/M_h) s_h² / n_h
//
// Its degrees of freedom follow Satterthwaite's approximation, which shrinks
// them when a few strata dominate the variance (at least the smallest
// n_h − 1, so never below one). Noisy per-stratum variances
// broke the adaptive precision loop, which stops on a low draw; a fixed
// design over hundreds of scenes averages that noise out. Without a stratum
// of two clips it falls back to collapsed strata, like estimateMean.
func estimateSeparate(
	strata []*stratum,
	confidence float64,
) estimate {
	total := totalFrames(strata)
	mean := stratifiedMean(strata, total)

	var variance, spread float64

	for _, s := range strata {
		n := float64(len(s.clipMeans))
		if n < 2 {
			continue
		}

		w := float64(s.frames()) / float64(total)
		term := w * w * (1 - n/float64(len(s.slots))) / n * sampleVariance(s.clipMeans, stats.Mean(s.clipMeans))
		variance += term
		spread += term * term / (n - 1)
	}

	switch {
	case !slices.ContainsFunc(strata, func(s *stratum) bool { return len(s.clipMeans) >= 2 }):
		return estimateCollapsed(strata, confidence)
	case spread == 0:
		// Every stratum is fully sampled or constant: no sampling error.
		return estimate{mean: mean, variance: VarianceSeparate}
	}

	// Rounded down, the usual conservative reading of Satterthwaite's
	// approximation (tQuantile is exact on whole degrees of freedom below 3).
	df := math.Floor(variance * variance / spread)

	return estimate{
		mean:      mean,
		halfWidth: tQuantile(0.5+confidence/2, df) * math.Sqrt(variance),
		df:        df,
		variance:  VarianceSeparate,
	}
}

// estimateCollapsed estimates the mean when no stratum holds two clips, from
// collapsed strata (collapsedVariance). The interval is infinite when fewer
// than two strata hold one clip and a sampling error: the variance is
// unknown.
func estimateCollapsed(
	strata []*stratum,
	confidence float64,
) estimate {
	total := totalFrames(strata)
	mean := stratifiedMean(strata, total)

	variance, groups := collapsedVariance(strata, total)
	if groups == 0 {
		return estimate{mean: mean, halfWidth: math.Inf(1)}
	}

	return estimate{
		mean:      mean,
		halfWidth: tQuantile(0.5+confidence/2, float64(groups)) * math.Sqrt(variance),
		df:        float64(groups),
		variance:  VarianceCollapsed,
	}
}

// stratifiedMean is Σ W_h ȳ_h over the strata holding clips, total being the
// frames of every stratum.
func stratifiedMean(
	strata []*stratum,
	total int,
) float64 {
	var mean float64

	for _, s := range strata {
		if len(s.clipMeans) > 0 {
			mean += float64(s.frames()) / float64(total) * s.mean()
		}
	}

	return mean
}

// collapsedVariance estimates Var(Ȳ) when every stratum holds a single
// clip, which leaves no within-stratum variance to pool (Cochran, Sampling
// Techniques, 3rd ed., §5A.12, after Hansen, Hurwitz and Madow). Adjacent
// strata are collapsed into groups of two, the last group taking three when
// their count is odd, and the spread of the stratum means within a group
// stands for their sampling variance:
//
//	v = Σ_groups G/(G−1) Σ_k (1 − f_k) W_k² (ȳ_k − ȳ_w)²
//
// with G strata per group, W_k their share of frames, f_k their sampling
// fraction and ȳ_w the W-weighted mean of the group. For a pair it reduces
// to 4 (W_1 W_2 / (W_1 + W_2))² (ȳ_1 − ȳ_2)². Differences between the true
// means of collapsed strata add to it, so the estimator is conservative: its
// intervals are wider where adjacent scenes differ more. Neighbours in time
// are grouped because they are the likeliest to look alike. Fully sampled
// strata have no sampling error and are left out. It returns the variance and
// its degrees of freedom Σ(G − 1), zero when fewer than two strata qualify.
func collapsedVariance(
	strata []*stratum,
	total int,
) (float64, int) {
	var singles []*stratum

	for _, s := range strata {
		if len(s.clipMeans) == 1 && len(s.slots) > 1 {
			singles = append(singles, s)
		}
	}

	if len(singles) < 2 {
		return 0, 0
	}

	var variance float64

	groups := len(singles) / 2

	for g := range groups {
		group := singles[2*g : 2*g+2]
		if g == groups-1 {
			group = singles[2*g:]
		}

		var sum, weight float64

		for _, s := range group {
			w := float64(s.frames()) / float64(total)
			sum += w * s.mean()
			weight += w
		}

		center := sum / weight
		size := float64(len(group))

		for _, s := range group {
			w := float64(s.frames()) / float64(total)
			fpc := 1 - 1/float64(len(s.slots))
			variance += size / (size - 1) * fpc * w * w * (s.mean() - center) * (s.mean() - center)
		}
	}

	return variance, len(singles) - groups
}

// pooledVariance returns the within-stratum variance of clip means pooled over
// strata holding at least two clips, and its degrees of freedom.
func pooledVariance(
	strata []*stratum,
) (float64, int) {
	var (
		ss float64
		df int
	)

	for _, s := range strata {
		if len(s.clipMeans) >= 2 {
			ss += sampleVariance(s.clipMeans, stats.Mean(s.clipMeans)) * float64(len(s.clipMeans)-1)
			df += len(s.clipMeans) - 1
		}
	}

	if df == 0 {
		return 0, 0
	}

	return ss / float64(df), df
}

// allocate picks the next clips to sample: batch clips given one at a time to
// the stratum with the largest marginal variance reduction,
// W_h² (1/n_h − 1/(n_h+1)) under the pooled variance. Returns the number of
// clips added.
func allocate(
	strata []*stratum,
	batch int,
	next func(s *stratum) int,
) int {
	total := totalFrames(strata)

	pending := make(map[*stratum]int)
	added := 0

	for range batch {
		var (
			best     *stratum
			bestGain = -1.0
		)

		for _, s := range strata {
			n := len(s.sampled) + pending[s]
			if n >= len(s.slots) {
				continue
			}

			w := float64(s.frames()) / float64(total)
			gain := w * w * (1/float64(max(n, 1)) - 1/float64(n+1))
			if gain > bestGain {
				best, bestGain = s, gain
			}
		}

		if best == nil {
			break
		}

		pending[best]++
		added++
	}

	for s, n := range pending {
		for range n {
			s.sampled = append(s.sampled, next(s))
		}
	}

	return added
}

// totalFrames is the number of frames covered by strata.
func totalFrames(
	strata []*stratum,
) int {
	total := 0
	for _, s := range strata {
		total += s.frames()
	}

	return total
}

// sampleVariance is the unbiased variance of values around their mean.
func sampleVariance(
	values []float64,
	mean float64,
) float64 {
	var ss float64
	for _, v := range values {
		ss += (v - mean) * (v - mean)
	}

	return ss / float64(len(values)-1)
}

// tQuantile returns the p-quantile of Student's t with df degrees of freedom:
// exact for df ≤ 2, Cornish-Fisher expansion above (error < 1e-3 for df ≥ 3).
func tQuantile(
	p, df float64,
) float64 {
	switch {
	case math.IsInf(df, 1):
		return normalQuantile(p)
	case df <= 1:
		return math.Tan(math.Pi * (p - 0.5))
	case df <= 2:
		return (2*p - 1) / math.Sqrt(2*p*(1-p))
	}

	z := normalQuantile(p)
	z3, z5, z7 := z*z*z, z*z*z*z*z, z*z*z*z*z*z*z

	return z +
		(z3+z)/(4*df) +
		(5*z5+16*z3+3*z)/(96*df*df) +
		(3*z7+19*z5+17*z3-15*z)/(384*df*df*df)
}

// Coefficients of Acklam's rational approximations of the inverse normal
// CDF: central region (a, b) and tails (c, d).
var (
	acklamA = [...]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	acklamB = [...]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	acklamC = [...]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	acklamD = [...]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
)

// acklamTail is the probability below which the tail approximation is used.
const acklamTail = 0.02425

// normalQuantile is the inverse standard normal CDF (Acklam's algorithm,
// relative error < 1.2e-9).
func normalQuantile(
	p float64,
) float64 {
	a, b, c, d := acklamA, acklamB, acklamC, acklamD

	switch {
	case p < acklamTail:
		q := math.Sqrt(-2 * math.Log(p))

		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > 1-acklamTail:
		return -normalQuantile(1 - p)
	}

	q := p - 0.5
	r := q * q

	return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q / (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
}
