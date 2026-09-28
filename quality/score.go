package quality

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/vmaf"
)

// score scores clips, choosing between two decoding plans:
//
//   - one sweep: each side is decoded once, sequentially, and ffmpeg's select
//     filter only scales and pipes the frames of the clips;
//   - seek runs: clips close to each other are grouped into runs, and each run
//     is decoded on its own from the keyframe before it, several runs in
//     parallel. Frames between runs are never decoded.
//
// Sparse clips on long videos favour runs, dense clips a single sweep; the
// plan with fewer decoded frames wins. With hardware decoding, a sweep is
// split into concurrent runs, and the segments of an exact measurement are
// each decoded by their own run (see plan). Every clip gets warm-up frames
// on each side so temporal features see real neighbours. Results are
// returned in the order of clips.
func (r *run) score(
	ctx context.Context,
	clips []clip,
	workers, threads, round int,
) ([]clipResult, error) {
	if len(clips) == 0 {
		return nil, nil
	}

	jobs := r.newJobs(clips)
	results := make([]clipResult, len(clips))
	collect := func(cr clipResult, idx int) {
		results[idx] = cr
		r.report(cr, round)
	}

	plan, runs := r.plan(jobs, workers)

	var err error
	if runs != nil {
		err = r.seekRuns(ctx, runs, workers, threads, collect)
	} else {
		err = r.sweep(ctx, jobs, window{to: r.n}, workers, threads, collect)
	}

	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.plans[plan]++
	r.mu.Unlock()

	return results, nil
}

// seekRuns decodes and scores runs in parallel. Few runs share the workers;
// many runs each get one.
func (r *run) seekRuns(
	ctx context.Context,
	runs []seekRun,
	workers, threads int,
	collect func(clipResult, int),
) error {
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(workers)

	runWorkers := max(1, workers/len(runs))

	for _, run := range runs {
		group.Go(func() error {
			return r.sweep(gctx, run.jobs, run.window, runWorkers, threads, collect)
		})
	}

	return group.Wait()
}

// work scores queued clips on its own model instances (and pure-Go
// meters) until the queue closes, freeing a slot after each clip.
func (r *run) work(
	queue <-chan *clipJob,
	slots <-chan struct{},
	threads int,
	collect func(clipResult, int),
) error {
	meter := r.newGoMeters(threads)

	score := func(job *clipJob) (clipResult, error) {
		return r.measureJob(meter, job)
	}

	if len(r.models) > 0 {
		models, err := r.meter.engine.LoadModels(r.models)
		if err != nil {
			return err
		}
		defer models.Close()

		score = func(job *clipJob) (clipResult, error) {
			return r.scoreJob(models, meter, job, threads)
		}
	}

	for job := range queue {
		cr, err := score(job)
		<-slots

		if err != nil {
			return err
		}

		collect(cr, job.idx)
	}

	return nil
}

// scoreJob scores the pairs of a clip with every model, extractor and
// pure-Go meter and keeps the values of the clip frames, dropping the
// warm-up ones.
func (r *run) scoreJob(
	models vmaf.Models,
	meter *goMeters,
	job *clipJob,
	threads int,
) (clipResult, error) {
	scorer, err := models.NewScorer(vmaf.ScorerConfig{
		Extractors: r.extractors,
		Width:      r.spec.Width,
		Height:     r.spec.Height,
		BitDepth:   r.bitDepth,
		Threads:    threads,
		Backend:    r.backend.Backend,
	})
	if err != nil {
		drainPairs(job.pairs)

		return clipResult{}, err
	}
	defer scorer.Close()

	pairs, err := r.push(scorer, meter, job)
	if err != nil {
		return clipResult{}, err
	}

	out, err := scorer.Collect()
	if err != nil {
		return clipResult{}, err
	}

	return r.clipFrames(job, out, meter, pairs)
}

// measureJob measures the pairs of a clip with the pure-Go meters alone (a
// pass without VMAF, see hdrMetricsPass) and keeps the values of the clip
// frames.
func (r *run) measureJob(
	meter *goMeters,
	job *clipJob,
) (clipResult, error) {
	meter.reset()

	pairs := 0

	for p := range job.pairs {
		meter.measure(p, pairs == 0 && job.warmFrom > 0)
		p.release()

		pairs++
	}

	c := job.clip
	lo := c.from - job.warmFrom
	hi := min(pairs, lo+(c.to-c.from))

	if lo >= hi {
		return clipResult{}, fmt.Errorf("%w: clip %d-%d, %d frames decoded", errShortDecode, c.from, c.to, pairs)
	}

	values := map[string][]float64{}
	meter.values(values)

	for name, v := range values {
		values[name] = v[lo:hi]
	}

	return clipResult{clip: c, values: values, decoded: 2 * pairs}, nil
}

// push feeds the pairs of job to scorer and to the pure-Go meters, and
// returns the number of pairs scored. On failure the remaining pairs are
// drained.
func (r *run) push(
	scorer vmaf.Scorer,
	meter *goMeters,
	job *clipJob,
) (int, error) {
	meter.reset()

	pairs := 0

	for p := range job.pairs {
		err := scorer.Push(p.ref, p.dist)
		if err == nil {
			// A clip starting mid-video primes XPSNR with its warm-up frame.
			meter.measure(p, pairs == 0 && job.warmFrom > 0)
		}

		// Warm-up frames are not counted as scored.
		scored := job.scores(p.ref.Index)
		p.release()

		if err != nil {
			drainPairs(job.pairs)

			return pairs, err
		}

		if scored {
			r.pairScored()
		}

		pairs++
	}

	return pairs, nil
}

// clipFrames keeps the values of the clip frames of job, dropping the
// warm-up ones. It fails when fewer frames than the clip starts with were
// decoded.
func (r *run) clipFrames(
	job *clipJob,
	out vmaf.Scores,
	meter *goMeters,
	pairs int,
) (clipResult, error) {
	c := job.clip
	lo := c.from - job.warmFrom
	hi := min(len(out.VMAF[0]), lo+(c.to-c.from))

	if lo >= hi {
		return clipResult{}, fmt.Errorf("%w: clip %d-%d, %d frames decoded", errShortDecode, c.from, c.to, pairs)
	}

	values := r.clipValues(out, meter)
	for name, v := range values {
		values[name] = v[lo:hi]
	}

	return clipResult{clip: c, scores: out.VMAF[0][lo:hi], values: values, decoded: 2 * pairs}, nil
}

// clipValues gathers the raw per-pair values of every series of a clip:
// device models, their aliases, libvmaf features and the pure-Go metrics.
func (r *run) clipValues(
	out vmaf.Scores,
	meter *goMeters,
) map[string][]float64 {
	values := map[string][]float64{}

	for i, name := range r.modelSeries {
		if name != "" {
			values[name] = out.VMAF[i]
		}
	}

	for _, name := range r.aliases {
		values[name] = out.VMAF[0]
	}

	featureValues(out.Features, values)
	meter.values(values)

	return values
}
