// Package xpsnr computes XPSNR, the extended perceptually weighted PSNR of
// Fraunhofer HHI (Helmrich et al.), on decoded 4:2:0 frames.
//
// It follows ffmpeg's vf_xpsnr.c operation for operation, so that per-frame
// and pooled values match ffmpeg's xpsnr filter given the same frames with
// the reference as its first input: the squared error of each block is
// weighted by the inverse of the block's spatial and temporal activity,
// measured on the reference. The integer arithmetic, the rounding of the
// weighted sums and the summation order are ffmpeg's; blocks are measured in
// parallel and their sums accumulated in raster order afterwards.
package xpsnr

import (
	"math"
	"sync"
	"unsafe"

	"github.com/eko/qc/frame"
)

const (
	// gamma weights temporal against spatial activity (XPSNR_GAMMA).
	gamma = 2
	// highFrameRate is the integer frame rate from which the temporal
	// activity is a second-order difference: at high frame rates
	// consecutive frames differ too little for a first-order one.
	highFrameRate = 32
	// downsampleArea is the picture area above which the spatial high-pass
	// runs on 2×2 groups of samples ("a bit more than HD").
	downsampleArea = 2048 * 1152
	// smoothingArea is the picture area up to which block weights are
	// min-smoothed with their neighbours, as in the paper.
	smoothingArea = 640 * 480
	// uhdArea is the area block sizes and activities are normalised to.
	uhdArea = 3840.0 * 2160.0
	// uhdBlock is the block size at UHD, scaled by √(area ratio) below it.
	uhdBlock = 32.0
	// minBlock is the smallest block XPSNR weights: below it the picture
	// is too small and the plain SSE is used.
	minBlock = 4
	// shortRow is the width up to which ffmpeg discards the result of its
	// downsampled high-pass (its C fallback ignores the return value): kept
	// for exactness.
	shortRow = 12
	// minRatio avoids dividing by zero for empty pictures.
	minRatio = 0.00001
	// planes are Y, Cb and Cr.
	planes = 3
)

// Distortion is the square root of the weighted sum of squared errors of one
// frame, per plane (Y, Cb, Cr). Its mean over frames is what ffmpeg pools
// (a square-mean-root average); Decibels converts it to XPSNR.
type Distortion [planes]float64

// sample is a stored sample: 8-bit, or 16-bit for higher depths.
type sample interface {
	~uint8 | ~int16
}

// view is a plane seen as samples; stride is in samples.
type view[T sample] struct {
	pix           []T
	width, height int
	stride        int
}

// row returns the samples [x, x+n) of row y.
func (v view[T]) row(
	y, x, n int,
) []T {
	start := y*v.stride + x

	return v.pix[start : start+n]
}

// Meter measures XPSNR frame after frame. The temporal activity makes it
// stateful: frames must be given in display order. It is not safe for
// concurrent use.
type Meter struct {
	width, height int
	depth         int
	secondOrder   bool
	workers       int

	block      int
	downsample bool
	blocksX    int
	blocksY    int
	avgAct     float64
	smooth     bool

	// prev1 and prev2 are the reference luma one and two frames back
	// (ffmpeg's buf_org_m1 and buf_org_m2), zero before the first frame.
	prev1, prev2 []int16

	sse     []float64
	weights []float64
	chroma  [planes][]float64
}

// New returns a Meter for width×height 4:2:0 frames (even dimensions) of
// bitDepth bits at frameRate frames per second (its integer part selects
// the temporal activity, as in ffmpeg). workers bounds the goroutines
// measuring the blocks of one frame.
func New(
	width, height int,
	bitDepth int,
	frameRate float64,
	workers int,
) *Meter {
	ratio := float64(width*height) / uhdArea
	block := max(1, 4*int(uhdBlock*math.Sqrt(ratio)+0.5))

	m := &Meter{
		width:       width,
		height:      height,
		depth:       bitDepth,
		secondOrder: int(frameRate) >= highFrameRate,
		workers:     max(1, workers),
		block:       block,
		downsample:  width*height > downsampleArea,
		blocksX:     (width + block - 1) / block,
		blocksY:     (height + block - 1) / block,
		avgAct:      math.Sqrt(16 * float64(int(1)<<(2*bitDepth-9)) / math.Sqrt(max(minRatio, ratio))),
		smooth:      width*height <= smoothingArea,
		prev1:       make([]int16, width*height),
	}

	if m.secondOrder {
		m.prev2 = make([]int16, width*height)
	}

	blocks := m.blocksX * m.blocksY
	m.sse = make([]float64, blocks)
	m.weights = make([]float64, blocks)

	for c := 1; c < planes; c++ {
		m.chroma[c] = make([]float64, blocks)
	}

	return m
}

// Reset clears the temporal history, as at the start of a video.
func (m *Meter) Reset() {
	clear(m.prev1)
	clear(m.prev2)
}

// Prime makes ref the history of the next frame, so that a sequence starting
// mid-video does not measure its first frame against black. The first frame
// measured after Prime(ref) should be ref itself: its temporal activity is
// then zero, and the next frames see their real predecessors (exactly for
// the first-order difference; the second-order one of the frame after ref
// is approximated).
func (m *Meter) Prime(
	ref *frame.Frame,
) {
	if ref.Luma.BytesPerSample == 2 {
		prime(m, view16(&ref.Luma))
	} else {
		prime(m, view8(&ref.Luma))
	}

	copy(m.prev2, m.prev1)
}

// Measure returns the distortion of dist against ref and advances the
// temporal history. Both frames must be width×height with chroma, at the
// Meter's bit depth.
func (m *Meter) Measure(
	ref, dist *frame.Frame,
) Distortion {
	if ref.Luma.BytesPerSample == 2 {
		return measure(m, views(ref, view16), views(dist, view16))
	}

	return measure(m, views(ref, view8), views(dist, view8))
}

// Decibels converts a distortion (or its mean over frames) of a width×height
// plane of bitDepth bits into XPSNR: 10·log10(W·H·peak² / d²). It is +Inf
// for identical frames.
func Decibels(
	distortion float64,
	width, height int,
	bitDepth int,
) float64 {
	peak := float64(int(1)<<bitDepth - 1)

	return 10 * math.Log10(float64(width*height)*peak*peak/(distortion*distortion))
}

func prime[T sample](
	m *Meter,
	ref view[T],
) {
	for y := range m.height {
		dst := m.prev1[y*m.width : (y+1)*m.width]
		for x, v := range ref.row(y, 0, m.width) {
			dst[x] = int16(v)
		}
	}
}

func measure[T sample](
	m *Meter,
	ref, dist [planes]view[T],
) Distortion {
	var out Distortion

	if m.block < minBlock {
		// Too small to weight: plain SSE, as ffmpeg does.
		for c, p := range ref {
			out[c] = math.Sqrt(float64(sse(p, dist[c], 0, 0, p.width, p.height)))
		}

		return out
	}

	m.parallelRows(func(by int) { measureRow(m, by, ref, dist) })

	if m.smooth {
		m.smoothWeights()
	}

	var wsse float64
	for i, s := range m.sse {
		wsse += s * m.weights[i]
	}

	out[0] = math.Sqrt(float64(m.round(wsse)))

	for c := 1; c < planes; c++ {
		var sum float64
		for i, s := range m.chroma[c] {
			sum += s * m.weights[i]
		}

		out[c] = math.Sqrt(float64(m.round(sum)))
	}

	return out
}

// round scales a weighted sum by the average activity and rounds it to an
// integer, as ffmpeg stores it in a uint64.
func (m *Meter) round(
	weighted float64,
) uint64 {
	if weighted <= 0 {
		return 0
	}

	return uint64(weighted*m.avgAct + 0.5)
}

// parallelRows runs measure for every row of blocks, on up to m.workers
// goroutines. Blocks only touch their own samples of the temporal history,
// so rows are independent.
func (m *Meter) parallelRows(
	measure func(by int),
) {
	workers := min(m.workers, m.blocksY)
	if workers <= 1 {
		for by := range m.blocksY {
			measure(by)
		}

		return
	}

	var wg sync.WaitGroup

	for w := range workers {
		wg.Go(func() {
			for by := w; by < m.blocksY; by += workers {
				measure(by)
			}
		})
	}

	wg.Wait()
}

// measureRow computes the luma SSE and weight of every block of row by, and
// the SSE of the co-located chroma blocks.
func measureRow[T sample](
	m *Meter,
	by int,
	ref, dist [planes]view[T],
) {
	y := by * m.block
	bh := min(m.block, m.height-y)

	for bx := range m.blocksX {
		x := bx * m.block
		bw := min(m.block, m.width-x)
		idx := by*m.blocksX + bx

		m.sse[idx], m.weights[idx] = lumaBlock(m, ref[0], dist[0], x, y, bw, bh)
	}

	for c := 1; c < planes; c++ {
		chromaRow(m, c, by, ref[c], dist[c])
	}
}

// chromaRow computes the SSE of the chroma blocks of row by. They cover the
// area of the luma blocks (ffmpeg's block size scaled by the subsampling)
// and are weighted by the luma block weights.
func chromaRow[T sample](
	m *Meter,
	c, by int,
	ref, dist view[T],
) {
	// With even luma dimensions and a block of at least minBlock, chroma
	// blocks are at least 2 samples wide and there are as many rows of
	// them as of luma blocks.
	bw := m.block * ref.width / m.width
	bh := m.block * ref.height / m.height
	y := by * bh
	h := min(bh, ref.height-y)
	idx := by * m.blocksX

	for x := 0; x < ref.width && idx < (by+1)*m.blocksX; x += bw {
		m.chroma[c][idx] = float64(sse(ref, dist, x, y, min(bw, ref.width-x), h))
		idx++
	}
}

// lumaBlock returns the SSE of a luma block and its weight, the inverse of
// its activity, and updates the temporal history of its samples
// (calc_squared_error_and_weight).
func lumaBlock[T sample](
	m *Meter,
	ref, dist view[T],
	x, y, bw, bh int,
) (float64, float64) {
	blockSSE := float64(sse(ref, dist, x, y, bw, bh))

	b := 1
	if m.downsample {
		b = 2
	}

	xAct, yAct := b, b
	if x > 0 {
		xAct = 0
	}

	if y > 0 {
		yAct = 0
	}

	wAct, hAct := bw, bh
	if x+bw >= m.width {
		wAct = bw - b
	}

	if y+bh >= m.height {
		hAct = bh - b
	}

	if wAct <= xAct || hAct <= yAct {
		// Too small to measure an activity: unit weight, history untouched.
		return blockSSE, 1
	}

	var spatial, temporal uint64

	switch {
	case !m.downsample:
		spatial = highpass(ref, x+xAct, y+yAct, x+wAct, y+hAct)
		temporal = temporalFull(m, ref, x, y, bw, bh)
	case wAct > shortRow:
		spatial = highpassDown(ref, x+xAct, y+yAct, x+wAct, y+hAct)
		temporal = temporalDown(m, ref, x, y, bw, bh)
	default:
		temporal = temporalDown(m, ref, x, y, bw, bh)
	}

	act := float64(spatial) / (float64(wAct-xAct) * float64(hAct-yAct))
	act += float64(temporal) / (float64(bw) * float64(bh))

	if floor := float64(int(1) << (m.depth - 6)); act < floor {
		act = floor
	}

	act *= act

	return blockSSE, 1 / math.Sqrt(act)
}

// highpass is the sum of absolute 3×3 high-pass responses over the samples
// [x0, x1) × [y0, y1), at full resolution, row by row (see highpassRow).
func highpass[T sample](
	o view[T],
	x0, y0, x1, y1 int,
) uint64 {
	n := x1 - x0
	rowSum := rowKernel(highpassRow[T], highpassRow8, highpassRow16)

	var sum uint64

	for y := y0; y < y1; y++ {
		cur := o.row(y, x0-1, n+2)
		sum += rowSum(o.row(y-1, x0-1, n+2), cur, o.row(y+1, x0-1, n+2))
	}

	return sum
}

// highpassRow is the sum of absolute 3×3 high-pass responses of the
// samples of cur but its first and last, whose neighbours they are, with
// up and down the rows above and below. The kernel (12 at the centre, −2
// on the cross, −1 on the diagonals) is rewritten with sliding column
// sums, 14·c − (l + r) − (v_l + 2·v_c + v_r) with v the vertical 3-sums,
// which is the same integer and loads three samples per position instead
// of nine.
func highpassRow[T sample](
	up, cur, down []T,
) uint64 {
	up, down = up[:len(cur)], down[:len(cur)]

	cl, cc := int(cur[0]), int(cur[1])
	vl, vc := int(up[0])+cl+int(down[0]), int(up[1])+cc+int(down[1])

	var sum uint64

	for i := 2; i < len(cur); i++ {
		cr := int(cur[i])
		vr := int(up[i]) + cr + int(down[i])
		sum += abs(14*cc - cl - cr - vl - 2*vc - vr)
		cl, cc, vl, vc = cc, cr, vc, vr
	}

	return sum
}

// highpassDown is the high-pass on 2×2 groups of samples used above HD
// (ffmpeg's highds), over the groups starting in [x0, x1) × [y0, y1).
func highpassDown[T sample](
	o view[T],
	x0, y0, x1, y1 int,
) uint64 {
	var sum uint64

	// Groups read two samples left of their start and three right of it.
	n := x1 - x0 + 4 + (x1-x0)%2

	for y := y0; y < y1; y += 2 {
		r := [6][]T{
			o.row(y-2, x0-2, n), o.row(y-1, x0-2, n), o.row(y, x0-2, n),
			o.row(y+1, x0-2, n), o.row(y+2, x0-2, n), o.row(y+3, x0-2, n),
		}

		for i := 2; i < x1-x0+2; i += 2 {
			at := func(dy, dx int) int { return int(r[dy+2][i+dx]) }

			f := 12*(at(0, 0)+at(0, 1)+at(1, 0)+at(1, 1)) -
				3*(at(-1, 0)+at(-1, 1)+at(2, 0)+at(2, 1)) -
				3*(at(0, -1)+at(0, 2)+at(1, -1)+at(1, 2)) -
				2*(at(-1, -1)+at(-1, 2)+at(2, -1)+at(2, 2)) -
				(at(-2, -1) + at(-2, 0) + at(-2, 1) + at(-2, 2) +
					at(3, -1) + at(3, 0) + at(3, 1) + at(3, 2) +
					at(-1, -2) + at(0, -2) + at(1, -2) + at(2, -2) +
					at(-1, 3) + at(0, 3) + at(1, 3) + at(2, 3))
			sum += abs(f)
		}
	}

	return sum
}

// temporalFull is the gamma-weighted sum of absolute first- or second-order
// frame differences over a block, and shifts the history of its samples.
func temporalFull[T sample](
	m *Meter,
	o view[T],
	x0, y0, bw, bh int,
) uint64 {
	first := rowKernel(firstOrderRow[T], firstOrderRow8, firstOrderRow16)
	second := rowKernel(secondOrderRow[T], secondOrderRow8, secondOrderRow16)

	var sum uint64

	for y := y0; y < y0+bh; y++ {
		cur := o.row(y, x0, bw)
		start := y*m.width + x0
		p1 := m.prev1[start : start+bw]

		if m.secondOrder {
			sum += second(cur, p1, m.prev2[start:start+bw])
		} else {
			sum += first(cur, p1)
		}
	}

	return gamma * sum
}

// firstOrderRow is the sum of absolute differences between the samples of
// cur and their history p1, which becomes cur.
func firstOrderRow[T sample](
	cur []T,
	p1 []int16,
) uint64 {
	p1 = p1[:len(cur)]

	var sum uint64

	for i, v := range cur {
		sum += abs(int(v) - int(p1[i]))
		p1[i] = int16(v)
	}

	return sum
}

// secondOrderRow is the sum of absolute second-order differences of the
// samples of cur with their history p1 and p2, which shifts to cur and p1.
func secondOrderRow[T sample](
	cur []T,
	p1, p2 []int16,
) uint64 {
	p1, p2 = p1[:len(cur)], p2[:len(cur)]

	var sum uint64

	for i, v := range cur {
		sum += abs(int(v) - 2*int(p1[i]) + int(p2[i]))
		p2[i] = p1[i]
		p1[i] = int16(v)
	}

	return sum
}

// temporalDown is the temporal activity on 2×2 groups of samples (ffmpeg's
// diff1st and diff2nd).
func temporalDown[T sample](
	m *Meter,
	o view[T],
	x0, y0, bw, bh int,
) uint64 {
	w := m.width

	var sum uint64

	for y := y0; y < y0+bh; y += 2 {
		for x := x0; x < x0+bw; x += 2 {
			cur := int(o.pix[y*o.stride+x]) + int(o.pix[y*o.stride+x+1]) +
				int(o.pix[(y+1)*o.stride+x]) + int(o.pix[(y+1)*o.stride+x+1])
			i := y*w + x
			p1 := int(m.prev1[i]) + int(m.prev1[i+1]) + int(m.prev1[i+w]) + int(m.prev1[i+w+1])

			if m.secondOrder {
				p2 := int(m.prev2[i]) + int(m.prev2[i+1]) + int(m.prev2[i+w]) + int(m.prev2[i+w+1])
				sum += abs(cur - 2*p1 + p2)
				m.prev2[i], m.prev2[i+1], m.prev2[i+w], m.prev2[i+w+1] = m.prev1[i], m.prev1[i+1], m.prev1[i+w], m.prev1[i+w+1]
			} else {
				sum += abs(cur - p1)
			}

			m.prev1[i], m.prev1[i+1] = int16(o.pix[y*o.stride+x]), int16(o.pix[y*o.stride+x+1])
			m.prev1[i+w], m.prev1[i+w+1] = int16(o.pix[(y+1)*o.stride+x]), int16(o.pix[(y+1)*o.stride+x+1])
		}
	}

	return sum * gamma
}

// smoothWeights replays ffmpeg's in-line min-smoothing of small pictures in
// raster order: each step only reads weights at or before the current block,
// so running it once all blocks are measured gives the same result.
func (m *Meter) smoothWeights() {
	wb := m.blocksX
	w := m.weights
	idx := 0

	for by := range m.blocksY {
		for bx := range m.blocksX {
			var prev float64

			switch {
			case bx == 0 && idx > 1:
				prev = w[idx-2]
			case bx == 0:
				prev = 0
			case bx > 1:
				prev = max(w[idx-2], w[idx])
			default:
				prev = w[idx]
			}

			if idx > wb {
				prev = max(prev, w[idx-1-wb])
			}

			if idx > 0 && w[idx-1] > prev {
				w[idx-1] = prev
			}

			if bx == m.blocksX-1 && by == m.blocksY-1 && idx > wb {
				if last := max(w[idx-1], w[idx-wb]); w[idx] > last {
					w[idx] = last
				}
			}

			idx++
		}
	}
}

// sse is the sum of squared differences of a w×h block at (x, y).
func sse[T sample](
	ref, dist view[T],
	x, y, w, h int,
) uint64 {
	rowSum := rowKernel(sseRow[T], sseRow8, sseRow16)

	var sum uint64

	for row := y; row < y+h; row++ {
		sum += rowSum(ref.row(row, x, w), dist.row(row, x, w))
	}

	return sum
}

// sseRow is the sum of squared differences of two rows of samples.
func sseRow[T sample](
	a, b []T,
) uint64 {
	b = b[:len(a)]

	var line int
	for i, v := range a {
		d := int(v) - int(b[i])
		line += d * d
	}

	return uint64(line)
}

// rowKernel returns the row loop of fast (vectorised on arm64: one for
// 8-bit samples, one for 16-bit ones) that has the type of portable, i.e.
// that of T's samples. Both compute the same integers.
func rowKernel[F any](
	portable F,
	fast ...any,
) F {
	for _, candidate := range fast {
		if f, ok := candidate.(F); ok {
			return f
		}
	}

	return portable
}

// views returns the three planes of f seen through see.
func views[T sample](
	f *frame.Frame,
	see func(*frame.Plane) view[T],
) [planes]view[T] {
	return [planes]view[T]{see(&f.Luma), see(&f.Cb), see(&f.Cr)}
}

// view8 sees an 8-bit plane as samples.
func view8(
	p *frame.Plane,
) view[uint8] {
	return view[uint8]{pix: p.Pix, width: p.Width, height: p.Height, stride: p.Stride}
}

// view16 sees a 16-bit little-endian plane as samples without copying it.
// Like the libvmaf binding, it relies on a little-endian host (arm64,
// amd64): the bytes are the samples.
func view16(
	p *frame.Plane,
) view[int16] {
	samples := unsafe.Slice((*int16)(unsafe.Pointer(unsafe.SliceData(p.Pix))), len(p.Pix)/2) //nolint:gosec // 16-bit samples stored little-endian, read on a little-endian host

	return view[int16]{pix: samples, width: p.Width, height: p.Height, stride: p.Stride / 2}
}

func abs(
	v int,
) uint64 {
	if v < 0 {
		return uint64(-v)
	}

	return uint64(v)
}
