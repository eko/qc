package motion

import "math"

// projectionRange is the share of a dimension the projection predictor
// searches on each side: a quarter of the picture per frame is beyond any
// camera move a viewer can follow (whip pans blur out anyway).
const projectionRange = 4

// projectionShift returns the integer shift s minimising the mean absolute
// difference between cur[x] and prev[x-s], both profiles with their mean
// removed so that a global brightness change (a fade) does not bias it.
//
// Correlating the row and column sums of two frames (integral projections)
// finds a global translation in O(width + height) per candidate shift
// instead of O(width × height): the classic predictor of digital image
// stabilisation (Ratakonda, "Real-time digital video stabilization for
// multi-media applications", ISCAS 1998). Block matching then refines it
// locally, so a wrong predictor (a large moving object) only costs the
// blocks a wider search.
func projectionShift(
	cur, prev []int32,
) int {
	n := len(cur)
	if n == 0 {
		return 0
	}

	meanCur, meanPrev := mean32(cur), mean32(prev)
	limit := n / projectionRange
	best, bestCost := 0, math.Inf(1)

	for s := -limit; s <= limit; s++ {
		from, to := max(0, s), min(n, n+s)

		var sum float64

		for x := from; x < to; x++ {
			sum += math.Abs(float64(cur[x]) - meanCur - float64(prev[x-s]) + meanPrev)
		}

		if cost := sum / float64(to-from); cost < bestCost {
			best, bestCost = s, cost
		}
	}

	return best
}

func mean32(
	values []int32,
) float64 {
	var sum int64
	for _, v := range values {
		sum += int64(v)
	}

	return float64(sum) / float64(len(values))
}
