package xpsnr_test

import (
	"fmt"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/quality/xpsnr"
)

// Frames go to the Meter in display order; the mean distortion over frames
// converts to XPSNR per plane, as ffmpeg's xpsnr filter pools it.
func Example() {
	const (
		width, height = 64, 64
		bitDepth      = 8
		frames        = 3
	)

	pool := frame.NewPool(width, height, frame.PoolOptions{Chroma: true})
	meter := xpsnr.New(width, height, bitDepth, 25, 1)

	var sum xpsnr.Distortion

	for i := range frames {
		ref, dist := pool.Get(), pool.Get()
		paint(ref, i, 0)
		paint(dist, i, 3) // the "encode": the same picture, slightly noisy

		d := meter.Measure(ref, dist)
		for p := range sum {
			sum[p] += d[p]
		}

		ref.Release()
		dist.Release()
	}

	y := xpsnr.Decibels(sum[0]/frames, width, height, bitDepth)
	fmt.Printf("XPSNR Y %.2f dB\n", y)
	// Output: XPSNR Y 32.42 dB
}

// paint fills f with a moving gradient, plus a deterministic ±noise pattern.
func paint(
	f *frame.Frame,
	t, noise int,
) {
	for _, p := range []*frame.Plane{&f.Luma, &f.Cb, &f.Cr} {
		for y := range p.Height {
			row := p.Row(y)
			for x := range p.Width {
				v := 32 + x + y + 4*t
				if noise > 0 && (x*7+y*13)%5 == 0 {
					v += noise * (1 - 2*((x+y)%2))
				}

				row[x] = byte(v)
			}
		}
	}
}
