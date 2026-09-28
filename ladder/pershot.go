package ladder

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/internal/shotalloc"
)

// StageShots reports the per-shot stage: shot detection, per-shot probes and
// the verification of the per-shot rungs.
const StageShots = "shots"

// ErrNoShots is returned when per-shot encoding finds no shot to allocate.
var ErrNoShots = errors.New("no shot detected")

// shotSpreadShare sets how far around the rungs' CRFs each resolution's two
// per-shot probes go, as a share of the codec's probe CRF span (≈4 x264
// CRF, 7 SVT-AV1 CRF): shots move a few CRF from the title's.
const shotSpreadShare = 0.3

// bitsPerByte converts packet sizes.
const bitsPerByte = 8

// Shot is a run of whole GOPs of the title that per-shot encoding gives one
// CRF: scene cuts are moved to the nearest GOP boundary, so every rung keeps
// its keyframes on the fixed GOP grid.
type Shot struct {
	Start  int `json:"start"`
	Frames int `json:"frames"`
	// Measured is set when the digest covers part of the shot: its model is
	// measured rather than predicted from similar shots.
	Measured bool `json:"measured,omitempty"`
	// SourceBitrate (b/s, geometric mean over the source's scenes) and TI
	// (mean temporal information) describe the shot's complexity: they are
	// the features predicting the models of the shots the digest misses.
	SourceBitrate int64   `json:"sourceBitrate,omitempty"`
	TI            float64 `json:"ti,omitempty"`
}

// PerShot is the per-shot version of a rung: one CRF per shot, at equal
// rate-quality slope, with the same pooled quality as the rung.
type PerShot struct {
	// Chunks are the shots of the title with their CRFs (adjacent shots
	// sharing a CRF are merged).
	Chunks []encode.Chunk `json:"chunks"`
	// Lambda is the common slope dVMAF/dbitrate of every shot, in VMAF per
	// Mb/s.
	Lambda float64 `json:"lambda"`
	// PredictedVMAF and PredictedBitrate are the model's pooled values on
	// the digest.
	PredictedVMAF    float64 `json:"predictedVmaf"`
	PredictedBitrate int64   `json:"predictedBitrate"`
	// Measured is the verification encode of the digest with per-shot CRFs.
	Measured *Measurement `json:"measured,omitempty"`
	// Gain is the bitrate saved against the per-title rung at equal VMAF
	// (both verified on the digest), as a fraction: the VMAF difference
	// between the two is turned into bitrate by the slope of the rung's
	// curve.
	Gain    float64 `json:"gain"`
	Command string  `json:"command"`
	// Width and Height are the resolution the rendition declares (its
	// largest) when its shots change resolution (Options.PerShotResolution):
	// players see a rendition of the rung's bitrate with this RESOLUTION.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Shots is the allocation of every shot of the title (Result.Shots, in
	// the same order): with the other rungs', the per-shot ladder.
	Shots []ShotAllocation `json:"shots,omitempty"`
}

// ShotAllocation is one cell of the per-shot ladder: the CRF a per-shot
// rung gives a shot, and its expected cost and quality.
type ShotAllocation struct {
	CRF float64 `json:"crf"`
	// Width and Height are the shot's resolution when shots pick theirs
	// (Options.PerShotResolution); the rung's otherwise.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// PredictedBitrate (b/s) and PredictedVMAF are the shot's model at CRF,
	// without the rung's rate cap: fitted on the digest when the shot is
	// Measured, predicted from similar shots otherwise.
	PredictedBitrate int64   `json:"predictedBitrate"`
	PredictedVMAF    float64 `json:"predictedVmaf"`
	// Measured is the shot's part of the verification encode, when the rung
	// was verified and the digest covers the shot.
	Measured *ShotMeasurement `json:"measured,omitempty"`
}

// ShotProbing reports what per-shot rungs cost in exact probes of the
// digest, on top of the per-title ladder and one verification per rung.
type ShotProbing struct {
	// Resolution is set when shots pick their resolution too.
	Resolution bool `json:"resolution,omitempty"`
	// Probes are the chunked encodes of the digest, every frame scored,
	// fitting the shot models. Extra of them widen the models of a
	// resolution to the qualities of the neighbouring resolutions' rungs.
	Probes int `json:"probes"`
	Extra  int `json:"extra,omitempty"`
}

// ShotMeasurement is the part of a verification encode falling in one shot
// (its digest pieces, each encoded at its own CRF).
type ShotMeasurement struct {
	// Bitrate is measured on every frame of the pieces.
	Bitrate int64 `json:"bitrate"`
	// VMAF is the mean of the scored frames among them: verification
	// samples frames, so a short piece may have none (ScoredFrames 0).
	VMAF         float64 `json:"vmaf,omitempty"`
	ScoredFrames int     `json:"scoredFrames,omitempty"`
}

// piece is the part of a shot inside one digest segment, in digest frames.
type piece struct {
	shot   int
	start  int
	frames int
}

// shotProbe is a per-shot probe: an exact measurement of the digest at one
// resolution and CRF, with per-frame sizes and scores.
type shotProbe struct {
	height int
	crf    float64
	sizes  []int
	scores []float64
}

// shotLevel is what per-shot allocation knows of one resolution between two
// adjacent per-shot probes: the models of the digest pieces and of the
// shots, exact through both probes, and the CRFs they are trusted at (the
// probes' segment, one half spread beyond the outer probes). Two probes
// give one level; widened ranges (per-shot resolution) give one level per
// segment, so that every model stays local.
type shotLevel struct {
	low, high     float64
	pieces, shots []shotalloc.Model
	grid          []float64
}

// levelAt returns the level whose probes bracket crf, or the nearest one.
func levelAt(
	levels []shotLevel,
	crf float64,
) shotLevel {
	for _, l := range levels {
		if crf <= l.high {
			return l
		}
	}

	return levels[len(levels)-1]
}

// shotPlan is what per-shot allocation works on: the shots of the title,
// the digest's pieces of them, and the per-shot probes to encode.
type shotPlan struct {
	shots    []Shot
	pieces   []piece
	features [][]float64
	// heights are the rung resolutions, highest first, and ranges their
	// CRF ranges (widened to the neighbours' qualities with
	// Options.PerShotResolution).
	heights []int
	ranges  map[int][2]float64
	// spread is how far beyond its range a resolution is probed.
	spread  float64
	probes  []shotProbe
	probing *ShotProbing
	// pieceWeights and shotWeights are the frame shares of the pieces in
	// the digest and of the shots in the title.
	pieceWeights, shotWeights []float64
}

// perShot turns every rung into its per-shot version (see docs/ladder.md):
// shots from the scene cuts of the source, exact probes of the digest per
// rung resolution giving every digest piece its model, models of the other
// shots predicted from their analysis features, then for each rung the
// slope λ whose allocation reaches the rung's quality on the digest,
// applied to every shot of the title, and a verification encode of the
// digest with the per-shot settings. With Options.PerShotResolution, each
// shot also picks its resolution among the rung's and its neighbours'.
func (b *build) perShot(
	ctx context.Context,
	rungs []Rung,
	curves []Curve,
	probes []Probe,
	digest Digest,
) ([]Shot, *ShotProbing, error) {
	b.resetProgress()

	plan, err := b.planShots(ctx, rungs, curves, digest)
	if err != nil {
		return nil, nil, err
	}

	measured, err := b.shotProbes(ctx, plan.probes, plan.pieces, len(rungs))
	if err != nil {
		return nil, nil, err
	}

	levels := b.fitLevels(plan, probes, measured)
	total := len(plan.probes) + len(rungs)

	for i := range rungs {
		picks := b.allocateRung(i, &rungs[i], plan, levels)

		if !b.opts.SkipVerify {
			if err := b.verifyPerShot(ctx, i, &rungs[i], plan.pieces, picks, curves, total); err != nil {
				return nil, nil, err
			}
		}
	}

	return plan.shots, plan.probing, nil
}

// planShots analyses the source into shots of whole GOPs, cuts the digest
// into their pieces, and places the per-shot probes of every rung
// resolution.
func (b *build) planShots(
	ctx context.Context,
	rungs []Rung,
	curves []Curve,
	digest Digest,
) (*shotPlan, error) {
	// The shots only: the audio has no part in the ladder.
	report, err := b.engine.inspector.Analyze(ctx, b.source, analysis.Options{Audio: analysis.AudioOptions{Skip: true}})
	if err != nil {
		return nil, fmt.Errorf("ladder: per-shot: analyse %s: %w", b.source, err)
	}

	if report.Video == nil || len(report.Video.Shots) == 0 {
		return nil, fmt.Errorf("ladder: per-shot: %w", ErrNoShots)
	}

	rate := b.video.AvgFrameRate.Float()
	plan := &shotPlan{shots: shotsOf(cuts(report), b.titleFrames(report), b.gop())}

	starts, lengths := make([]int, len(digest.Segments)), make([]int, len(digest.Segments))
	for i, s := range digest.Segments {
		starts[i], lengths[i] = int(math.Round(s.Start.Seconds()*rate)), int(math.Round(s.Length().Seconds()*rate))
	}

	plan.pieces = piecesOf(plan.shots, starts, lengths)
	plan.features = shotFeatures(report, plan.shots)
	describeShots(plan.shots, plan.features)

	plan.spread = shotSpreadShare * (b.codec.ProbeCRFs[len(b.codec.ProbeCRFs)-1] - b.codec.ProbeCRFs[0])
	plan.heights = shotHeights(rungs)
	plan.ranges = crfRanges(rungs)

	if b.opts.PerShotResolution {
		plan.ranges = b.neighbourRanges(rungs, curves, plan.heights, plan.ranges)
	}

	plan.probes, plan.probing = b.shotProbePlan(plan.heights, plan.ranges, plan.spread)

	pieceFrames := make([]float64, len(plan.pieces))
	for p, pc := range plan.pieces {
		pieceFrames[p] = float64(pc.frames)
	}

	plan.pieceWeights, plan.shotWeights = shotalloc.Normalise(pieceFrames), shotalloc.Normalise(shotWeights(plan.shots))

	return plan, nil
}

// fitLevels fits, for every rung resolution and every two adjacent per-shot
// probes of it, the models of the digest pieces and of the shots, and marks
// the shots the digest measures.
func (b *build) fitLevels(
	plan *shotPlan,
	probes []Probe,
	measured []shotProbe,
) map[int][]shotLevel {
	rate := b.video.AvgFrameRate.Float()
	levels := map[int][]shotLevel{}

	for _, h := range plan.heights {
		q := fitCurve(probesAt(probes, h), defaultPrior).mean[2]
		set := probesOfHeight(measured, h)

		for k := range len(set) - 1 {
			low, high := set[k], set[k+1]
			pieceModels := pieceModelsOf(plan.pieces, low, high, rate, q)
			shotModels, covered := shotModelsOf(plan.shots, plan.pieces, pieceModels, plan.features)

			// The outer ends reach half a spread beyond the widened range,
			// as with two probes.
			from, to := low.crf, high.crf
			if k == 0 {
				from = plan.ranges[h][0] - plan.spread - plan.spread/2
			}

			if k == len(set)-2 {
				to = plan.ranges[h][1] + plan.spread + plan.spread/2
			}

			levels[h] = append(levels[h], shotLevel{low: low.crf, high: high.crf, pieces: pieceModels, shots: shotModels, grid: b.crfGrid(from, to, 0)})

			for s := range plan.shots {
				plan.shots[s].Measured = plan.shots[s].Measured || covered[s]
			}
		}
	}

	return levels
}

// allocateRung gives rung r (the i-th) its per-shot version: the slope λ
// whose allocation of the digest pieces reaches the rung's quality, applied
// to every shot of the title. It returns the pieces' settings, which the
// verification encodes.
func (b *build) allocateRung(
	i int,
	r *Rung,
	plan *shotPlan,
	levels map[int][]shotLevel,
) []shotalloc.Option {
	// The target is the rung itself seen through the same models (every
	// shot at the rung's CRF): the models' biases (no rate cap, every frame
	// scored) then cancel between the two allocations, where aiming at the
	// rung's measured VMAF left per-shot rungs ~1 VMAF short.
	own := shotalloc.Menu(levelAt(levels[r.Height], r.CRF).pieces, r.Height, []float64{r.CRF})
	target := shotalloc.Allocate(own, plan.pieceWeights, 0).VMAF

	var pieceMenus, shotMenus [][][]shotalloc.Option

	for _, h := range b.candidateHeights(r.Height, plan.heights) {
		for _, l := range levels[h] {
			pieceMenus = append(pieceMenus, shotalloc.Menu(l.pieces, h, l.grid))
			shotMenus = append(shotMenus, shotalloc.Menu(l.shots, h, l.grid))
		}
	}

	digestAlloc := shotalloc.SolveLambda(shotalloc.JoinMenus(pieceMenus...), plan.pieceWeights, target)
	titleAlloc := shotalloc.Allocate(shotalloc.JoinMenus(shotMenus...), plan.shotWeights, digestAlloc.Lambda)

	ps := &PerShot{
		Chunks:           b.mergeChunks(plan.shots, titleAlloc.Picks),
		Lambda:           digestAlloc.Lambda,
		PredictedVMAF:    digestAlloc.VMAF,
		PredictedBitrate: int64(math.Round(digestAlloc.Bitrate)),
		Shots:            b.shotAllocations(titleAlloc.Picks),
	}

	if b.opts.PerShotResolution {
		ps.Width, ps.Height = declaredGeometry(ps.Chunks)
	}

	// The command encodes the source: its video may start after the
	// container's timeline, unlike the digest's.
	source := encode.ChunkSource{Path: b.source, Rate: b.video.AvgFrameRate, Origin: b.origin}
	ps.Command = b.codec.ChunkCommandLine(source, fmt.Sprintf("%02d-%dp-pershot.mp4", i+1, r.Height),
		ps.Chunks, encode.Params{Width: r.Width, Height: r.Height, Preset: b.opts.Preset, GOP: b.gop(),
			MaxRate: r.MaxRate, BufSize: r.BufSize, BitDepth: b.opts.BitDepth, Signal: b.signal})

	r.PerShot = ps

	return digestAlloc.Picks
}

// PooledBitrate is the predicted bitrate of the per-shot rung over the whole
// title: its shots' predicted bitrates weighted by their frames (0 without
// per-shot allocations). A shot's bitrate over it tells how much more or
// less than the rung's average the shot costs.
func (p *PerShot) PooledBitrate(
	shots []Shot,
) float64 {
	var bits, frames float64

	for i, a := range p.Shots {
		if i < len(shots) {
			bits += float64(a.PredictedBitrate) * float64(shots[i].Frames)
			frames += float64(shots[i].Frames)
		}
	}

	if frames == 0 {
		return 0
	}

	return bits / frames
}
