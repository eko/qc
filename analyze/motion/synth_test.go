package motion

import (
	"math"
	"math/rand/v2"
)

// texture is a smooth random image (value noise summed over octaves),
// sampled bilinearly: a stand-in for natural picture content.
type texture struct {
	size   int
	values []float64
}

// newTexture builds a size×size texture whose finest detail is about two
// pixels, from a fixed seed.
func newTexture(
	size int,
	seed uint64,
) texture {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	t := texture{size: size, values: make([]float64, size*size)}

	for _, octave := range []struct{ cell, weight float64 }{{32, 0.45}, {8, 0.35}, {2.5, 0.2}} {
		n := int(float64(size)/octave.cell) + 2
		grid := make([]float64, n*n)

		for i := range grid {
			grid[i] = rng.Float64()
		}

		for y := range size {
			for x := range size {
				t.values[y*size+x] += octave.weight * bilinear(grid, n, float64(x)/octave.cell, float64(y)/octave.cell)
			}
		}
	}

	return t
}

func bilinear(
	values []float64,
	n int,
	x, y float64,
) float64 {
	x, y = min(max(x, 0), float64(n)-1.001), min(max(y, 0), float64(n)-1.001)
	x0, y0 := int(x), int(y)
	fx, fy := x-float64(x0), y-float64(y0)
	at := func(i, j int) float64 { return values[j*n+i] }

	return (1-fy)*((1-fx)*at(x0, y0)+fx*at(x0+1, y0)) + fy*((1-fx)*at(x0, y0+1)+fx*at(x0+1, y0+1))
}

// render draws a w×h frame of the texture seen through a camera centred on
// (cx, cy) texture pixels, magnified by scale and rolled by angle
// (radians): frame pixel p shows texture point centre + R(−angle)·p/scale.
func (t texture) render(
	w, h int,
	cx, cy, scale, angle float64,
) []byte {
	out := make([]byte, w*h)
	cos, sin := math.Cos(angle), math.Sin(angle)

	for y := range h {
		for x := range w {
			px, py := (float64(x)+0.5-float64(w)/2)/scale, (float64(y)+0.5-float64(h)/2)/scale
			tx, ty := cx+cos*px+sin*py, cy-sin*px+cos*py
			out[y*w+x] = byte(40 + 180*bilinear(t.values, t.size, tx, ty))
		}
	}

	return out
}
