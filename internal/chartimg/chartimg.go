// Package chartimg draws line charts as PNG pictures, smooth and sharp at
// twice the size they are seen at: what a terminal that draws pictures
// shows in place of a chart of characters (see package termimg).
package chartimg

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"slices"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomedium"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Theme are the colours of what is not data: the text, the secondary
// text and the grid. The background stays transparent: the terminal's.
type Theme struct {
	Ink, Muted, Grid color.Color
}

// Line is a polyline of a chart, or its points alone.
type Line struct {
	Points [][2]float64
	Color  color.Color
	// Width is the stroke, in points; with Dots, the diameter of a dot at
	// every point instead of a line.
	Width float64
	Dots  bool
}

// Marker is a numbered disc at a point: a rung.
type Marker struct {
	X, Y  float64
	Label string
	// Fill is the colour of the disc, and Text that of its label.
	Fill, Text color.Color
}

// Key is an entry of the legend.
type Key struct {
	Label string
	Color color.Color
}

// Chart is a chart of lines on a log or linear x axis and a linear y axis.
type Chart struct {
	// Width and Height are the size of the picture as seen, in points;
	// it is drawn at Scale times that.
	Width, Height int
	Scale         float64
	XLo, XHi      float64
	YLo, YHi      float64
	XLog          bool
	// XTicks are the labelled values of x; YStep labels y every step.
	XTicks []float64
	XLabel func(float64) string
	YStep  float64
	YLabel func(float64) string
	// Lines are drawn in order, then the markers over them.
	Lines   []Line
	Markers []Marker
	Legend  []Key
	Theme   Theme
}

// Margins of the plot and sizes of the text, in points.
const (
	marginLeft   = 40
	marginRight  = 14
	marginTop    = 26
	marginBottom = 26
	textSize     = 11.5
	markerSize   = 8.5
	legendSwatch = 14
	legendGap    = 16
)

// fonts are the faces of the charts: Go Regular for the axes, Go Medium
// for the legend and the markers (BSD licence, part of x/image).
var (
	regular, medium *opentype.Font
	errFonts        error
)

func init() { //nolint:gochecknoinits // the embedded fonts parse once, and cannot fail but by a broken build
	if regular, errFonts = opentype.Parse(goregular.TTF); errFonts == nil {
		medium, errFonts = opentype.Parse(gomedium.TTF)
	}
}

// PNG draws the chart.
func (c Chart) PNG() ([]byte, error) {
	if errFonts != nil {
		return nil, fmt.Errorf("chart fonts: %w", errFonts)
	}

	if c.Scale <= 0 {
		c.Scale = 1
	}

	p, err := c.newPainter()
	if err != nil {
		return nil, err
	}

	defer p.close()

	c.drawAxes(p)

	for _, l := range c.Lines {
		c.drawLine(p, l)
	}

	for _, m := range c.Markers {
		c.drawMarker(p, m)
	}

	c.drawLegend(p)

	var buf bytes.Buffer
	if err := png.Encode(&buf, p.img); err != nil {
		return nil, fmt.Errorf("encode chart: %w", err)
	}

	return buf.Bytes(), nil
}

// painter draws on the picture of a chart.
type painter struct {
	img          *image.RGBA
	scale        float64
	text, strong font.Face
	marker       font.Face
	left, right  float64
	top, bottom  float64
	xLo, xHi     float64
	yLo, yHi     float64
	logX         bool
}

func (c Chart) newPainter() (*painter, error) {
	s := c.Scale
	p := &painter{
		img:   image.NewRGBA(image.Rect(0, 0, int(float64(c.Width)*s), int(float64(c.Height)*s))),
		scale: s,
		left:  marginLeft * s, right: (float64(c.Width) - marginRight) * s,
		top: marginTop * s, bottom: (float64(c.Height) - marginBottom) * s,
		xLo: c.XLo, xHi: c.XHi, yLo: c.YLo, yHi: c.YHi, logX: c.XLog,
	}

	if c.XLog {
		p.xLo, p.xHi = math.Log(c.XLo), math.Log(c.XHi)
	}

	faces := []struct {
		face *font.Face
		font *opentype.Font
		size float64
	}{{&p.text, regular, textSize}, {&p.strong, medium, textSize}, {&p.marker, medium, markerSize}}

	for _, f := range faces {
		face, err := opentype.NewFace(f.font, &opentype.FaceOptions{Size: f.size * s, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return nil, fmt.Errorf("chart font: %w", err)
		}

		*f.face = face
	}

	return p, nil
}

func (p *painter) close() {
	for _, f := range []font.Face{p.text, p.strong, p.marker} {
		_ = f.Close()
	}
}

// at maps a point of the data to the picture.
func (p *painter) at(
	x, y float64,
) (float64, float64) {
	if p.logX {
		x = math.Log(math.Max(x, 1e-9))
	}

	return p.left + (x-p.xLo)/(p.xHi-p.xLo)*(p.right-p.left), p.bottom - (y-p.yLo)/(p.yHi-p.yLo)*(p.bottom-p.top)
}

// fill paints the shapes path adds to a pen, in col, anti-aliased,
// inside the rectangle clip of the picture.
func (p *painter) fill(
	col color.Color,
	clip image.Rectangle,
	path func(pen *pen),
) {
	clip = clip.Intersect(p.img.Bounds())
	if clip.Empty() {
		return
	}

	// The mask of a rasterizer starts at the corner of where it draws:
	// the pen moves the shapes by that corner.
	r := vector.NewRasterizer(clip.Dx(), clip.Dy())
	r.DrawOp = draw.Over
	path(&pen{r: r, dx: float64(clip.Min.X), dy: float64(clip.Min.Y)})
	r.Draw(p.img, clip, image.NewUniform(col), image.Point{})
}

// plotClip is the plot area, a stroke's width wider.
func (p *painter) plotClip() image.Rectangle {
	pad := int(4 * p.scale)

	return image.Rect(int(p.left)-pad, int(p.top)-pad, int(p.right)+pad, int(p.bottom)+pad)
}

// pen adds polygons to a rasterizer whose mask starts at (dx, dy) of the
// picture, every one wound the same way: overlapping shapes add up instead
// of cancelling where they cross.
type pen struct {
	r      *vector.Rasterizer
	dx, dy float64
}

// polygon adds the polygon of the points pts, x then y.
func (pn *pen) polygon(
	pts ...[2]float64,
) {
	area := 0.0
	for i, a := range pts {
		b := pts[(i+1)%len(pts)]
		area += a[0]*b[1] - b[0]*a[1]
	}

	if area < 0 {
		slices.Reverse(pts)
	}

	for i, pt := range pts {
		x, y := float32(pt[0]-pn.dx), float32(pt[1]-pn.dy)
		if i == 0 {
			pn.r.MoveTo(x, y)
		} else {
			pn.r.LineTo(x, y)
		}
	}

	pn.r.ClosePath()
}

// segment adds the stroke from (x0, y0) to (x1, y1), width wide, with a
// round cap at its end: consecutive segments join smoothly.
func (pn *pen) segment(
	x0, y0, x1, y1, width float64,
) {
	dx, dy := x1-x0, y1-y0

	if n := math.Hypot(dx, dy); n > 0 {
		nx, ny := -dy/n*width/2, dx/n*width/2
		pn.polygon([2]float64{x0 + nx, y0 + ny}, [2]float64{x1 + nx, y1 + ny}, [2]float64{x1 - nx, y1 - ny}, [2]float64{x0 - nx, y0 - ny})
	}

	pn.disc(x1, y1, width/2)
}

// discSides is the number of sides of the polygon a disc is drawn as.
const discSides = 24

// disc adds a disc of radius at (x, y).
func (pn *pen) disc(
	x, y, radius float64,
) {
	pts := make([][2]float64, discSides)
	for i := range pts {
		a := 2 * math.Pi * float64(i) / discSides
		pts[i] = [2]float64{x + radius*math.Cos(a), y + radius*math.Sin(a)}
	}

	pn.polygon(pts...)
}

// rect adds the rectangle from (x0, y0) to (x1, y1).
func (pn *pen) rect(
	x0, y0, x1, y1 float64,
) {
	pn.polygon([2]float64{x0, y0}, [2]float64{x1, y0}, [2]float64{x1, y1}, [2]float64{x0, y1})
}

// text writes s with face, its left end at x when anchor is 0, its middle
// when 0.5, its right end when 1; y is its baseline.
func (p *painter) write(
	face font.Face,
	col color.Color,
	s string,
	x, y, anchor float64,
) {
	d := font.Drawer{Dst: p.img, Src: image.NewUniform(col), Face: face}
	width := float64(d.MeasureString(s)) / 64
	d.Dot = fixed.Point26_6{X: fixed.Int26_6((x - anchor*width) * 64), Y: fixed.Int26_6(y * 64)}
	d.DrawString(s)
}

func (c Chart) drawAxes(
	p *painter,
) {
	s := p.scale

	if c.YStep > 0 {
		for v := math.Ceil(c.YLo/c.YStep) * c.YStep; v <= c.YHi+c.YStep/1e6; v += c.YStep {
			_, y := p.at(c.XLo, v)
			p.fill(c.Theme.Grid, p.img.Bounds(), func(pn *pen) { pn.rect(p.left, y-0.5*s, p.right, y+0.5*s) })
			p.write(p.text, c.Theme.Muted, c.YLabel(v), p.left-8*s, y+4*s, 1)
		}
	}

	for _, v := range c.XTicks {
		x, _ := p.at(v, c.YLo)
		p.fill(c.Theme.Grid, p.img.Bounds(), func(pn *pen) { pn.rect(x-0.5*s, p.top, x+0.5*s, p.bottom) })
		p.write(p.text, c.Theme.Muted, c.XLabel(v), x, p.bottom+17*s, 0.5)
	}

	p.fill(c.Theme.Muted, p.img.Bounds(), func(pn *pen) { pn.rect(p.left, p.bottom, p.right, p.bottom+s) })
}

func (c Chart) drawLine(
	p *painter,
	l Line,
) {
	if len(l.Points) == 0 {
		return
	}

	width := l.Width * p.scale

	p.fill(l.Color, p.plotClip(), func(pn *pen) {
		x0, y0 := p.at(l.Points[0][0], l.Points[0][1])
		if l.Dots || len(l.Points) == 1 {
			pn.disc(x0, y0, width/2)
		}

		for _, pt := range l.Points[1:] {
			x1, y1 := p.at(pt[0], pt[1])
			if l.Dots {
				pn.disc(x1, y1, width/2)
			} else {
				pn.segment(x0, y0, x1, y1, width)
			}

			x0, y0 = x1, y1
		}
	})
}

// markerRadius is the radius of a marker, in points.
const markerRadius = 7.5

func (c Chart) drawMarker(
	p *painter,
	m Marker,
) {
	x, y := p.at(m.X, m.Y)
	radius := markerRadius * p.scale

	p.fill(m.Fill, p.img.Bounds(), func(pn *pen) { pn.disc(x, y, radius) })
	p.write(p.marker, m.Text, m.Label, x, y+3.2*p.scale, 0.5)
}

// drawLegend lays the keys out from the left of the plot, above it.
func (c Chart) drawLegend(
	p *painter,
) {
	s := p.scale
	x, y := p.left, 14*s

	for _, k := range c.Legend {
		p.fill(k.Color, p.img.Bounds(), func(pn *pen) { pn.rect(x, y-5*s, x+legendSwatch*s, y-2*s) })
		x += (legendSwatch + 5) * s
		p.write(p.strong, c.Theme.Ink, k.Label, x, y, 0)

		d := font.Drawer{Face: p.strong}
		x += float64(d.MeasureString(k.Label))/64 + legendGap*s
	}
}
