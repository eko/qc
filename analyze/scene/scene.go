// Package scene detects hard cuts from the luma difference between consecutive
// thumbnails, with an adaptive threshold: a frame is a cut when its score is
// both high in absolute terms and much higher than its neighbours' (so that
// fast motion, which raises every score, does not trigger cuts).
package scene

import (
	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

// Defaults, tuned on real content: a hard cut changes most of the picture
// (score well above 12) while being several times the local motion.
const (
	defaultMinScore = 12
	defaultRatio    = 3
	defaultWindow   = 2
	defaultMinShot  = 0.5 // seconds
)

// Options tunes the detector. The zero value uses the defaults.
type Options struct {
	// MinScore is the minimum mean absolute difference (0-255) of a cut. Default 12.
	MinScore float64
	// Ratio is how much a cut must exceed its neighbours' average. Default 3.
	Ratio float64
	// Window is the number of neighbours considered on each side. Default 2.
	Window int
	// MinShot is the minimum shot length. Default 0.5s.
	MinShot media.Duration
}

func (o Options) withDefaults() Options {
	if o.MinScore <= 0 {
		o.MinScore = defaultMinScore
	}

	if o.Ratio <= 0 {
		o.Ratio = defaultRatio
	}

	if o.Window <= 0 {
		o.Window = defaultWindow
	}

	if o.MinShot <= 0 {
		o.MinShot = media.Seconds(defaultMinShot)
	}

	return o
}

// Shot is a run of frames between two cuts.
type Shot struct {
	media.Interval
	FirstFrame int `json:"firstFrame"`
	LastFrame  int `json:"lastFrame"`
}

// Result holds per-frame scores and the detected shots.
type Result struct {
	Scores []float64 `json:"-"`
	Shots  []Shot    `json:"shots"`
}

// Analyzer implements analyze.Analyzer and analyze.Forker.
type Analyzer struct {
	opts   Options
	series analyze.Series[scored]
	// seq is the run of a sequential pass (Consume).
	seq *run
}

// scored is the measure of one frame.
type scored struct {
	score float64
	pts   media.Duration
}

// New returns an Analyzer.
func New(
	opts Options,
) *Analyzer {
	return &Analyzer{opts: opts.withDefaults()}
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	if a.seq == nil {
		a.seq = &run{series: &a.series}
	}

	return a.seq.Consume(f)
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	if a.seq != nil {
		return a.seq.Close()
	}

	return nil
}

// Fork implements analyze.Forker.
func (a *Analyzer) Fork() analyze.Analyzer {
	return &run{series: &a.series}
}

// Result returns the shots. end is the end time of the last frame.
func (a *Analyzer) Result(
	end media.Duration,
) Result {
	frames := a.series.Merge()
	if len(frames) == 0 {
		return Result{}
	}

	scores, pts := make([]float64, len(frames)), make([]media.Duration, len(frames))

	for i, fr := range frames {
		scores[i], pts[i] = fr.score, fr.pts
	}

	return Result{Scores: scores, Shots: shots(DetectCuts(scores, pts, a.opts), pts, end)}
}

// run scores the frames of one run.
type run struct {
	series *analyze.Series[scored]
	// prev is a copy of the previous thumbnail (frames are recycled).
	prev   []byte
	first  int
	frames []scored
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	thumb := analyze.Thumbnail(f)

	score := 0.0
	if r.prev != nil {
		score = MeanAbsDiff(r.prev, thumb)
	} else {
		r.prev, r.first = make([]byte, len(thumb)), f.Index
	}

	copy(r.prev, thumb)
	r.frames = append(r.frames, scored{score: score, pts: f.PTS})

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	r.series.Add(r.first, r.frames)

	return nil
}

// DetectCuts returns the indices of frames starting a new shot (frame 0
// excluded). scores and pts are per-frame and of equal length.
func DetectCuts(
	scores []float64,
	pts []media.Duration,
	opts Options,
) []int {
	opts = opts.withDefaults()

	var (
		cuts    []int
		lastCut = 0
	)

	for i := 1; i < len(scores); i++ {
		if scores[i] < opts.MinScore {
			continue
		}

		if mean, ok := neighbourMean(scores, i, opts.Window); ok && scores[i] < opts.Ratio*mean {
			continue
		}

		if pts[i]-pts[lastCut] < opts.MinShot {
			continue
		}

		cuts = append(cuts, i)
		lastCut = i
	}

	return cuts
}

// neighbourMean averages the scores of up to window frames on each side of i.
// Frame 0 is left out: its score is not a difference (it has no predecessor).
func neighbourMean(
	scores []float64,
	i, window int,
) (float64, bool) {
	var sum float64

	n := 0

	for j := max(1, i-window); j <= min(len(scores)-1, i+window); j++ {
		if j == i {
			continue
		}

		sum += scores[j]
		n++
	}

	if n == 0 {
		return 0, false
	}

	return sum / float64(n), true
}

// shots splits the frames (at least one) at cuts. end closes the last
// shot.
func shots(
	cuts []int,
	pts []media.Duration,
	end media.Duration,
) []Shot {
	starts := append([]int{0}, cuts...)
	out := make([]Shot, len(starts))

	for i, first := range starts {
		last, stop := len(pts)-1, end
		if i+1 < len(starts) {
			last, stop = starts[i+1]-1, pts[starts[i+1]]
		}

		out[i] = Shot{
			Interval:   media.Interval{Start: pts[first], End: stop},
			FirstFrame: first,
			LastFrame:  last,
		}
	}

	return out
}

// MeanAbsDiff returns the mean absolute difference of two equal-size buffers
// (0 when they are empty).
func MeanAbsDiff(
	a, b []byte,
) float64 {
	if len(b) == 0 {
		return 0
	}

	var sum int

	for i, v := range b {
		d := int(v) - int(a[i])
		if d < 0 {
			d = -d
		}

		sum += d
	}

	return float64(sum) / float64(len(b))
}
