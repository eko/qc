package ladder

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/linalg"
	"github.com/eko/qc/ladder/internal/shotalloc"
)

// shotProbes encodes the digest at every (resolution, CRF) of the plan and
// measures every frame. The probes are encoded chunk by chunk at the
// digest pieces, as the per-shot rungs will be: a chunk restarts rate
// control and lookahead, which costs ~3% bitrate or a few tenths of VMAF at
// equal CRF, and a model fitted on continuous encodes would miss the rung's
// quality by that much.
func (b *build) shotProbes(
	ctx context.Context,
	plan []shotProbe,
	pieces []piece,
	rungs int,
) ([]shotProbe, error) {
	jobs := slices.Clone(plan)
	total := len(jobs) + rungs
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(b.opts.Parallel)

	for i := range jobs {
		group.Go(func() error {
			job := &jobs[i]
			w, h := b.geometry(job.height)
			name := fmt.Sprintf("shots-%d", i)
			path := filepath.Join(b.workDir, name+".mp4")

			defer os.Remove(path)

			chunks := make([]encode.Chunk, len(pieces))
			for p, pc := range pieces {
				chunks[p] = encode.Chunk{Start: pc.start, Frames: pc.frames, CRF: job.crf}
			}

			params := b.params(Probe{Width: w, Height: h, CRF: job.crf}, encode.Params{})
			if err := b.engine.encoder.EncodeChunks(gctx, b.codec, b.digestSource(), path, chunks, params); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}

			cmp, err := b.score(gctx, path, scoreExact)
			if err != nil {
				return fmt.Errorf("%s: measure: %w", name, err)
			}

			job.sizes = cmp.Distorted.Bitstream.FrameSizes
			job.scores = make([]float64, len(job.sizes))

			for _, f := range cmp.VMAF.Frames {
				if f.Index < len(job.scores) {
					job.scores[f.Index] = f.Score
				}
			}

			probe := Probe{Width: w, Height: h, CRF: job.crf, Bitrate: cmp.Distorted.Bitstream.AverageBitrate, VMAF: cmp.VMAF.Mean}
			b.tick(Progress{Stage: StageShots, Total: total, Probe: &probe})

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("ladder: per-shot: %w", err)
	}

	return jobs, nil
}

// probesOfHeight are the per-shot probes of height, by increasing CRF.
func probesOfHeight(
	probes []shotProbe,
	height int,
) []shotProbe {
	var out []shotProbe

	for _, p := range probes {
		if p.height == height {
			out = append(out, p)
		}
	}

	slices.SortFunc(out, func(a, b shotProbe) int { return cmp.Compare(a.crf, b.crf) })

	return out
}

// minBendProbes is the number of probe CRFs a curvature takes: three points
// define a parabola.
const minBendProbes = 3

// titleBend is the curvature in CRF of the title's curves at one
// resolution between the CRFs low and high: the coefficient of CRF² of the
// parabolas fitted, by least squares, to the ln(bitrate) and to the VMAF of
// the per-title probes of that resolution. The probes are those within half
// the gap of low and high, or the three nearest to their middle when fewer
// lie there: curves bend more where quality saturates, and probes far from
// the shots' CRFs would say how the curve bends elsewhere. Without three
// probe CRFs, no curvature.
func titleBend(
	probes []Probe,
	low, high float64,
) shotalloc.Bend {
	mid, reach := (low+high)/2, high-low

	usable := slices.DeleteFunc(slices.Clone(probes), func(p Probe) bool { return p.Bitrate <= 0 })
	slices.SortStableFunc(usable, func(a, b Probe) int {
		return cmp.Compare(math.Abs(a.CRF-mid), math.Abs(b.CRF-mid))
	})

	var (
		xs           [][]float64
		rates, vmafs []float64
		crfs         []float64
	)

	for _, p := range usable {
		// Nearest first: beyond the reach, only what three CRFs still need.
		if math.Abs(p.CRF-mid) > reach && len(crfs) >= minBendProbes {
			break
		}

		if !slices.Contains(crfs, p.CRF) {
			crfs = append(crfs, p.CRF)
		}

		d := p.CRF - mid
		xs = append(xs, []float64{1, d, d * d})
		rates, vmafs = append(rates, math.Log(float64(p.Bitrate))), append(vmafs, p.VMAF)
	}

	if len(crfs) < minBendProbes {
		return shotalloc.Bend{}
	}

	weights := make([]float64, len(xs))
	for i := range weights {
		weights[i] = 1
	}

	return shotalloc.Bend{
		Rate: linalg.Ridge(xs, rates, weights, 0)[2],
		VMAF: linalg.Ridge(xs, vmafs, weights, 0)[2],
	}
}

// pieceModelsOf fits the model of every digest piece through two per-shot
// probes of one resolution, bent as the title's curves are there.
func pieceModelsOf(
	pieces []piece,
	low, high shotProbe,
	rate float64,
	bend shotalloc.Bend,
) []shotalloc.Model {
	models := make([]shotalloc.Model, len(pieces))

	for i, pc := range pieces {
		rateLo, vmafLo := pieceStats(low, pc, rate)
		rateHi, vmafHi := pieceStats(high, pc, rate)

		models[i] = shotalloc.NewModel(low.crf, high.crf, rateLo, rateHi, vmafLo, vmafHi, bend)
	}

	return models
}

// pieceStats returns the bitrate (b/s) and mean VMAF of a piece in a probe.
func pieceStats(
	p shotProbe,
	pc piece,
	rate float64,
) (float64, float64) {
	bytes, vmaf, n := 0, 0.0, 0

	for f := pc.start; f < pc.start+pc.frames && f < len(p.sizes); f++ {
		bytes += p.sizes[f]
		vmaf += p.scores[f]
		n++
	}

	if n == 0 {
		return 1, 0
	}

	return math.Max(float64(bytes*bitsPerByte)*rate/float64(n), 1), vmaf / float64(n)
}

// shotModelsOf gives every shot of the title a model: the blend of its
// digest pieces when it has some, a prediction from its features otherwise.
func shotModelsOf(
	shots []Shot,
	pieces []piece,
	pieceModels []shotalloc.Model,
	features [][]float64,
) ([]shotalloc.Model, []bool) {
	models := make([]shotalloc.Model, len(shots))
	measured := make([]bool, len(shots))

	for s := range shots {
		var ms []shotalloc.Model

		var ws []float64

		for p, pc := range pieces {
			if pc.shot == s {
				ms, ws = append(ms, pieceModels[p]), append(ws, float64(pc.frames))
			}
		}

		if len(ms) > 0 {
			models[s], measured[s] = shotalloc.Blend(ms, shotalloc.Normalise(ws)), true
		}
	}

	return shotalloc.Predict(models, measured, shotalloc.Normalise(shotWeights(shots)), features), measured
}

// shotWeights are the frame counts of shots.
func shotWeights(
	shots []Shot,
) []float64 {
	out := make([]float64, len(shots))
	for i, s := range shots {
		out[i] = float64(s.Frames)
	}

	return out
}

// probesAt returns the probes of height.
func probesAt(
	probes []Probe,
	height int,
) []Probe {
	var out []Probe

	for _, p := range probes {
		if p.Height == height {
			out = append(out, p)
		}
	}

	return out
}
