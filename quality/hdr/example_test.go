package hdr_test

import (
	"fmt"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality/hdr"
)

// Frame pairs of a PQ video, 10-bit 4:2:0: wPSNR per plane and the mean
// and 99th percentile of ΔE ITP. The same 4-code error weighs 0.5 at luma
// 300 and 2^1.5 at luma 800 in wPSNR: 10·log10(1020² / (0.5·16)) = 51.14 dB
// and 10·log10(1020² / (2.83·16)) = 43.62 dB.
func Example() {
	const width, height = 64, 32

	pool := frame.NewPool(width, height, frame.PoolOptions{Chroma: true, HighBitDepth: true})
	meter := hdr.New(width, height, media.Color{Transfer: media.TransferPQ, Range: "tv"}, 10, 1)

	for _, level := range []uint16{300, 800} {
		ref, dist := pool.Get(), pool.Get()
		fill(ref, level, 0)
		fill(dist, level, 4) // the "encode": every luma sample 4 codes off

		v := meter.Measure(ref, dist)
		fmt.Printf("luma %d: wPSNR Y %.2f dB, ΔE ITP %.2f (p99 %.2f)\n", level, v.WPSNR[0], v.DeltaE, v.DeltaEP99)

		ref.Release()
		dist.Release()
	}
	// Output:
	// luma 300: wPSNR Y 51.14 dB, ΔE ITP 3.30 (p99 3.30)
	// luma 800: wPSNR Y 43.62 dB, ΔE ITP 3.25 (p99 3.26)
}

// fill paints a grey frame of luma code y, off by delta.
func fill(
	f *frame.Frame,
	y, delta uint16,
) {
	for i, p := range []*frame.Plane{&f.Luma, &f.Cb, &f.Cr} {
		v := uint16(512)
		if i == 0 {
			v = y + delta
		}

		samples := p.Uint16()
		for k := range samples {
			samples[k] = v
		}
	}
}
