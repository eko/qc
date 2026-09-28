//go:build arm64 && !purego

package levels

import "github.com/eko/qc/frame"

// statsNEON is the vector loop of addPlane (stats_arm64.s), over a multiple
// of statsLanes samples; the others go through the portable loop.
//
//go:noescape
func statsNEON(p *byte, n int, black, white byte, s *rowStats)

const (
	// statsLanes is the vector width of statsNEON, in samples.
	statsLanes = 16
	// maxStatsSamples bounds one statsNEON call: its out-of-range counters
	// are 8-bit lanes incremented at most once per statsLanes samples.
	maxStatsSamples = 255 * statsLanes
)

// addPlane accumulates the samples of p, sixteen at a time.
func (s *rowStats) addPlane(
	p *frame.Plane,
	black, white byte,
) {
	for y := range p.Height {
		samples := p.Row(y)

		for len(samples) >= statsLanes {
			n := min(len(samples)-len(samples)%statsLanes, maxStatsSamples)
			statsNEON(&samples[0], n, black, white, s)
			samples = samples[n:]
		}

		// The last samples of the row, fewer than a vector.
		for _, v := range samples {
			s.sum += uint64(v)
			s.lo, s.hi = min(s.lo, v), max(s.hi, v)

			switch {
			case v < black:
				s.below++
			case v > white:
				s.above++
			}
		}
	}
}
