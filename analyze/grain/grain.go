// Package grain estimates the grain of video from its 8-bit luma: the
// standard deviation of the noise and its lag-1 correlation, measured on the
// flattest blocks of each frame, where the high-pass residual is grain and
// not texture. It is pure: frames come from a decoder (encode.FFmpeg.Noise
// pipes them out of ffmpeg), which keeps the estimator testable on
// synthetic frames and reusable with any source.
package grain

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
)

// ErrNoFrames is returned when a measurement has no frame to measure.
var ErrNoFrames = errors.New("no frame to measure noise on")

// ErrFrameSize is returned for an empty frame size, or a frame smaller than
// the estimator's.
var ErrFrameSize = errors.New("invalid frame size")

// SampleFrames is the number of frames a grain measurement samples evenly
// over a file: grain is stationary, so a few frames give a stable estimate,
// and callers spacing the samples need the count.
const SampleFrames = 8

// Estimator settings.
const (
	// blockSize is the side of the blocks classified as flat or textured.
	blockSize = 16
	// flatShare is the share of blocks, the flattest, where noise is
	// measured.
	flatShare = 0.2
)

// Stats describes the grain of a video, measured on its luma in 8-bit code
// values.
type Stats struct {
	// Sigma is the standard deviation of the noise (Immerkær's estimator
	// on the flattest blocks: the Laplacian-difference mask cancels smooth
	// content, so what it measures there is grain).
	Sigma float64 `json:"sigma"`
	// Correlation is the lag-1 autocorrelation of the high-pass residual on
	// the same blocks: a coarse grain is more correlated than a fine one.
	// Together with Sigma it is a two-number signature of the grain's
	// spectrum.
	Correlation float64 `json:"correlation"`
	Frames      int     `json:"frames"`
}

// Estimator accumulates the flat-block statistics of luma frames of one
// size. The zero value is not usable: see NewEstimator.
type Estimator struct {
	width, height int
	frames        int
	// laplacian sums |Laplacian difference| over flat pixels, count them.
	laplacian float64
	count     float64
	// Residual lag-1 moments.
	sumXY, sumXX float64
}

// NewEstimator returns an Estimator of width×height frames.
func NewEstimator(
	width, height int,
) (*Estimator, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("%w %dx%d", ErrFrameSize, width, height)
	}

	return &Estimator{width: width, height: height}, nil
}

// Measure adds the 8-bit gray frames of the estimator's size read from r
// until its end (a trailing partial frame is ignored) and returns the
// statistics of every frame added.
func (e *Estimator) Measure(
	r io.Reader,
) (Stats, error) {
	frame := make([]byte, e.width*e.height)

	for {
		if _, err := io.ReadFull(r, frame); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return Stats{}, fmt.Errorf("read frame: %w", err)
			}

			return e.Stats()
		}

		e.add(frame)
	}
}

// Add accumulates one luma frame of the estimator's size (8-bit samples,
// row by row, no padding).
func (e *Estimator) Add(
	luma []byte,
) error {
	if len(luma) < e.width*e.height {
		return fmt.Errorf("%w: %d samples for %dx%d", ErrFrameSize, len(luma), e.width, e.height)
	}

	e.add(luma)

	return nil
}

// Stats returns the statistics of the frames added so far.
func (e *Estimator) Stats() (Stats, error) {
	if e.frames == 0 {
		return Stats{}, ErrNoFrames
	}

	s := Stats{Frames: e.frames}

	if e.count > 0 {
		// Immerkær (1996): σ = √(π/2) · mean|L| / 6 for the mask below.
		s.Sigma = math.Sqrt(math.Pi/2) * e.laplacian / e.count / 6
	}

	if e.sumXX > 0 {
		s.Correlation = e.sumXY / e.sumXX
	}

	return s, nil
}

// block is a blockSize square at (x, y) and its texture.
type block struct {
	x, y     int
	activity float64
}

// add accumulates one luma frame: the flattest flatShare of its blocks
// contribute their Laplacian differences and residual moments.
func (e *Estimator) add(
	y []byte,
) {
	e.frames++

	var blocks []block

	for by := 1; by+blockSize+1 < e.height; by += blockSize {
		for bx := 1; bx+blockSize+2 < e.width; bx += blockSize {
			blocks = append(blocks, block{x: bx, y: by, activity: gradient(y, e.width, bx, by)})
		}
	}

	slices.SortFunc(blocks, func(p, q block) int { return cmp.Compare(p.activity, q.activity) })

	for _, b := range blocks[:int(math.Ceil(float64(len(blocks))*flatShare))] {
		for py := b.y; py < b.y+blockSize; py++ {
			for px := b.x; px < b.x+blockSize; px++ {
				e.laplacian += math.Abs(laplacianDifference(y, e.width, px, py))
				e.count++

				// Residual after a 3×3 box blur, and its right neighbour's.
				r0, r1 := residual(y, e.width, px, py), residual(y, e.width, px+1, py)
				e.sumXY += r0 * r1
				e.sumXX += r0 * r0
			}
		}
	}
}

// laplacianDifference applies Immerkær's mask [1 −2 1; −2 4 −2; 1 −2 1]
// at (x, y), which is zero on any locally linear or quadratic surface.
func laplacianDifference(
	y []byte,
	w, x, py int,
) float64 {
	at := func(dx, dy int) float64 { return float64(y[(py+dy)*w+x+dx]) }

	return at(-1, -1) - 2*at(0, -1) + at(1, -1) -
		2*at(-1, 0) + 4*at(0, 0) - 2*at(1, 0) +
		at(-1, 1) - 2*at(0, 1) + at(1, 1)
}

// residual is the pixel minus the mean of its 3×3 neighbourhood.
func residual(
	y []byte,
	w, x, py int,
) float64 {
	sum := 0.0

	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			sum += float64(y[(py+dy)*w+x+dx])
		}
	}

	return float64(y[py*w+x]) - sum/9
}

// gradient is the mean absolute horizontal and vertical difference inside
// the block at (bx, by): its texture.
func gradient(
	y []byte,
	w, bx, by int,
) float64 {
	sum := 0.0

	for py := by; py < by+blockSize; py++ {
		for px := bx; px < bx+blockSize; px++ {
			c := float64(y[py*w+px])
			sum += math.Abs(c-float64(y[py*w+px+1])) + math.Abs(c-float64(y[(py+1)*w+px]))
		}
	}

	return sum
}
