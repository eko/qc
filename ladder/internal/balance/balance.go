// Package balance is the pure math of a content-aware digest: it places the
// segments of a digest so that the frames they hold look, on average, like
// the frames of the whole title (Segments), or on its most complex
// stretches (Top). The ladder engine analyses the title; this package only
// computes, so every step is table-testable.
package balance

import (
	"math"
	"sort"

	"github.com/eko/qc/media"
)

// maxSweeps bounds the local search. It converges in a few sweeps: every
// move lowers the imbalance.
const maxSweeps = 50

// minGain is the relative drop of the imbalance worth moving a segment for:
// below it, a segment stays where systematic sampling puts it.
const minGain = 1e-9

// Frames are the frames of a title in presentation order, with the features
// a digest is balanced on.
type Frames struct {
	// PTS is the presentation time of every frame, from the first one.
	PTS []media.Duration
	// Features holds one series per feature, a value per frame.
	Features [][]float64
}

// usable reports whether every feature has a value for every frame.
func (f Frames) usable() bool {
	if len(f.PTS) == 0 || len(f.Features) == 0 {
		return false
	}

	for _, series := range f.Features {
		if len(series) != len(f.PTS) {
			return false
		}
	}

	return true
}

// Means returns the mean of every feature over the frames inside the
// intervals (over every frame without intervals), and false when they hold
// no frame.
func (f Frames) Means(
	intervals ...media.Interval,
) ([]float64, bool) {
	if !f.usable() {
		return nil, false
	}

	sums := newPrefixSums(f.Features)
	total, frames := make([]float64, len(f.Features)), 0

	if len(intervals) == 0 {
		intervals = []media.Interval{{Start: f.PTS[0], End: f.PTS[len(f.PTS)-1] + 1}}
	}

	for _, iv := range intervals {
		from, to := f.frameAt(iv.Start), f.frameAt(iv.End)
		frames += to - from

		for k := range total {
			total[k] += sums.sum(k, from, to)
		}
	}

	if frames == 0 {
		return nil, false
	}

	for k := range total {
		total[k] /= float64(frames)
	}

	return total, true
}

// leadShare is how long before its first frame a segment starts, as a
// share of the gap to the previous frame: a quarter, so that a seek to the
// start lands on that frame, and the start times the frame rate rounds to
// its index, whichever way either is rounded.
const leadShare = 4

// startBefore returns where a segment whose first frame is frame i starts
// (see leadShare).
func (f Frames) startBefore(
	i int,
) media.Duration {
	if i == 0 {
		return f.PTS[0]
	}

	return f.PTS[i] - (f.PTS[i]-f.PTS[i-1])/leadShare
}

// frameAt returns the index of the first frame at or after t.
func (f Frames) frameAt(
	t media.Duration,
) int {
	return sort.Search(len(f.PTS), func(i int) bool { return f.PTS[i] >= t })
}

// Segments places count segments of length segment over a title of the
// given duration, one in each of count equal slots of the title, as
// systematic sampling does: every part of the title stays represented. Each
// segment is then moved inside its slot until the features averaged over
// the frames of the segments equal those of the whole title. It returns nil
// when the frames cannot place every segment (a slot without a frame, or
// shorter than a segment).
//
// The search is a deterministic coordinate descent from the centred
// segments: one slot at a time, the segment moves to the start minimising
// the imbalance, the sum over the features of the squared difference
// between the digest's mean and the title's, in units of the spread of that
// feature between candidate segments.
func Segments(
	frames Frames,
	duration, segment media.Duration,
	count int,
) []media.Interval {
	if !frames.usable() || count <= 0 || segment <= 0 {
		return nil
	}

	slots := candidates(frames, duration, segment, count)
	if slots == nil {
		return nil
	}

	picks := search(slots, len(frames.Features))
	out := make([]media.Interval, count)

	for i, slot := range slots {
		start := frames.startBefore(slot.first + picks[i])
		out[i] = media.Interval{Start: start, End: start + segment}
	}

	return out
}

// slot holds the candidate segments of one slot of the title: those whose
// first frames are first, first+1… and which lie inside the slot.
type slot struct {
	first int
	// centre is the candidate closest to the centred segment.
	centre int
	// deviations holds, per candidate, the difference between its mean
	// features and the title's, each in units of the feature's spread.
	deviations [][]float64
}

// candidates lists the candidate segments of every slot with their
// deviations from the title, or nil when a slot has none.
func candidates(
	frames Frames,
	duration, segment media.Duration,
	count int,
) []slot {
	sums := newPrefixSums(frames.Features)
	step := duration / media.Duration(count)
	slots := make([]slot, count)

	for i := range slots {
		from, to := media.Duration(i)*step, media.Duration(i+1)*step
		centred := from + (step-segment)/2
		s := slot{first: frames.frameAt(from)}

		// The first frame of the slot whose segment starts inside it.
		if s.first > 0 && s.first < len(frames.PTS) && frames.startBefore(s.first) < from {
			s.first++
		}

		for j := s.first; j < len(frames.PTS) && frames.startBefore(j)+segment <= to; j++ {
			start := frames.startBefore(j)
			end := frames.frameAt(start + segment)
			means := make([]float64, len(frames.Features))

			for k := range means {
				means[k] = sums.sum(k, j, end) / float64(end-j)
			}

			if start <= centred {
				s.centre = j - s.first
			}

			s.deviations = append(s.deviations, means)
		}

		if len(s.deviations) == 0 {
			return nil
		}

		slots[i] = s
	}

	standardise(slots, sums, len(frames.PTS))

	return slots
}

// standardise turns the mean features of every candidate into deviations
// from the title's mean, in units of their spread across the candidates. A
// feature that does not vary weighs nothing.
func standardise(
	slots []slot,
	sums prefixSums,
	frames int,
) {
	for k := range sums {
		title := sums.sum(k, 0, frames) / float64(frames)

		var sum, squares float64

		n := 0

		for _, s := range slots {
			for _, means := range s.deviations {
				d := means[k] - title
				sum += d
				squares += d * d
				n++
			}
		}

		mean := sum / float64(n)
		spread := math.Sqrt(max(squares/float64(n)-mean*mean, 0))

		for _, s := range slots {
			for _, means := range s.deviations {
				if spread > 0 {
					means[k] = (means[k] - title) / spread
				} else {
					means[k] = 0
				}
			}
		}
	}
}

// search returns the candidate picked in every slot.
func search(
	slots []slot,
	features int,
) []int {
	picks := make([]int, len(slots))
	total := make([]float64, features)

	for i, s := range slots {
		picks[i] = s.centre
		add(total, s.deviations[s.centre], 1)
	}

	for range maxSweeps {
		moved := false

		for i, s := range slots {
			add(total, s.deviations[picks[i]], -1)

			best, cost := picks[i], imbalance(total, s.deviations[picks[i]])
			threshold := cost * (1 - minGain)

			for j, deviation := range s.deviations {
				if c := imbalance(total, deviation); c < threshold && c < cost {
					best, cost = j, c
				}
			}

			moved = moved || best != picks[i]
			picks[i] = best
			add(total, s.deviations[best], 1)
		}

		if !moved {
			break
		}
	}

	return picks
}

// imbalance is the squared norm of rest + deviation.
func imbalance(
	rest, deviation []float64,
) float64 {
	var sum float64

	for k, r := range rest {
		d := r + deviation[k]
		sum += d * d
	}

	return sum
}

// add adds sign × deviation to total.
func add(
	total, deviation []float64,
	sign float64,
) {
	for k, d := range deviation {
		total[k] += sign * d
	}
}

// prefixSums holds, per feature, the running sum of its series: the sum
// over frames [from, to) is one subtraction.
type prefixSums [][]float64

func newPrefixSums(
	features [][]float64,
) prefixSums {
	out := make(prefixSums, len(features))

	for k, series := range features {
		out[k] = make([]float64, len(series)+1)
		for i, v := range series {
			out[k][i+1] = out[k][i] + v
		}
	}

	return out
}

func (p prefixSums) sum(
	feature, from, to int,
) float64 {
	return p[feature][to] - p[feature][from]
}

// Top places count segments of length segment on the most complex stretches
// of the title: candidate segments are ranked by score, computed from the
// means of their features, and taken from the highest down, never two
// overlapping. cuts are the shot cuts of the title: a shot gives one
// segment at most, so that the segments show different scenes, unless the
// title has too few shots to fill the digest. Segments come back in the
// order of the title; nil when the frames cannot place every segment.
func Top(
	frames Frames,
	cuts []media.Duration,
	segment media.Duration,
	count int,
	score func(means []float64) float64,
) []media.Interval {
	if !frames.usable() || len(frames.PTS) < 2 || count <= 0 || segment <= 0 {
		return nil
	}

	ranked := frames.ranked(segment, score)
	picked := make([]media.Interval, 0, count)
	shots := map[int]bool{}

	// The first pass takes one segment per shot; the second fills what is
	// left from shots already shown.
	for _, perShot := range []bool{true, false} {
		for _, c := range ranked {
			if len(picked) == count {
				break
			}

			shot := sort.Search(len(cuts), func(i int) bool { return cuts[i] > c.Start+segment/2 })
			if (perShot && shots[shot]) || overlaps(picked, c) {
				continue
			}

			picked, shots[shot] = append(picked, c), true
		}
	}

	if len(picked) < count {
		return nil
	}

	sort.Slice(picked, func(i, j int) bool { return picked[i].Start < picked[j].Start })

	return picked
}

// ranked lists every segment of the title starting on a frame and ending
// with the video, from the highest score down, the earliest first among
// equals.
func (f Frames) ranked(
	segment media.Duration,
	score func(means []float64) float64,
) []media.Interval {
	sums := newPrefixSums(f.Features)
	last := len(f.PTS) - 1
	end := f.PTS[last] + (f.PTS[last]-f.PTS[0])/media.Duration(last)

	var (
		out    []media.Interval
		scores []float64
	)

	for j := 0; j <= last && f.startBefore(j)+segment <= end; j++ {
		start := f.startBefore(j)
		to := f.frameAt(start + segment)
		means := make([]float64, len(f.Features))

		for k := range means {
			means[k] = sums.sum(k, j, to) / float64(to-j)
		}

		out = append(out, media.Interval{Start: start, End: start + segment})
		scores = append(scores, score(means))
	}

	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}

	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })

	sorted := make([]media.Interval, len(out))
	for i, o := range order {
		sorted[i] = out[o]
	}

	return sorted
}

// overlaps reports whether c shares time with one of the picked segments.
func overlaps(
	picked []media.Interval,
	c media.Interval,
) bool {
	for _, p := range picked {
		if c.Start < p.End && p.Start < c.End {
			return true
		}
	}

	return false
}
