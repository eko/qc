package ladder

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// RenditionEncoder encodes the renditions of a ladder: the whole title
// with a rung's settings, reporting its progress (encode.FFmpeg implements
// it).
type RenditionEncoder interface {
	EncodeRendition(
		ctx context.Context,
		codec encode.Codec,
		spec encode.RenditionSpec,
	) error
}

// ErrNoRenditionEncoder is returned when renditions are asked of an engine
// built without a RenditionEncoder.
var ErrNoRenditionEncoder = errors.New("encoding renditions needs a rendition encoder (see WithRenditionEncoder)")

// WithRenditionEncoder gives the engine what encoding the renditions of a
// ladder (Engine.Encode) needs.
func WithRenditionEncoder(
	enc RenditionEncoder,
) Option {
	return func(e *Engine) {
		e.renditions = enc
	}
}

// Rendition is a rung of the ladder encoded on the whole title.
type Rendition struct {
	// Rung is the index of the rung in Result.Rungs.
	Rung int `json:"rung"`
	// PerShot is set for the per-shot version of the rung.
	PerShot bool   `json:"perShot,omitempty"`
	Path    string `json:"path"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	// Bitrate is the rendition's average bitrate over the whole title.
	Bitrate int64 `json:"bitrate"`
	// Checked is the rendition measured against the source when checked
	// (RenditionOptions.Check): its VMAF over the whole title, with its
	// confidence interval, where the rung's was predicted on the digest.
	Checked *Measurement   `json:"checked,omitempty"`
	Elapsed media.Duration `json:"elapsed"`
}

// Prediction is what the ladder predicted, on the digest, for rendition rd
// of res: the rung's quality and bitrate, or its per-shot version's
// (pooled over the title's shots).
func (res *Result) Prediction(
	rd Rendition,
) (vmaf, bitrate float64) {
	r := res.Rungs[rd.Rung]
	if rd.PerShot && r.PerShot != nil {
		return r.PerShot.PredictedVMAF, r.PerShot.PooledBitrate(res.Shots)
	}

	return r.PredictedVMAF, float64(r.Bitrate)
}

// RenditionProgress reports the encoding of the renditions: the frames
// written over all of them, and the rendition just completed, if any.
type RenditionProgress struct {
	Done, Total int
	Rendition   *Rendition
}

// RenditionOptions configures Engine.Encode.
type RenditionOptions struct {
	// Dir receives the renditions, named like the rungs' commands
	// (01-1080p.mp4, 01-1080p-pershot.mp4).
	Dir string
	// SkipPerShot leaves out the per-shot versions of the rungs.
	SkipPerShot bool
	// Check, when set, measures every rendition against the source with
	// these quality options (a precision rather than every frame keeps it
	// fast on long titles).
	Check *quality.Options
	// Parallel is the number of renditions encoded at once. Default 2.
	Parallel int
	// Progress, when set, is called as the renditions advance. Calls never
	// overlap.
	Progress func(RenditionProgress)
}

// RungParams are the settings rung r of the ladder is encoded with, those
// of its command.
func (res *Result) RungParams(
	r Rung,
) encode.Params {
	p := res.renditionParams(r)
	p.CRF = r.CRF

	if res.Grain != nil {
		p.FilmGrain = res.Grain.Level
	}

	return p
}

// renditionParams are the settings every rendition of rung r shares: its
// resolution and rate cap, and the ladder's preset, GOP, depth and signal.
func (res *Result) renditionParams(
	r Rung,
) encode.Params {
	p := encode.Params{
		Width: r.Width, Height: r.Height, Preset: res.Preset, GOP: res.GOP,
		MaxRate: r.MaxRate, BufSize: r.BufSize, BitDepth: res.BitDepth,
	}

	if res.HDR != nil {
		p.Signal = res.HDR.Signal
	}

	return p
}

// renditionJob is one rendition to encode.
type renditionJob struct {
	rendition Rendition
	params    encode.Params
	chunks    []encode.Chunk
}

// renditionJobs lists the renditions of res: every rung, and its per-shot
// version unless skipped.
func renditionJobs(
	res *Result,
	opts RenditionOptions,
) []renditionJob {
	var jobs []renditionJob

	for i, r := range res.Rungs {
		jobs = append(jobs, renditionJob{
			rendition: Rendition{Rung: i, Width: r.Width, Height: r.Height, Path: filepath.Join(opts.Dir, fmt.Sprintf("%02d-%dp.mp4", i+1, r.Height))},
			params:    res.RungParams(r),
		})

		if ps := r.PerShot; ps != nil && !opts.SkipPerShot {
			width, height := r.Width, r.Height
			if ps.Height > 0 {
				width, height = ps.Width, ps.Height
			}

			jobs = append(jobs, renditionJob{
				rendition: Rendition{Rung: i, PerShot: true, Width: width, Height: height, Path: filepath.Join(opts.Dir, fmt.Sprintf("%02d-%dp-pershot.mp4", i+1, r.Height))},
				params:    res.renditionParams(r),
				chunks:    ps.Chunks,
			})
		}
	}

	return jobs
}

// Encode encodes the renditions of the ladder res of source on the whole
// title, into opts.Dir, checks them against the source when asked, and
// records them in res.Renditions.
func (e *Engine) Encode(
	ctx context.Context,
	source string,
	res *Result,
	opts RenditionOptions,
) ([]Rendition, error) {
	if e.renditions == nil {
		return nil, ErrNoRenditionEncoder
	}

	video, duration, err := usableVideo(res.Source)
	if err != nil {
		return nil, err
	}

	jobs := renditionJobs(res, opts)
	frames := int(math.Round(duration.Seconds() * video.AvgFrameRate.Float()))
	src := encode.ChunkSource{Path: source, Rate: video.AvgFrameRate, Origin: videoOrigin(res.Source, video)}
	run := &renditionRun{engine: e, res: res, source: src, opts: opts, done: make([]int, len(jobs)), total: frames * len(jobs)}

	out := make([]Rendition, len(jobs))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(cmp.Or(max(opts.Parallel, 0), defaultParallel))

	for i, job := range jobs {
		group.Go(func() error {
			r, err := run.encode(gctx, i, job)
			out[i] = r

			return err
		})
	}

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("ladder: renditions: %w", err)
	}

	res.Renditions = out

	return out, nil
}

// renditionRun is the state of one Encode.
type renditionRun struct {
	engine *Engine
	res    *Result
	source encode.ChunkSource
	opts   RenditionOptions

	// mu guards done and serialises Progress calls.
	mu    sync.Mutex
	done  []int
	total int
}

// encode encodes job i, measures it and reports it.
func (r *renditionRun) encode(
	ctx context.Context,
	i int,
	job renditionJob,
) (Rendition, error) {
	started := time.Now()
	rendition := job.rendition

	spec := encode.RenditionSpec{
		Source: r.source, Destination: rendition.Path, Params: job.params, Chunks: job.chunks,
		Progress: func(frames int) { r.report(i, frames, nil) },
	}

	if err := r.engine.renditions.EncodeRendition(ctx, r.res.Codec, spec); err != nil {
		return Rendition{}, fmt.Errorf("%s: %w", filepath.Base(rendition.Path), err)
	}

	encoded, err := r.engine.inspector.Analyze(ctx, rendition.Path, analysis.Options{SkipVideo: true})
	if err != nil {
		return Rendition{}, fmt.Errorf("%s: inspect: %w", filepath.Base(rendition.Path), err)
	}

	if encoded.Bitstream != nil {
		rendition.Bitrate = encoded.Bitstream.AverageBitrate
	}

	if r.opts.Check != nil {
		cmp, err := r.engine.inspector.Compare(ctx, r.source.Path, rendition.Path, analysis.CompareOptions{
			Reference: r.res.Source, Distorted: encoded, Quality: *r.opts.Check,
		})
		if err != nil {
			return Rendition{}, fmt.Errorf("%s: check: %w", filepath.Base(rendition.Path), err)
		}

		m := measurementOf(cmp)
		m.Bitrate = rendition.Bitrate
		rendition.Checked = &m
	}

	rendition.Elapsed = media.Duration(time.Since(started))
	r.report(i, r.total/max(len(r.done), 1), &rendition)

	return rendition, nil
}

// report records that rendition i wrote frames frames and reports the
// total, with the rendition just completed when set.
func (r *renditionRun) report(
	i, frames int,
	done *Rendition,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.done[i] = frames

	if r.opts.Progress == nil {
		return
	}

	total := 0
	for _, n := range r.done {
		total += n
	}

	r.opts.Progress(RenditionProgress{Done: min(total, r.total), Total: r.total, Rendition: done})
}
