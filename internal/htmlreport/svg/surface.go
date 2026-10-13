package svg

import (
	"cmp"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Geometry of a surface. The page script draws with the same numbers
// (report.js, Surface): the picture it replaces is the one drawn here.
const (
	// surfaceYaw and surfacePitch are the angles the surface is first
	// seen from, in radians: turned enough to show both of its slopes.
	surfaceYaw   = -0.62
	surfacePitch = 0.5
	// surfaceHalfX and surfaceHalfY are the half sides of the floor, and
	// surfaceRise the height of the full scale, in units of the scene.
	surfaceHalfX = 1.0
	surfaceHalfY = 0.8
	surfaceRise  = 1.3
	// surfaceScale is the size of a unit of the scene, as a share of the
	// width of a panel.
	surfaceScale = 0.31
	// surfaceCentre places the middle of the scene down the height of the
	// figure.
	surfaceCentre = 0.47
	// surfaceLabel and surfaceName are the distances of the tick labels
	// and of the names of the axes to the floor.
	surfaceLabel = 0.16
	surfaceName  = 0.52
	// surfaceLean is how much a side of the floor leans across the figure
	// before its labels hang from their end.
	surfaceLean = 0.35
	// surfaceFaint is the share of the colour of a face of shade zero.
	surfaceFaint = 16
	// surfaceLevels is the number of levels labelled on the height, and
	// surfaceTick the length of their ticks.
	surfaceLevels = 4
	surfaceTick   = 4
)

// SurfacePanel is one of the heights drawn side by side over the same
// floor: the VMAF of a grid, its bitrate.
type SurfacePanel struct {
	Name string `json:"name"`
	Unit Unit   `json:"unit"`
	// Log draws the height on a log scale.
	Log bool `json:"log,omitempty"`
	// Min and Max are the ends of the scale.
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// SurfaceShape is one surface over the floor: the heights of each panel
// at every point (a line of the floor after the other), and the shade of
// each point, from 0 to 1.
type SurfaceShape struct {
	Name  string      `json:"name"`
	Z     [][]float64 `json:"z"`
	Shade []float64   `json:"shade"`
}

// Surface draws values over a grid as surfaces in perspective, one per
// panel. The first shape is the one drawn; the page script lets the
// reader turn the surfaces, read their points and go through the other
// shapes, each drawn over the outline of the first.
type Surface struct {
	Title         string         `json:"-"`
	Width, Height int            `json:"-"`
	Rows          []string       `json:"rows"`
	Cols          []float64      `json:"cols"`
	RowTitle      string         `json:"rowTitle"`
	ColTitle      string         `json:"colTitle"`
	Panels        []SurfacePanel `json:"panels"`
	Shapes        []SurfaceShape `json:"shapes"`
	// Lo and Hi are, for each panel, the lowest and highest values of
	// each point over the shapes: drawn as a bar through the first.
	Lo [][]float64 `json:"lo,omitempty"`
	Hi [][]float64 `json:"hi,omitempty"`
	// Color is the hue of the faces and ShadeTitle what their shade is.
	Color      string `json:"color"`
	ShadeTitle string `json:"shadeTitle"`
}

// HTML renders the surfaces as a figure, with the data the page script
// redraws them from.
func (c Surface) HTML() template.HTML {
	var b strings.Builder

	b.WriteString(`<figure class="figure surface"><div class="chart-head">`)

	if c.Title != "" {
		fmt.Fprintf(&b, `<figcaption class="chart-title">%s</figcaption>`, template.HTMLEscapeString(c.Title))
	}

	fmt.Fprintf(&b, `<span class="ramp" style="--c:%s"><span>%s</span><i></i></span>`, c.Color, template.HTMLEscapeString(c.ShadeTitle))
	b.WriteString(`<div class="chart-tools surface-tools" hidden></div></div><div class="plot">`)
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" class="chart-svg surface-svg" role="img" aria-label="%s">`, c.Width, c.Height, template.HTMLEscapeString(c.Title))

	if len(c.Shapes) > 0 {
		for k := range c.Panels {
			c.panel(&b, k)
		}
	}

	b.WriteString(`</svg></div>`)

	// json.Marshal escapes <, > and &: the data cannot close the script.
	data, _ := json.Marshal(c) //nolint:errchkjson // numbers and strings: cannot fail
	b.WriteString(`<script type="application/json" class="surface-data">`)
	b.Write(data)
	b.WriteString(`</script></figure>`)

	return template.HTML(b.String()) //nolint:gosec // built from numbers, JSON with escaped HTML and escaped labels
}

// view projects the scene of a panel on the figure.
type view struct {
	cx, cy, scale float64
	yaw, pitch    float64
}

// project returns where the point (x, y) of the floor raised to z, from 0
// to 1, is drawn, and its depth: the larger, the nearer.
func (v view) project(
	x, y, z float64,
) (sx, sy, depth float64) {
	across := x*math.Cos(v.yaw) - y*math.Sin(v.yaw)
	depth = x*math.Sin(v.yaw) + y*math.Cos(v.yaw)

	return v.cx + v.scale*across, v.cy + v.scale*(depth*math.Sin(v.pitch)-(z-0.5)*surfaceRise*math.Cos(v.pitch)), depth
}

// floorX and floorY place the column i and the row j on the floor: the
// columns at their values, the rows evenly.
func (c Surface) floorX(
	i int,
) float64 {
	first, last := c.Cols[0], c.Cols[len(c.Cols)-1]
	if last == first {
		return 0
	}

	return ((c.Cols[i]-first)/(last-first)*2 - 1) * surfaceHalfX
}

func (c Surface) floorY(
	j int,
) float64 {
	if len(c.Rows) < 2 {
		return 0
	}

	return (float64(j)/float64(len(c.Rows)-1)*2 - 1) * surfaceHalfY
}

// level places v on the scale of the panel, from 0 to 1.
func (p SurfacePanel) level(
	v float64,
) float64 {
	if p.Max <= p.Min {
		return 0
	}

	if p.Log {
		return max(0, min(1, math.Log(max(v, minLogX)/p.Min)/math.Log(p.Max/p.Min)))
	}

	return max(0, min(1, (v-p.Min)/(p.Max-p.Min)))
}

// value is the value at level of the scale of the panel.
func (p SurfacePanel) value(
	level float64,
) float64 {
	if p.Log {
		return p.Min * math.Pow(p.Max/p.Min, level)
	}

	return p.Min + level*(p.Max-p.Min)
}

// face is a face of a surface, with its depth.
type face struct {
	points string
	shade  float64
	depth  float64
}

// panel draws the first shape on panel k: the floor and its labels, the
// faces from the farthest to the nearest, then the bars of the spread.
func (c Surface) panel(
	b *strings.Builder,
	k int,
) {
	width := float64(c.Width) / float64(len(c.Panels))
	v := view{cx: width * (float64(k) + 0.5), cy: float64(c.Height) * surfaceCentre, scale: width * surfaceScale, yaw: surfaceYaw, pitch: surfacePitch}
	panel, shape := c.Panels[k], c.Shapes[0]
	cols := len(c.Cols)

	fmt.Fprintf(b, `<g class="surface-panel"><text class="panel-title" x="%.1f" y="16" text-anchor="middle">%s</text>`, v.cx, template.HTMLEscapeString(panel.Name))
	c.floor(b, v, panel)

	at := func(i, j int) (float64, float64, float64) {
		return v.project(c.floorX(i), c.floorY(j), panel.level(shape.Z[k][j*cols+i]))
	}

	var faces []face

	for j := range len(c.Rows) - 1 {
		for i := range cols - 1 {
			var (
				pts   []string
				depth float64
				shade float64
			)

			for _, corner := range [][2]int{{i, j}, {i + 1, j}, {i + 1, j + 1}, {i, j + 1}} {
				sx, sy, d := at(corner[0], corner[1])
				pts = append(pts, fmt.Sprintf("%.1f,%.1f", sx, sy))
				depth += d
				shade += shape.Shade[corner[1]*cols+corner[0]] / 4
			}

			faces = append(faces, face{points: strings.Join(pts, " "), shade: shade, depth: depth})
		}
	}

	slices.SortStableFunc(faces, func(a, b face) int { return cmp.Compare(a.depth, b.depth) })

	for _, f := range faces {
		fmt.Fprintf(b, `<polygon class="face" points="%s" fill="color-mix(in oklab, %s %.0f%%, var(--surface-3))"/>`,
			f.points, c.Color, surfaceFaint+(100-surfaceFaint)*f.shade)
	}

	if k < len(c.Lo) && k < len(c.Hi) {
		for j := range c.Rows {
			for i := range cols {
				x1, y1, _ := v.project(c.floorX(i), c.floorY(j), panel.level(c.Lo[k][j*cols+i]))
				x2, y2, _ := v.project(c.floorX(i), c.floorY(j), panel.level(c.Hi[k][j*cols+i]))
				fmt.Fprintf(b, `<line class="spread" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, x1, y1, x2, y2)
			}
		}
	}

	b.WriteString(`</g>`)
}

// floor draws the outline of the floor, the labels of its columns and
// rows hanging from its nearest sides, and the scale of the height on the
// post at its leftmost corner.
func (c Surface) floor(
	b *strings.Builder,
	v view,
	panel SurfacePanel,
) {
	corners := [][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}}

	var outline []string

	// The post stands on the corner drawn leftmost.
	postX, postY, leftmost := 0.0, 0.0, math.Inf(1)

	for _, corner := range corners {
		sx, sy, _ := v.project(corner[0]*surfaceHalfX, corner[1]*surfaceHalfY, 0)
		outline = append(outline, fmt.Sprintf("%.1f,%.1f", sx, sy))

		if sx < leftmost {
			postX, postY, leftmost = corner[0]*surfaceHalfX, corner[1]*surfaceHalfY, sx
		}
	}

	fmt.Fprintf(b, `<polygon class="floor" points="%s"/>`, strings.Join(outline, " "))

	// The sides nearest to the reader: where labels hide nothing.
	nearY, nearX := 1.0, 1.0
	if math.Cos(v.yaw) < 0 {
		nearY = -1
	}

	if math.Sin(v.yaw) < 0 {
		nearX = -1
	}

	// Labels hang away from the floor: the way their side leans.
	colAnchor, rowAnchor := hang(-nearY*math.Sin(v.yaw)), hang(nearX*math.Cos(v.yaw))

	for i, col := range c.Cols {
		sx, sy, _ := v.project(c.floorX(i), nearY*(surfaceHalfY+surfaceLabel), 0)
		fmt.Fprintf(b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, sx, sy+4, colAnchor, strconv.FormatFloat(col, 'f', -1, 64))
	}

	sx, sy, _ := v.project(0, nearY*(surfaceHalfY+surfaceName), 0)
	fmt.Fprintf(b, `<text class="axis name" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, sx, sy+4, colAnchor, template.HTMLEscapeString(c.ColTitle))

	for j, row := range c.Rows {
		sx, sy, _ := v.project(nearX*(surfaceHalfX+surfaceLabel), c.floorY(j), 0)
		fmt.Fprintf(b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, sx, sy+4, rowAnchor, template.HTMLEscapeString(row))
	}

	sx, sy, _ = v.project(nearX*(surfaceHalfX+surfaceName), 0, 0)
	fmt.Fprintf(b, `<text class="axis name" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, sx, sy+4, rowAnchor, template.HTMLEscapeString(c.RowTitle))

	x0, y0, _ := v.project(postX, postY, 0)
	x1, y1, _ := v.project(postX, postY, 1)
	fmt.Fprintf(b, `<line class="baseline" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, x0, y0, x1, y1)

	for n := range surfaceLevels + 1 {
		level := float64(n) / surfaceLevels
		sx, sy, _ := v.project(postX, postY, level)
		fmt.Fprintf(b, `<line class="tick" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, sx-surfaceTick, sy, sx, sy)

		// The lowest level is written above its tick: the labels of the
		// floor start under it.
		lift := 4.0
		if n == 0 {
			lift = -2
		}

		fmt.Fprintf(b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, sx-surfaceTick-3, sy+lift, //nolint:mnd // gap to the tick
			template.HTMLEscapeString(panel.Unit.tickLabel(panel.value(level))))
	}
}

// hang returns how a label is anchored on a side of the floor leaning by
// lean across the figure, from -1 (to the left) to 1: its end on a side
// leaning left, its start on one leaning right, its middle on a side
// facing the reader.
func hang(
	lean float64,
) string {
	switch {
	case lean < -surfaceLean:
		return "end"
	case lean > surfaceLean:
		return "start"
	}

	return "middle"
}

// tickLabel writes a level of a height: bitrates short, numbers whole.
func (u Unit) tickLabel(
	v float64,
) string {
	if u == UnitBitrate {
		return bitrateLabel(v)
	}

	return strconv.FormatFloat(v, 'f', 0, 64)
}
