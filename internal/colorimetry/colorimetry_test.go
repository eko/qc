package colorimetry

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPQ(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		signal float64
		nits   float64
		delta  float64
	}{
		{name: "black", signal: 0, nits: 0, delta: 1e-9},
		{name: "peak", signal: 1, nits: 10000, delta: 1e-6},
		// BT.2408: 100 cd/m² is PQ 0.508, 1000 cd/m² is 0.752.
		{name: "100 nits", signal: 0.508078, nits: 100, delta: 0.01},
		{name: "1000 nits", signal: 0.751827, nits: 1000, delta: 0.1},
		{name: "above 1 clamps", signal: 1.5, nits: 10000, delta: 1e-6},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.nits, PQEOTF(testCase.signal), testCase.delta)

			if testCase.signal <= 1 && testCase.nits > 0 {
				assert.InDelta(t, testCase.signal, PQInverseEOTF(testCase.nits), 1e-6)
			}
		})
	}
}

func TestHLG(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		signal float64
		scene  float64
	}{
		{name: "black", signal: 0, scene: 0},
		{name: "knee", signal: 0.5, scene: 1.0 / 12},
		{name: "peak", signal: 1, scene: 1},
		{name: "negative clamps", signal: -0.1, scene: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.scene, HLGInverseOETF(testCase.signal), 1e-6)
		})
	}

	assert.InDelta(t, 1000, HLGOOTFGain(1, 1000), 1e-9)
	assert.InDelta(t, 1000*math.Pow(0.1, 0.2), HLGOOTFGain(0.1, 1000), 1e-9)
}

// TestITPWorkedExample reproduces the example of ITU-R BT.2124 Annex 4:
// the 58% PQ BT.709 blue of BT.2111, 10-bit full-range code values [296,
// 201, 582], is linear [8.753, 2.291, 181.3] and ITP [0.3554, 0.1346,
// −0.1613]; a measurement of XYZ [36, 15, 190] is ITP [0.3568, 0.1321,
// −0.1629]; the two differ by ΔE ITP 2.363. The measured side matches to
// the printed precision. The expected blue comes out I = 0.3557 here and
// in an independent Python evaluation of the same equations: the
// Recommendation's I is 3·10⁻⁴ lower, hence the looser tolerance.
func TestITPWorkedExample(
	t *testing.T,
) {
	r, g, b := PQEOTF(296.0/1023), PQEOTF(201.0/1023), PQEOTF(582.0/1023)
	// The Recommendation rounds its intermediate values.
	assert.InEpsilon(t, 8.753, r, 2e-3)
	assert.InEpsilon(t, 2.291, g, 2e-3)
	assert.InEpsilon(t, 181.3, b, 2e-3)

	i1, t1, p1 := ITP(r, g, b)
	assert.InDelta(t, 0.3554, i1, 4e-4)
	assert.InDelta(t, 0.1346, t1, 5e-5)
	assert.InDelta(t, -0.1613, p1, 1e-4)

	// Annex 2, conversion 1: XYZ to BT.2100 RGB.
	x, y, z := 36.0, 15.0, 190.0
	r2 := 1.716651187971268*x - 0.355670783776392*y - 0.253366281373660*z
	g2 := -0.666684351832489*x + 1.616481236634939*y + 0.015768545813911*z
	b2 := 0.017639857445311*x - 0.042770613257809*y + 0.942103121235474*z
	i2, t2, p2 := ITP(r2, g2, b2)
	assert.InDelta(t, 0.3568, i2, 5e-5)
	assert.InDelta(t, 0.1321, t2, 5e-5)
	assert.InDelta(t, -0.1629, p2, 5e-5)

	deltaE := DeltaEITPScale * math.Sqrt(sq(0.3554-0.3568)+sq(0.1346-0.1321)+sq(-0.1613+0.1629))
	assert.InDelta(t, 2.363, deltaE, 0.001)
}

func sq(
	v float64,
) float64 {
	return v * v
}

func TestDecoderMaxLight(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		signal     Signal
		y, cb, cr  int
		want       float64
		relTolance float64
	}{
		{name: "pq limited white", signal: Signal{Transfer: transferPQ, BitDepth: 10}, y: 940, cb: 512, cr: 512, want: 10000, relTolance: 1e-4},
		{name: "pq limited black", signal: Signal{Transfer: transferPQ, BitDepth: 10}, y: 64, cb: 512, cr: 512, want: 0, relTolance: 0},
		{name: "pq below black clamps", signal: Signal{Transfer: transferPQ, BitDepth: 10}, y: 0, cb: 512, cr: 512, want: 0, relTolance: 0},
		{name: "pq full range white", signal: Signal{Transfer: transferPQ, BitDepth: 10, FullRange: true}, y: 1023, cb: 512, cr: 512, want: 10000, relTolance: 1e-3},
		{name: "pq 8-bit 100 nits grey", signal: Signal{Transfer: transferPQ, BitDepth: 8}, y: 127, cb: 128, cr: 128, want: PQEOTF(111.0 / 219), relTolance: 1e-3},
		{name: "pq saturated red", signal: Signal{Transfer: transferPQ, BitDepth: 10}, y: 294, cb: 387, cr: 960, want: 10000, relTolance: 0.03},
		// BT.2408: HLG 75% is reference white, 203 cd/m² on a 1000 cd/m² display.
		{name: "hlg 75% grey", signal: Signal{Transfer: transferHLG, BitDepth: 10}, y: 721, cb: 512, cr: 512, want: 203, relTolance: 0.01},
		{name: "hlg white", signal: Signal{Transfer: transferHLG, BitDepth: 10}, y: 940, cb: 512, cr: 512, want: 1000, relTolance: 1e-3},
		{name: "hlg on a 2000 nits display", signal: Signal{Transfer: transferHLG, BitDepth: 10, Peak: 2000}, y: 940, cb: 512, cr: 512, want: 2000, relTolance: 1e-3},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := float64(NewDecoder(testCase.signal).MaxLight(testCase.y, testCase.cb, testCase.cr))
			assert.InDelta(t, testCase.want, got, max(testCase.relTolance*testCase.want, 1e-3))
		})
	}
}

func TestDecoderLight(
	t *testing.T,
) {
	d := NewDecoder(Signal{Transfer: transferPQ, BitDepth: 10})
	r, g, b := d.RGB(64+438, 512, 512)
	assert.InDelta(t, r, g, 1e-6)
	assert.InDelta(t, g, b, 1e-6)

	lr, lg, lb := d.Light(r, g, b)
	assert.InDelta(t, PQEOTF(0.5), float64(lr), 0.05)
	assert.Equal(t, lr, lg)
	assert.Equal(t, lg, lb)
}

func TestPQTable(
	t *testing.T,
) {
	table := NewPQTable()

	var worst float64

	for nits := 1e-6; nits <= 10000; nits *= 1.0137 {
		got := float64(table.Encode(float32(nits)))
		worst = max(worst, math.Abs(got-PQInverseEOTF(nits)))
	}

	assert.Less(t, worst, 3e-6, "table error in PQ units")

	testCases := []struct {
		name string
		nits float32
		want float64
	}{
		{name: "zero", nits: 0, want: PQInverseEOTF(0)},
		{name: "negative", nits: -5, want: PQInverseEOTF(0)},
		{name: "nan", nits: float32(math.NaN()), want: PQInverseEOTF(0)},
		{name: "below the table", nits: 1e-12, want: PQInverseEOTF(0)},
		{name: "peak", nits: 10000, want: 1},
		{name: "above the peak", nits: 20000, want: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, float64(table.Encode(testCase.nits)), 1e-5)
		})
	}
}

func TestDecoderITPMatchesExact(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		signal    Signal
		y, cb, cr int
	}{
		{name: "pq grey", signal: Signal{Transfer: transferPQ, BitDepth: 10}, y: 500, cb: 512, cr: 512},
		{name: "pq colour", signal: Signal{Transfer: transferPQ, BitDepth: 10}, y: 600, cb: 400, cr: 700},
		{name: "pq dark colour", signal: Signal{Transfer: transferPQ, BitDepth: 10}, y: 90, cb: 500, cr: 530},
		{name: "hlg colour", signal: Signal{Transfer: transferHLG, BitDepth: 10}, y: 600, cb: 400, cr: 700},
	}

	pq := NewPQTable()

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d := NewDecoder(testCase.signal)
			gi, gt, gp := d.ITP(testCase.y, testCase.cb, testCase.cr, pq)

			// The same pixel through the exact functions.
			r, g, b := d.RGB(testCase.y, testCase.cb, testCase.cr)
			lr, lg, lb := exactLight(testCase.signal.Transfer, float64(r), float64(g), float64(b))
			wi, wt, wp := ITP(lr, lg, lb)

			// A ΔE ITP of 0.05 between the tabulated and exact pixel.
			tolerance := 0.05 / DeltaEITPScale
			assert.InDelta(t, wi, float64(gi), tolerance)
			assert.InDelta(t, wt, float64(gt), tolerance)
			assert.InDelta(t, wp, float64(gp), tolerance)
		})
	}
}

// exactLight is display light without tables: the PQ EOTF, or HLG at
// 1000 cd/m².
func exactLight(
	transfer string,
	r, g, b float64,
) (float64, float64, float64) {
	if transfer == transferPQ {
		return PQEOTF(r), PQEOTF(g), PQEOTF(b)
	}

	rs, gs, bs := HLGInverseOETF(r), HLGInverseOETF(g), HLGInverseOETF(b)
	k := HLGOOTFGain(KR*rs+KG*gs+KB*bs, 1000)

	return k * rs, k * gs, k * bs
}

func TestDecoderMaxLights(
	t *testing.T,
) {
	ys := []uint16{64, 502, 940, 721, 294}
	cbs := []uint16{512, 480, 512, 530, 387}
	crs := []uint16{512, 560, 512, 500, 960}
	pq := NewPQTable()

	for _, transfer := range []string{transferPQ, transferHLG} {
		t.Run(transfer, func(t *testing.T) {
			d := NewDecoder(Signal{Transfer: transfer, BitDepth: 10})
			nits, bins := make([]float32, len(ys)), make([]uint16, len(ys))

			d.MaxLights(ys, cbs, crs, nits, bins, pq)

			for i := range ys {
				want := d.MaxLight(int(ys[i]), int(cbs[i]), int(crs[i]))
				assert.InDelta(t, want, nits[i], 1e-3*float64(want)+1e-6)
				assert.InDelta(t, PQInverseEOTF(float64(want))*(PQBins-1), float64(bins[i]), 1.01)
			}
		})
	}
}
