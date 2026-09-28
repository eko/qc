// Package crop detects letterboxing and pillarboxing: the smallest rectangle
// that contains the picture content across the whole video.
package crop

import (
	"github.com/eko/qc/analyze"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

const (
	// defaultEvery samples one frame in 10: borders do not change from one
	// frame to the next, and the full-resolution scan is the costly part.
	defaultEvery = 10
	// defaultTolerance is how far above nominal black a border line's mean
	// may be (bars are rarely perfectly black after encoding).
	defaultTolerance = 10
	// brightDetail is how far above the tolerated level a single pixel may
	// be before its line counts as content: burnt-in subtitles in the bars
	// must not be cropped away, while codec noise stays well below it.
	brightDetail = 32
)

// Options tunes the detector. The zero value uses the defaults.
type Options struct {
	// Every analyses one frame out of Every. Default 10.
	Every int
	// Tolerance is how far above black a border line may be. Default 10.
	Tolerance int
}

func (o Options) withDefaults() Options {
	if o.Every <= 0 {
		o.Every = defaultEvery
	}

	if o.Tolerance <= 0 {
		o.Tolerance = defaultTolerance
	}

	return o
}

// Rect is a pixel rectangle.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Result is the detected content area.
type Result struct {
	Content    Rect `json:"content"`
	Letterbox  bool `json:"letterbox"`
	Pillarbox  bool `json:"pillarbox"`
	FramesUsed int  `json:"framesUsed"`
}

// Analyzer implements analyze.Analyzer and analyze.Forker.
type Analyzer struct {
	opts Options
	// limit is the brightest mean a border line may have.
	limit         int
	width, height int
	series        analyze.Series[box]
	// seq is the run of a sequential pass (Consume).
	seq *run
}

// box is the content box of one frame as [x0, y0, x1, y1), when the frame
// was analysed and not fully dark.
type box struct {
	x0, y0, x1, y1 int
	used           bool
}

// New returns an Analyzer for width×height frames.
func New(
	width, height int,
	levels media.Levels,
	opts Options,
) *Analyzer {
	opts = opts.withDefaults()

	return &Analyzer{
		opts:   opts,
		limit:  levels.Black + opts.Tolerance,
		width:  width,
		height: height,
	}
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
	return &run{parent: a, first: -1}
}

// Result returns the content rectangle, aligned on even coordinates as
// required by 4:2:0 chroma subsampling: the union of the content boxes of
// the analysed frames.
func (a *Analyzer) Result() Result {
	x0, y0, x1, y1, used := a.width, a.height, 0, 0, 0

	for _, b := range a.series.Merge() {
		if b.used {
			x0, y0 = min(x0, b.x0), min(y0, b.y0)
			x1, y1 = max(x1, b.x1), max(y1, b.y1)
			used++
		}
	}

	if used == 0 {
		return Result{Content: Rect{Width: a.width, Height: a.height}}
	}

	x0, y0 = x0&^1, y0&^1
	x1, y1 = min(a.width, (x1+1)&^1), min(a.height, (y1+1)&^1)

	return Result{
		Content:    Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0},
		Letterbox:  y0 > 0 || y1 < a.height,
		Pillarbox:  x0 > 0 || x1 < a.width,
		FramesUsed: used,
	}
}

// run finds the content boxes of the frames of one run.
type run struct {
	parent *Analyzer
	first  int
	boxes  []box
}

// Consume implements analyze.Analyzer.
func (r *run) Consume(
	f *frame.Frame,
) error {
	if r.first < 0 {
		r.first = f.Index
	}

	b := box{}
	if f.Index%r.parent.opts.Every == 0 {
		b = r.parent.contentBox(&f.Luma)
	}

	r.boxes = append(r.boxes, b)

	return nil
}

// Close implements analyze.Analyzer.
func (r *run) Close() error {
	r.parent.series.Add(r.first, r.boxes)

	return nil
}

// contentBox scans p from each edge inwards. Fully dark frames (fades,
// black inserts) say nothing about the borders: their box is unused.
func (a *Analyzer) contentBox(
	p *frame.Plane,
) box {
	top := 0
	for top < p.Height && a.darkRow(p, top) {
		top++
	}

	if top == p.Height {
		return box{}
	}

	bottom := p.Height
	for bottom > top && a.darkRow(p, bottom-1) {
		bottom--
	}

	left := 0
	for left < p.Width && a.darkColumn(p, left, top, bottom) {
		left++
	}

	right := p.Width
	for right > left && a.darkColumn(p, right-1, top, bottom) {
		right--
	}

	return box{x0: left, y0: top, x1: right, y1: bottom, used: true}
}

// darkRow reports whether a line is border: dark on average and without any
// bright detail (subtitles burnt into the bars must not be cropped away).
func (a *Analyzer) darkRow(
	p *frame.Plane,
	y int,
) bool {
	var sum int

	for _, v := range p.Row(y) {
		if int(v) > a.limit+brightDetail {
			return false
		}

		sum += int(v)
	}

	return sum <= a.limit*p.Width
}

// darkColumn is darkRow for column x, restricted to the content rows.
func (a *Analyzer) darkColumn(
	p *frame.Plane,
	x, top, bottom int,
) bool {
	var sum int

	for y := top; y < bottom; y++ {
		v := int(p.Pix[y*p.Stride+x])
		if v > a.limit+brightDetail {
			return false
		}

		sum += v
	}

	return sum <= a.limit*(bottom-top)
}
