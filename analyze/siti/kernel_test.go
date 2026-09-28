package siti

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/stats"
)

// referenceSI is the straightforward P.910 SI loop the optimised kernel
// replaced: one row at a time, one 3×3 neighbourhood per pixel.
func referenceSI(
	p *frame.Plane,
) float64 {
	var (
		sum   float64
		sumSq int64
	)

	for y := 1; y < p.Height-1; y++ {
		above, row, below := p.Row(y-1), p.Row(y), p.Row(y+1)

		var rowSum float64

		for x := 0; x+2 < len(row); x++ {
			gx := int32(above[x+2]) + 2*int32(row[x+2]) + int32(below[x+2]) - int32(above[x]) - 2*int32(row[x]) - int32(below[x])
			gy := int32(below[x]) + 2*int32(below[x+1]) + int32(below[x+2]) - int32(above[x]) - 2*int32(above[x+1]) - int32(above[x+2])
			sq := gx*gx + gy*gy
			sumSq += int64(sq)
			rowSum += math.Sqrt(float64(sq))
		}

		sum += rowSum
	}

	return stats.StdDev(sum, float64(sumSq), float64((p.Width-2)*(p.Height-2)))
}

// referenceTI is the straightforward TI loop.
func referenceTI(
	prev, cur *frame.Plane,
) float64 {
	var sum, sumSq int64

	for i, v := range cur.Pix {
		d := int64(v) - int64(prev.Pix[i])
		sum += d
		sumSq += d * d
	}

	return stats.StdDev(float64(sum), float64(sumSq), float64(cur.Width*cur.Height))
}

// maxRowSamples exceeds the longest run of the vector loops.
const maxRowSamples = 1<<16 + 16

func TestKernelsMatchReferenceBitForBit(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		width, height int
	}{
		{name: "smallest", width: 3, height: 3},
		{name: "height not a multiple of the pass", width: 17, height: 8},
		{name: "odd sizes", width: 33, height: 29},
		{name: "1080p", width: 1920, height: 1080},
		{name: "rows longer than a vector run", width: maxRowSamples + 37, height: 3},
	}

	rng := rand.New(rand.NewPCG(1, 2))

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			noise := plane(testCase.width, testCase.height, func(int, int) byte { return byte(rng.IntN(256)) })
			smooth := plane(testCase.width, testCase.height, func(x, y int) byte { return byte(x/3 + y/5 + rng.IntN(4)) })

			for _, p := range []*frame.Plane{noise, smooth} {
				assert.Equal(t, math.Float64bits(referenceSI(p)), math.Float64bits(SpatialInformation(p)))
			}

			assert.Equal(t, math.Float64bits(referenceTI(noise, smooth)), math.Float64bits(TemporalInformation(noise, smooth)))
		})
	}
}
