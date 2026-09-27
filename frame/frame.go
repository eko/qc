// Package frame defines decoded frames shared between analyzers.
//
// A frame is decoded once and fanned out to every analyzer: it is reference
// counted and its buffers return to a pool when the last user releases it.
package frame

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/eko/qc/media"
)

// Plane is an image plane of 8-bit samples, or of 16-bit little-endian
// samples when BytesPerSample is 2. Stride is in bytes.
type Plane struct {
	Width          int
	Height         int
	Stride         int
	BytesPerSample int
	Pix            []byte
}

// Row returns the bytes of row y.
func (p *Plane) Row(
	y int,
) []byte {
	return p.Pix[y*p.Stride : y*p.Stride+p.Width*max(p.BytesPerSample, 1)]
}

// Uint16 sees a plane of 16-bit samples as a slice of samples, without
// copying it: row y starts at y·Stride/2. Like the libvmaf binding and
// quality/xpsnr, it relies on a little-endian host (arm64, amd64), where
// the bytes of ffmpeg's little-endian rawvideo are the samples.
func (p *Plane) Uint16() []uint16 {
	return unsafe.Slice((*uint16)(unsafe.Pointer(unsafe.SliceData(p.Pix))), len(p.Pix)/2) //nolint:gosec // 16-bit samples stored little-endian, read on a little-endian host
}

// Frame is a decoded picture. Luma is the full-resolution luma plane; Cb and
// Cr are the 4:2:0 chroma planes when the pool carries chroma. Thumb is a small
// box-filtered copy of the luma computed once for cheap analyzers (scene cuts,
// black, freeze, crop) when the pool builds thumbnails.
type Frame struct {
	Index int
	PTS   media.Duration
	Luma  Plane
	Cb    Plane
	Cr    Plane
	Thumb Plane
	// Samples is a sparse grid of the decoded 10-bit Y′CbCr samples, when
	// the pool keeps one (PoolOptions.SampleStep): the colour and light of
	// an HDR frame, next to its 8-bit luma.
	Samples Samples

	refs atomic.Int32
	pool *Pool
}

// Retain adds a reference. Every Retain must be paired with a Release.
func (f *Frame) Retain() {
	f.refs.Add(1)
}

// Release drops a reference and recycles the frame when none remain. Frames
// that do not come from a Pool are simply left to the garbage collector.
func (f *Frame) Release() {
	if f.refs.Add(-1) == 0 && f.pool != nil {
		f.pool.put(f)
	}
}

// PoolOptions selects what a pooled frame carries. The zero value is luma only.
type PoolOptions struct {
	// ThumbMaxWidth enables thumbnails: the luma is downscaled by the smallest
	// integer factor that brings its width to at most ThumbMaxWidth.
	ThumbMaxWidth int
	// Chroma adds 4:2:0 Cb and Cr planes.
	Chroma bool
	// HighBitDepth stores samples on 16 bits (10-bit video); thumbnails
	// are not available then.
	HighBitDepth bool
	// SampleStep, when positive, gives the frames of the pool, next to
	// their 8-bit luma (the pixel analyzers'), a grid of 10-bit Y′CbCr
	// samples (Samples) of one point per SampleStep×SampleStep cell
	// (rounded up to an even step), for the light analyzer. Chroma and
	// HighBitDepth are ignored then.
	SampleStep int
}

// Samples holds the 10-bit Y′CbCr samples of a grid of points of a frame,
// one per Step×Step cell of luma pixels: the pixel at the centre of the
// cell (Step/2 right and down of its corner) and the chroma sample
// covering it, as ffmpeg's neighbour scaler picks them. Planes store 16-bit
// little-endian samples, the three at the grid's size.
type Samples struct {
	Step      int
	Y, Cb, Cr Plane
}

// Pool recycles frames of a fixed geometry to avoid per-frame allocations.
type Pool struct {
	width, height int
	opts          PoolOptions
	thumbFactor   int
	frames        sync.Pool
}

// NewPool returns a pool for width×height frames.
func NewPool(
	width, height int,
	opts PoolOptions,
) *Pool {
	p := &Pool{width: width, height: height, opts: opts}
	if opts.ThumbMaxWidth > 0 {
		p.thumbFactor = max(1, (width+opts.ThumbMaxWidth-1)/opts.ThumbMaxWidth)
	}

	if opts.SampleStep > 0 {
		// Grid points sit on chroma samples: the step is even.
		opts.SampleStep += opts.SampleStep % 2
		opts.Chroma, opts.HighBitDepth = false, false
		p.opts = opts
	}

	bps := 1
	if opts.HighBitDepth {
		bps, p.thumbFactor = 2, 0
	}

	p.frames.New = func() any {
		f := &Frame{Luma: newPlane(width, height, bps), pool: p}

		if step := opts.SampleStep; step > 0 {
			grid := p.GridSize()
			f.Samples = Samples{
				Step: step, Y: newPlane(grid[0], grid[1], 2), Cb: newPlane(grid[0], grid[1], 2), Cr: newPlane(grid[0], grid[1], 2),
			}
		}

		if opts.Chroma {
			cw, ch := (width+1)/2, (height+1)/2
			f.Cb, f.Cr = newPlane(cw, ch, bps), newPlane(cw, ch, bps)
		}

		if p.thumbFactor > 0 {
			f.Thumb = newPlane(width/p.thumbFactor, height/p.thumbFactor, 1)
		}

		return f
	}

	return p
}

// newPlane allocates a tightly packed plane (Stride = Width × BytesPerSample),
// the layout of ffmpeg's rawvideo output.
func newPlane(
	width, height, bytesPerSample int,
) Plane {
	return Plane{
		Width: width, Height: height, Stride: width * bytesPerSample, BytesPerSample: bytesPerSample,
		Pix: make([]byte, width*height*bytesPerSample),
	}
}

// Get returns a frame holding one reference.
func (p *Pool) Get() *Frame {
	f, _ := p.frames.Get().(*Frame)
	f.refs.Store(1)

	return f
}

// Planes returns the planes of f in raw video order (Y, then Cb and Cr when
// the pool carries chroma).
func (p *Pool) Planes(
	f *Frame,
) []*Plane {
	if p.opts.Chroma {
		return []*Plane{&f.Luma, &f.Cb, &f.Cr}
	}

	return []*Plane{&f.Luma}
}

// SampleStep is the spacing of the sample grid of pooled frames, 0 when
// they have none (see PoolOptions.SampleStep).
func (p *Pool) SampleStep() int {
	return p.opts.SampleStep
}

// GridSize is the width and height of the sample grid of pooled frames:
// one point per cell of SampleStep pixels, partial cells included.
func (p *Pool) GridSize() [2]int {
	step := p.opts.SampleStep
	if step == 0 {
		return [2]int{}
	}

	return [2]int{(p.width + step - 1) / step, (p.height + step - 1) / step}
}

// SamplePlanes returns the planes of the sample grid of f in raw video
// order (Y, Cb, Cr), as a sampling pool's decoder reads them.
func (p *Pool) SamplePlanes(
	f *Frame,
) []*Plane {
	return []*Plane{&f.Samples.Y, &f.Samples.Cb, &f.Samples.Cr}
}

// Width is the luma width of pooled frames.
func (p *Pool) Width() int {
	return p.width
}

// Height is the luma height of pooled frames.
func (p *Pool) Height() int {
	return p.height
}

// HighBitDepth reports whether samples are stored on 16 bits.
func (p *Pool) HighBitDepth() bool {
	return p.opts.HighBitDepth
}

// Chroma reports whether frames carry chroma planes.
func (p *Pool) Chroma() bool {
	return p.opts.Chroma
}

// BuildThumb fills f.Thumb from f.Luma with a box filter. It is a no-op when
// the pool has no thumbnails.
func (p *Pool) BuildThumb(
	f *Frame,
) {
	if p.thumbFactor > 0 {
		boxDownscale(&f.Luma, &f.Thumb, p.thumbFactor)
	}
}

// put returns a frame whose last reference was released to the pool.
func (p *Pool) put(
	f *Frame,
) {
	p.frames.Put(f)
}

// boxDownscale averages factor×factor blocks of 8-bit src into dst, rounding
// to nearest. Source rows and columns beyond dst×factor are ignored.
func boxDownscale(
	src, dst *Plane,
	factor int,
) {
	if factor == 1 {
		copy(dst.Pix, src.Pix)

		return
	}

	area := factor * factor
	sums := make([]int, dst.Width)

	for ty := range dst.Height {
		clear(sums)

		for dy := range factor {
			row := src.Row(ty*factor + dy)
			for tx := range dst.Width {
				for _, v := range row[tx*factor : tx*factor+factor] {
					sums[tx] += int(v)
				}
			}
		}

		out := dst.Row(ty)
		for tx, s := range sums {
			out[tx] = byte((s + area/2) / area) //nolint:gosec // an average of bytes fits in a byte
		}
	}
}
