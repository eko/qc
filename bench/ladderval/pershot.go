package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"slices"
	"text/tabwriter"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf/libvmaf"
)

// ErrNoPerShot is returned when per-shot checks run on a ladder built
// without --per-shot.
var ErrNoPerShot = errors.New("the ladder has no per-shot rungs (qc ladder --per-shot)")

// lambdaSweep is the number of slopes swept to trace the per-shot optimum.
const lambdaSweep = 400

// frames is a full-title encode measured frame by frame.
type frames struct {
	bitrate float64
	vmaf    float64
	sizes   []int
	scores  []float64
}

// frameMeasureFunc encodes the full title with p, chunk by chunk when chunks
// are given, and measures every frame.
type frameMeasureFunc func(p encode.Params, chunks []encode.Chunk) (frames, error)

// newFrameMeasure returns a frameMeasureFunc encoding into out with the
// ladder's preset and GOP, scoring every frame.
func newFrameMeasure(
	ctx context.Context,
	fast *ladder.Result,
	codec encode.Codec,
	out string,
	opts options,
) frameMeasureFunc {
	dec := decode.NewFFmpeg(opts.ffmpeg, 0)
	analyzer := analysis.New(slog.New(slog.DiscardHandler),
		probe.NewFFprobe(opts.ffprobe), bitstream.NewFFprobeReader(opts.ffprobe), dec, quality.NewMeter(dec, libvmaf.NewEngine()))
	enc := encode.NewFFmpeg(opts.ffmpeg)

	source := fast.Source.Info.Path
	video, _ := fast.Source.Info.PrimaryVideo()
	gop := int(math.Round(2 * video.AvgFrameRate.Float()))

	return func(p encode.Params, chunks []encode.Chunk) (frames, error) {
		p.Preset, p.GOP = fast.Preset, gop

		var err error
		if chunks == nil {
			err = enc.Encode(ctx, codec, source, out, p)
		} else {
			err = enc.EncodeChunks(ctx, codec, source, out, video.AvgFrameRate, chunks, p)
		}

		if err != nil {
			return frames{}, fmt.Errorf("encode %dp crf %g: %w", p.Height, p.CRF, err)
		}

		if err := retime(ctx, opts.ffmpeg, codec, out, video.AvgFrameRate); err != nil {
			return frames{}, err
		}

		cmp, err := analyzer.Compare(ctx, source, out, analysis.CompareOptions{
			Reference: fast.Source,
			Quality:   quality.Options{Exact: true},
		})
		if err != nil {
			return frames{}, fmt.Errorf("measure %dp crf %g: %w", p.Height, p.CRF, err)
		}

		return framesOf(cmp), nil
	}
}

// framesOf extracts the per-frame sizes and scores of a comparison.
func framesOf(
	cmp *analysis.Comparison,
) frames {
	f := frames{
		bitrate: float64(cmp.Distorted.Bitstream.AverageBitrate),
		vmaf:    cmp.VMAF.Mean,
		sizes:   cmp.Distorted.Bitstream.FrameSizes,
		scores:  make([]float64, len(cmp.Distorted.Bitstream.FrameSizes)),
	}

	for _, s := range cmp.VMAF.Frames {
		if s.Index < len(f.scores) {
			f.scores[s.Index] = s.Score
		}
	}

	return f
}

// validatePerShot runs the per-shot checks opts asks for.
func validatePerShot(
	stdout, stderr io.Writer,
	fast *ladder.Result,
	codec encode.Codec,
	measure frameMeasureFunc,
	opts options,
) error {
	if opts.perShot {
		if err := checkPerShot(stdout, fast, codec, opts.shotRungs, measure); err != nil {
			return err
		}
	}

	if opts.optimum > 0 {
		return checkShotOptimum(stdout, stderr, fast, opts.optimum, opts.shotSpan, opts.shotStep, opts.shotResolutions, measure)
	}

	return nil
}

// checkPerShot encodes, for the first n per-shot rungs (all when n is 0),
// the full title with each rung's per-title and
// per-shot settings (plus the per-title rung one CRF step lower, which gives
// the local slope of the curve), and reports the bitrate per-shot saves at
// equal pooled VMAF.
func checkPerShot(
	w io.Writer,
	fast *ladder.Result,
	codec encode.Codec,
	n int,
	measure frameMeasureFunc,
) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "rung\tres\tper-title VMAF\tper-title bitrate\tper-shot VMAF\tper-shot bitrate\tgain at equal VMAF\tdigest gain\t")

	var gains []float64

	for i, r := range fast.Rungs {
		if r.PerShot == nil || (n > 0 && len(gains) == n) {
			continue
		}

		title, err := measure(rungParams(r), nil)
		if err != nil {
			return err
		}

		lower := rungParams(r)
		lower.CRF -= slopeStep(codec)

		richer, err := measure(lower, nil)
		if err != nil {
			return err
		}

		shots, err := measure(rungParams(r), r.PerShot.Chunks)
		if err != nil {
			return err
		}

		gain := equalQualityGain(title, shots, localSlope(title, richer))
		gains = append(gains, gain)

		fmt.Fprintf(tw, "%d\t%dp\t%.2f\t%.0f\t%.2f\t%.0f\t%.1f%%\t%.1f%%\t\n",
			i+1, r.Height, title.vmaf, title.bitrate, shots.vmaf, shots.bitrate, gain*100, r.PerShot.Gain*100)
	}

	if len(gains) == 0 {
		return ErrNoPerShot
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	fmt.Fprintf(w, "\nmean bitrate saved by per-shot at equal VMAF (full title): %.1f%%\n", mean(gains)*100)

	return nil
}

// slopeStep is the CRF step used to measure a curve's local slope.
func slopeStep(
	codec encode.Codec,
) float64 {
	if codec.Name == "av1" {
		return 4
	}

	return 2
}

// localSlope is dVMAF/d(ln bitrate) between two encodes.
func localSlope(
	a, b frames,
) float64 {
	if a.bitrate == b.bitrate {
		return 0
	}

	return (b.vmaf - a.vmaf) / (math.Log(b.bitrate) - math.Log(a.bitrate))
}

// equalQualityGain is the bitrate b saves over a at a's VMAF, b's bitrate
// moved along a curve of the given slope (dVMAF/d ln R).
func equalQualityGain(
	a, b frames,
	slope float64,
) float64 {
	shift := 0.0
	if slope > 0 {
		shift = (a.vmaf - b.vmaf) / slope
	}

	return 1 - b.bitrate*math.Exp(shift)/a.bitrate
}

// setting is one (resolution, CRF) of the per-shot grid.
type setting struct {
	width, height int
	crf           float64
}

// shotTable is the bitrate (b/s) and mean VMAF of every shot at every
// setting of the grid.
type shotTable struct {
	settings []setting
	rates    [][]float64
	vmafs    [][]float64
	weights  []float64
}

// measureShots encodes the whole title at each setting (shot by shot, as
// per-shot encoding does) and reads every shot's bitrate and VMAF.
func measureShots(
	log io.Writer,
	fast *ladder.Result,
	r ladder.Rung,
	settings []setting,
	measure frameMeasureFunc,
) (shotTable, error) {
	video, _ := fast.Source.Info.PrimaryVideo()
	rate := video.AvgFrameRate.Float()
	table := shotTable{settings: settings}

	total := 0
	for _, s := range fast.Shots {
		total += s.Frames
	}

	for _, s := range fast.Shots {
		table.weights = append(table.weights, float64(s.Frames)/float64(total))
	}

	for _, st := range settings {
		chunks := make([]encode.Chunk, len(fast.Shots))
		for i, s := range fast.Shots {
			chunks[i] = encode.Chunk{Start: s.Start, Frames: s.Frames, CRF: st.crf}
		}

		p := rungParams(r)
		p.Width, p.Height, p.CRF = st.width, st.height, st.crf

		f, err := measure(p, chunks)
		if err != nil {
			return shotTable{}, err
		}

		rates, vmafs := make([]float64, len(fast.Shots)), make([]float64, len(fast.Shots))

		for i, s := range fast.Shots {
			bytes, score, n := 0, 0.0, 0

			for k := s.Start; k < s.Start+s.Frames && k < len(f.sizes); k++ {
				bytes += f.sizes[k]
				score += f.scores[k]
				n++
			}

			rates[i] = float64(bytes*8) * rate / math.Max(float64(n), 1)
			vmafs[i] = score / math.Max(float64(n), 1)
		}

		table.rates = append(table.rates, rates)
		table.vmafs = append(table.vmafs, vmafs)
		fmt.Fprintf(log, "shots %dp crf %g: %.0f b/s VMAF %.2f\n", st.height, st.crf, f.bitrate, f.vmaf)
	}

	return table, nil
}

// pooled is the frame-weighted bitrate and VMAF of choosing setting index
// choice[i] for shot i.
func (t shotTable) pooled(
	choice []int,
) (float64, float64) {
	rate, vmaf := 0.0, 0.0

	for i, c := range choice {
		rate += t.weights[i] * t.rates[c][i]
		vmaf += t.weights[i] * t.vmafs[c][i]
	}

	return rate, vmaf
}

// optimumAt is the lowest pooled bitrate reaching vmaf with one setting per
// shot, among the settings at height (every setting when height is 0): the
// Lagrangian allocations of the measured table (the convex hull the Dynamic
// Optimizer searches, over resolution and CRF when every setting counts),
// interpolated in log bitrate between the two hull points around vmaf as a
// single CRF is between grid CRFs. NaN when out of reach.
func (t shotTable) optimumAt(
	vmaf float64,
	height int,
) float64 {
	type point struct{ rate, vmaf float64 }

	var hull []point

	for k := range lambdaSweep {
		lambda := math.Exp(math.Log(1e-4) + (math.Log(1e4)-math.Log(1e-4))*float64(k)/float64(lambdaSweep-1))
		choice := make([]int, len(t.weights))

		for i := range t.weights {
			value := math.Inf(-1)

			for c, st := range t.settings {
				if height != 0 && st.height != height {
					continue
				}

				if v := t.vmafs[c][i] - lambda*t.rates[c][i]/1e6; v > value {
					choice[i], value = c, v
				}
			}
		}

		rate, v := t.pooled(choice)
		hull = append(hull, point{rate, v})
	}

	best := math.NaN()

	for _, a := range hull {
		for _, b := range hull {
			if a.vmaf > vmaf || b.vmaf < vmaf || b.vmaf <= a.vmaf {
				continue
			}

			f := (vmaf - a.vmaf) / (b.vmaf - a.vmaf)
			if rate := math.Exp(math.Log(a.rate) + f*(math.Log(b.rate)-math.Log(a.rate))); math.IsNaN(best) || rate < best {
				best = rate
			}
		}
	}

	return best
}

// perTitleAt is the bitrate of a single CRF at height for every shot
// reaching vmaf, interpolated in log bitrate between the measured CRFs
// (ascending in the grid). NaN when out of reach.
func (t shotTable) perTitleAt(
	vmaf float64,
	height int,
) float64 {
	type point struct{ rate, vmaf float64 }

	var points []point

	for c, st := range t.settings {
		if st.height != height {
			continue
		}

		choice := make([]int, len(t.weights))
		for i := range choice {
			choice[i] = c
		}

		var p point
		p.rate, p.vmaf = t.pooled(choice)
		points = append(points, p)
	}

	for c := 1; c < len(points); c++ {
		hi, lo := points[c-1], points[c]
		if lo.vmaf <= vmaf && vmaf <= hi.vmaf && hi.vmaf > lo.vmaf {
			f := (vmaf - lo.vmaf) / (hi.vmaf - lo.vmaf)

			return math.Exp(math.Log(lo.rate) + f*(math.Log(hi.rate)-math.Log(lo.rate)))
		}
	}

	return math.NaN()
}

// shotGrid is the per-shot grid of a rung: CRFs span either side of the
// rung's, every step, at its resolution; with resolutions, the same span
// around the CRF reaching the rung's quality on the probe curve of each
// neighbouring rung resolution (the ones per-shot resolution may pick).
func shotGrid(
	fast *ladder.Result,
	r ladder.Rung,
	span, step float64,
	resolutions bool,
) []setting {
	heights := []int{r.Height}
	if resolutions {
		heights = neighbourHeights(fast.Rungs, r.Height)
	}

	var out []setting

	for _, h := range heights {
		center, width := r.CRF, r.Width

		if h != r.Height {
			var probes []ladder.Probe

			for _, p := range fast.Probes {
				if p.Height == h {
					probes, width = append(probes, p), p.Width
				}
			}

			curve := ladder.NewCurve(probes)
			bitrate, _ := curve.BitrateFor(r.PredictedVMAF)
			center = math.Round(curve.CRFAt(bitrate)/step) * step
		}

		for crf := center - span; crf <= center+span+1e-9; crf += step {
			out = append(out, setting{width: width, height: h, crf: crf})
		}
	}

	return out
}

// neighbourHeights are the rung resolution of height and the rung
// resolutions just above and below it, highest first.
func neighbourHeights(
	rungs []ladder.Rung,
	height int,
) []int {
	var heights []int

	for _, r := range rungs {
		if !slices.Contains(heights, r.Height) {
			heights = append(heights, r.Height)
		}
	}

	slices.Sort(heights)
	slices.Reverse(heights)
	i := slices.Index(heights, height)

	return heights[max(i-1, 0):min(i+2, len(heights))]
}

// checkShotOptimum compares, for the first n per-shot rungs, the bitrate at
// the per-shot rung's pooled VMAF of: one CRF for every shot, the per-shot
// allocation, and the exhaustive per-shot optimum over a CRF grid (span
// either side of the rung's CRF, every step), all encoded shot by shot.
// With resolutions, the grid spans the neighbouring rung resolutions too,
// and the optimum over (resolution, CRF) is reported next to the CRF one.
func checkShotOptimum(
	w, log io.Writer,
	fast *ladder.Result,
	n int,
	span, step float64,
	resolutions bool,
	measure frameMeasureFunc,
) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)

	head := "rung\tres\tpooled VMAF\tper-title\tper-shot\toptimum\tper-shot gain\toptimum gain\t"
	if resolutions {
		head += "res+CRF optimum\tres+CRF gain\t"
	}

	fmt.Fprintln(tw, head)

	checked := 0

	for i, r := range fast.Rungs {
		if r.PerShot == nil || checked == n {
			continue
		}

		checked++

		table, err := measureShots(log, fast, r, shotGrid(fast, r, span, step, resolutions), measure)
		if err != nil {
			return err
		}

		shots, err := measure(rungParams(r), r.PerShot.Chunks)
		if err != nil {
			return err
		}

		title, optimum := table.perTitleAt(shots.vmaf, r.Height), table.optimumAt(shots.vmaf, r.Height)

		fmt.Fprintf(tw, "%d\t%dp\t%.2f\t%.0f\t%.0f\t%.0f\t%.1f%%\t%.1f%%\t", i+1, r.Height, shots.vmaf,
			title, shots.bitrate, optimum, (1-shots.bitrate/title)*100, (1-optimum/title)*100)

		if resolutions {
			both := table.optimumAt(shots.vmaf, 0)
			fmt.Fprintf(tw, "%.0f\t%.1f%%\t", both, (1-both/title)*100)
		}

		fmt.Fprintln(tw)
	}

	if checked == 0 {
		return ErrNoPerShot
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	return nil
}
