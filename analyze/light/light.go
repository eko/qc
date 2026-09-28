// Package light measures the light levels of HDR (PQ or HLG) frames: per
// frame, the brightest and the average display light of max(R, G, B) in
// cd/m², the quantities behind CTA-861.3's MaxCLL and MaxFALL.
//
// It reads the sparse grid of 10-bit Y′CbCr samples a sampling pool keeps
// next to the 8-bit luma (frame.PoolOptions.SampleStep): the frame analysis
// decodes the video once for every analyzer, and the light levels come
// with it at the cost of a few table lookups per grid point.
package light

import (
	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/colorimetry"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
)

// RobustPercentile is the share of a frame's grid points at or below its
// robust peak. 4:2:0 chroma subsampling makes a few pixels at saturated
// colour edges overshoot when chroma is upsampled (one component jumps
// towards the peak): on a 1000 cd/m² master whose 4:4:4 MaxCLL is 1557,
// the strict maximum of its 4:2:0 encode reached 7906 cd/m², while its
// 99.9th percentile stayed at 1512. The robust peak ignores that brightest
// thousandth of the picture.
const RobustPercentile = 99.9

// TargetPoints sizes the sample grid (see Step): about 130 000 points per
// frame, one every 4 pixels in each direction at 1080p and every 8 at
// 2160p. On a 1080p HDR10 title this kept MaxFALL within 0.1% and the
// robust peak within 0.5% of a full-resolution computation (see
// docs/hdr.md).
const TargetPoints = 130_000

// histogramBins bins max(R, G, B) by its PQ code value, 10-bit steps
// (below 1% of the luminance at 1000 cd/m²).
const histogramBins = colorimetry.PQBins

// Step is the sample grid spacing, in luma pixels, for a width×height
// video: an even step giving about TargetPoints points.
func Step(
	width, height int,
) int {
	half := 1
	for (width/(2*half))*(height/(2*half)) > TargetPoints {
		half++
	}

	return 2 * half
}

// Result holds the light levels of the frames, in cd/m².
type Result struct {
	// Transfer is media.TransferPQ or media.TransferHLG.
	Transfer string `json:"transfer"`
	// DisplayPeak is the display HLG light is computed for (cd/m²), 0 for
	// PQ, whose light is absolute.
	DisplayPeak float64 `json:"displayPeak,omitempty"`
	// SampleStep is the grid spacing in luma pixels.
	SampleStep int `json:"sampleStep"`
	// MaxCLL is the brightest max(R, G, B) of any grid point of any
	// frame: CTA-861.3's MaxCLL, on the grid.
	MaxCLL float64 `json:"maxCLL"`
	// MaxCLLRobust is the highest robust peak of a frame (see
	// RobustPercentile).
	MaxCLLRobust float64 `json:"maxCLLRobust"`
	// MaxFALL is the highest frame-average max(R, G, B): CTA-861.3's
	// MaxFALL, on the grid and over the active picture (see Active).
	MaxFALL float64 `json:"maxFALL"`
	// MaxCLLFrame and MaxFALLFrame are the indexes of the frames reaching
	// MaxCLL and MaxFALL.
	MaxCLLFrame  int `json:"maxCLLFrame"`
	MaxFALLFrame int `json:"maxFALLFrame"`
	// ActiveShare is the share of the picture the averages are taken on:
	// 1, or less once black borders are excluded (Active).
	ActiveShare float64 `json:"activeShare"`
	// Peak, Robust and Average are the per-frame series.
	Peak    []float64 `json:"-"`
	Robust  []float64 `json:"-"`
	Average []float64 `json:"-"`
	// AverageSummary summarises Average.
	AverageSummary stats.Summary `json:"average"`
}

// Analyzer implements analyze.Analyzer and analyze.Forker on the frames
// of a sampling pool.
type Analyzer struct {
	decoder *colorimetry.Decoder
	pq      *colorimetry.PQTable
	res     Result
	series  analyze.Series[levels]
	// seq is the run of a sequential pass (Consume).
	seq *run
}

// levels are the light levels of one frame, in cd/m².
type levels struct {
	peak, robust, average float64
	// step is the sample grid spacing, 0 for a frame without grid.
	step  int
	index int
}

// New returns an Analyzer of a PQ or HLG signal with the given colour
// description (range, transfer). The samples are 10-bit, as sampling pools
// decode them.
func New(
	color media.Color,
) *Analyzer {
	a := &Analyzer{
		decoder: colorimetry.NewDecoder(colorimetry.Signal{
			Transfer: color.Transfer, BitDepth: 10, FullRange: color.FullRange(), Peak: media.HLGNominalPeak,
		}),
		pq:  colorimetry.NewPQTable(),
		res: Result{Transfer: color.Transfer, ActiveShare: 1},
	}

	if color.Transfer == media.TransferHLG {
		a.res.DisplayPeak = media.HLGNominalPeak
	}

	return a
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	if a.seq == nil {
		a.seq = a.fork()
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
	return a.fork()
}

func (a *Analyzer) fork() *run {
	return &run{parent: a, first: -1, hist: make([]int, histogramBins)}
}

// Result returns the light levels. It must be called after Close.
func (a *Analyzer) Result() Result {
	res := a.res

	for _, lv := range a.series.Merge() {
		if lv.step > 0 {
			res.SampleStep = lv.step
			res.add(lv)
		}
	}

	res.AverageSummary = stats.Summarize(res.Average)

	return res
}

// add records the light levels of a frame.
func (r *Result) add(
	lv levels,
) {
	if lv.peak > r.MaxCLL || len(r.Peak) == 0 {
		r.MaxCLL, r.MaxCLLFrame = lv.peak, lv.index
	}

	if lv.average > r.MaxFALL || len(r.Average) == 0 {
		r.MaxFALL, r.MaxFALLFrame = lv.average, lv.index
	}

	r.MaxCLLRobust = max(r.MaxCLLRobust, lv.robust)
	r.Peak = append(r.Peak, lv.peak)
	r.Robust = append(r.Robust, lv.robust)
	r.Average = append(r.Average, lv.average)
}

// run measures the frames of one run.
type run struct {
	parent *Analyzer
	first  int
	frames []levels
	hist   []int
	// nits and bins receive the light and PQ bin of each grid point.
	nits []float32
	bins []uint16
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	if r.first < 0 {
		r.first = f.Index
	}

	s := &f.Samples
	if s.Step == 0 {
		r.frames = append(r.frames, levels{})

		return nil
	}

	clear(r.hist)

	ys := s.Y.Uint16()
	if len(r.nits) != len(ys) {
		r.nits, r.bins = make([]float32, len(ys)), make([]uint16, len(ys))
	}

	r.parent.decoder.MaxLights(ys, s.Cb.Uint16(), s.Cr.Uint16(), r.nits, r.bins, r.parent.pq)

	var (
		sum  float64
		peak float32
	)

	for i, nits := range r.nits {
		peak = max(peak, nits)
		sum += float64(nits)
		r.hist[r.bins[i]]++
	}

	r.frames = append(r.frames, levels{
		peak: float64(peak), robust: r.robustPeak(len(ys)), average: sum / float64(max(len(ys), 1)),
		step: s.Step, index: f.Index,
	})

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	r.parent.series.Add(r.first, r.frames)

	return nil
}

// robustPeak reads the RobustPercentile of the frame's histogram, in cd/m².
func (r *run) robustPeak(
	points int,
) float64 {
	above := int(float64(points) * (100 - RobustPercentile) / 100)

	for bin := len(r.hist) - 1; bin > 0; bin-- {
		above -= r.hist[bin]
		if above < 0 {
			return colorimetry.PQEOTF(float64(bin) / (histogramBins - 1))
		}
	}

	return 0
}

// Active returns r with its averages taken over the active picture, share
// of the frame (0–1], instead of the whole frame: CTA-861.3 computes
// MaxFALL without the black borders of letterboxed content. Borders are
// black, 0 cd/m², so the sums over the active picture are those of the
// whole frame.
func (r Result) Active(
	share float64,
) Result {
	if share <= 0 || share >= 1 {
		return r
	}

	scale := r.ActiveShare / share
	r.ActiveShare = share
	r.Average = scaled(r.Average, scale)
	r.MaxFALL *= scale
	r.AverageSummary = stats.Summarize(r.Average)

	return r
}

func scaled(
	values []float64,
	k float64,
) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = v * k
	}

	return out
}
