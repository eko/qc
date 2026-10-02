package ladder

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRejectPerShot(
	t *testing.T,
) {
	verified := func(gain float64) *PerShot {
		return &PerShot{Gain: gain, Measured: &Measurement{VMAF: 90, Bitrate: 1_000_000}}
	}

	testCases := []struct {
		name       string
		rung       Rung
		wantKept   bool
		wantReject bool
	}{
		{
			name:     "a verified gain is kept",
			rung:     Rung{Measured: &Measurement{VMAF: 90}, PerShot: verified(0.02)},
			wantKept: true,
		},
		{
			name:       "a verified loss is rejected",
			rung:       Rung{Measured: &Measurement{VMAF: 90}, PerShot: verified(-0.18)},
			wantReject: true,
		},
		{
			name:       "no gain is no reason for chunks",
			rung:       Rung{Measured: &Measurement{VMAF: 90}, PerShot: verified(0)},
			wantReject: true,
		},
		{
			name:     "an unverified version cannot be judged",
			rung:     Rung{Measured: &Measurement{VMAF: 90}, PerShot: &PerShot{}},
			wantKept: true,
		},
		{
			name:     "nor one whose rung was not verified",
			rung:     Rung{PerShot: verified(-0.18)},
			wantKept: true,
		},
		{name: "no per-shot version"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := testCase.rung
			tried := r.PerShot

			rejectPerShot(&r)

			assert.Equal(t, testCase.wantKept, r.PerShot != nil)
			assert.Equal(t, testCase.wantReject, r.PerShotRejected != nil)

			if testCase.wantReject {
				assert.Same(t, tried, r.PerShotRejected)
			}
		})
	}
}

func TestTitleBend(
	t *testing.T,
) {
	// A title whose curves are exact parabolas in CRF: ln(bitrate) convex,
	// VMAF concave.
	probe := func(crf float64) Probe {
		d := crf - 27

		return Probe{CRF: crf, Bitrate: int64(math.Round(math.Exp(14 - 0.11*d + 0.002*d*d))), VMAF: 85 - 1.2*d - 0.05*d*d}
	}

	testCases := []struct {
		name      string
		probes    []Probe
		low, high float64
		wantRate  float64
		wantVMAF  float64
	}{
		{
			name:     "three probes around the shots' CRFs",
			probes:   []Probe{probe(20), probe(27), probe(34)},
			low:      22,
			high:     32,
			wantRate: 0.002,
			wantVMAF: -0.05,
		},
		{
			name:     "more probes, by least squares",
			probes:   []Probe{probe(13), probe(20), probe(27), probe(34), probe(40)},
			low:      16,
			high:     30,
			wantRate: 0.002,
			wantVMAF: -0.05,
		},
		{
			name: "the three nearest when fewer lie in reach",
			// Around CRF 20 to 22 only one probe is within reach: the two
			// next nearest complete the parabola, and the far one, which
			// bends otherwise, is left out.
			probes:   []Probe{probe(20), probe(27), probe(34), {CRF: 50, Bitrate: 1, VMAF: 99}},
			low:      20,
			high:     22,
			wantRate: 0.002,
			wantVMAF: -0.05,
		},
		{
			name:   "a probe encoded twice counts once as a CRF",
			probes: []Probe{probe(20), probe(20), probe(27)},
			low:    20,
			high:   27,
		},
		{name: "two probes: no curvature", probes: []Probe{probe(20), probe(34)}, low: 22, high: 32},
		{name: "probes without a bitrate are not read", probes: []Probe{probe(20), probe(27), {CRF: 34, VMAF: 70}}, low: 22, high: 32},
		{name: "no probe", low: 22, high: 32},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			bend := titleBend(testCase.probes, testCase.low, testCase.high)

			assert.InDelta(t, testCase.wantRate, bend.Rate, 1e-5)
			assert.InDelta(t, testCase.wantVMAF, bend.VMAF, 1e-6)
		})
	}
}
