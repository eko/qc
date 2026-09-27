package hdr

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/colorimetry"
	"github.com/eko/qc/media"
)

var pq = media.Color{Transfer: media.TransferPQ, Range: "tv"}

// uniform returns a width×height frame whose Y, Cb and Cr samples are y,
// cb and cr, at 8 or 10 bits.
func uniform(
	width, height, depth int,
	y, cb, cr int,
) *frame.Frame {
	f := frame.NewPool(width, height, frame.PoolOptions{Chroma: true, HighBitDepth: depth > 8}).Get()
	for i, p := range []*frame.Plane{&f.Luma, &f.Cb, &f.Cr} {
		v := []int{y, cb, cr}[i]
		if depth > 8 {
			samples := p.Uint16()
			for k := range samples {
				samples[k] = uint16(v)
			}

			continue
		}

		for k := range p.Pix {
			p.Pix[k] = byte(v)
		}
	}

	return f
}

func set(
	p *frame.Plane,
	x, y, v int,
) {
	if p.BytesPerSample == 2 {
		p.Uint16()[y*p.Stride/2+x] = uint16(v)

		return
	}

	p.Pix[y*p.Stride+x] = byte(v)
}

// weight is the JVET weight of a 10-bit luma code value.
func weight(
	code float64,
) float64 {
	return math.Exp2(min(max(0.015*code-7.5, -3), 6) / 3)
}

func TestIdenticalFrames(
	t *testing.T,
) {
	ref := uniform(64, 32, 10, 600, 400, 700)
	v := New(64, 32, pq, 10, 2).Measure(ref, ref)

	for _, w := range v.WPSNR {
		assert.True(t, math.IsInf(w, 1))
	}

	assert.Zero(t, v.DeltaE)
	assert.InDelta(t, deltaEBinWidth, v.DeltaEP99, 1e-12, "the first bin's upper edge")
}

func TestWPSNRHandComputed(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		code  int
		plane int
		error int
	}{
		{name: "dark luma error", code: 100, plane: 0, error: 4},
		{name: "bright luma error weighs more", code: 900, plane: 0, error: 4},
		{name: "chroma error weighted by the co-sited luma", code: 700, plane: 1, error: 10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ref := uniform(8, 4, 10, testCase.code, 512, 512)
			dist := uniform(8, 4, 10, testCase.code, 512, 512)
			planes := []*frame.Plane{&dist.Luma, &dist.Cb, &dist.Cr}
			base := []int{testCase.code, 512, 512}[testCase.plane]
			set(planes[testCase.plane], 1, 1, base+testCase.error)

			v := New(8, 4, pq, 10, 1).Measure(ref, dist)

			samples := 32
			if testCase.plane > 0 {
				samples = 8
			}

			wsse := weight(float64(testCase.code)) * float64(testCase.error*testCase.error)
			want := 10 * math.Log10(1020*1020*float64(samples)/wsse)
			assert.InDelta(t, want, v.WPSNR[testCase.plane], 1e-9)
		})
	}

	assert.Greater(t, weight(900), weight(100))
}

func TestDeltaEOfUniformFrames(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		color media.Color
	}{
		{name: "pq", color: pq},
		{name: "hlg", color: media.Color{Transfer: media.TransferHLG}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ref := uniform(64, 32, 10, 600, 480, 560)
			dist := uniform(64, 32, 10, 606, 484, 556)

			v := New(64, 32, testCase.color, 10, 3).Measure(ref, dist)

			d := colorimetry.NewDecoder(colorimetry.Signal{Transfer: testCase.color.Transfer, BitDepth: 10})
			table := colorimetry.NewPQTable()
			i1, t1, p1 := d.ITP(600, 480, 560, table)
			i2, t2, p2 := d.ITP(606, 484, 556, table)
			want := colorimetry.DeltaEITPScale * math.Sqrt(float64((i1-i2)*(i1-i2)+(t1-t2)*(t1-t2)+(p1-p2)*(p1-p2)))

			require.Greater(t, want, 1.0)
			assert.InDelta(t, want, v.DeltaE, 1e-4)
			assert.InDelta(t, want, v.DeltaEP99, deltaEBinWidth)
		})
	}
}

func TestPercentileSeesTheWorstPoints(
	t *testing.T,
) {
	// 16×8 chroma samples, ΔE points every DeltaEStep: 32 points, of
	// which the first one of a row gets a large error.
	ref := uniform(32, 16, 10, 500, 512, 512)
	dist := uniform(32, 16, 10, 500, 512, 512)
	set(&dist.Luma, 0, 0, 700)

	v := New(32, 16, pq, 10, 2).Measure(ref, dist)

	assert.Greater(t, v.DeltaEP99, 10.0, "one point in 32 is above the 99th percentile")
	assert.Less(t, v.DeltaE, v.DeltaEP99)
	assert.InDelta(t, 0, percentile(make([]int32, deltaEBins), 0, Percentile), 1e-12)
}

func TestMonotonicWithDistortion(
	t *testing.T,
) {
	ref := uniform(64, 32, 10, 600, 480, 560)
	m := New(64, 32, pq, 10, 2)

	var prev Values

	for i, delta := range []int{2, 8, 32} {
		v := m.Measure(ref, uniform(64, 32, 10, 600+delta, 480-delta, 560+delta))

		if i > 0 {
			assert.Less(t, v.WPSNR[0], prev.WPSNR[0])
			assert.Greater(t, v.DeltaE, prev.DeltaE)
		}

		prev = v
	}
}

func TestEightBitMatchesTenBit(
	t *testing.T,
) {
	ref8, dist8 := uniform(32, 16, 8, 150, 120, 140), uniform(32, 16, 8, 152, 121, 139)
	ref10, dist10 := uniform(32, 16, 10, 600, 480, 560), uniform(32, 16, 10, 608, 484, 556)

	v8 := New(32, 16, pq, 8, 1).Measure(ref8, dist8)
	v10 := New(32, 16, pq, 10, 1).Measure(ref10, dist10)

	for c := range v8.WPSNR {
		assert.InDelta(t, v10.WPSNR[c], v8.WPSNR[c], 1e-9)
	}

	assert.InDelta(t, v10.DeltaE, v8.DeltaE, 1e-3)
}

func TestWorkersDoNotChangeResults(
	t *testing.T,
) {
	ref := uniform(96, 54, 10, 600, 480, 560)
	dist := uniform(96, 54, 10, 600, 480, 560)

	for y := range 54 {
		for x := range 96 {
			set(&dist.Luma, x, y, 590+(x*7+y*3)%23)
		}
	}

	one := New(96, 54, pq, 10, 1).Measure(ref, dist)
	many := New(96, 54, pq, 10, 7).Measure(ref, dist)

	assert.Equal(t, one, many)
}

func BenchmarkMeasure1080p(
	b *testing.B,
) {
	ref := uniform(1920, 1080, 10, 600, 480, 560)
	dist := uniform(1920, 1080, 10, 600, 480, 560)

	for y := range 1080 {
		for x := range 1920 {
			set(&dist.Luma, x, y, 590+(x*7+y*3)%23)
		}
	}

	m := New(1920, 1080, pq, 10, 1)

	for b.Loop() {
		m.Measure(ref, dist)
	}
}

func TestMoreWorkersThanBands(
	t *testing.T,
) {
	// 9 chroma rows on 4 workers: bands of 3 rows, the fourth is empty.
	ref := uniform(32, 18, 10, 600, 480, 560)
	dist := uniform(32, 18, 10, 610, 480, 560)

	assert.Equal(t, New(32, 18, pq, 10, 1).Measure(ref, dist), New(32, 18, pq, 10, 4).Measure(ref, dist))
}
