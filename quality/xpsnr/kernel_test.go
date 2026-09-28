package xpsnr

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

// kernelLengths cover rows shorter than a vector, with a tail after the
// vectors, and longer than one call of the vector loops.
var kernelLengths = []int{3, 16, 18, 64, 65, 1920, 1<<16 + 37}

// randomRow returns n random samples, extremes included.
func randomRow(
	rng *rand.Rand,
	n int,
) []uint8 {
	out := make([]uint8, n)
	for i := range out {
		switch rng.IntN(8) {
		case 0:
			out[i] = 0
		case 1:
			out[i] = 255
		default:
			out[i] = uint8(rng.IntN(256))
		}
	}

	return out
}

// randomRow16 returns n random samples of depth bits, extremes included.
func randomRow16(
	rng *rand.Rand,
	n, depth int,
) []int16 {
	peak := 1<<depth - 1

	out := make([]int16, n)
	for i := range out {
		switch rng.IntN(8) {
		case 0:
			out[i] = 0
		case 1:
			out[i] = int16(peak)
		default:
			out[i] = int16(rng.IntN(peak + 1))
		}
	}

	return out
}

// TestRowKernels checks the row loops of 8-bit samples (NEON on arm64)
// against the portable ones: same sums, same histories.
func TestRowKernels(
	t *testing.T,
) {
	rng := rand.New(rand.NewPCG(1, 2))

	for _, n := range kernelLengths {
		a, b, c := randomRow(rng, n), randomRow(rng, n), randomRow(rng, n)
		p1, p2 := randomRow16(rng, n, 8), randomRow16(rng, n, 8)

		t.Run("sse", func(t *testing.T) {
			assert.Equal(t, sseRow(a, b), sseRow8(a, b), "%d samples", n)
		})

		t.Run("highpass", func(t *testing.T) {
			assert.Equal(t, highpassRow(a, b, c), highpassRow8(a, b, c), "%d samples", n)
		})

		t.Run("first order", func(t *testing.T) {
			want, got := slices.Clone(p1), slices.Clone(p1)

			assert.Equal(t, firstOrderRow(a, want), firstOrderRow8(a, got), "%d samples", n)
			assert.Equal(t, want, got)
		})

		t.Run("second order", func(t *testing.T) {
			want1, want2 := slices.Clone(p1), slices.Clone(p2)
			got1, got2 := slices.Clone(p1), slices.Clone(p2)

			assert.Equal(t, secondOrderRow(a, want1, want2), secondOrderRow8(a, got1, got2), "%d samples", n)
			assert.Equal(t, want1, got1)
			assert.Equal(t, want2, got2)
		})
	}
}

// TestRowKernels16 checks the row loops of 16-bit samples, at 10 and 12
// bits, against the portable ones.
func TestRowKernels16(
	t *testing.T,
) {
	rng := rand.New(rand.NewPCG(3, 4))

	for _, depth := range []int{10, 12} {
		for _, n := range kernelLengths {
			a, b, c := randomRow16(rng, n, depth), randomRow16(rng, n, depth), randomRow16(rng, n, depth)
			p1, p2 := randomRow16(rng, n, depth), randomRow16(rng, n, depth)

			assert.Equal(t, sseRow(a, b), sseRow16(a, b), "sse, %d bits, %d samples", depth, n)
			assert.Equal(t, highpassRow(a, b, c), highpassRow16(a, b, c), "highpass, %d bits, %d samples", depth, n)

			want, got := slices.Clone(p1), slices.Clone(p1)
			assert.Equal(t, firstOrderRow(a, want), firstOrderRow16(a, got), "first order, %d bits, %d samples", depth, n)
			assert.Equal(t, want, got)

			want1, want2 := slices.Clone(p1), slices.Clone(p2)
			got1, got2 := slices.Clone(p1), slices.Clone(p2)
			assert.Equal(t, secondOrderRow(a, want1, want2), secondOrderRow16(a, got1, got2), "second order, %d bits, %d samples", depth, n)
			assert.Equal(t, want1, got1)
			assert.Equal(t, want2, got2)
		}
	}
}

// TestRowKernelSelection checks that each sample type takes its own row
// loop.
func TestRowKernelSelection(
	t *testing.T,
) {
	pointer := func(f any) uintptr { return reflect.ValueOf(f).Pointer() }

	assert.Equal(t, pointer(sseRow8), pointer(rowKernel(sseRow[uint8], sseRow8, sseRow16)))
	assert.Equal(t, pointer(sseRow16), pointer(rowKernel(sseRow[int16], sseRow8, sseRow16)))
	assert.Equal(t, pointer(sseRow[int16]), pointer(rowKernel(sseRow[int16], sseRow8)))
}
