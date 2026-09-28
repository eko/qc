package quality

import "slices"

// Decoding plans, as counted in Result.Plans.
const (
	planSweep = "sweep"
	planSeek  = "seek"
	// planSegments scores every frame in concurrent segments (see
	// exactPlan).
	planSegments = "segments"
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

// scores reports whether the frame at index is scored, i.e. not one of the
// warm-up frames of the job.
func (j *clipJob) scores(
	index int,
) bool {
	return index >= j.clip.from && index < j.clip.to
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

	lead := r.warmUp()

	for i, c := range clips {
		from, to := max(0, c.from-lead), min(r.n, c.to+warmUpFrames)
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

// plan picks how jobs (sorted) are decoded: exact segments each decoded by
// its own run, seek runs when they decode fewer frames than a sweep, or a
// sweep, split into concurrent runs of about equal length when decodes
// are hardware sessions. It returns the plan and its runs, none for a
// single sweep of the whole video.
func (r *run) plan(
	jobs []*clipJob,
	workers int,
) (string, []seekRun) {
	seekFrom, seekCost := r.seekPoints()

	if r.segments {
		runs := make([]seekRun, len(jobs))
		for i, job := range jobs {
			runs[i] = seekRun{window: window{from: job.warmFrom, to: job.warmTo, seek: seekFrom(job.warmFrom)}, jobs: []*clipJob{job}}
		}

		return planSegments, runs
	}

	runs, seekFrames := planRuns(jobs, seekFrom, seekCost)
	if len(runs) >= 2 && float64(seekFrames) <= seekAdvantage*2*float64(r.n) {
		return planSeek, runs
	}

	if count := min(workers*runsPerWorker, r.n/minRunFrames); r.decoders > 1 && count > 1 {
		if runs := r.sweepRuns(jobs, count, seekFrom); len(runs) > 1 {
			return planSweep, runs
		}
	}

	return planSweep, nil
}

// seekPoints returns, for a run starting at frame i, the first frame it
// outputs (ffmpeg then decodes from the keyframe before it) and the frames
// decoded before reaching i on both sides.
func (r *run) seekPoints() (seekFrom, seekCost func(i int) int) {
	refKeys := framesAt(r.ref.Bitstream.PTS, r.ref.Bitstream.Keyframes)
	distKeys := framesAt(r.ref.Bitstream.PTS, r.dist.Bitstream.Keyframes)

	// With open GOPs, frames just before a keyframe in display order are
	// decoded after it and reference the previous GOP: a run must start
	// decoding one GOP earlier.
	seekFrom = func(i int) int {
		return max(0, min(keyBefore(refKeys, i), keyBefore(distKeys, i))-reorderMargin)
	}

	seekCost = func(i int) int {
		s := seekFrom(i)

		return i - keyBefore(refKeys, s) + i - keyBefore(distKeys, s) + 2*runStartCost
	}

	return seekFrom, seekCost
}

// planRuns groups jobs (sorted) into runs: a clip joins the previous run when
// decoding through the gap costs less than seeking again from a keyframe. It
// returns the runs and the frames the seek plan would decode on both sides.
func planRuns(
	jobs []*clipJob,
	seekFrom, seekCost func(i int) int,
) ([]seekRun, int) {
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

// sweepRuns splits a sweep of jobs (sorted) into runs of consecutive jobs
// covering about equal parts of the video, up to count of them. Each run
// is decoded on its own from the keyframe before it (see seekPoints), and
// only the frames of its jobs are piped, as in the sweep.
func (r *run) sweepRuns(
	jobs []*clipJob,
	count int,
	seekFrom func(i int) int,
) []seekRun {
	var runs []seekRun

	last := -1

	for _, job := range jobs {
		if part := job.warmFrom * count / r.n; part != last {
			last = part
			runs = append(runs, seekRun{window: window{from: job.warmFrom, to: job.warmTo, seek: seekFrom(job.warmFrom)}})
		}

		run := &runs[len(runs)-1]
		run.to = max(run.to, job.warmTo)
		run.jobs = append(run.jobs, job)
	}

	return runs
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
