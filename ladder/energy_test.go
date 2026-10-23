package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// line is a curve of height h through two probes: VMAF lo at 1 Mb/s and
// hi at 4 Mb/s, linear in the logarithm of the bitrate.
func line(
	h int,
	lo, hi float64,
) Curve {
	return NewCurve([]Probe{
		{Width: h * 16 / 9, Height: h, CRF: 30, Bitrate: 1_000_000, VMAF: lo},
		{Width: h * 16 / 9, Height: h, CRF: 20, Bitrate: 4_000_000, VMAF: hi},
	})
}

func TestLowerForEnergy(
	t *testing.T,
) {
	// At 2 Mb/s: 1080p 85, 720p 84.5, 540p 82.
	curves := []Curve{line(1080, 80, 90), line(720, 79.5, 89.5), line(540, 77, 87)}

	testCases := []struct {
		name      string
		targets   []Target
		tolerance float64
		want      []int
		lowered   []LoweredRung
	}{
		{
			name:    "no tolerance",
			targets: []Target{{Bitrate: 2_000_000, Height: 1080, VMAF: 85}},
			want:    []int{1080},
		},
		{
			name:      "the lowest resolution within the tolerance",
			targets:   []Target{{Bitrate: 2_000_000, Height: 1080, VMAF: 85}},
			tolerance: 0.6,
			want:      []int{720},
			lowered:   []LoweredRung{{Rung: 0, From: 1080, To: 720, VMAFLost: 0.5}},
		},
		{
			name:      "a wider tolerance reaches lower",
			targets:   []Target{{Bitrate: 2_000_000, Height: 1080, VMAF: 85}},
			tolerance: 3,
			want:      []int{540},
			lowered:   []LoweredRung{{Rung: 0, From: 1080, To: 540, VMAFLost: 3}},
		},
		{
			name:      "an imposed resolution stays",
			targets:   []Target{{Bitrate: 2_000_000, Height: 1080, VMAF: 85, Fixed: true}},
			tolerance: 3,
			want:      []int{1080},
		},
		{
			name: "resolutions never increase down the ladder",
			targets: []Target{
				{Bitrate: 2_000_000, Height: 1080, VMAF: 85},
				{Bitrate: 1_000_000, Height: 1080, VMAF: 80},
			},
			tolerance: 3,
			want:      []int{540, 540},
			lowered: []LoweredRung{
				{Rung: 0, From: 1080, To: 540, VMAFLost: 3},
				{Rung: 1, From: 1080, To: 540, VMAFLost: 3},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, lowered := lowerForEnergy(testCase.targets, curves, testCase.tolerance)
			require.Len(t, got, len(testCase.want))

			for i, h := range testCase.want {
				assert.Equal(t, h, got[i].Height, "rung %d", i)
				assert.Equal(t, testCase.targets[i].Bitrate, got[i].Bitrate, "the bitrate is kept")
				assert.GreaterOrEqual(t, got[i].VMAF, testCase.targets[i].VMAF-testCase.tolerance-1e-9)
			}

			require.Len(t, lowered, len(testCase.lowered))

			for i, l := range testCase.lowered {
				assert.Equal(t, l.Rung, lowered[i].Rung)
				assert.Equal(t, l.From, lowered[i].From)
				assert.Equal(t, l.To, lowered[i].To)
				assert.InDelta(t, l.VMAFLost, lowered[i].VMAFLost, 1e-9)
			}
		})
	}
}

func TestEnergyOf(
	t *testing.T,
) {
	// The digest is a hundredth of the title: an encode of 36 CPU seconds
	// of it is one core-hour on the title.
	probes := []Probe{
		{Height: 1080, EncodeCPU: 72},
		{Height: 1080, EncodeCPU: 72},
		{Height: 720, EncodeCPU: 36},
		{Height: 540, EncodeCPU: 18, Predicted: true},
	}

	testCases := []struct {
		name      string
		rungs     []Rung
		lowered   []LoweredRung
		watts     float64
		wantNil   bool
		wantHours []float64
		estimated []bool
		total     float64
		kWh       float64
		saved     float64
	}{
		{
			name:      "verified rungs, at their own cost",
			rungs:     []Rung{{Height: 1080, Measured: &Measurement{EncodeCPU: 90}}, {Height: 720, Measured: &Measurement{EncodeCPU: 36}}},
			wantHours: []float64{2.5, 1},
			estimated: []bool{false, false},
			total:     3.5,
		},
		{
			name:      "an unverified rung costs as its probes",
			rungs:     []Rung{{Height: 1080}, {Height: 540}},
			wantHours: []float64{2, 0},
			estimated: []bool{true, false},
			total:     2,
		},
		{
			name:      "energy at a power per core",
			rungs:     []Rung{{Height: 720, Measured: &Measurement{EncodeCPU: 36}}},
			watts:     10,
			wantHours: []float64{1},
			estimated: []bool{false},
			total:     1,
			kWh:       0.015,
		},
		{
			name:      "a rung lowered from 1080p saves in proportion of pixels",
			rungs:     []Rung{{Height: 720, Measured: &Measurement{EncodeCPU: 36}}},
			lowered:   []LoweredRung{{Rung: 0, From: 1080, To: 720, VMAFLost: 0.4}},
			wantHours: []float64{1},
			estimated: []bool{false},
			total:     1,
			saved:     1.25,
		},
		{
			name:    "nothing measured",
			rungs:   []Rung{{Height: 540}},
			wantNil: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := &Result{Probes: probes, Rungs: testCase.rungs, Digest: Digest{Share: 0.01, DecodeCPU: 18}}

			e := energyOf(res, testCase.lowered, 0.5, testCase.watts)
			if testCase.wantNil {
				assert.Nil(t, e)

				return
			}

			require.NotNil(t, e)

			for i, hours := range testCase.wantHours {
				if hours == 0 {
					assert.Nil(t, res.Rungs[i].Encode)

					continue
				}

				require.NotNil(t, res.Rungs[i].Encode)
				assert.InDelta(t, hours, res.Rungs[i].Encode.CPUHours, 1e-9)
				assert.Equal(t, testCase.estimated[i], res.Rungs[i].Encode.Estimated)
			}

			assert.InDelta(t, testCase.total, e.CPUHours, 1e-9)
			assert.InDelta(t, 0.5, e.DecodeCPUHours, 1e-9, "18 s of digest, a hundredth of the title")
			assert.InDelta(t, testCase.kWh, e.KWh, 1e-12)
			assert.InDelta(t, testCase.saved, e.SavedCPUHours, 1e-9)
		})
	}
}
