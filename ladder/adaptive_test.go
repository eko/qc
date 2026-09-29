package ladder

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

func TestParseProbing(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		in      string
		want    Probing
		wantErr bool
	}{
		{name: "default", in: "", want: ""},
		{name: "fixed", in: "fixed", want: ProbingFixed},
		{name: "adaptive", in: "adaptive", want: ProbingAdaptive},
		{name: "unknown", in: "random", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseProbing(testCase.in)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidProbing)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestBuildAdaptive(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		model rateModel
		opts  Options
		check func(t *testing.T, res *Result)
	}{
		{
			name: "starts from two CRFs per resolution and stays within budget",
			opts: Options{Codec: "h264", Probing: ProbingAdaptive},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, ProbingAdaptive, res.Probing.Mode)
				assert.Equal(t, 14, res.Probing.Budget, "two fewer than the fixed design of 5 resolutions")
				assert.LessOrEqual(t, len(res.Probes), 14)
				assert.Greater(t, len(res.Probes), 10, "probes are added beyond the initial design")
				assert.Greater(t, res.Probing.Rounds, 1)

				for i, p := range res.Probes[:10] {
					assert.Equal(t, []float64{20, 34}[i%2], p.CRF, "probe %d", i)
				}

				for _, r := range res.Rungs {
					assert.Positive(t, r.PredictionError)
					assert.InDelta(t, r.PredictedVMAF, r.Measured.VMAF, CalibrationTolerance+0.5, "the fitted curves predict the rungs")
				}
			},
		},
		{
			name: "a looser tolerance converges before the budget",
			opts: Options{Codec: "h264", Probing: ProbingAdaptive, Tolerance: 5, SkipVerify: true},
			check: func(t *testing.T, res *Result) {
				assert.True(t, res.Probing.Converged)
				assert.Less(t, len(res.Probes), res.Probing.Budget)
			},
		},
		{
			name:  "hard content extends the top curve",
			model: rateModel{knee: 0.8},
			opts:  Options{Codec: "h264", Probing: ProbingAdaptive, SkipVerify: true},
			check: func(t *testing.T, res *Result) {
				top := 0

				for _, p := range res.Probes {
					if p.Extra && p.Height == 1080 {
						top++
					}
				}

				assert.Positive(t, top, "the top curve is extended")
			},
		},
		{
			name: "a budget of the initial design stops there",
			opts: Options{Codec: "av1", Probing: ProbingAdaptive, MaxProbes: 10, SkipVerify: true},
			check: func(t *testing.T, res *Result) {
				assert.Len(t, res.Probes, 10)
				assert.Equal(t, 1, res.Probing.Rounds)
				assert.False(t, res.Probing.Converged)
			},
		},
		{
			name: "a single imposed resolution gets one more probe",
			opts: Options{Codec: "h264", Probing: ProbingAdaptive, Constraints: Constraints{Resolutions: []int{720}}, SkipVerify: true},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, 3, res.Probing.Budget)
				assert.Len(t, res.Rungs, 1)
			},
		},
		{
			name: "fixed mode reports its rounds",
			opts: Options{Codec: "h264", SkipVerify: true},
			check: func(t *testing.T, res *Result) {
				rounds := 1
				if slices.ContainsFunc(res.Probes, func(p Probe) bool { return p.Extra }) {
					rounds = 2
				}

				require.NotNil(t, res.Probing.Level, "the probes' level is measured")
				assert.Equal(t, ProbingReport{Mode: ProbingFixed, Rounds: rounds, Level: res.Probing.Level}, res.Probing)

				for _, r := range res.Rungs {
					assert.Zero(t, r.PredictionError, "linear interpolation has no error model")
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(testCase.model, sourceReport(1920, 1080, 8, 25, 600))
			progress := &progressLog{}
			opts := testCase.opts
			opts.Progress = progress.record

			res, err := labEngine(lab).Build(t.Context(), sourcePath, opts)
			require.NoError(t, err)

			probes := progress.stage(StageProbe)
			require.Len(t, probes, len(res.Probes))

			if res.Probing.Mode == ProbingAdaptive {
				for i, p := range probes {
					assert.Equal(t, i+1, p.Done, "adaptive probes count up across rounds")
					assert.Equal(t, res.Probing.Budget, p.Total)
				}
			}

			testCase.check(t, res)
		})
	}
}

func TestNextProbes(
	t *testing.T,
) {
	model := rateModel{}
	source := media.VideoStream{Width: 1920, Height: 1080}

	var initial []Probe

	for _, h := range []int{1080, 720, 540} {
		for _, crf := range []float64{20, 34} {
			p := encodeParams(h, crf)
			initial = append(initial, Probe{
				Width: p.Width, Height: h, CRF: crf, Bitrate: model.bitrate(p), VMAF: model.vmaf(p), HalfWidth: 0.5,
			})
		}
	}

	testCases := []struct {
		name        string
		n           int
		constraints Constraints
		want        func(t *testing.T, jobs []Probe)
	}{
		{
			name: "no room",
			n:    0,
			want: func(t *testing.T, jobs []Probe) { assert.Empty(t, jobs) },
		},
		{
			name: "a batch holds distinct probes",
			n:    3,
			want: func(t *testing.T, jobs []Probe) {
				require.Len(t, jobs, 3)

				for i, a := range jobs {
					assert.False(t, probed(initial, a), "job %d is new", i)
					assert.False(t, probed(jobs[i+1:], a), "job %d is unique", i)
				}
			},
		},
		{
			name:        "an unplannable ladder asks for curve extensions only",
			n:           2,
			constraints: Constraints{Rungs: 3, MinVMAF: 99.5, TopVMAF: 90},
			want: func(t *testing.T, jobs []Probe) {
				for _, p := range jobs {
					assert.True(t, p.Extra, "%+v", p)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec := mustCodec(t, "h264")
			opts := Options{Probing: ProbingAdaptive, Constraints: testCase.constraints}.withDefaults(codec)
			b := &build{codec: codec, opts: opts, video: source}

			testCase.want(t, b.nextProbes(initial, testCase.n))
		})
	}
}

func TestCrossovers(
	t *testing.T,
) {
	low := fitCurve(cubicProbes(0.5, 300e3, 1e6, 3e6), defaultPrior)
	low.height = 360

	high := fitCurve(cubicProbes(0.5, 900e3, 3e6, 9e6), defaultPrior)
	high.height = 720

	far := fitCurve(cubicProbes(0.5, 5e6, 9e6), defaultPrior)
	far.height = 1080

	fits := map[int]curveFit{360: low, 720: high, 1080: far}
	b := &build{opts: Options{Tolerance: 0.5, Constraints: Constraints{}.WithDefaults()}}

	testCases := []struct {
		name  string
		x     float64
		limit int
		want  []quantity
	}{
		{
			name:  "a higher resolution just below its range competes",
			x:     math.Log(600e3),
			limit: math.MaxInt,
			want:  []quantity{{height: 360, other: 720, below: true}},
		},
		{
			name:  "resolutions above the previous rung do not compete",
			x:     math.Log(600e3),
			limit: 360,
		},
		{
			name:  "far below every other range",
			x:     math.Log(200e3),
			limit: math.MaxInt,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := b.crossovers(fits, low, testCase.x, testCase.limit)
			require.Len(t, got, len(testCase.want))

			for i, q := range got {
				assert.Equal(t, testCase.want[i].other, q.other)
				assert.Equal(t, testCase.want[i].below, q.below)
			}
		})
	}
}

func TestQuantityExcess(
	t *testing.T,
) {
	low := fitCurve(cubicProbes(0.5, 300e3, 3e6), defaultPrior)
	low.height = 360

	high := fitCurve(cubicProbes(0.5, 900e3, 9e6), defaultPrior)
	high.height = 720

	fits := map[int]curveFit{360: low, 720: high}
	x := math.Log(1e6)

	rung := quantity{height: 360, x: x, tolerance: 0.5}
	assert.Positive(t, rung.excess(fits), "a two-probe curve is uncertain between its probes")

	fits[360] = low.with(x)
	assert.Zero(t, rung.excess(fits), "a probe at the rung settles it")

	extension := quantity{height: 360, other: 720, x: math.Log(600e3), tolerance: 0.5, gap: 3, below: true}
	assert.Positive(t, extension.excess(fits))

	b := &build{codec: mustCodec(t, "h264")}
	job, fit, ok := b.bestProbe(fits, []quantity{extension}, nil)
	require.True(t, ok)
	assert.Equal(t, 720, job.Height)
	assert.LessOrEqual(t, fit.reach, math.Log(600e3), "the extension reaches the rung")

	fits[720] = high.with(math.Log(500e3))
	assert.Zero(t, extension.excess(fits), "a probe below the range settles the extension")
}

// encodeParams are the settings of a 16:9 encode at height and crf.
func encodeParams(
	height int,
	crf float64,
) encode.Params {
	return encode.Params{Width: height * 16 / 9, Height: height, CRF: crf}
}

func TestBestProbe(
	t *testing.T,
) {
	low := fitCurve(cubicProbes(0.5, 300e3, 3e6), defaultPrior)
	x := math.Log(1e6)
	quantities := []quantity{{height: 720, x: x, tolerance: 0.5}}
	b := &build{codec: mustCodec(t, "h264")}

	testCases := []struct {
		name   string
		taken  []Probe
		wantOK bool
	}{
		{name: "a probe at the uncertain rung", wantOK: true},
		{name: "the rung's CRF is already probed", taken: []Probe{{Height: 720, CRF: 27}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			job, fit, ok := b.bestProbe(map[int]curveFit{720: low}, quantities, testCase.taken)
			require.Equal(t, testCase.wantOK, ok)

			if ok {
				assert.Equal(t, Probe{Width: 1280, Height: 720, CRF: 27}, job)
				assert.Less(t, fit.shapeError(x), low.shapeError(x))
			}
		})
	}
}
