package svg

import (
	"fmt"
	"math"
	"strconv"
)

// scaleX maps x to the chart's horizontal scale.
func (c Chart) scaleX(
	x float64,
) float64 {
	if c.LogX {
		return math.Log(math.Max(x, minLogX))
	}

	return x
}

// bounds is the data range of the plot area, in scaled x, and the step of
// the y ticks. An automatic y maximum is rounded up to a tick.
func (c Chart) bounds() (xLo, xHi, yLo, yHi, yStep float64) {
	yLo, yHi = c.YMin, c.YMax
	autoY := c.YMax == 0 || c.LogY

	xLo, xHi, dataYMax, yMin := c.extent()
	if autoY {
		yHi = dataYMax
	}

	if math.IsInf(xLo, 1) {
		xLo, xHi = 0, 1
	}

	// Time axes start at the beginning of the title, not at the first
	// frame's timestamp.
	if c.X == UnitTime && xLo > 0 && xLo < timeOrigin*(xHi-xLo) {
		xLo = 0
	}

	if xHi == xLo {
		xHi = xLo + 1
	}

	if c.LogY {
		yLo, yHi = logRange(yMin, yHi)

		return xLo, xHi, yLo, yHi, 0
	}

	if math.IsInf(yHi, -1) || yHi <= yLo {
		yHi = yLo + 1
	}

	yStep = c.Y.step(yHi-yLo, yTicks)
	if autoY {
		yHi = yLo + math.Ceil((yHi-yLo)/yStep)*yStep
	}

	return xLo, xHi, yLo, yHi, yStep
}

// extent is the range of the drawn samples: scaled x bounds, the highest y
// and the lowest positive y (for log axes). Samples bound the axes, not
// their drawing: decimated markers may leave out the last sample or the
// highest one. With no finite sample, xLo is +Inf and yMax -Inf.
func (c Chart) extent() (xLo, xHi, yMax, yMinPositive float64) {
	xLo, xHi, yMax, yMinPositive = math.Inf(1), math.Inf(-1), math.Inf(-1), math.Inf(1)

	for _, s := range c.Series {
		if s.TipOnly {
			continue
		}

		xs, ys, _, _ := pointColumns(s)

		for i, x := range xs {
			if !isFinite(x) || !isFinite(ys[i]) {
				continue
			}

			x = c.scaleX(x)
			xLo, xHi = math.Min(xLo, x), math.Max(xHi, x)
			yMax = math.Max(yMax, ys[i])

			if ys[i] > 0 {
				yMinPositive = math.Min(yMinPositive, ys[i])
			}
		}
	}

	return xLo, xHi, yMax, yMinPositive
}

// logRange rounds the positive range lo..hi out to 1, 2, 5 × 10^k values,
// at least one such step apart: the ends of a log axis get labels.
func logRange(
	lo, hi float64,
) (float64, float64) {
	if math.IsInf(lo, 1) || hi <= 0 {
		return 1, 10
	}

	lo, hi = niceFloor(lo), niceCeil(hi)
	if hi <= lo {
		hi = niceCeil(lo * (1 + 1e-6))
	}

	return lo, hi
}

// niceFloor is the largest 1, 2, 5 × 10^k value not above v > 0.
func niceFloor(
	v float64,
) float64 {
	mag := math.Pow(10, math.Floor(math.Log10(v)))

	for _, m := range []float64{5, 2} {
		if m*mag <= v*(1+1e-9) {
			return m * mag
		}
	}

	return mag
}

// niceCeil is the smallest 1, 2, 5 × 10^k value not below v > 0.
func niceCeil(
	v float64,
) float64 {
	mag := math.Pow(10, math.Floor(math.Log10(v)))

	for _, m := range []float64{1, 2, 5} {
		if m*mag >= v*(1-1e-9) {
			return m * mag
		}
	}

	return 10 * mag
}

// niceStep is a 1, 2, 2.5 or 5 × 10^k step giving about n ticks over span.
func niceStep(
	span float64,
	n int,
) float64 {
	raw := span / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))

	for _, m := range []float64{1, 2, 2.5, 5} {
		if raw <= m*mag {
			return m * mag
		}
	}

	return 10 * mag
}

// step is a tick step giving about n ticks over span: sizes step in
// binary multiples, to read 64 KiB rather than 62.5 KiB.
func (u Unit) step(
	span float64,
	n int,
) float64 {
	if u == UnitBytes {
		for _, unit := range []float64{1 << 20, 1 << 10} {
			if span >= unit*float64(n) {
				return niceStep(span/unit, n) * unit
			}
		}
	}

	return niceStep(span, n)
}

// timeStep is a clock-friendly tick step giving about xTicks ticks.
func timeStep(
	span float64,
) float64 {
	raw := span / xTicks

	for _, s := range timeSteps {
		if raw <= s {
			return s
		}
	}

	return timeSteps[len(timeSteps)-1]
}

// linearTicks are the multiples of step between lo and hi.
func linearTicks(
	lo, hi, step float64,
) []float64 {
	const epsilon = 1e-9

	var out []float64

	for k := math.Ceil(lo/step - epsilon); k*step <= hi+epsilon*step; k++ {
		out = append(out, k*step)
	}

	return out
}

// logTicks are the 1, 2, 5 × 10^k values between lo and hi, or the bounds
// when the range holds fewer than two of them.
func logTicks(
	lo, hi float64,
) []float64 {
	var all, wide []float64

	for d := math.Floor(math.Log10(lo)); d <= math.Ceil(math.Log10(hi)); d++ {
		for _, m := range []float64{1, 2, 5} {
			v := m * math.Pow(10, d)
			if v >= lo*(1-1e-9) && v <= hi*(1+1e-9) {
				all = append(all, v)
				if m != 2 {
					wide = append(wide, v)
				}
			}
		}
	}

	switch {
	case len(all) < 2:
		return []float64{lo, hi}
	case len(all) > maxLogTicks:
		return wide
	}

	return all
}

// tick formats an axis label, with the precision of the tick step.
func (u Unit) tick(
	v, step float64,
) string {
	switch u {
	case UnitTime:
		return timeLabel(v, stepDigits(step))
	case UnitBitrate:
		return bitrateTick(v)
	case UnitBytes:
		return bytesTick(v)
	}

	// Adding zero turns a rounded -0 into 0.
	return strconv.FormatFloat(math.Round(v*1000)/1000+0, 'f', -1, 64)
}

// Format writes a value of the unit in full, as tooltips do: times to the
// millisecond, bitrates in b/s, kb/s or Mb/s, sizes in B, KiB or MiB.
func (u Unit) Format(
	v float64,
) string {
	switch u {
	case UnitTime:
		return timeLabel(v, TimeDigits)
	case UnitBitrate:
		return bitrateLabel(v)
	case UnitBytes:
		return bytesLabel(v)
	}

	return strconv.FormatFloat(v, 'f', 2, 64)
}

// stepDigits is the number of decimals showing a tick step.
func stepDigits(
	step float64,
) int {
	if step <= 0 || step >= 1 {
		return 0
	}

	return min(3, int(math.Ceil(-math.Log10(step)-1e-9)))
}

// timeLabel is m:ss or h:mm:ss with the given decimals of seconds.
func timeLabel(
	s float64,
	digits int,
) string {
	return clockLabel(s, digits, false)
}

// clockLabel is m:ss with the given decimals of seconds, or h:mm:ss from an
// hour on or when hours is set.
func clockLabel(
	s float64,
	digits int,
	hours bool,
) string {
	scale := math.Pow(10, float64(digits))
	total := math.Round(math.Max(s, 0) * scale)
	whole := int64(total / scale)
	h, m := whole/3600, whole/60%60

	sec := fmt.Sprintf("%02d", whole%60)
	if digits > 0 {
		frac := int64(total) - whole*int64(scale)
		sec += fmt.Sprintf(".%0*d", digits, frac)
	}

	if h > 0 || hours {
		return fmt.Sprintf("%d:%02d:%s", h, m, sec)
	}

	return fmt.Sprintf("%d:%s", m, sec)
}

// bitrateTick is a short bitrate label for axes: 2.5 Mb/s, 500 kb/s.
func bitrateTick(
	bps float64,
) string {
	switch {
	case bps >= 1e6:
		return trimFloat(bps/1e6) + " Mb/s"
	case bps >= 1e3:
		return trimFloat(bps/1e3) + " kb/s"
	}

	return trimFloat(bps) + " b/s"
}

// bytesTick is a short size label for axes: 40 KiB, 1.5 MiB.
func bytesTick(
	b float64,
) string {
	switch {
	case b >= 1<<20:
		return trimFloat(b/(1<<20)) + " MiB"
	case b >= 1<<10:
		return trimFloat(b/(1<<10)) + " KiB"
	}

	return trimFloat(b) + " B"
}

// trimFloat prints v with at most 2 decimals, without trailing zeros.
func trimFloat(
	v float64,
) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

func bitrateLabel(
	bps float64,
) string {
	switch {
	case bps >= 1e6:
		return fmt.Sprintf("%.2f Mb/s", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.0f kb/s", bps/1e3)
	}

	return fmt.Sprintf("%.0f b/s", bps)
}

func bytesLabel(
	b float64,
) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", b/(1<<20))
	case b < 1<<10:
		return fmt.Sprintf("%.0f B", b)
	}

	return fmt.Sprintf("%.1f KiB", b/(1<<10))
}
