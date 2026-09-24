package quality

import "slices"

// Decoding plans, as counted in Result.Plans.
const (
	planSweep = "sweep"
	planSeek  = "seek"
)

const (
	// seekAdvantage is how much fewer frames the seek plan must decode to be
	// preferred. Runs decode in parallel, which more than pays for their
	// process starts (measured: 18% fewer frames gave a 16% faster
	// measurement on a 10-minute title).
	seekAdvantage = 0.9
	// runStartCost is the decoding cost of starting a run, in frames.
	runStartCost = 12
	// reorderMargin exceeds the frame reordering depth of common encoders:
	// runs start decoding that many frames before the previous keyframe, in
	// the GOP before it, so that open-GOP leading pictures are decoded.
	reorderMargin = 4
	// warmUpFrames are decoded and scored on each side of a clip, then
	// dropped, so that temporal features (motion) see real neighbours.
	warmUpFrames = 1
)

// clipJob is a clip being scored: the dispatcher feeds its pairs, a worker
// scores them. [warmFrom, warmTo) is the clip with its warm-up frames.
type clipJob struct {
	clip     clip
	warmFrom int
	warmTo   int
	pairs    chan pair
	// idx is the position of the clip in the slice given to score.
	idx int
}

// window is a frame range decoded by one pass, [from, to). A seek pass
// starts decoding at seek (≤ from): frames in between are decoded and
// dropped.
type window struct {
	from, to int
	seek     int
}

// seekRun is a group of clips decoded together after one seek.
type seekRun struct {
	window
	jobs []*clipJob
}

// newJobs wraps clips into jobs sorted by position, their range widened by
// the warm-up frames.
func (r *run) newJobs(
	clips []clip,
) []*clipJob {
	jobs := make([]*clipJob, len(clips))

	for i, c := range clips {
		from, to := max(0, c.from-warmUpFrames), min(r.n, c.to+warmUpFrames)
		jobs[i] = &clipJob{
			clip:     c,
			warmFrom: from,
			warmTo:   to,
			pairs:    make(chan pair, min(to-from, pairBuffer)),
			idx:      i,
		}
	}

	slices.SortFunc(jobs, func(a, b *clipJob) int { return a.warmFrom - b.warmFrom })

	return jobs
}

// planRuns groups jobs (sorted) into runs: a clip joins the previous run when
// decoding through the gap costs less than seeking again from a keyframe. It
// returns the runs and the frames the seek plan would decode on both sides.
func (r *run) planRuns(
	jobs []*clipJob,
) ([]seekRun, int) {
	refKeys := framesAt(r.ref.Bitstream.PTS, r.ref.Bitstream.Keyframes)
	distKeys := framesAt(r.ref.Bitstream.PTS, r.dist.Bitstream.Keyframes)

	// With open GOPs, frames just before a keyframe in display order are
	// decoded after it and reference the previous GOP: a run must start
	// decoding one GOP earlier. seekFrom is the first frame a run starting
	// at i outputs; ffmpeg then decodes from the keyframe before it.
	seekFrom := func(i int) int {
		return max(0, min(keyBefore(refKeys, i), keyBefore(distKeys, i))-reorderMargin)
	}

	// seekCost is the frames decoded before reaching frame i on both sides.
	seekCost := func(i int) int {
		s := seekFrom(i)

		return i - keyBefore(refKeys, s) + i - keyBefore(distKeys, s) + 2*runStartCost
	}

	var runs []seekRun

	for _, job := range jobs {
		if n := len(runs); n > 0 {
			last := &runs[n-1]
			if gap := job.warmFrom - last.to; gap <= 0 || 2*gap < seekCost(job.warmFrom) {
				last.to = max(last.to, job.warmTo)
				last.jobs = append(last.jobs, job)

				continue
			}
		}

		runs = append(runs, seekRun{window: window{from: job.warmFrom, to: job.warmTo}, jobs: []*clipJob{job}})
	}

	total := 0

	for i := range runs {
		runs[i].seek = seekFrom(runs[i].from)
		total += seekCost(runs[i].from) + 2*(runs[i].to-runs[i].from)
	}

	return runs, total
}

// keyBefore returns the last keyframe index not after i (0 when none).
func keyBefore(
	keys []int,
	i int,
) int {
	k, found := slices.BinarySearch(keys, i)
	if found {
		return i
	}

	if k == 0 {
		return 0
	}

	return keys[k-1]
}

// outputFrames is the number of frames a pass outputs. ffmpeg's -frames:v
// counts output frames, i.e. after select: bounding a pass by it stops
// decoding once the last selected frame is out instead of at the end of the
// file, and keeps a longer video from outputting more frames than the other.
func outputFrames(
	w window,
	selection [][2]int,
) int {
	if selection == nil {
		return w.to - w.seek
	}

	total := 0
	for _, s := range selection {
		total += s[1] - s[0]
	}

	return total
}

// selectRanges merges the warm ranges of jobs (sorted by warmFrom), relative
// to the window start. A nil result selects every frame of the window, which
// lets ffmpeg skip the select filter.
func selectRanges(
	jobs []*clipJob,
	w window,
) [][2]int {
	var out [][2]int

	for _, job := range jobs {
		from, to := job.warmFrom-w.seek, job.warmTo-w.seek

		if len(out) > 0 && from <= out[len(out)-1][1] {
			out[len(out)-1][1] = max(out[len(out)-1][1], to)

			continue
		}

		out = append(out, [2]int{from, to})
	}

	if len(out) == 1 && out[0] == [2]int{0, w.to - w.seek} {
		return nil
	}

	return out
}
