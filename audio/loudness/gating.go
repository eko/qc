package loudness

import (
	"math"
	"slices"
)

// Result is the loudness of a signal. Levels are in LUFS (LKFS), ranges
// in LU, peaks in dBTP (true peak) or dBFS (sample peak); Floor stands for
// silence.
type Result struct {
	// Integrated is the gated loudness of the whole signal (BS.1770:
	// 400 ms blocks overlapping by 75%, absolute gate -70 LUFS, relative
	// gate -10 LU).
	Integrated float64 `json:"integrated"`
	// Range is the loudness range (EBU Tech 3342): the spread between the
	// 10th and the 95th percentiles (RangeLow, RangeHigh) of the
	// short-term loudness, gated at -70 LUFS and 20 LU below its mean.
	Range     float64 `json:"range"`
	RangeLow  float64 `json:"rangeLow"`
	RangeHigh float64 `json:"rangeHigh"`
	// TruePeak is the highest true peak of any channel, TruePeakAt the
	// start of the 100 ms step holding it, in seconds from the first
	// sample.
	TruePeak   float64 `json:"truePeak"`
	TruePeakAt float64 `json:"truePeakAt"`
	// ChannelTruePeaks is the true peak of each channel, in stream order.
	ChannelTruePeaks []float64 `json:"channelTruePeaks"`
	SamplePeak       float64   `json:"samplePeak"`
	// MaxMomentary and MaxShortTerm are the loudest 400 ms and 3 s windows
	// (complete windows only).
	MaxMomentary float64 `json:"maxMomentary"`
	MaxShortTerm float64 `json:"maxShortTerm"`
	Series       Series  `json:"series"`
}

// Series are the meters every 100 ms (Step): value i ends at (i+1) × Step
// seconds from the first sample. The windows of the first values reach
// before the start, which counts as silence (as ffmpeg's ebur128 filter
// shows them).
type Series struct {
	// Momentary (400 ms) and ShortTerm (3 s) loudness, in LUFS.
	Momentary []float64 `json:"momentary"`
	ShortTerm []float64 `json:"shortTerm"`
	// TruePeak is the true peak of the step over every channel, in dBTP.
	TruePeak []float64 `json:"truePeak"`
}

// windowed computes the level of the window of steps ending at every step,
// norm turning its summed energy into a mean square. Windows reaching
// before the first step take silence for the missing steps. Each window is
// summed afresh rather than slid: a running sum would keep the rounding
// residue of loud steps in the silence after them.
func windowed(
	energies []float64,
	steps int,
	norm float64,
) []float64 {
	levels := make([]float64, len(energies))

	for i := range energies {
		sum := 0.0
		for _, e := range energies[max(0, i-steps+1) : i+1] {
			sum += e
		}

		levels[i] = level(sum * norm)
	}

	return levels
}

// complete drops the levels of windows reaching before the first step.
func complete(
	levels []float64,
	steps int,
) []float64 {
	return levels[min(steps-1, len(levels)):]
}

// integrated is the gated loudness of the blocks (BS.1770): the blocks
// above the absolute gate, then those above their mean minus 10 LU, are
// averaged in energy.
func integrated(
	blocks []float64,
) float64 {
	gated := gate(blocks, absoluteGate)
	if len(gated) == 0 {
		return Floor
	}

	gated = gate(gated, powerMean(gated)+relativeGate)

	return powerMean(gated)
}

// loudnessRange is the loudness range of the short-term levels (EBU Tech
// 3342) and the percentiles bounding it; zero without levels above the
// gates.
func loudnessRange(
	shortTerm []float64,
) (lra, low, high float64) {
	gated := gate(shortTerm, absoluteGate)
	if len(gated) == 0 {
		return 0, Floor, Floor
	}

	gated = gate(gated, powerMean(gated)+rangeRelativeGate)
	slices.Sort(gated)

	low, high = percentile(gated, rangeLow), percentile(gated, rangeHigh)

	return Round(high - low), Round(low), Round(high)
}

// percentile is the nearest-rank percentile p of sorted values, as
// libebur128 and the reference implementations of Tech 3342 take it.
func percentile(
	sorted []float64,
	p float64,
) float64 {
	return sorted[int(float64(len(sorted)-1)*p+0.5)]
}

// gate keeps the levels strictly above threshold.
func gate(
	levels []float64,
	threshold float64,
) []float64 {
	out := make([]float64, 0, len(levels))
	for _, l := range levels {
		if l > threshold {
			out = append(out, l)
		}
	}

	return out
}

// powerMean is the level of the mean energy of levels.
func powerMean(
	levels []float64,
) float64 {
	sum := 0.0
	for _, l := range levels {
		sum += math.Pow(10, (l-offset)/10)
	}

	return level(sum / float64(len(levels)))
}

// maxLevel is the highest level, Floor without any.
func maxLevel(
	levels []float64,
) float64 {
	top := Floor
	for _, l := range levels {
		top = max(top, l)
	}

	return Round(top)
}
