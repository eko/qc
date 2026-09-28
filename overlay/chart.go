package overlay

import (
	"math"
	"slices"
	"strconv"

	"github.com/eko/qc/media"
)

// chartSeries is the series of the timeline in at most bins bins: the
// lowest VMAF of each bin when a comparison scored frames (drops stand
// out), the bitrate of each bin otherwise.
func (t *title) chartSeries(
	bins int,
) series {
	bins = max(min(bins, t.n()), 1)
	values := make([]float64, bins)

	if t.quality != nil && len(t.quality.Frames) > 0 {
		return t.vmafSeries(values)
	}

	return t.bitrateSeries(values)
}

// bin is the bin of frame i among bins: by its time, or by its index when
// there are as many bins as frames (frames a little closer than bins would
// otherwise leave some bins empty).
func (t *title) bin(
	i, bins int,
) int {
	if bins >= t.n() || t.duration <= 0 {
		return i * bins / t.n()
	}

	return min(int(t.pts[i].Seconds()/t.duration.Seconds()*float64(bins)), bins-1)
}

// vmafSeries charts the lowest score of each bin, from a scale floor of
// vmafFloor or lower, to 100; bins without a scored frame stay empty.
func (t *title) vmafSeries(
	values []float64,
) series {
	for k := range values {
		values[k] = math.NaN()
	}

	lowest := 100.0

	for i, s := range t.score {
		if s < 0 {
			continue
		}

		v := t.quality.Frames[s].Score
		k := t.bin(i, len(values))

		if math.IsNaN(values[k]) || v < values[k] {
			values[k] = v
		}

		lowest = min(lowest, v)
	}

	// The floor steps down by tens to include the lowest score.
	const step = 10

	lo := min(vmafFloor, math.Floor(lowest/step)*step)

	return series{name: "VMAF", values: values, lo: lo, hi: 100, scale: integer(lo) + "–100"}
}

// bitrateSeries charts the bitrate around the middle of each bin: the bits
// of the frames within half a window of it, per second of the window that
// lies within the title (frame by frame, keyframes would make spikes).
func (t *title) bitrateSeries(
	values []float64,
) series {
	// cumulated[i] is the size of the frames before frame i.
	cumulated := make([]int, t.n()+1)
	for i := range t.pts {
		size := 0
		if i < len(t.sizes) {
			size = t.sizes[i]
		}

		cumulated[i+1] = cumulated[i] + size
	}

	end := t.duration
	if end <= 0 {
		end = bitrateWindow
	}

	highest := 0.0
	half := bitrateWindow / 2

	for k := range values {
		mid := media.Duration(float64(t.duration) * (float64(k) + 0.5) / float64(len(values)))
		from, to := max(mid-half, 0), min(mid+half, end)

		first, _ := slices.BinarySearch(t.pts, from)
		last, _ := slices.BinarySearch(t.pts, to)

		values[k] = float64((cumulated[last]-cumulated[first])*8) / (to - from).Seconds()
		highest = max(highest, values[k])
	}

	return series{name: "BITRATE", values: values, lo: 0, hi: highest, scale: "0–" + bitRate(highest) + " · " + strconv.Itoa(t.n()) + " frames"}
}
