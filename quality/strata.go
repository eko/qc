package quality

import (
	"math/rand/v2"
	"slices"

	"github.com/eko/qc/media"
)

// buildStrata splits frames [0, n) into strata of about targetFrames made of
// whole GOPs of the distorted stream: encoders insert keyframes at scene
// cuts, so strata boundaries follow shots without decoding anything. GOPs
// longer than twice the target are split evenly, and a trailing stratum
// shorter than two clips is merged into its predecessor. Each stratum is cut
// into clip slots of clipFrames.
func buildStrata(
	n int,
	pts []media.Duration,
	keyframes []media.Duration,
	targetFrames int,
	clipFrames int,
) []*stratum {
	targetFrames = max(targetFrames, 2*clipFrames)

	bounds := append([]int{0}, framesAt(pts, keyframes)...)
	bounds = append(bounds, n)
	slices.Sort(bounds)
	bounds = slices.Compact(bounds)

	merged := mergeShort(gopRanges(bounds, n, targetFrames), clipFrames)

	strata := make([]*stratum, len(merged))
	for i, r := range merged {
		strata[i] = newStratum(r[0], r[1], clipFrames)
	}

	return strata
}

// gopRanges groups the GOPs delimited by the sorted frame bounds into ranges
// of at least targetFrames, splitting GOPs longer than twice the target
// evenly. The frames left after the last full range form a last range.
func gopRanges(
	bounds []int,
	n int,
	targetFrames int,
) [][2]int {
	var (
		ranges [][2]int
		start  = 0
	)

	for i := 1; i < len(bounds); i++ {
		a, b := bounds[i-1], min(bounds[i], n)
		if a >= b {
			continue
		}

		if b-a >= 2*targetFrames {
			if a > start {
				ranges = append(ranges, [2]int{start, a})
			}

			parts := (b - a + targetFrames/2) / targetFrames
			for p := range parts {
				ranges = append(ranges, [2]int{a + (b-a)*p/parts, a + (b-a)*(p+1)/parts})
			}

			start = b

			continue
		}

		if b-start >= targetFrames {
			ranges = append(ranges, [2]int{start, b})
			start = b
		}
	}

	if start < n {
		ranges = append(ranges, [2]int{start, n})
	}

	return ranges
}

// mergeShort merges the ranges too short to hold two clips into their
// predecessor, in place.
func mergeShort(
	ranges [][2]int,
	clipFrames int,
) [][2]int {
	merged := ranges[:0]
	for _, r := range ranges {
		if len(merged) > 0 && r[1]-r[0] < 2*clipFrames {
			merged[len(merged)-1][1] = r[1]

			continue
		}

		merged = append(merged, r)
	}

	return merged
}

// newStratum cuts [first, last) into slots of clipFrames; the last slot takes
// the remainder so every frame belongs to a slot.
func newStratum(
	first, last, clipFrames int,
) *stratum {
	s := &stratum{first: first, last: last}

	count := max(1, (last-first)/clipFrames)
	for i := range count {
		from := first + i*clipFrames
		to := from + clipFrames

		if i == count-1 {
			to = last
		}

		s.slots = append(s.slots, [2]int{from, to})
	}

	return s
}

// framesAt maps times to the index of the frame presented at that time.
func framesAt(
	pts []media.Duration,
	times []media.Duration,
) []int {
	out := make([]int, 0, len(times))

	for _, t := range times {
		i, _ := slices.BinarySearch(pts, t)
		if i < len(pts) {
			out = append(out, i)
		}
	}

	return out
}

// seedMix derives the second PCG word from the seed (the 64-bit golden ratio,
// as in SplitMix64).
const seedMix = 0x9e3779b97f4a7c15

// slotPicker draws slots without replacement, in a random order fixed by seed
// so that runs are reproducible.
type slotPicker struct {
	orders map[*stratum][]int
	rng    *rand.Rand
}

func newSlotPicker(
	seed uint64,
) *slotPicker {
	// Sampling needs reproducibility, not unpredictability.
	rng := rand.New(rand.NewPCG(seed, seed^seedMix)) //nolint:gosec // seeded on purpose

	return &slotPicker{orders: map[*stratum][]int{}, rng: rng}
}

// next returns the next slot to sample in s, i.e. the one at position
// len(s.sampled) of the stratum's random order.
func (p *slotPicker) next(
	s *stratum,
) int {
	order, ok := p.orders[s]
	if !ok {
		order = p.rng.Perm(len(s.slots))
		p.orders[s] = order
	}

	return order[len(s.sampled)]
}
