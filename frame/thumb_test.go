package frame

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
)

// referenceBox is the direct box filter: the sum of each block, rounded.
func referenceBox(
	src *Plane,
	factor, width, height int,
) []byte {
	out := make([]byte, width*height)
	area := factor * factor

	for ty := range height {
		for tx := range width {
			sum := 0

			for dy := range factor {
				for dx := range factor {
					sum += int(src.Pix[(ty*factor+dy)*src.Stride+tx*factor+dx])
				}
			}

			out[ty*width+tx] = byte((sum + area/2) / area)
		}
	}

	return out
}

func TestBoxDownscaleMatchesReference(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		width, height int
		factor        int
	}{
		{name: "1080p to 240", width: 1920, height: 1080, factor: 8},
		{name: "odd factor, partial blocks ignored", width: 1283, height: 725, factor: 6},
		{name: "narrower than a vector", width: 13, height: 9, factor: 3},
		{name: "column sums beyond 16 bits", width: 520, height: 260, factor: 260},
	}

	rng := rand.New(rand.NewPCG(5, 6))

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			src := newPlane(testCase.width, testCase.height, 1)
			for i := range src.Pix {
				src.Pix[i] = byte(rng.IntN(256))
			}

			w, h := testCase.width/testCase.factor, testCase.height/testCase.factor
			dst := newPlane(w, h, 1)

			boxDownscale(&src, &dst, testCase.factor)

			assert.Equal(t, referenceBox(&src, testCase.factor, w, h), dst.Pix)
		})
	}
}
