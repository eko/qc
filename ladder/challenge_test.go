package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

// curveOfPoints is the curve of probes at height through (CRF, kb/s, VMAF)
// points.
func curveOfPoints(
	height int,
	points ...[3]float64,
) Curve {
	probes := make([]Probe, len(points))
	for i, p := range points {
		probes[i] = Probe{Width: height * 16 / 9, Height: height, CRF: p[0], Bitrate: int64(p[1] * 1000), VMAF: p[2]}
	}

	return NewCurve(probes)
}

func TestChallenger(
	t *testing.T,
) {
	// A 1080p curve probed down to 1 Mb/s, still ahead of 720p there.
	high := curveOfPoints(1080, [3]float64{28, 4000, 94}, [3]float64{40, 1800, 90}, [3]float64{52, 1000, 86})
	low := curveOfPoints(720, [3]float64{28, 2200, 88}, [3]float64{40, 1000, 83}, [3]float64{52, 400, 74})
	// A 1080p curve losing to 720p at its lowest probe.
	losing := curveOfPoints(1080, [3]float64{28, 4000, 94}, [3]float64{40, 1800, 90}, [3]float64{52, 1000, 82})
	// A 1080p curve so steep at its low end that even extended it loses at
	// 400 kb/s.
	steep := curveOfPoints(1080, [3]float64{28, 4000, 94}, [3]float64{40, 1800, 89}, [3]float64{52, 1000, 83.1})

	testCases := []struct {
		name    string
		curves  []Curve
		chosen  Curve
		bitrate float64
		want    bool
		wantCRF float64
	}{
		{name: "higher resolution still ahead below its probes", curves: []Curve{high, low}, chosen: low, bitrate: 600e3, want: true, wantCRF: 62},
		{name: "bitrate within the higher resolution's probes", curves: []Curve{high, low}, chosen: low, bitrate: 1200e3},
		{name: "crossover within the probes", curves: []Curve{losing, low}, chosen: low, bitrate: 600e3},
		{name: "even extended, the higher resolution loses", curves: []Curve{steep, low}, chosen: low, bitrate: 400e3},
		{name: "no higher resolution", curves: []Curve{high, low}, chosen: high, bitrate: 600e3},
	}

	b := &build{codec: mustCodec(t, "av1")}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			jobs := b.challengersOf(testCase.curves, testCase.chosen, testCase.bitrate)

			if !testCase.want {
				assert.Empty(t, jobs)

				return
			}

			require.Len(t, jobs, 1)
			assert.Equal(t, 1080, jobs[0].Height)
			assert.InDelta(t, testCase.wantCRF, jobs[0].CRF, 1, "the CRF reaching the rung's bitrate, extrapolated")
		})
	}
}

func TestChallengersOfEveryHigherResolution(
	t *testing.T,
) {
	// 270p rung at 150 kb/s; 360p and 540p were both probed only above
	// it, and both beat 270p at their lowest probes.
	r540 := curveOfPoints(540, [3]float64{40, 600, 70}, [3]float64{52, 300, 60})
	r360 := curveOfPoints(360, [3]float64{40, 450, 60}, [3]float64{52, 250, 52})
	r270 := curveOfPoints(270, [3]float64{40, 400, 50}, [3]float64{52, 150, 38})

	b := &build{codec: mustCodec(t, "av1")}
	jobs := b.challengersOf([]Curve{r540, r360, r270}, r270, 150e3)

	heights := make([]int, len(jobs))
	for i, j := range jobs {
		heights[i] = j.Height
	}

	assert.ElementsMatch(t, []int{540, 360}, heights, "every higher resolution at once")
}

func TestExtendedVMAF(
	t *testing.T,
) {
	c := curveOfPoints(720, [3]float64{28, 2000, 88}, [3]float64{40, 1000, 82})
	flat := curveOfPoints(720, [3]float64{28, 2000, 80}, [3]float64{40, 1000, 80})

	testCases := []struct {
		name    string
		curve   Curve
		bitrate float64
		want    float64
	}{
		{name: "within the probes", curve: c, bitrate: 1414213.56, want: 85},
		{name: "below: the lowest segment extended", curve: c, bitrate: 500e3, want: 76},
		{name: "above: the highest segment extended", curve: c, bitrate: 4000e3, want: 94},
		{name: "flat curve", curve: flat, bitrate: 500e3, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, testCase.curve.extendedVMAF(testCase.bitrate), 1e-6)
		})
	}
}

func TestUnprobed(
	t *testing.T,
) {
	probes := []Probe{{Height: 360, CRF: 58}, {Height: 360, CRF: 59}, {Height: 720, CRF: 63}}

	testCases := []struct {
		name   string
		job    Probe
		want   float64
		wantOK bool
	}{
		{name: "new CRF", job: Probe{Height: 360, CRF: 50}, want: 50, wantOK: true},
		{name: "probed: the next free CRF", job: Probe{Height: 360, CRF: 58}, want: 60, wantOK: true},
		{name: "another resolution's CRF", job: Probe{Height: 540, CRF: 58}, want: 58, wantOK: true},
		{name: "probed at the maximum CRF", job: Probe{Height: 720, CRF: 63}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := unprobed(probes, testCase.job, 1, 63)

			require.Equal(t, testCase.wantOK, ok)

			if ok {
				assert.InDelta(t, testCase.want, got.CRF, 1e-9)
			}
		})
	}
}

// stoppedProbes are probes of a title whose 1080p curve stops at 1 Mb/s
// while still ahead of 720p, probed down to 400 kb/s.
func stoppedProbes() []Probe {
	var out []Probe

	for _, p := range [][4]float64{
		{1080, 28, 4000, 94}, {1080, 40, 1800, 90}, {1080, 52, 1000, 86},
		{720, 28, 2200, 88}, {720, 40, 1000, 83}, {720, 52, 400, 72},
	} {
		h := int(p[0])
		out = append(out, Probe{Width: h * 16 / 9, Height: h, CRF: p[1], Bitrate: int64(p[2] * 1000), VMAF: p[3], HalfWidth: 0.5})
	}

	return out
}

func TestChallenge(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		failOn  func(op, target string, seen int) bool
		wantErr error
	}{
		{name: "challengers probed and added"},
		{name: "a challenger encode fails", failOn: failing(opEncode, "probe-0.mp4", 0), wantErr: errFake},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
			lab.failOn = testCase.failOn
			b := &build{
				engine: labEngine(lab), codec: mustCodec(t, "av1"),
				digestReport: lab.digest, referenceReport: lab.digest, workDir: t.TempDir(),
				video: lab.source.Info.Video[0],
				opts:  Options{GOPDuration: media.Seconds(2), Parallel: 2, Constraints: Constraints{MinVMAF: 60}.WithDefaults(), Probing: ProbingFixed},
			}

			probes := stoppedProbes()

			got, err := b.challenge(t.Context(), probes)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Positive(t, b.probing.Challengers)
			assert.Len(t, got, len(probes)+b.probing.Challengers)

			for _, p := range got[len(probes):] {
				assert.Equal(t, 1080, p.Height, "the higher resolution probed below its probes")
				assert.Greater(t, p.CRF, 52.0)
			}
		})
	}
}
