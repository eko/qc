// Package hdr measures the fidelity of HDR (PQ or HLG) video on decoded
// 4:2:0 frames, in pure Go, with two metrics of the HDR literature:
//
//   - wPSNR, the luma-weighted PSNR of the JVET HDR common test conditions
//     (JVET-H1002 Annex D, as implemented by the VTM reference software):
//     the squared error of every sample is weighted by 2^(y/3), y =
//     clip(0.015·Y − 7.5, −3, 6) with Y the co-located 10-bit luma code
//     value of the reference, so that errors in bright PQ areas, where a
//     code value spans more light, weigh more;
//   - ΔE ITP, the colour difference of ITU-R BT.2124: 720 times the
//     distance between reference and distorted pixels in ICtCp (T = Ct/2),
//     scaled so that 1 is a just noticeable difference. Its mean and 99th
//     percentile are reported per frame.
//
// Both are measured on the frames the VMAF engine already decodes (at its
// evaluation resolution and depth), so they cost no decoding. wPSNR is
// defined on PQ code values: package quality reports it for PQ references
// only, ΔE ITP for PQ and HLG (on the 1000 cd/m² reference display of
// BT.2124).
package hdr

import (
	"math"
	"sync"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/colorimetry"
	"github.com/eko/qc/media"
)

// wPSNR weighting of JVET-H1002 Annex D.
const (
	weightSlope  = 0.015
	weightOffset = -1.5 - 6
	weightMin    = -3.0
	weightMax    = 6.0
	// weightDepth is the depth luma code values are expressed at before
	// weighting.
	weightDepth = 10
)

// DeltaEStep is the spacing, in chroma samples, of the points ΔE ITP is
// evaluated on: every second chroma sample of every second row, co-sited
// with a luma sample. Each point converts two pixels into ICtCp (four
// lookups, two matrices, three inverse PQ each): scoring every chroma
// sample would cost four times as much, about a quarter of VMAF v1's CPU at
// 1080p, while a quarter of them moves the mean ΔE ITP of the validation
// encodes by at most 0.5% and its 99th percentile by 0.7% (see
// docs/hdr.md).
const DeltaEStep = 2

// deltaEBins bins ΔE ITP by deltaEBinWidth up to deltaEBins·width for the
// per-frame percentile: 0.02 is a fiftieth of a just noticeable
// difference, and larger differences are counted in the last bin.
const (
	deltaEBins     = 4096
	deltaEBinWidth = 0.02
)

// Percentile is the per-frame percentile of ΔE ITP reported next to its
// mean: the colour errors of the worst 1% of a picture (a sky, a face)
// that its mean dilutes.
const Percentile = 99

// Values are the HDR metrics of one frame.
type Values struct {
	// WPSNR is the wPSNR of the Y, Cb and Cr planes in dB, +Inf for
	// identical planes.
	WPSNR [3]float64
	// DeltaE and DeltaEP99 are the mean and 99th percentile of ΔE ITP.
	DeltaE    float64
	DeltaEP99 float64
}

// Meter measures frame pairs. It is stateless between frames but keeps
// scratch space: one Meter per goroutine.
type Meter struct {
	width, height int
	workers       int
	weights       []float64
	decoder       *colorimetry.Decoder
	pq            *colorimetry.PQTable
	peak          float64

	// rows hold the ΔE sums of each chroma row, combined in row order so
	// that results do not depend on the number of workers; bands hold the
	// integer sums of each worker, which add up in any order.
	rows  []rowSums
	bands []band
}

// rowSums are the ΔE ITP sums of a chroma row.
type rowSums struct {
	deltaE float64
	points int
}

// band holds the sums of the rows a worker measures: per plane, the
// squared errors of the samples by the reference luma code value that
// weighs them (weighted once per frame, which is exact and several times
// faster than weighting every sample), and the ΔE histogram.
type band struct {
	sse  [3][]int64
	hist []int32
}

// New returns a Meter of width×height 4:2:0 frames (even dimensions) of
// bitDepth bits (8 to 16), carrying a PQ or HLG signal of the given colour
// description (range, transfer). workers bounds the goroutines measuring
// the rows of one frame.
func New(
	width, height int,
	color media.Color,
	bitDepth int,
	workers int,
) *Meter {
	m := &Meter{
		width:   width,
		height:  height,
		workers: max(1, workers),
		weights: lumaWeights(bitDepth),
		decoder: colorimetry.NewDecoder(colorimetry.Signal{
			Transfer: color.Transfer, BitDepth: bitDepth, FullRange: color.FullRange(), Peak: media.HLGNominalPeak,
		}),
		pq: colorimetry.NewPQTable(),
		// VTM's peak: 255 scaled to the bit depth (1020 at 10 bits).
		peak: float64(int(255) << max(bitDepth-8, 0)),
	}

	m.rows = make([]rowSums, (height+1)/2)
	m.bands = make([]band, min(m.workers, len(m.rows)))

	for i := range m.bands {
		b := &m.bands[i]
		b.hist = make([]int32, deltaEBins)

		for c := range b.sse {
			b.sse[c] = make([]int64, lanes*len(m.weights))
		}
	}

	return m
}

// lumaWeights tabulates the wPSNR weight of every luma code value of
// depth bits, expressed at 10 bits as in the VTM.
func lumaWeights(
	depth int,
) []float64 {
	out := make([]float64, 1<<depth)

	for v := range out {
		x := float64(v)
		if depth != weightDepth {
			x = math.Ldexp(x, weightDepth-depth)
		}

		y := min(max(weightSlope*x+weightOffset, weightMin), weightMax)
		out[v] = math.Exp2(y / 3)
	}

	return out
}

// Decibels converts the weighted SSE of a plane of samples samples into
// wPSNR: 10·log10(peak²·samples / wsse), +Inf when wsse is 0.
func (m *Meter) Decibels(
	wsse float64,
	samples int,
) float64 {
	return 10 * math.Log10(m.peak*m.peak*float64(samples)/wsse)
}

// Measure returns the HDR metrics of dist against ref. Both frames must be
// width×height with chroma, at the Meter's bit depth.
func (m *Meter) Measure(
	ref, dist *frame.Frame,
) Values {
	for i := range m.bands {
		b := &m.bands[i]
		clear(b.hist)

		for c := range b.sse {
			clear(b.sse[c])
		}
	}

	if ref.Luma.BytesPerSample == 2 {
		m.parallel(func(b *band, from, to int) {
			measureRows(m, b, views(ref, samples16), views(dist, samples16), from, to)
		})
	} else {
		m.parallel(func(b *band, from, to int) {
			measureRows(m, b, views(ref, samples8), views(dist, samples8), from, to)
		})
	}

	return m.values()
}

// parallel splits the chroma rows into one band per worker, measured on
// the calling goroutine when there is a single band.
func (m *Meter) parallel(
	measure func(b *band, from, to int),
) {
	rows := len(m.rows)
	per := (rows + len(m.bands) - 1) / len(m.bands)

	if len(m.bands) == 1 {
		measure(&m.bands[0], 0, rows)

		return
	}

	var wg sync.WaitGroup

	for i := range m.bands {
		from, to := i*per, min((i+1)*per, rows)
		if from >= to {
			continue
		}

		wg.Go(func() { measure(&m.bands[i], from, to) })
	}

	wg.Wait()
}

// values combines the rows and bands of a frame.
func (m *Meter) values() Values {
	var (
		v      Values
		wsse   [3]float64
		sum    float64
		points int
	)

	for _, r := range m.rows {
		sum += r.deltaE
		points += r.points
	}

	first := &m.bands[0]
	for _, b := range m.bands[1:] {
		for k, n := range b.hist {
			first.hist[k] += n
		}

		for c := range b.sse {
			for code, e := range b.sse[c] {
				first.sse[c][code] += e
			}
		}
	}

	for c := range wsse {
		for k, e := range first.sse[c] {
			wsse[c] += m.weights[k/lanes] * float64(e)
		}
	}

	hist := first.hist

	cw, ch := (m.width+1)/2, (m.height+1)/2
	v.WPSNR[0] = m.Decibels(wsse[0], m.width*m.height)
	v.WPSNR[1] = m.Decibels(wsse[1], cw*ch)
	v.WPSNR[2] = m.Decibels(wsse[2], cw*ch)
	v.DeltaE = sum / float64(max(points, 1))
	v.DeltaEP99 = percentile(hist, points, Percentile)

	return v
}

// percentile reads the p-th percentile of a ΔE histogram of points
// values, at the upper edge of its bin.
func percentile(
	hist []int32,
	points int,
	p float64,
) float64 {
	rank := int(math.Ceil(float64(points) * p / 100))
	seen := 0

	for k, n := range hist {
		seen += int(n)
		if seen >= rank && seen > 0 {
			return float64(k+1) * deltaEBinWidth
		}
	}

	return 0
}

// sample is a stored sample: 8-bit, or 16-bit for higher depths.
type sample interface {
	~uint8 | ~uint16
}

// view is a plane seen as samples; stride is in samples.
type view[T sample] struct {
	pix    []T
	stride int
}

func samples8(
	p *frame.Plane,
) view[uint8] {
	return view[uint8]{pix: p.Pix, stride: p.Stride}
}

func samples16(
	p *frame.Plane,
) view[uint16] {
	return view[uint16]{pix: p.Uint16(), stride: p.Stride / 2}
}

// views returns the Y, Cb and Cr planes of f as samples.
func views[T sample](
	f *frame.Frame,
	of func(*frame.Plane) view[T],
) [3]view[T] {
	return [3]view[T]{of(&f.Luma), of(&f.Cb), of(&f.Cr)}
}

// measureRows accumulates the metrics of chroma rows [from, to) and of the
// two luma rows of each.
func measureRows[T sample](
	m *Meter,
	b *band,
	ref, dist [3]view[T],
	from, to int,
) {
	cw := (m.width + 1) / 2

	for cy := from; cy < to; cy++ {
		sums := &m.rows[cy]
		*sums = rowSums{}

		for dy := range 2 {
			if y := 2*cy + dy; y < m.height {
				lumaRow(b.sse[0], ref[0].pix[y*ref[0].stride:][:m.width], dist[0].pix[y*dist[0].stride:][:m.width])
			}
		}

		lumaRef := ref[0].pix[2*cy*ref[0].stride:][:m.width]

		for c := 1; c < 3; c++ {
			chromaRow(b.sse[c], lumaRef, ref[c].pix[cy*ref[c].stride:][:cw], dist[c].pix[cy*dist[c].stride:][:cw])
		}

		if cy%DeltaEStep == 0 {
			deltaERow(m, b, sums, ref, dist, cy)
		}
	}
}

// lanes interleaves the squared error sums of neighbouring samples: flat
// areas give runs of samples of one code value, and adding them all to one
// counter would chain every addition to the previous one. Four counters
// per code value (x mod 4) run the additions in parallel.
const lanes = 4

// lumaRow adds the squared errors of a luma row to sse, by the reference
// value of each sample, which weighs it (lanes counters per value).
func lumaRow[T sample](
	sse []int64,
	ref, dist []T,
) {
	mask := len(sse) - 1
	dist = dist[:len(ref)]

	for x, r := range ref {
		d := int64(r) - int64(dist[x])
		sse[(int(r)*lanes+x&(lanes-1))&mask] += d * d
	}
}

// chromaRow adds the squared errors of a chroma row to sse, by the
// reference luma at the top-left luma position of each sample, which
// weighs it (the VTM's choice).
func chromaRow[T sample](
	sse []int64,
	luma, ref, dist []T,
) {
	mask := len(sse) - 1
	dist = dist[:len(ref)]
	luma = luma[:2*len(ref)-1]

	for x, r := range ref {
		d := int64(r) - int64(dist[x])
		sse[(int(luma[2*x])*lanes+x&(lanes-1))&mask] += d * d
	}
}

// deltaERow adds the ΔE ITP of the points of chroma row cy: every
// DeltaEStep-th chroma sample, with the luma sample co-sited with it.
func deltaERow[T sample](
	m *Meter,
	b *band,
	sums *rowSums,
	ref, dist [3]view[T],
	cy int,
) {
	cw := (m.width + 1) / 2
	ry, dy := ref[0].pix[2*cy*ref[0].stride:], dist[0].pix[2*cy*dist[0].stride:]
	rcb, rcr := ref[1].pix[cy*ref[1].stride:][:cw], ref[2].pix[cy*ref[2].stride:][:cw]
	dcb, dcr := dist[1].pix[cy*dist[1].stride:][:cw], dist[2].pix[cy*dist[2].stride:][:cw]

	for cx := 0; cx < cw; cx += DeltaEStep {
		sums.points++

		y1, cb1, cr1 := ry[2*cx], rcb[cx], rcr[cx]
		y2, cb2, cr2 := dy[2*cx], dcb[cx], dcr[cx]

		if y1 == y2 && cb1 == cb2 && cr1 == cr2 {
			b.hist[0]++

			continue
		}

		i1, t1, p1 := m.decoder.ITP(int(y1), int(cb1), int(cr1), m.pq)
		i2, t2, p2 := m.decoder.ITP(int(y2), int(cb2), int(cr2), m.pq)

		di, dt, dp := i1-i2, t1-t2, p1-p2
		e := colorimetry.DeltaEITPScale * math.Sqrt(float64(di*di+dt*dt+dp*dp))

		sums.deltaE += e
		b.hist[min(int(e/deltaEBinWidth), deltaEBins-1)]++
	}
}
