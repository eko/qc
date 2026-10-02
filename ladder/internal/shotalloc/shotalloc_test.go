package shotalloc

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bent is the curvature of a title whose ln(bitrate) is convex and whose
// VMAF is concave in CRF, as real curves are.
var bent = Bend{Rate: 0.002, VMAF: -0.05}

func TestModelAt(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		model    Model
		crf      float64
		wantRate float64
		wantVMAF float64
	}{
		{name: "through the first probe", model: NewModel(20, 30, 4e6, 1e6, 95, 80, bent), crf: 20, wantRate: 4e6, wantVMAF: 95},
		{name: "through the second probe", model: NewModel(20, 30, 4e6, 1e6, 95, 80, bent), crf: 30, wantRate: 1e6, wantVMAF: 80},
		{
			name:  "between the probes, bent as the title",
			model: NewModel(20, 30, 4e6, 1e6, 95, 80, bent),
			crf:   25,
			// The chords give 2 Mb/s and 87.5; the bends, 25 CRF² from
			// both probes, take 0.002·25 off ln(bitrate) and add 0.05·25.
			wantRate: 2e6 * math.Exp(-0.05),
			wantVMAF: 87.5 + 1.25,
		},
		{
			name:     "no curvature: the chords",
			model:    NewModel(20, 30, 4e6, 1e6, 95, 80, Bend{}),
			crf:      25,
			wantRate: 2e6,
			wantVMAF: 87.5,
		},
		{name: "one probe CRF: flat", model: NewModel(20, 20, 1e6, 1e6, 90, 90, bent), crf: 30, wantRate: 1e6, wantVMAF: 90},
		{name: "VMAF never exceeds 100", model: NewModel(10, 20, 8e6, 4e6, 99.9, 98, Bend{}), crf: 0, wantRate: 16e6, wantVMAF: 100},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rate, vmaf := testCase.model.At(testCase.crf)
			assert.InDelta(t, testCase.wantRate, rate, 1)
			assert.InDelta(t, testCase.wantVMAF, vmaf, 1e-9)
		})
	}
}

func TestModelShape(
	t *testing.T,
) {
	// The same bump for every shot of a title, whatever the gap between
	// its two bitrates: a shot twice as far apart in bitrate is not
	// promised four times the bump.
	_, near := NewModel(20, 30, 2e6, 1e6, 95, 80, bent).At(25)
	_, far := NewModel(20, 30, 16e6, 1e6, 95, 80, bent).At(25)
	assert.InDelta(t, near, far, 1e-9)

	// A shot already saturated at the first probe gains nothing below it:
	// its quality stays flat where the bent curve would turn back down.
	saturating := NewModel(10, 20, 8e6, 4e6, 99.5, 99, bent)
	_, top := saturating.At(5)
	_, beyond := saturating.At(-10)
	assert.InDelta(t, top, beyond, 1e-9, "flat past the turning point")
	assert.GreaterOrEqual(t, top, 99.5)

	// Neither the bitrate nor the quality ever grows with the CRF.
	m := NewModel(20, 30, 4e6, 1e6, 95, 80, bent)
	prevRate, prevVMAF := m.At(0)

	for crf := 0.5; crf <= 63; crf += 0.5 {
		rate, vmaf := m.At(crf)
		assert.LessOrEqual(t, rate, prevRate, "crf %g", crf)
		assert.LessOrEqual(t, vmaf, prevVMAF, "crf %g", crf)

		prevRate, prevVMAF = rate, vmaf
	}
}

func TestBlend(
	t *testing.T,
) {
	a := NewModel(20, 30, 4e6, 1e6, 95, 80, Bend{Rate: 0.002, VMAF: -0.06})
	b := NewModel(20, 30, 2e6, 0.5e6, 90, 70, Bend{Rate: 0.004, VMAF: -0.02})

	testCases := []struct {
		name    string
		weights []float64
		want    Model
	}{
		{name: "all of one", weights: []float64{1, 0}, want: a},
		{name: "half each", weights: []float64{0.5, 0.5}, want: Model{
			crfA: 20, crfB: 30, xA: (a.xA + b.xA) / 2, xB: (a.xB + b.xB) / 2,
			vA: 92.5, vB: 75, bend: Bend{Rate: 0.003, VMAF: -0.04},
		}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := Blend([]Model{a, b}, testCase.weights)
			assert.InDeltaSlice(t, fields(testCase.want), fields(got), 1e-12)
		})
	}
}

// fields lists the parameters of m.
func fields(
	m Model,
) []float64 {
	return []float64{m.crfA, m.crfB, m.xA, m.xB, m.vA, m.vB, m.bend.Rate, m.bend.VMAF}
}

func TestNormalise(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		weights []float64
		want    []float64
	}{
		{name: "frame counts", weights: []float64{1, 3}, want: []float64{0.25, 0.75}},
		{name: "no weight", weights: []float64{0, 0}, want: []float64{0, 0}},
		{name: "empty", weights: nil, want: []float64{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Normalise(testCase.weights))
		})
	}
}

func TestMenus(
	t *testing.T,
) {
	models := []Model{NewModel(20, 30, 4e6, 1e6, 95, 80, bent), NewModel(20, 30, 2e6, 0.5e6, 90, 70, bent)}

	high := Menu(models, 1080, []float64{20, 30})
	require.Len(t, high, 2)
	assert.Equal(t, Option{Height: 1080, CRF: 20, Rate: high[0][0].Rate, VMAF: 95}, high[0][0])
	assert.InDelta(t, 0.5e6, high[1][1].Rate, 1)

	joined := JoinMenus(high, Menu(models, 720, []float64{25}))
	require.Len(t, joined, 2)

	for _, options := range joined {
		assert.Equal(t, []int{1080, 1080, 720}, []int{options[0].Height, options[1].Height, options[2].Height})
	}
}

func TestSolveLambda(
	t *testing.T,
) {
	// Two shots: an easy one saturating early, a hard one still climbing.
	models := []Model{
		NewModel(20, 30, 2e6, 0.5e6, 98, 94, bent),
		NewModel(20, 30, 6e6, 1.5e6, 92, 76, bent),
	}
	weights := []float64{0.5, 0.5}

	var grid []float64
	for crf := 14.0; crf <= 36; crf += 0.5 {
		grid = append(grid, crf)
	}

	testCases := []struct {
		name   string
		target float64
	}{
		{name: "mid quality", target: 90},
		{name: "high quality", target: 95},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := SolveLambda(Menu(models, 1080, grid), weights, testCase.target)
			require.GreaterOrEqual(t, a.VMAF, testCase.target)
			assert.Positive(t, a.Lambda)
			assert.Greater(t, a.Picks[0].CRF, a.Picks[1].CRF, "the easy shot gives bits to the hard one")

			// Any single CRF reaching the target costs more.
			for _, crf := range grid {
				same := Allocate(Menu(models, 1080, []float64{crf}), weights, 1)
				if same.VMAF >= testCase.target {
					assert.LessOrEqual(t, a.Bitrate, same.Bitrate*1.02, "crf %g", crf)
				}
			}
		})
	}

	unreachable := SolveLambda(Menu(models, 1080, grid), weights, 99.9)
	assert.InDelta(t, 14.0, unreachable.Picks[1].CRF, 1e-9, "an unreachable target gets the best quality")
}

func TestPredict(
	t *testing.T,
) {
	models := []Model{{vA: 90, vB: 10}, {}, {vA: 80, vB: 20}, {vA: 70, vB: 30}, {}}
	features := [][]float64{{1, 1, 0}, {1, 1.5, 0}, {1, 2, 0}, {1, 3, 0}, {1, 3, 0}}
	weights := Normalise([]float64{1, 1, 1, 1, 1})

	testCases := []struct {
		name     string
		measured []bool
		want     func(t *testing.T, got []Model)
	}{
		{
			name:     "regressed on features",
			measured: []bool{true, false, true, true, false},
			want: func(t *testing.T, got []Model) {
				// The measured shots lose 10 VMAF per unit of the feature
				// around their mean (80 at 2); the penalty keeps 1/1.3 of
				// that slope, and the last feature, which never varies,
				// predicts nothing.
				assert.InDelta(t, 80+5/1.3, got[1].vA, 1e-9)
				assert.InDelta(t, 80-10/1.3, got[4].vA, 1e-9)
				assert.InDelta(t, 20-5/1.3, got[1].vB, 1e-9)
				assert.InDelta(t, 90, got[0].vA, 1e-9, "measured shots keep their model")
			},
		},
		{
			name:     "too few measured shots: their average",
			measured: []bool{true, false, false, false, false},
			want: func(t *testing.T, got []Model) {
				assert.InDelta(t, 90, got[4].vA, 1e-9)
			},
		},
		{
			name:     "nothing measured",
			measured: []bool{false, false, false, false, false},
			want: func(t *testing.T, got []Model) {
				assert.Equal(t, models, got)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.want(t, Predict(models, testCase.measured, weights, features))
		})
	}
}
