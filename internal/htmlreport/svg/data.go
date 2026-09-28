package svg

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"math"
	"slices"
	"strings"
)

// data is the JSON the page script reads: plot geometry, then every
// series at full resolution in compact columns.
func (p plot) data() []byte {
	c := p.chart
	d := chartData{
		X: c.X, Y: c.Y, Log: c.LogX, LogY: c.LogY,
		Box: [4]float64{p.left, p.top, p.pw, p.ph},
		W:   c.Width, H: c.Height,
		XR: [2]float64{p.xLo, p.xHi}, YR: [2]float64{p.yLo, p.yHi}, YStep: p.yStep,
	}

	xDigits := TimeDigits
	if c.X != UnitTime {
		xDigits = valueDigits
	}

	for i, s := range c.Series {
		sd := seriesData{
			Index: i, Name: s.Name, Key: s.key(), Color: s.Color, Style: s.style(),
			Bold: s.Bold, NoTip: s.NoTip, TipOnly: s.TipOnly, Digits: s.Digits,
		}
		sd.columns(s, xDigits)
		d.Series = append(d.Series, sd)
	}

	for _, band := range c.Bands {
		d.Bands = append(d.Bands, bandData{From: band.From, To: band.To, Label: band.Label, Color: band.Color})
	}

	for _, z := range c.Zones {
		d.Zones = append(d.Zones, bandData{From: z.From, To: z.To, Label: z.Label, Color: z.Color})
	}

	// Every field is a number, a bool or a string: marshalling cannot fail.
	out, _ := json.Marshal(d) //nolint:errchkjson // see above

	return out
}

// compressAbove is the size of chart data (bytes) from which it is
// gzipped: per-frame series of long titles shrink about threefold, which
// keeps a two-hour report at a couple of megabytes.
const compressAbove = 32 << 10

// writeData embeds chart data as JSON, or gzipped and base64-encoded when
// large; the page script inflates it with the browser's
// DecompressionStream.
func writeData(
	b *strings.Builder,
	data []byte,
) {
	if len(data) >= compressAbove {
		b.WriteString(`<script type="application/octet-stream" class="chart-data" data-encoding="gzip">`)
		b.WriteString(gzipBase64(data))
		b.WriteString(`</script>`)

		return
	}

	// json.Marshal escapes <, > and &: the data cannot close the script.
	b.WriteString(`<script type="application/json" class="chart-data">`)
	b.Write(data)
	b.WriteString(`</script>`)
}

// gzipBase64 compresses data at the best level: the page is written once
// and read many times.
func gzipBase64(
	data []byte,
) string {
	var buf bytes.Buffer

	// The level is valid and a bytes.Buffer never fails a write: gzip
	// cannot return an error here.
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(data)
	_ = zw.Close()

	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

const (
	// TimeDigits keeps times to the millisecond, in the chart data and in
	// UnitTime.Format.
	TimeDigits = 3
	// valueDigits keeps non-time x values (bitrates) to 2 decimals.
	valueDigits = 2
)

type chartData struct {
	X      Unit         `json:"x"`
	Y      Unit         `json:"y"`
	Log    bool         `json:"log,omitempty"`
	LogY   bool         `json:"logy,omitempty"`
	Box    [4]float64   `json:"box"`
	W      int          `json:"w"`
	H      int          `json:"h"`
	XR     [2]float64   `json:"xr"`
	YR     [2]float64   `json:"yr"`
	YStep  float64      `json:"ys"`
	Series []seriesData `json:"series"`
	Bands  []bandData   `json:"bands,omitempty"`
	Zones  []bandData   `json:"zones,omitempty"`
}

type seriesData struct {
	Index   int          `json:"i"`
	Name    string       `json:"n"`
	Key     string       `json:"k"`
	Color   string       `json:"c"`
	Style   string       `json:"s"`
	Bold    bool         `json:"b,omitempty"`
	NoTip   bool         `json:"notip,omitempty"`
	TipOnly bool         `json:"tiponly,omitempty"`
	Digits  int          `json:"d"`
	X       column       `json:"x"`
	Y       column       `json:"y"`
	Frames  *column      `json:"f,omitempty"`
	Details []detailData `json:"info,omitempty"`
}

type detailData struct {
	Title  string      `json:"t"`
	Fields [][2]string `json:"r"`
}

type bandData struct {
	From  float64 `json:"a"`
	To    float64 `json:"b"`
	Label string  `json:"l"`
	Color string  `json:"c"`
}

// style is how the page script draws the series.
func (s Series) style() string {
	switch {
	case s.Guide:
		return "guide"
	case s.Markers:
		return "markers"
	case s.Step:
		return "step"
	case s.Area:
		return "area"
	}

	return "line"
}

// columns encodes the series' samples, or its points when it has none,
// skipping non-finite values (PSNR of identical frames).
func (sd *seriesData) columns(
	s Series,
	xDigits int,
) {
	xs, ys, frames, details := pointColumns(s)

	keep := make([]int, 0, len(xs))
	for i := range xs {
		if isFinite(xs[i]) && isFinite(ys[i]) {
			keep = append(keep, i)
		}
	}

	// Tooltips search x by bisection: sort when the samples are not.
	slices.SortStableFunc(keep, func(a, b int) int { return compareFloat(xs[a], xs[b]) })

	x, y := make([]float64, len(keep)), make([]float64, len(keep))
	for j, i := range keep {
		x[j], y[j] = xs[i], ys[i]
	}

	sd.X, sd.Y = encodeColumn(x, xDigits), encodeColumn(y, sd.Digits)

	if frames != nil {
		f := make([]float64, len(keep))
		for j, i := range keep {
			f[j] = float64(frames[i])
		}

		col := encodeColumn(f, 0)
		sd.Frames = &col
	}

	if details != nil {
		for _, i := range keep {
			dd := detailData{Title: details[i].Title}
			for _, f := range details[i].Fields {
				dd.Fields = append(dd.Fields, [2]string{f.Label, f.Value})
			}

			sd.Details = append(sd.Details, dd)
		}
	}
}

// pointColumns are the columns of a series: its samples, or its points.
// Frames and details are dropped unless they match the samples one to one.
func pointColumns(
	s Series,
) (xs, ys []float64, frames []int, details []Detail) {
	if sm := s.Samples; sm != nil {
		n := min(len(sm.X), len(sm.Y))
		xs, ys = sm.X[:n], sm.Y[:n]

		if len(sm.Frames) >= n {
			frames = sm.Frames[:n]
		}

		if len(sm.Details) >= n {
			details = sm.Details[:n]
		}

		return xs, ys, frames, details
	}

	xs, ys = make([]float64, len(s.Points)), make([]float64, len(s.Points))
	for i, pt := range s.Points {
		xs[i], ys[i] = pt[0], pt[1]
	}

	return xs, ys, nil, nil
}

func isFinite(
	v float64,
) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func compareFloat(
	a, b float64,
) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}

	return 0
}

// column is a compact numeric column: an arithmetic progression (constant
// frame rate timestamps, frame numbers) as start, step and count, or else
// values quantized to 10^-Digits as integer deltas, which JSON writes in a
// few characters each.
type column struct {
	Start  float64 `json:"a,omitempty"`
	Step   float64 `json:"s,omitempty"`
	N      int     `json:"n"`
	Scale  float64 `json:"q,omitempty"`
	Deltas []int64 `json:"d,omitempty"`
}

// encodeColumn encodes finite values to the given decimals.
func encodeColumn(
	values []float64,
	digits int,
) column {
	n := len(values)
	col := column{N: n}

	if n == 0 {
		return col
	}

	scale := math.Pow(10, float64(digits))

	step := 0.0
	if n > 1 {
		step = (values[n-1] - values[0]) / float64(n-1)
	}

	progression := true

	for i, v := range values {
		if math.Abs(v-(values[0]+float64(i)*step))*scale > 0.5 {
			progression = false

			break
		}
	}

	if progression {
		col.Start, col.Step = values[0], step

		return col
	}

	col.Scale = scale
	col.Deltas = make([]int64, n)

	var prev int64

	for i, v := range values {
		q := int64(math.Round(v * scale))
		col.Deltas[i] = q - prev
		prev = q
	}

	return col
}
