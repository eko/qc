package defect

import (
	"math"

	"github.com/eko/qc/audio/loudness"
)

// maxClipSegments bounds the clipped segments kept per channel: a heavily
// clipped signal would otherwise list thousands (their count is kept).
const maxClipSegments = 100

// clipMerge is how close (seconds) two clipping events must be to form one
// clipped segment: a clipped passage clips on every loud wave.
const clipMerge = 0.5

// channel is the state of one channel.
type channel struct {
	// peak is the largest absolute sample, sum and squares the sum of the
	// samples and of their squares.
	peak           float32
	sum, squares   float64
	windowPeak     float32
	stepSquares    float64
	levels         []float64
	silentWindows  int64
	silentActive   int64
	silence        run
	silences       []span
	clipRun        int
	clipStart      int64
	clippedSamples int64
	clipEvents     int
	clips          []span
}

// span is a range of samples [from, to).
type span struct {
	from, to int64
}

// run is an open range of consecutive flagged windows or steps.
type run struct {
	open     bool
	from, to int64
}

// extend adds [from, to) to the run, opening it at from.
func (r *run) extend(
	from, to int64,
) {
	if !r.open {
		r.open, r.from = true, from
	}

	r.to = to
}

// close ends the run and returns its span, if it lasts at least minLength
// samples.
func (r *run) close(
	minLength int64,
) (span, bool) {
	if !r.open {
		return span{}, false
	}

	r.open = false

	return span{r.from, r.to}, r.to-r.from >= minLength
}

// clipRule is what counts as clipping: runs of at least run samples at or
// above level, merged into one segment when merge samples apart or less.
type clipRule struct {
	level float32
	run   int
	merge int64
}

// measure adds samples, the first at position pos, to the window and the
// step: peak, sums, and runs of full-scale samples.
func (c *channel) measure(
	samples []float32,
	pos int64,
	rule clipRule,
) {
	peak := c.windowPeak
	sum, squares := 0.0, 0.0

	for i, x := range samples {
		a := max(x, -x)
		peak = max(peak, a)

		v := float64(x)
		sum += v
		squares += v * v

		if a >= rule.level {
			if c.clipRun == 0 {
				c.clipStart = pos + int64(i)
			}

			c.clipRun++
		} else if c.clipRun > 0 {
			c.endClip(rule)
		}
	}

	c.windowPeak = peak
	c.sum += sum
	c.squares += squares
	c.stepSquares += squares
}

// endClip ends a run of full-scale samples, counted as clipping when it
// lasts long enough.
func (c *channel) endClip(
	rule clipRule,
) {
	n := c.clipRun
	c.clipRun = 0

	if n < rule.run {
		return
	}

	c.clippedSamples += int64(n)
	c.clipEvents++
	c.addClip(span{c.clipStart, c.clipStart + int64(n)}, rule.merge)
}

// addClip merges a clipping event into the last clipped segment when it
// is at most merge samples after it, or starts a new one while there is
// room.
func (c *channel) addClip(
	s span,
	merge int64,
) {
	if n := len(c.clips); n > 0 && s.from-c.clips[n-1].to <= merge {
		c.clips[n-1].to = s.to

		return
	}

	if len(c.clips) < maxClipSegments {
		c.clips = append(c.clips, s)
	}
}

// closeStep records the level of the step (RMS, dBFS) of stepSize samples.
func (c *channel) closeStep(
	stepSize int,
) {
	c.levels = append(c.levels, loudness.Round(rmsDecibels(c.stepSquares, int64(stepSize))))
	c.stepSquares = 0
}

// rmsDecibels is the RMS level of n samples whose squares sum to squares,
// in dBFS (a full-scale square wave reads 0, a full-scale sine -3).
func rmsDecibels(
	squares float64,
	n int64,
) float64 {
	if n == 0 {
		return loudness.Floor
	}

	return loudness.Decibels(math.Sqrt(squares / float64(n)))
}

// amplitude converts dBFS to a linear amplitude.
func amplitude(
	dbfs float64,
) float64 {
	return math.Pow(10, dbfs/20)
}
