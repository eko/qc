package ladder

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cubicProbes samples VMAF = 80 + 10u − 4u² + 0.5u³ (u = ln R − ln 1 Mb/s)
// at the given bitrates, with a CRF linear in log bitrate.
func cubicProbes(
	halfWidth float64,
	bitrates ...float64,
) []Probe {
	probes := make([]Probe, len(bitrates))
	for i, b := range bitrates {
		u := math.Log(b / 1e6)
		probes[i] = Probe{
			Width: 1280, Height: 720, CRF: 27 - u/0.11, Bitrate: int64(b),
			VMAF: 80 + 10*u - 4*u*u + 0.5*u*u*u, HalfWidth: halfWidth,
		}
	}

	return probes
}

func TestFitCurve(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		bitrates []float64
		// at is the bitrate where the fit is checked.
		at        float64
		maxError  float64
		maxShape  float64
		wantShape bool
	}{
		{
			name:      "two probes lean on the curvature prior",
			bitrates:  []float64{300e3, 3e6},
			at:        1e6,
			maxError:  1.5,
			wantShape: true,
		},
		{
			name:     "three probes pin the curvature",
			bitrates: []float64{300e3, 1e6, 3e6},
			at:       550e3,
			maxError: 0.4,
			maxShape: 1.5,
		},
		{
			name:     "five probes pin the cubic",
			bitrates: []float64{300e3, 550e3, 1e6, 1.7e6, 3e6},
			at:       750e3,
			maxError: 0.05,
			maxShape: 0.1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f := fitCurve(cubicProbes(0, testCase.bitrates...), defaultPrior)
			truth := cubicProbes(0, testCase.at)[0].VMAF

			v, hw := f.predict(math.Log(testCase.at))
			assert.InDelta(t, truth, v, testCase.maxError)
			assert.Positive(t, hw)

			shape := f.shapeError(math.Log(testCase.at))
			if testCase.wantShape {
				assert.Greater(t, shape, 2.0, "the curvature is not measured")
			} else {
				assert.Less(t, shape, testCase.maxShape)
			}

			assert.Less(t, f.with(math.Log(testCase.at)).shapeError(math.Log(testCase.at)), math.Max(shape, 1e-9),
				"a probe where the fit is checked reduces its uncertainty")
		})
	}
}

func TestFitCurveNoise(
	t *testing.T,
) {
	probes := cubicProbes(1, 300e3, 1e6, 3e6)
	f := fitCurve(probes, defaultPrior)

	_, hw := f.predict(math.Log(1e6))
	assert.Greater(t, hw, f.shapeError(math.Log(1e6)), "measurement noise adds to the shape uncertainty")
	assert.InDelta(t, 1/z95, probeNoise(probes[0]), 1e-12)
	assert.InDelta(t, minNoise, probeNoise(Probe{}), 1e-12, "exact probes keep a noise floor")
}

func TestFitCurveEmpty(
	t *testing.T,
) {
	f := fitCurve([]Probe{{Width: 640, Height: 360, CRF: 30}}, defaultPrior)

	_, _, ok := f.rangeLog()
	assert.False(t, ok, "a probe without bitrate is ignored")
	assert.Equal(t, Curve{Width: 640, Height: 360}, f.curve())
}

func TestCurveFitCurve(
	t *testing.T,
) {
	f := fitCurve(cubicProbes(0.5, 300e3, 1e6, 3e6), defaultPrior)
	c := f.curve()

	lo, hi := c.Range()
	assert.InDelta(t, 300e3, lo, 1)
	assert.InDelta(t, 3e6, hi, 1)
	assert.Greater(t, len(c.logR), 40, "sampled every 5% of bitrate")

	for i := 1; i < len(c.vmaf); i++ {
		assert.GreaterOrEqual(t, c.vmaf[i], c.vmaf[i-1], "non-decreasing")
		assert.Less(t, c.crf[i], c.crf[i-1], "CRF falls as bitrate grows")
	}

	v, ok := c.VMAFAt(1e6)
	require.True(t, ok)
	assert.InDelta(t, 80, v, 0.3)
	assert.InDelta(t, 27, c.CRFAt(1e6), 1e-6)
	assert.Positive(t, c.halfWidthAt(1e6))
	assert.InDelta(t, c.halfWidthAt(300e3), c.halfWidthAt(100e3), 1e-9, "the nearest point's outside the range")
}

func TestFitsOf(
	t *testing.T,
) {
	var probes []Probe

	for i, height := range []int{360, 1080, 720} {
		for _, p := range cubicProbes(0.5, 300e3, 1e6, 3e6) {
			p.Height, p.VMAF = height, p.VMAF-float64(i)
			probes = append(probes, p)
		}
	}

	fits := fitsOf(probes)
	require.Len(t, fits, 3)
	assert.Equal(t, []int{1080, 720, 360}, []int{fits[0].height, fits[1].height, fits[2].height})
	assert.Len(t, smoothCurves(fits), 3)
}

func TestPooledPrior(
	t *testing.T,
) {
	three := fitCurve(cubicProbes(0.5, 300e3, 1e6, 3e6), defaultPrior)
	two := fitCurve(cubicProbes(0.5, 300e3, 3e6), defaultPrior)

	testCases := []struct {
		name   string
		fits   []curveFit
		wantOK bool
	}{
		{name: "one measured curvature is not enough", fits: []curveFit{three, two}},
		{name: "two measured curvatures are pooled", fits: []curveFit{three, three, two}, wantOK: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			prior, ok := pooledPrior(testCase.fits)
			require.Equal(t, testCase.wantOK, ok)

			if ok {
				assert.InDelta(t, three.mean[2], prior.mean, 1e-9)
				assert.InDelta(t, pooledSD, prior.sd, 1e-9)
			}
		})
	}
}

func TestMatrixInverse(
	t *testing.T,
) {
	m := matrix{{4, 1, 0, 0}, {1, 3, 1, 0}, {0, 1, 2, 1}, {0, 0, 1, 5}}
	inv := m.inverse()

	for i := range terms {
		var unit vector
		unit[i] = 1

		got := m.mul(inv.mul(unit))
		for j := range terms {
			assert.InDelta(t, unit[j], got[j], 1e-12, "row %d col %d", j, i)
		}
	}

	pivoting := matrix{{0, 1, 0, 0}, {1, 0, 0, 0}, {0, 0, 1, 0}, {0, 0, 0, 1}}
	assert.Equal(t, pivoting, pivoting.inverse(), "a permutation is its own inverse")
}
