package svg

import "math"

// Points are all the samples, for series short enough to draw in full.
func (sm *Samples) Points() [][2]float64 {
	n := min(len(sm.X), len(sm.Y))
	out := make([][2]float64, n)

	for i := range n {
		out[i] = [2]float64{sm.X[i], sm.Y[i]}
	}

	return out
}

// drawn are the points drawn for the samples: bucket means for lines, and
// real samples evenly picked for markers, which stand for measured points.
func (sm *Samples) drawn(
	limit int,
	markers bool,
) [][2]float64 {
	if !markers {
		return Downsample(sm.X, sm.Y, limit)
	}

	count := min(len(sm.X), len(sm.Y))
	n := min(count, limit)
	out := make([][2]float64, n)

	for i := range n {
		j := i * count / n
		out[i] = [2]float64{sm.X[j], sm.Y[j]}
	}

	return out
}

// envelope is the mean x, the minimum and the maximum y of the buckets
// Downsample averages.
func envelope(
	xs, ys []float64,
	n int,
) [][3]float64 {
	count := min(len(xs), len(ys))
	buckets := min(count, n)
	out := make([][3]float64, buckets)

	for b := range buckets {
		from, to := b*count/buckets, (b+1)*count/buckets
		sx, lo, hi := 0.0, math.Inf(1), math.Inf(-1)

		for i := from; i < to; i++ {
			sx += xs[i]
			lo, hi = math.Min(lo, ys[i]), math.Max(hi, ys[i])
		}

		out[b] = [3]float64{sx / float64(to-from), lo, hi}
	}

	return out
}

// Downsample keeps at most n points, averaging buckets of consecutive
// points: a chart cannot show more points than it has pixels.
func Downsample(
	xs, ys []float64,
	n int,
) [][2]float64 {
	count := min(len(xs), len(ys))
	if count == 0 || n <= 0 {
		return nil
	}

	buckets := min(count, n)
	out := make([][2]float64, buckets)

	for b := range buckets {
		// Buckets are never empty since buckets ≤ count.
		from, to := b*count/buckets, (b+1)*count/buckets

		var sx, sy float64
		for i := from; i < to; i++ {
			sx += xs[i]
			sy += ys[i]
		}

		out[b] = [2]float64{sx / float64(to-from), sy / float64(to-from)}
	}

	return out
}
