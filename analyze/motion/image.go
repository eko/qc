package motion

// The estimator works on two levels of a small pyramid: level 0, the
// working image (the shared thumbnail, 240 pixels wide for 1080p and 2160p
// sources), and level 1, its half. Both are tightly packed 8-bit images.

const (
	// maxWorkWidth bounds the working image: a wider input (the full luma,
	// when the pool builds no thumbnail) is box-downscaled to about
	// targetWorkWidth, the thumbnail width, which the block grid and the
	// thresholds are tuned for.
	maxWorkWidth    = 320
	targetWorkWidth = 240
	// minWorkWidth and minWorkHeight are the smallest working images the
	// block grid fits in; smaller pictures get no estimate.
	minWorkWidth  = 64
	minWorkHeight = 36
)

// downscaleFactor is the box factor bringing a width to the working width.
func downscaleFactor(
	width int,
) int {
	if width <= maxWorkWidth {
		return 1
	}

	return (width + targetWorkWidth - 1) / targetWorkWidth
}

// load copies (factor 1) or box-downscales src into dst, a packed image of
// width×height, the size of src divided by factor.
func load(
	dst []byte,
	width, height int,
	src []byte,
	stride, factor int,
) {
	if factor == 1 {
		for y := range height {
			copy(dst[y*width:(y+1)*width], src[y*stride:y*stride+width])
		}

		return
	}

	area := factor * factor

	for y := range height {
		out := dst[y*width : (y+1)*width]

		for x := range out {
			sum := 0

			for dy := range factor {
				row := src[(y*factor+dy)*stride+x*factor : (y*factor+dy)*stride+(x+1)*factor]
				for _, v := range row {
					sum += int(v)
				}
			}

			out[x] = byte((sum + area/2) / area) //nolint:gosec // an average of bytes fits in a byte
		}
	}
}

// halve fills dst (width/2 × height/2) with the 2×2 averages of src, and
// cols and rows with the column and row sums of dst: the projections the
// translation predictor correlates. It returns the sum of dst.
func halve(
	dst, src []byte,
	width, height int,
	cols, rows []int32,
) int {
	w1, h1 := width/2, height/2

	clear(cols)

	total := 0

	for y := range h1 {
		top := src[2*y*width : 2*y*width+2*w1]
		bottom := src[(2*y+1)*width : (2*y+1)*width+2*w1]
		out := dst[y*w1 : (y+1)*w1]
		cols := cols[:w1]
		rowSum := 0

		for x := range out {
			v := (int(top[2*x]) + int(top[2*x+1]) + int(bottom[2*x]) + int(bottom[2*x+1]) + 2) >> 2
			out[x] = byte(v)    //nolint:gosec // an average of bytes fits in a byte
			cols[x] += int32(v) //nolint:gosec // at most 255 × the image height
			rowSum += v
		}

		rows[y] = int32(rowSum)
		total += rowSum
	}

	return total
}
