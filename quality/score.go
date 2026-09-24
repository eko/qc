package quality

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/quality/xpsnr"
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
// plan with fewer decoded frames wins. Every clip gets one warm-up frame on
// each side so temporal features see real neighbours. Results are returned in
// the order of clips.
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

	plan := planSweep
	runs, seekFrames := r.planRuns(jobs)

	var err error
	if len(runs) >= 2 && float64(seekFrames) <= seekAdvantage*2*float64(r.n) {
		plan = planSeek
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

// work scores queued clips on its own model instances (and XPSNR meter)
// until the queue closes, freeing a slot after each clip.
func (r *run) work(
	queue <-chan *clipJob,
	slots <-chan struct{},
	threads int,
	collect func(clipResult, int),
) error {
	models, err := r.meter.engine.LoadModels(r.models)
	if err != nil {
		return err
	}
	defer models.Close()

	var meter *xpsnr.Meter
	if r.xpsnr {
		meter = xpsnr.New(r.spec.Width, r.spec.Height, r.bitDepth, r.ref.Video.AvgFrameRate.Float(), threads)
	}

	for job := range queue {
		cr, err := r.scoreJob(models, meter, job, threads)
		<-slots

		if err != nil {
			return err
		}

		collect(cr, job.idx)
	}

	return nil
}

// scoreJob scores the pairs of a clip with every model, extractor and XPSNR
// (when meter is set) and keeps the values of the clip frames, dropping the
// warm-up ones.
func (r *run) scoreJob(
	models vmaf.Models,
	meter *xpsnr.Meter,
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

	distortions, pairs, err := r.push(scorer, meter, job)
	if err != nil {
		return clipResult{}, err
	}

	out, err := scorer.Collect()
	if err != nil {
		return clipResult{}, err
	}

	return r.clipFrames(job, out, distortions, pairs)
}

// push feeds the pairs of job to scorer, and to the XPSNR meter when set,
// and returns the XPSNR distortions and the number of pairs scored. On
// failure the remaining pairs are drained.
func (r *run) push(
	scorer vmaf.Scorer,
	meter *xpsnr.Meter,
	job *clipJob,
) ([]xpsnr.Distortion, int, error) {
	if meter != nil {
		meter.Reset()
	}

	var distortions []xpsnr.Distortion

	pairs := 0

	for p := range job.pairs {
		err := scorer.Push(p.ref, p.dist)
		if err == nil && meter != nil {
			if pairs == 0 && job.warmFrom > 0 {
				// The clip starts mid-video: its warm-up frame becomes
				// the history instead of black.
				meter.Prime(p.ref)
			}

			distortions = append(distortions, meter.Measure(p.ref, p.dist))
		}

		p.release()

		if err != nil {
			drainPairs(job.pairs)

			return nil, pairs, err
		}

		pairs++
		r.pairScored()
	}

	return distortions, pairs, nil
}

// clipFrames keeps the values of the clip frames of job, dropping the
// warm-up ones. It fails when fewer frames than the clip starts with were
// decoded.
func (r *run) clipFrames(
	job *clipJob,
	out vmaf.Scores,
	distortions []xpsnr.Distortion,
	pairs int,
) (clipResult, error) {
	c := job.clip
	lo := c.from - job.warmFrom
	hi := min(len(out.VMAF[0]), lo+(c.to-c.from))

	if lo >= hi {
		return clipResult{}, fmt.Errorf("%w: clip %d-%d, %d frames decoded", errShortDecode, c.from, c.to, pairs)
	}

	values := r.clipValues(out, distortions)
	for name, v := range values {
		values[name] = v[lo:hi]
	}

	return clipResult{clip: c, scores: out.VMAF[0][lo:hi], values: values, decoded: 2 * pairs}, nil
}

// clipValues gathers the raw per-pair values of every series of a clip:
// device models, their aliases, libvmaf features and XPSNR distortions.
func (r *run) clipValues(
	out vmaf.Scores,
	distortions []xpsnr.Distortion,
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

	if r.xpsnr {
		for plane, name := range []string{SeriesXPSNRY, SeriesXPSNRU, SeriesXPSNRV} {
			v := make([]float64, len(distortions))
			for i, d := range distortions {
				v[i] = d[plane]
			}

			values[name] = v
		}
	}

	return values
}
