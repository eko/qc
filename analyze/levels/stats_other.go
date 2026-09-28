//go:build !arm64 || purego

package levels

import "github.com/eko/qc/frame"

// addPlane accumulates the samples of p from their histogram: counting
// samples per value keeps the loop to one increment per sample.
func (s *rowStats) addPlane(
	p *frame.Plane,
	black, white byte,
) {
	for v, n := range histogram(p) {
		if n == 0 {
			continue
		}

		value := byte(v)
		s.sum += uint64(v) * n
		s.lo, s.hi = min(s.lo, value), max(s.hi, value)

		switch {
		case value < black:
			s.below += n
		case value > white:
			s.above += n
		}
	}
}

// histogram counts the samples of an 8-bit plane per value. Four partial
// histograms, summed at the end, let consecutive equal samples (flat areas)
// increment different counters instead of waiting on each other.
func histogram(
	p *frame.Plane,
) [256]uint64 {
	var parts [4][256]uint32

	for y := range p.Height {
		row := p.Row(y)

		x := 0
		for ; x+4 <= len(row); x += 4 {
			px := row[x : x+4 : x+4]
			parts[0][px[0]]++
			parts[1][px[1]]++
			parts[2][px[2]]++
			parts[3][px[3]]++
		}

		for _, v := range row[x:] {
			parts[0][v]++
		}
	}

	var hist [256]uint64
	for v := range hist {
		hist[v] = uint64(parts[0][v]) + uint64(parts[1][v]) + uint64(parts[2][v]) + uint64(parts[3][v])
	}

	return hist
}
