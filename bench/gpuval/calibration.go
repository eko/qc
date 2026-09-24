package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/eko/qc/encode"
)

// Calibration targets: the CPU probe sets span VMAF ~97 at the top
// resolution (lowest CRF) to ~40 at the bottom one (highest CRF), see
// docs/ladder.md.
const (
	calibrationHigh   = 97.0
	calibrationLow    = 40.0
	calibrationBottom = 360
	calibrationGOP    = 50
)

// cqSweeps are the CQs swept per codec: wide enough to cross both targets
// on most content.
var cqSweeps = map[string][]float64{
	"h264": {15, 19, 23, 27, 31, 35, 39, 43, 47},
	"hevc": {15, 19, 23, 27, 31, 35, 39, 43, 47},
	"av1":  {18, 24, 30, 36, 42, 48, 54, 60},
}

// sweepPoint is one calibration encode.
type sweepPoint struct {
	cq      float64
	bitrate float64
	vmaf    float64
}

// calibration encodes the source with NVENC over a CQ sweep at the top and
// bottom resolutions and suggests probe CQs spanning VMAF 97 to 40, like
// the CPU sets.
func (v *validator) calibration(
	ctx context.Context,
	c content,
) error {
	suggestions := make([][]string, 0, len(v.opts.codecs))

	for _, name := range v.opts.codecs {
		codec, err := encode.LookupFor(name, encode.HardwareNVENC)
		if err != nil {
			return fmt.Errorf("calibration: %w", err)
		}

		v.progress("calibration: %s", codec.Encoder)

		top, err := v.sweep(ctx, c, codec, c.height)
		if err != nil {
			suggestions = append(suggestions, []string{codec.Encoder, cqList(codec.ProbeCRFs), "failed: " + oneLine(err.Error())})

			continue
		}

		bottom, err := v.sweep(ctx, c, codec, min(calibrationBottom, c.height))
		if err != nil {
			suggestions = append(suggestions, []string{codec.Encoder, cqList(codec.ProbeCRFs), "failed: " + oneLine(err.Error())})

			continue
		}

		v.report.line("**%s** (VMAF v1, sampled ±1 like the ladder probes):", codec.Encoder)
		v.report.table([]string{"CQ", fmt.Sprintf("%dp bitrate", c.height), fmt.Sprintf("%dp VMAF", c.height),
			fmt.Sprintf("%dp bitrate", min(calibrationBottom, c.height)), fmt.Sprintf("%dp VMAF", min(calibrationBottom, c.height))},
			sweepRows(top, bottom))

		suggestions = append(suggestions, []string{codec.Encoder, cqList(codec.ProbeCRFs), suggest(codec, top, bottom)})
	}

	v.report.line("Suggested probe CQs: the CQ reaching VMAF %.0f at the top resolution, the one reaching VMAF %.0f at %dp, "+
		"and their midpoint. Paste this section back to calibrate the NVENC probe CQs (encode/nvenc.go).", calibrationHigh, calibrationLow, calibrationBottom)
	v.report.table([]string{"encoder", "current probe CQs", "suggested"}, suggestions)

	return nil
}

// sweep encodes and measures the source at height for every CQ of the
// codec's sweep.
func (v *validator) sweep(
	ctx context.Context,
	c content,
	codec encode.Codec,
	height int,
) ([]sweepPoint, error) {
	width := int(math.Round(float64(height)*float64(c.width)/float64(c.height)/2)) * 2
	points := make([]sweepPoint, 0, len(cqSweeps[codec.Name]))

	for _, cq := range cqSweeps[codec.Name] {
		out := v.path(fmt.Sprintf("calib-%s-%dp-cq%g.mp4", codec.Name, height, cq))
		params := encode.Params{Width: width, Height: height, CRF: cq, GOP: calibrationGOP}

		args := append([]string{"-v", "error", "-nostdin", "-y"}, codec.InputArgs()...)
		args = append(append(args, "-i", c.source), codec.Args(params)...)

		if _, err := v.run.run(ctx, v.opts.ffmpeg, append(args, out)...); err != nil {
			return nil, fmt.Errorf("%s CQ %g: %w", codec.Encoder, cq, err)
		}

		cmp, _, err := v.qcVMAF(ctx, c.source, out, "--gpu", "--precision", "1")
		if err != nil {
			return nil, err
		}

		bitrate := 0.0
		if cmp.Distorted != nil && cmp.Distorted.Bitstream != nil {
			bitrate = float64(cmp.Distorted.Bitstream.AverageBitrate)
		}

		points = append(points, sweepPoint{cq: cq, bitrate: bitrate, vmaf: cmp.VMAF.Mean})
	}

	return points, nil
}

// sweepRows lays both sweeps side by side (they share the CQs).
func sweepRows(
	top, bottom []sweepPoint,
) [][]string {
	rows := make([][]string, 0, len(top))

	for i, p := range top {
		row := []string{strconv.FormatFloat(p.cq, 'f', -1, 64), kbps(p.bitrate), fmt.Sprintf("%.1f", p.vmaf), "", ""}
		if i < len(bottom) {
			row[3], row[4] = kbps(bottom[i].bitrate), fmt.Sprintf("%.1f", bottom[i].vmaf)
		}

		rows = append(rows, row)
	}

	return rows
}

// suggest proposes probe CQs from the sweeps: where the top resolution
// crosses calibrationHigh, where the bottom one crosses calibrationLow, and
// their midpoint, on the codec's grid.
func suggest(
	codec encode.Codec,
	top, bottom []sweepPoint,
) string {
	hi, hiOK := cqAt(top, calibrationHigh)
	lo, loOK := cqAt(bottom, calibrationLow)

	round := func(cq float64) float64 {
		step := codec.Step()

		return math.Min(math.Max(math.Round(cq/step)*step, codec.MinCRF), codec.MaxCRF)
	}

	hi, lo = round(hi), round(lo)
	if lo <= hi {
		return "inconsistent sweep: keep the current CQs"
	}

	text := cqList([]float64{hi, round((hi + lo) / 2), lo})

	var notes []string
	if !hiOK {
		notes = append(notes, fmt.Sprintf("VMAF %.0f not reached in the sweep", calibrationHigh))
	}

	if !loOK {
		notes = append(notes, fmt.Sprintf("VMAF %.0f not reached in the sweep", calibrationLow))
	}

	if len(notes) > 0 {
		text += " (" + strings.Join(notes, "; ") + ")"
	}

	return text
}

// cqAt interpolates the CQ at which VMAF crosses target (VMAF falls as the
// CQ grows), or returns the nearest end of the sweep and false.
func cqAt(
	points []sweepPoint,
	target float64,
) (float64, bool) {
	if len(points) == 0 {
		return 0, false
	}

	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if a.vmaf >= target && b.vmaf <= target && a.vmaf != b.vmaf {
			return a.cq + (a.vmaf-target)/(a.vmaf-b.vmaf)*(b.cq-a.cq), true
		}
	}

	if points[0].vmaf < target {
		return points[0].cq, false
	}

	return points[len(points)-1].cq, false
}

// cqList renders CQs as a Go slice literal body.
func cqList(
	cqs []float64,
) string {
	parts := make([]string, len(cqs))
	for i, cq := range cqs {
		parts[i] = strconv.FormatFloat(cq, 'f', -1, 64)
	}

	return "{" + strings.Join(parts, ", ") + "}"
}
