package ladder

import (
	"maps"
	"math"
	"slices"

	"github.com/eko/qc/analysis"
)

// shotsOf turns the scene cuts of the analysed source into shots of whole
// GOPs: each cut moves to the nearest GOP boundary; cuts falling on the same
// boundary merge.
func shotsOf(
	cuts []int,
	frames, gop int,
) []Shot {
	bounds := []int{0}

	for _, c := range cuts {
		b := int(math.Round(float64(c)/float64(gop))) * gop
		if b > bounds[len(bounds)-1] && b < frames {
			bounds = append(bounds, b)
		}
	}

	bounds = append(bounds, frames)
	shots := make([]Shot, 0, len(bounds)-1)

	for i := 1; i < len(bounds); i++ {
		shots = append(shots, Shot{Start: bounds[i-1], Frames: bounds[i] - bounds[i-1]})
	}

	return shots
}

// piecesOf splits the digest into the parts of each shot it contains: the
// digest concatenates segments of the title (starting at segment starts, in
// title frames, lasting lengths frames).
func piecesOf(
	shots []Shot,
	starts, lengths []int,
) []piece {
	var out []piece

	offset := 0

	for s, start := range starts {
		end := start + lengths[s]

		for i, shot := range shots {
			a, b := max(start, shot.Start), min(end, shot.Start+shot.Frames)
			if a < b {
				out = append(out, piece{shot: i, start: offset + a - start, frames: b - a})
			}
		}

		offset += lengths[s]
	}

	return out
}

// shotComplexity is what the analysis of the source says of a shot: its
// mean spatial and temporal information, and the bitrate of the source
// over it.
type shotComplexity struct {
	si, ti, logRate float64
}

// shotComplexities reads the complexity of every shot off the analysis of
// the source: the frame-weighted mean of the scenes a shot (a run of whole
// GOPs) overlaps.
func shotComplexities(
	report *analysis.Report,
	shots []Shot,
) []shotComplexity {
	out := make([]shotComplexity, len(shots))

	for i, s := range shots {
		var (
			c      shotComplexity
			weight float64
		)

		for _, sr := range report.Video.Shots {
			first, last := sr.FirstFrame, sr.LastFrame+1
			overlap := float64(min(last, s.Start+s.Frames) - max(first, s.Start))

			if overlap > 0 {
				c.logRate += overlap * math.Log(math.Max(float64(sr.Bitrate), 1))
				c.si += overlap * sr.SIMean
				c.ti += overlap * sr.TIMean
				weight += overlap
			}
		}

		weight = math.Max(weight, 1)
		out[i] = shotComplexity{si: c.si / weight, ti: c.ti / weight, logRate: c.logRate / weight}
	}

	return out
}

// tiFloor keeps the logarithm of the temporal information of a still shot
// finite.
const tiFloor = 0.5

// shotFeatures are the regressors predicting the model of a shot the digest
// misses: a constant and the logarithms of its mean spatial and temporal
// information. In replays of the allocation on a title encoded whole, they
// did as well as or better than the source's bitrate and the temporal
// information the predictions used before, and do not depend on how the
// mezzanine was encoded: a near-constant-bitrate source says nothing of
// its shots (see docs/validation.md).
func shotFeatures(
	complexities []shotComplexity,
) [][]float64 {
	features := make([][]float64, len(complexities))

	for i, c := range complexities {
		features[i] = []float64{1, math.Log1p(math.Max(c.si, 0)), math.Log(math.Max(c.ti, 0) + tiFloor)}
	}

	return features
}

// crfGrid lists the CRFs a shot may take around lo..hi, one spread beyond
// each side, at the encoder's granularity within the codec's range: the
// shot models are only trusted near their probes.
func (b *build) crfGrid(
	lo, hi, spread float64,
) []float64 {
	step := b.codec.Step()

	var grid []float64
	for crf := b.roundCRF(lo - spread); crf <= b.roundCRF(hi+spread)+1e-9; crf += step {
		grid = append(grid, crf)
	}

	return grid
}

// shotHeights are the rung resolutions, highest first.
func shotHeights(
	rungs []Rung,
) []int {
	var out []int

	for _, r := range rungs {
		if !slices.Contains(out, r.Height) {
			out = append(out, r.Height)
		}
	}

	slices.Sort(out)
	slices.Reverse(out)

	return out
}

// candidateHeights are the resolutions a shot of a rung at height may take:
// the rung's own, and with Options.PerShotResolution the rung resolutions
// just above and below it (the ones the probes are widened to reach).
func (b *build) candidateHeights(
	height int,
	heights []int,
) []int {
	if !b.opts.PerShotResolution {
		return []int{height}
	}

	i := slices.Index(heights, height)

	return heights[max(i-1, 0):min(i+2, len(heights))]
}

// neighbourRanges widens the CRF range of every rung resolution to the
// qualities of the rungs of the neighbouring resolutions, read on the
// resolution's own curve: a shot of those rungs may move to it.
func (b *build) neighbourRanges(
	rungs []Rung,
	curves []Curve,
	heights []int,
	ranges map[int][2]float64,
) map[int][2]float64 {
	out := maps.Clone(ranges)

	for _, h := range heights {
		// Rungs are placed on the curves: every rung resolution has one.
		c, _ := curveOf(curves, h)

		neighbours := b.candidateHeights(h, heights)

		for _, r := range rungs {
			if r.Height == h || !slices.Contains(neighbours, r.Height) {
				continue
			}

			bitrate, _ := c.BitrateFor(r.PredictedVMAF)
			crf := b.roundCRF(c.CRFAt(bitrate))
			cur := out[h]
			out[h] = [2]float64{math.Min(cur[0], crf), math.Max(cur[1], crf)}
		}
	}

	return out
}

// maxProbeGapShare bounds the CRF gap between two per-shot probes of one
// resolution, in spreads: the log-bitrate line of the shot models bends
// past about a dozen x264 CRF.
const maxProbeGapShare = 3

// shotProbePlan places the per-shot probes of every resolution: its CRF
// range widened by spread on both sides, at both ends and, when the range
// is wider than maxProbeGapShare spreads (neighbour qualities), evenly
// inside. Probes beyond two per resolution are reported as extra.
func (b *build) shotProbePlan(
	heights []int,
	ranges map[int][2]float64,
	spread float64,
) ([]shotProbe, *ShotProbing) {
	var plan []shotProbe

	probing := &ShotProbing{Resolution: b.opts.PerShotResolution}

	for _, h := range heights {
		lo, hi := b.roundCRF(ranges[h][0]-spread), b.roundCRF(ranges[h][1]+spread)
		gaps := 1
		if b.opts.PerShotResolution {
			gaps = max(1, int(math.Ceil((hi-lo)/(maxProbeGapShare*spread)-1e-9)))
		}

		var crfs []float64

		for g := range gaps + 1 {
			crf := b.roundCRF(lo + (hi-lo)*float64(g)/float64(gaps))
			if !slices.Contains(crfs, crf) {
				crfs = append(crfs, crf)
			}
		}

		for _, crf := range crfs {
			plan = append(plan, shotProbe{height: h, crf: crf})
		}

		probing.Extra += max(len(crfs)-2, 0)
	}

	probing.Probes = len(plan)

	return plan, probing
}

// describeShots records the complexity of every shot the reports show.
func describeShots(
	shots []Shot,
	complexities []shotComplexity,
) {
	for i, c := range complexities {
		if c.logRate > 0 {
			shots[i].SourceBitrate = int64(math.Round(math.Exp(c.logRate)))
		}

		shots[i].TI = c.ti
	}
}

// cuts returns the first frame of every shot but the first.
func cuts(
	report *analysis.Report,
) []int {
	out := make([]int, 0, len(report.Video.Shots))
	for _, s := range report.Video.Shots[1:] {
		out = append(out, s.FirstFrame)
	}

	return out
}

// titleFrames is the number of frames of the title: the decoded count, or
// the duration at the frame rate.
func (b *build) titleFrames(
	report *analysis.Report,
) int {
	if report.Video.FramesDecoded > 0 {
		return report.Video.FramesDecoded
	}

	last := report.Video.Shots[len(report.Video.Shots)-1]

	return last.LastFrame + 1
}

// crfRanges returns, per rung height, the lowest and highest rung CRF.
func crfRanges(
	rungs []Rung,
) map[int][2]float64 {
	out := map[int][2]float64{}

	for _, r := range rungs {
		cur, ok := out[r.Height]
		if !ok {
			cur = [2]float64{r.CRF, r.CRF}
		}

		out[r.Height] = [2]float64{math.Min(cur[0], r.CRF), math.Max(cur[1], r.CRF)}
	}

	return out
}
