// Package crop detects letterboxing and pillarboxing: the smallest rectangle
// that contains the picture content across the whole video.
package crop

import (
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

// Analyzer implements analyze.Analyzer.
type Analyzer struct {
	opts Options
	// limit is the brightest mean a border line may have.
	limit         int
	width, height int
	// union of content boxes, as [x0, y0, x1, y1) with x0 > x1 when empty.
	x0, y0, x1, y1 int
	used           int
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
		x0:     width,
		y0:     height,
	}
}

// Consume implements analyze.Analyzer.
func (a *Analyzer) Consume(
	f *frame.Frame,
) error {
	if f.Index%a.opts.Every != 0 {
		return nil
	}

	p := &f.Luma

	top := 0
	for top < p.Height && a.darkRow(p, top) {
		top++
	}

	// Fully dark frames (fades, black inserts) say nothing about the borders.
	if top == p.Height {
		return nil
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

	a.x0, a.y0 = min(a.x0, left), min(a.y0, top)
	a.x1, a.y1 = max(a.x1, right), max(a.y1, bottom)
	a.used++

	return nil
}

// Close implements analyze.Analyzer.
func (a *Analyzer) Close() error {
	return nil
}

// Result returns the content rectangle, aligned on even coordinates as
// required by 4:2:0 chroma subsampling.
func (a *Analyzer) Result() Result {
	if a.used == 0 {
		return Result{Content: Rect{Width: a.width, Height: a.height}}
	}

	x0, y0 := a.x0&^1, a.y0&^1
	x1, y1 := min(a.width, (a.x1+1)&^1), min(a.height, (a.y1+1)&^1)

	return Result{
		Content:    Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0},
		Letterbox:  y0 > 0 || y1 < a.height,
		Pillarbox:  x0 > 0 || x1 < a.width,
		FramesUsed: a.used,
	}
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
