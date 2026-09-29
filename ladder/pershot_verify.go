package ladder

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/internal/shotalloc"
)

// verifyPerShot encodes the digest with the per-shot settings of its pieces and
// the rung's rate cap, measures it, and compares it to the per-title rung
// at equal quality.
func (b *build) verifyPerShot(
	ctx context.Context,
	i int,
	r *Rung,
	pieces []piece,
	picks []shotalloc.Option,
	curves []Curve,
	total int,
) error {
	name := fmt.Sprintf("pershot-%d", i)
	path := filepath.Join(b.workDir, name+".mp4")

	defer os.Remove(path)

	chunks := make([]encode.Chunk, len(pieces))
	for p, pc := range pieces {
		chunks[p] = b.chunkOf(pc.start, pc.frames, picks[p])
	}

	params := b.params(Probe{Width: r.Width, Height: r.Height}, encode.Params{MaxRate: r.MaxRate, BufSize: r.BufSize})
	if err := b.engine.encoder.EncodeChunks(ctx, b.codec, b.digestSource(), path, chunks, params); err != nil {
		return fmt.Errorf("ladder: per-shot: %s: %w", name, err)
	}

	cmp, err := b.score(ctx, path, scoreRung)
	if err != nil {
		return fmt.Errorf("ladder: per-shot: %s: measure: %w", name, err)
	}

	m := b.leveled(measurementOf(cmp), scoreRung)
	r.PerShot.Measured = &m
	b.measureShots(r.PerShot.Shots, pieces, cmp)

	if r.Measured != nil {
		slope := rungSlope(curves, *r)
		r.PerShot.Gain = equalQualityGain(*r.Measured, m, slope)
	}

	rung := *r
	b.tick(Progress{Stage: StageShots, Total: total, Rung: &rung})

	return nil
}

// measureShots splits a verification encode of the digest into its shots:
// the bitrate of every frame and the VMAF of the scored frames of each
// shot's digest pieces.
func (b *build) measureShots(
	allocs []ShotAllocation,
	pieces []piece,
	cmp *analysis.Comparison,
) {
	sizes := cmp.Distorted.Bitstream.FrameSizes
	owner := make([]int, len(sizes))

	for f := range owner {
		owner[f] = -1
	}

	bytes, frames := make([]int, len(allocs)), make([]int, len(allocs))

	for _, pc := range pieces {
		for f := pc.start; f < pc.start+pc.frames && f < len(sizes); f++ {
			owner[f] = pc.shot
			bytes[pc.shot] += sizes[f]
			frames[pc.shot]++
		}
	}

	scores, scored := make([]float64, len(allocs)), make([]int, len(allocs))

	for _, f := range cmp.VMAF.Frames {
		if f.Index < len(owner) && owner[f.Index] >= 0 {
			scores[owner[f.Index]] += f.Score
			scored[owner[f.Index]]++
		}
	}

	rate := b.video.AvgFrameRate.Float()

	for s := range allocs {
		if frames[s] == 0 {
			continue
		}

		m := &ShotMeasurement{Bitrate: int64(math.Round(float64(bytes[s]*bitsPerByte) * rate / float64(frames[s]))), ScoredFrames: scored[s]}
		if scored[s] > 0 {
			m.VMAF = scores[s] / float64(scored[s])
		}

		allocs[s].Measured = m
	}
}

// rungSlope is dVMAF/d(ln bitrate) of the rung's curve around its bitrate.
func rungSlope(
	curves []Curve,
	r Rung,
) float64 {
	c, ok := curveOf(curves, r.Height)
	if !ok || len(c.logR) < 2 {
		return 0
	}

	x := math.Log(float64(r.Bitrate))
	i, _ := slices.BinarySearch(c.logR, x)
	i = min(max(i, 1), len(c.logR)-1)

	return (c.vmaf[i] - c.vmaf[i-1]) / (c.logR[i] - c.logR[i-1])
}

// equalQualityGain is the bitrate saved by b over a at equal VMAF, as a
// fraction: b's bitrate is moved along the curve (slope dVMAF/dln R) to a's
// quality. Without a slope only the bitrates compare.
func equalQualityGain(
	a, b Measurement,
	slope float64,
) float64 {
	shift := 0.0
	if slope > 0 {
		shift = (a.VMAF - b.VMAF) / slope
	}

	return 1 - float64(b.Bitrate)*math.Exp(shift)/float64(a.Bitrate)
}
