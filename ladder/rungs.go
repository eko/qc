package ladder

import (
	"fmt"
	"math"

	"github.com/eko/qc/encode"
)

// peakRatio is the VBV cap relative to the rung bitrate: 2× over a 2 s
// buffer is the HLS peak limit for VOD. 1.5× cost up to 4 VMAF points on
// content with a bursty bitrate.
const peakRatio = 2.0

// bufferSeconds is the VBV buffer length in seconds of peak bitrate.
const bufferSeconds = 2

// rungs turns targets into encoding settings: the CRF expected to hit the
// bitrate at the rung resolution, and a VBV cap at peakRatio× the average.
func (b *build) rungs(
	targets []Target,
	curves []Curve,
) []Rung {
	out := make([]Rung, 0, len(targets))

	for i, t := range targets {
		curve := rungCurve(curves, t)
		bitrate := float64(t.Bitrate)

		// The curve covers the bitrate unless the fallback curve was taken:
		// the envelope quality is then the best estimate.
		predicted, ok := curve.VMAFAt(bitrate)
		if !ok {
			predicted = t.VMAF
		}

		r := Rung{
			Width: curve.Width, Height: curve.Height,
			Bitrate:       t.Bitrate,
			CRF:           b.roundCRF(curve.CRFAt(bitrate)),
			MaxRate:       int64(peakRatio * bitrate),
			BufSize:       int64(bufferSeconds * peakRatio * bitrate),
			PredictedVMAF: predicted,
			Extrapolated:  t.Extrapolated,
		}
		if b.opts.Probing == ProbingAdaptive {
			r.PredictionError = curve.halfWidthAt(bitrate)
		}

		r.Command = b.command(i, r)

		out = append(out, r)
	}

	return out
}

// command renders the ffmpeg command encoding the whole title with the
// settings of the i-th rung. The output name carries the rung index, since
// several rungs can share a resolution.
func (b *build) command(
	i int,
	r Rung,
) string {
	return b.codec.CommandLine(b.source, fmt.Sprintf("%02d-%dp.mp4", i+1, r.Height), encode.Params{
		Width: r.Width, Height: r.Height, CRF: r.CRF, Preset: b.opts.Preset,
		GOP: b.gop(), MaxRate: r.MaxRate, BufSize: r.BufSize, BitDepth: b.opts.BitDepth, FilmGrain: b.grain,
	})
}

// curveFor returns the curve of the target resolution, or the highest
// resolution not above it whose probes span the target bitrate.
func curveFor(
	curves []Curve,
	t Target,
) Curve {
	var fallback *Curve

	for i, c := range curves {
		if c.Height > t.Height {
			continue
		}

		if _, ok := c.VMAFAt(float64(t.Bitrate)); ok {
			return c
		}

		if fallback == nil {
			fallback = &curves[i]
		}
	}

	if fallback != nil {
		return *fallback
	}

	return curves[len(curves)-1]
}

// roundCRF rounds to the encoder granularity within the codec bounds.
func (b *build) roundCRF(
	crf float64,
) float64 {
	step := b.codec.Step()

	return math.Min(math.Max(math.Round(crf/step)*step, b.codec.MinCRF), b.codec.MaxCRF)
}
