package motion

import (
	"math"
	"math/cmplx"
)

// Block matching parameters, in pixels of the level they apply to.
const (
	// blockSize is the side of a block at level 0 (half at level 1): 16
	// pixels of a 240-pixel thumbnail is 1/15 of the width, enough texture
	// for a unique match, small enough for 8 × 5 blocks to sample the
	// whole picture.
	blockSize = 16
	// gridCols is the number of block columns; rows follow the aspect ratio.
	gridCols    = 8
	minGridRows = 3
	maxGridRows = 8
	// gridMargin keeps blocks off the picture edges (level 0).
	gridMargin = 6
	// coarseRadius is the exhaustive search around the best predictor at
	// level 1 (±4 pixels at level 0); level 0 then searches ±1 pixel.
	coarseRadius = 2
	// minActivity is the mean absolute difference between neighbouring
	// level-1 pixels under which a block is too flat to match (sky, black
	// bars): it is skipped before any search.
	minActivity = 1.0
	// minTexture is the smallest eigenvalue of the block's structure
	// tensor (squared grey levels per pixel) under which its displacement
	// is ambiguous along some direction (an edge, the aperture problem).
	minTexture = 1.0
	// maxMatchError is the mean absolute difference (grey levels) above
	// which the best match is not the same content (occlusion, a cut).
	maxMatchError = 12.0
	// warpSteps are the Lucas–Kanade steps after the first.
	warpSteps = 1
	// maxStep bounds the sub-pixel step: beyond, the linearisation failed.
	maxStep = 1.0
)

// estimator measures the global motion between consecutive frames: block
// vectors on a two-level pyramid (predictors, then exhaustive search at
// level 1, then ±1 pixel and Lucas–Kanade steps at level 0),
// then a robust similarity fit. Its buffers are allocated on the first
// frame: the per-frame path does not allocate.
type estimator struct {
	// factor downscales the input to level 0; w, h are level 0's size.
	factor, w, h, w1, h1 int
	cur, prev            []byte
	cur1, prev1          []byte
	colCur, colPrev      []int32
	rowCur, rowPrev      []int32
	sumCur, sumPrev      int
	blocks               []point
	vectors              []vector
	fitter               fitter
	primed               bool
}

// point is the top-left corner of a block at level 0 (even coordinates).
type point struct {
	x, y int
}

// estimate is the global motion of one frame against the previous one.
type estimate struct {
	model similarity
	// confidence is 0 (no estimate) to 1.
	confidence float64
	// residual and magnitude are those of the fit (working pixels).
	residual, magnitude float64
}

// reset sizes the estimator for input frames of width×height.
func (e *estimator) reset(
	width, height int,
) {
	e.factor = downscaleFactor(width)
	e.w, e.h = width/e.factor, height/e.factor
	e.w1, e.h1 = e.w/2, e.h/2
	e.cur, e.prev = make([]byte, e.w*e.h), make([]byte, e.w*e.h)
	e.cur1, e.prev1 = make([]byte, e.w1*e.h1), make([]byte, e.w1*e.h1)
	e.colCur, e.colPrev = make([]int32, e.w1), make([]int32, e.w1)
	e.rowCur, e.rowPrev = make([]int32, e.h1), make([]int32, e.h1)
	e.blocks = grid(e.w, e.h)
	e.vectors = make([]vector, 0, len(e.blocks))
	e.fitter.scratch = make([]float64, 0, len(e.blocks))
	e.primed = false
}

// grid places the blocks evenly over the picture, rows following its
// aspect ratio.
func grid(
	w, h int,
) []point {
	if w < minWorkWidth || h < minWorkHeight {
		return nil
	}

	rows := min(max(int(math.Round(float64(gridCols*h)/float64(w))), minGridRows), maxGridRows)
	spanX, spanY := w-2*gridMargin-blockSize, h-2*gridMargin-blockSize
	out := make([]point, 0, gridCols*rows)

	for r := range rows {
		for c := range gridCols {
			x := gridMargin + c*spanX/(gridCols-1)
			y := gridMargin + r*spanY/(rows-1)
			out = append(out, point{x: x &^ 1, y: y &^ 1})
		}
	}

	return out
}

// next consumes a frame (8-bit samples, stride bytes per row) and returns
// its motion against the previous one; the first frame has none.
func (e *estimator) next(
	pix []byte,
	width, height, stride int,
) estimate {
	if e.cur == nil || width/e.factor != e.w || height/e.factor != e.h {
		e.reset(width, height)
	}

	e.cur, e.prev = e.prev, e.cur
	e.cur1, e.prev1 = e.prev1, e.cur1
	e.colCur, e.colPrev = e.colPrev, e.colCur
	e.rowCur, e.rowPrev = e.rowPrev, e.rowCur
	e.sumPrev = e.sumCur

	load(e.cur, e.w, e.h, pix, stride, e.factor)
	e.sumCur = halve(e.cur1, e.cur, e.w, e.h, e.colCur, e.rowCur)

	primed := e.primed
	e.primed = true

	if !primed || len(e.blocks) == 0 {
		return estimate{}
	}

	return e.estimate()
}

// estimate matches every textured block and fits the global model.
func (e *estimator) estimate() estimate {
	bias := (e.sumCur - e.sumPrev) / (e.w1 * e.h1)
	predictor := point{x: projectionShift(e.colCur, e.colPrev), y: projectionShift(e.rowCur, e.rowPrev)}
	spatial := predictor
	e.vectors = e.vectors[:0]

	for _, b := range e.blocks {
		if !e.textured(b) {
			continue
		}

		at := e.centre(b)
		candidates := [...]point{{}, predictor, spatial}

		coarse := e.searchCoarse(b, candidates[:], bias)

		d, ok := e.refine(b, point{x: 2 * coarse.x, y: 2 * coarse.y}, bias)
		if !ok {
			continue
		}

		spatial = point{x: roundHalf(real(d)), y: roundHalf(imag(d))}
		e.vectors = append(e.vectors, vector{at: at, d: d})
	}

	f, ok := e.fitter.fit(e.vectors)
	if !ok {
		return estimate{}
	}

	return estimate{
		model:      f.model,
		confidence: confidence(f.inliers, len(e.vectors), len(e.blocks)),
		residual:   f.residual,
		magnitude:  f.magnitude,
	}
}

// fullConfidenceShare is the share of the grid agreeing on the model at
// which the estimate is fully trusted.
const fullConfidenceShare = 0.5

// confidence grows with the blocks supporting the model (textured
// pictures) and with their share of the matched blocks (one dominant
// motion rather than competing ones).
func confidence(
	inliers, matched, blocks int,
) float64 {
	support := min(1, float64(inliers)/(fullConfidenceShare*float64(blocks)))

	return support * float64(inliers) / float64(matched)
}

// centre is the centre of block b relative to the image centre, both in
// pixel-centre coordinates (pixel x covers [x, x+1), its centre is x+½).
func (e *estimator) centre(
	b point,
) complex128 {
	return complex(float64(b.x)+blockSize/2-float64(e.w)/2, float64(b.y)+blockSize/2-float64(e.h)/2)
}

// roundHalf halves a level-0 displacement to the nearest level-1 pixel.
func roundHalf(
	v float64,
) int {
	return int(math.Round(v / 2))
}

// textured reports whether the block has enough detail to be matched.
func (e *estimator) textured(
	b point,
) bool {
	const size = blockSize / 2

	x1, y1 := b.x/2, b.y/2
	activity := 0

	for y := y1; y < y1+size; y++ {
		row := e.cur1[y*e.w1+x1 : y*e.w1+x1+size+1]
		below := e.cur1[(y+1)*e.w1+x1 : (y+1)*e.w1+x1+size]

		for x, v := range below {
			activity += absInt(int(row[x+1])-int(row[x])) + absInt(int(v)-int(row[x]))
		}
	}

	return float64(activity) >= minActivity*2*size*size
}

// searchCoarse returns the level-1 displacement of block b: the best of
// the predictors, then the best within coarseRadius of it.
func (e *estimator) searchCoarse(
	b point,
	candidates []point,
	bias int,
) point {
	best, bestCost := point{}, math.MaxInt

	try := func(d point) {
		if c := e.coarseCost(b, d, bias, bestCost); c < bestCost {
			best, bestCost = d, c
		}
	}

	for _, d := range candidates {
		try(d)
	}

	center := best
	for dy := -coarseRadius; dy <= coarseRadius; dy++ {
		for dx := -coarseRadius; dx <= coarseRadius; dx++ {
			try(point{x: center.x + dx, y: center.y + dy})
		}
	}

	return best
}

// coarseCost is the sum of absolute differences of block b at level 1
// against the previous frame displaced by d, math.MaxInt out of the
// picture. It stops once past limit.
func (e *estimator) coarseCost(
	b, d point,
	bias, limit int,
) int {
	const size = blockSize / 2

	x1, y1 := b.x/2, b.y/2

	px, py := x1-d.x, y1-d.y
	if px < 0 || py < 0 || px+size > e.w1 || py+size > e.h1 {
		return math.MaxInt
	}

	return sad(e.cur1, y1*e.w1+x1, e.prev1, py*e.w1+px, e.w1, size, bias, limit)
}

// fineOffsets are the level-0 candidates around the coarse estimate
// (±1 pixel), the centre first: its cost lets the others stop early.
var fineOffsets = [...]point{{0, 0}, {-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-1, -1}, {1, -1}, {-1, 1}, {1, 1}}

// refine returns the level-0 displacement of block b around the integer
// estimate d (twice the level-1 one): the best within ±1 pixel, then
// Lucas–Kanade steps. A level-0 search is worth its cost: starting
// Lucas–Kanade from a sub-pixel fit of the level-1 costs instead lost
// fast zooms and pans on sharp real content (animation, live action).
func (e *estimator) refine(
	b point,
	d point,
	bias int,
) (complex128, bool) {
	best, bestCost := point{}, math.MaxInt

	for _, o := range fineOffsets {
		c := point{x: d.x + o.x, y: d.y + o.y}

		// Room for the gradients and the bilinear warp of the second
		// Lucas–Kanade step: two pixels on every side.
		px, py := b.x-c.x, b.y-c.y
		if px < 2 || py < 2 || px+blockSize+2 > e.w || py+blockSize+2 > e.h {
			continue
		}

		if cost := sad(e.cur, b.y*e.w+b.x, e.prev, py*e.w+px, e.w, blockSize, bias, bestCost); cost < bestCost {
			best, bestCost = c, cost
		}
	}

	if bestCost > maxMatchError*blockSize*blockSize {
		return 0, false
	}

	step, ok := e.lucasKanade(b, best, bias)
	if !ok {
		return 0, false
	}

	return complex(float64(best.x), float64(best.y)) + step, true
}

// lucasKanade returns the sub-pixel step δ minimising
// Σ (prev(x − d − δ) + bias − cur(x))² over block b, linearised with the
// mean of both frames' gradients (a symmetric, second-order variant of
// Lucas & Kanade, "An iterative image registration technique", IJCAI
// 1981). Its structure tensor also rejects edges and flat blocks. A single
// step overestimates sub-pixel motion (central differences attenuate fine
// detail, by ~12% on thumbnails), so a second step re-measures the error
// on the previous block warped by the first one (bilinear), reusing the
// structure tensor as in inverse compositional schemes (Baker & Matthews,
// "Lucas-Kanade 20 years on", IJCV 2004).
func (e *estimator) lucasKanade(
	b, d point,
	bias int,
) (complex128, bool) {
	t := e.tensor(b, d, bias)

	step, ok := t.solve(t.sxt, t.syt)
	if !ok {
		return 0, false
	}

	for range warpSteps {
		sxt, syt := e.warpedError(b, d, bias, step)

		correction, ok := t.solve(sxt, syt)
		if !ok || cmplx.Abs(step+correction) > maxStep {
			return 0, false
		}

		step += correction
	}

	return step, cmplx.Abs(step) <= maxStep
}

// tensor holds the Lucas–Kanade sums of a block: the structure tensor
// (sxx, sxy, syy) and the error projections (sxt, syt), of gradients four
// times too large (two central differences of two frames).
type tensor struct {
	sxx, sxy, syy, sxt, syt float64
}

// gradientScale is the factor of the integer gradients of tensor sums.
const gradientScale = 4

// tensor accumulates the sums of block b against the previous block
// displaced by d.
func (e *estimator) tensor(
	b, d point,
	bias int,
) tensor {
	var sxx, sxy, syy, sxt, syt int

	w := e.w
	px, py := b.x-d.x, b.y-d.y

	for y := range blockSize {
		c := e.cur[(b.y+y)*w+b.x-1 : (b.y+y)*w+b.x+blockSize+1]
		cu := e.cur[(b.y+y-1)*w+b.x : (b.y+y-1)*w+b.x+blockSize]
		cd := e.cur[(b.y+y+1)*w+b.x : (b.y+y+1)*w+b.x+blockSize]
		p := e.prev[(py+y)*w+px-1 : (py+y)*w+px+blockSize+1]
		pu := e.prev[(py+y-1)*w+px : (py+y-1)*w+px+blockSize]
		pd := e.prev[(py+y+1)*w+px : (py+y+1)*w+px+blockSize]

		for x := range blockSize {
			gx := int(c[x+2]) - int(c[x]) + int(p[x+2]) - int(p[x])
			gy := int(cd[x]) - int(cu[x]) + int(pd[x]) - int(pu[x])
			et := int(p[x+1]) + bias - int(c[x+1])
			sxx += gx * gx
			sxy += gx * gy
			syy += gy * gy
			sxt += gx * et
			syt += gy * et
		}
	}

	return tensor{sxx: float64(sxx), sxy: float64(sxy), syy: float64(syy), sxt: float64(sxt), syt: float64(syt)}
}

// warpedError is the error projection of block b against the previous
// block displaced by d + step, sampled bilinearly (|step| ≤ 1 per axis).
func (e *estimator) warpedError(
	b, d point,
	bias int,
	step complex128,
) (sxt, syt float64) {
	w := e.w
	// The previous frame is read at x − d − step: split −step into an
	// integer offset and a fraction in [0, 1).
	ox, oy := math.Floor(-real(step)), math.Floor(-imag(step))
	fx, fy := -real(step)-ox, -imag(step)-oy
	px, py := b.x-d.x+int(ox), b.y-d.y+int(oy)
	w00, w10, w01, w11 := (1-fx)*(1-fy), fx*(1-fy), (1-fx)*fy, fx*fy

	for y := range blockSize {
		c := e.cur[(b.y+y)*w+b.x-1 : (b.y+y)*w+b.x+blockSize+1]
		cu := e.cur[(b.y+y-1)*w+b.x : (b.y+y-1)*w+b.x+blockSize]
		cd := e.cur[(b.y+y+1)*w+b.x : (b.y+y+1)*w+b.x+blockSize]
		p := e.prev[(py+y)*w+px-1 : (py+y)*w+px+blockSize+2]
		pu := e.prev[(py+y-1)*w+px : (py+y-1)*w+px+blockSize+1]
		pd := e.prev[(py+y+1)*w+px : (py+y+1)*w+px+blockSize+1]

		for x := range blockSize {
			gx := float64(int(c[x+2]) - int(c[x]) + int(p[x+2]) - int(p[x]))
			gy := float64(int(cd[x]) - int(cu[x]) + int(pd[x]) - int(pu[x]))
			warped := w00*float64(p[x+1]) + w10*float64(p[x+2]) + w01*float64(pd[x]) + w11*float64(pd[x+1])
			et := warped + float64(bias) - float64(c[x+1])
			sxt += gx * et
			syt += gy * et
		}
	}

	return sxt, syt
}

// solve solves the 2×2 Lucas–Kanade system for the error projections
// (sxt, syt); ok is false for a block too flat or an edge.
func (t tensor) solve(
	sxt, syt float64,
) (complex128, bool) {
	const samples = blockSize * blockSize

	// Smallest eigenvalue of the structure tensor, per pixel and in grey
	// levels per pixel squared.
	half, diff := (t.sxx+t.syy)/2, (t.sxx-t.syy)/2
	lambda := (half - math.Sqrt(diff*diff+t.sxy*t.sxy)) / (gradientScale * gradientScale * samples)

	if lambda < minTexture {
		return 0, false
	}

	det := t.sxx*t.syy - t.sxy*t.sxy
	step := complex(gradientScale*(t.syy*sxt-t.sxy*syt)/det, gradientScale*(t.sxx*syt-t.sxy*sxt)/det)

	return step, cmplx.Abs(step) <= maxStep
}

// sad is the sum of absolute differences between size×size blocks of a and
// b (offsets of their top-left samples, rows stride apart), b brightened by
// bias. It stops once the sum exceeds limit: the caller keeps the minimum.
func sad(
	a []byte,
	aOff int,
	b []byte,
	bOff, stride, size, bias, limit int,
) int {
	sum := 0

	for y := range size {
		ra := a[aOff+y*stride : aOff+y*stride+size]
		rb := b[bOff+y*stride : bOff+y*stride+size]
		rb = rb[:len(ra)]

		for x, v := range ra {
			sum += absInt(int(v) - int(rb[x]) - bias)
		}

		if sum >= limit {
			return sum
		}
	}

	return sum
}

func absInt(
	v int,
) int {
	if v < 0 {
		return -v
	}

	return v
}
