package ladder

import (
	"math"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/internal/shotalloc"
)

// shotAllocations are the per-shot ladder cells of a rung: every shot's
// setting and its model there. Resolutions are recorded when shots pick
// theirs.
func (b *build) shotAllocations(
	picks []shotalloc.Option,
) []ShotAllocation {
	out := make([]ShotAllocation, len(picks))

	for i, o := range picks {
		out[i] = ShotAllocation{CRF: o.CRF, PredictedBitrate: int64(math.Round(o.Rate)), PredictedVMAF: o.VMAF}
		if b.opts.PerShotResolution {
			out[i].Width, out[i].Height = b.geometry(o.Height)
		}
	}

	return out
}

// declaredGeometry is the resolution a rendition whose chunks change
// resolution declares in a manifest: its largest.
func declaredGeometry(
	chunks []encode.Chunk,
) (int, int) {
	w, h := 0, 0

	for _, c := range chunks {
		if c.Height > h {
			w, h = c.Width, c.Height
		}
	}

	return w, h
}

// mergeChunks turns shots and their settings into encoder chunks, merging
// adjacent shots sharing them. Chunks carry their resolution when shots
// pick theirs.
func (b *build) mergeChunks(
	shots []Shot,
	picks []shotalloc.Option,
) []encode.Chunk {
	var out []encode.Chunk

	for i, s := range shots {
		c := b.chunkOf(s.Start, s.Frames, picks[i])

		if n := len(out); n > 0 && out[n-1].CRF == c.CRF && out[n-1].Height == c.Height {
			out[n-1].Frames += s.Frames

			continue
		}

		out = append(out, c)
	}

	return out
}

// chunkOf is the chunk of frames encoded with option o.
func (b *build) chunkOf(
	start, frames int,
	o shotalloc.Option,
) encode.Chunk {
	c := encode.Chunk{Start: start, Frames: frames, CRF: o.CRF}
	if b.opts.PerShotResolution {
		c.Width, c.Height = b.geometry(o.Height)
	}

	return c
}
