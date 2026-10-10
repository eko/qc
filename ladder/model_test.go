package ladder

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
)

// kneeFeaturer is a Featurer that sees the difficulty of the fake lab's
// content, as the features of labModel's titles are.
type kneeFeaturer struct {
	knee  float64
	specs []features.Spec
	err   error
}

func (f *kneeFeaturer) Extract(
	_ context.Context,
	spec features.Spec,
) (features.Set, error) {
	f.specs = append(f.specs, spec)

	return features.Set{Segments: 2, Values: map[string][]float64{
		"knee": {f.knee, f.knee}, "knee_log": {math.Log(f.knee), math.Log(f.knee)},
	}}, f.err
}

// labModel trains a ladder model on titles the fake lab's rate model
// would give at several difficulties (knees), on the grid of heights and
// CRFs: what a corpus measured with that encoder would teach.
func labModel(
	t *testing.T,
	preset string,
	heights []int,
) *predict.Model {
	t.Helper()

	crfs := []float64{13, 16.5, 20, 23.5, 27, 30.5, 34, 37.5}

	var titles []predict.Title

	for i, knees := range [][]float64{{0.2, 0.25, 0.3}, {0.35, 0.45, 0.5}, {0.6, 0.7, 0.9}} {
		title := predict.Title{Name: string(rune('a' + i)), Codec: "h264", Preset: preset, SourceHeight: heights[0]}

		for _, h := range heights {
			for _, crf := range crfs {
				point := predict.TitlePoint{Point: predict.Point{Height: h, CRF: crf}, Width: h * 16 / 9}

				for _, knee := range knees {
					p := encode.Params{Height: h, CRF: crf}
					point.Bitrate = append(point.Bitrate, float64(rateModel{knee: knee}.bitrate(p)))
					point.VMAF = append(point.VMAF, rateModel{knee: knee}.vmaf(p))
				}

				title.Points = append(title.Points, point)
			}
		}

		// What an analysis would see of each segment: here, its difficulty.
		title.Features = map[string][]float64{"knee": knees, "knee_log": {math.Log(knees[0]), math.Log(knees[1]), math.Log(knees[2])}}

		titles = append(titles, title)
	}

	model, err := predict.Train(titles, predict.TrainOptions{})
	require.NoError(t, err)

	return model
}

func TestBuildWithModel(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	model := labModel(t, "fast", heights)
	lab := newFakeLab(rateModel{knee: 0.55}, sourceReport(1920, 1080, 8, 25, 600))
	progress := &progressLog{}

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec: "h264", Probing: ProbingModel, Curves: []*predict.Model{model}, Progress: progress.record,
	})
	require.NoError(t, err)

	assert.Equal(t, ProbingModel, res.Probing.Mode)
	require.NotNil(t, res.Probing.Model)
	assert.Empty(t, res.Probing.Model.Fallback)
	assert.Equal(t, 3, res.Probing.Model.Titles)
	anchors := len(res.Probing.Model.Anchors)
	require.Equal(t, 2*len(heights), anchors, "the design with the most anchors: two per resolution")
	assert.Positive(t, res.Probing.Model.MeanVMAFError)

	// Only the anchors were encoded as probes; the rest of the grid is
	// predicted, with the model's error as its half-width.
	assert.Len(t, progress.stage(StageProbe), anchors)
	require.Len(t, res.Probes, len(model.Heights)*len(model.CRFs))

	predicted := 0

	for _, p := range res.Probes {
		if p.Predicted {
			predicted++

			assert.Positive(t, p.HalfWidth)

			// The level correction moved every probe by the same offset.
			truth := lab.model.vmaf(encode.Params{Height: p.Height, CRF: p.CRF}) + res.Probing.Level.Offset
			assert.InDelta(t, truth, p.VMAF, 2.5, "%dp CRF %g", p.Height, p.CRF)
		}
	}

	assert.Equal(t, len(res.Probes)-anchors, predicted)

	// The stages after the probes are those of any build: every rung is
	// verified by an encode, and lands on its prediction.
	require.NotEmpty(t, res.Rungs)

	for _, r := range res.Rungs {
		require.NotNil(t, r.Measured)
		assert.InDelta(t, r.PredictedVMAF, r.Measured.VMAF, CalibrationTolerance+1)
	}
}

func TestBuildWithModelFallsBackToFixedProbing(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]

	testCases := []struct {
		name   string
		models []*predict.Model
		opts   Options
		want   string
	}{
		{name: "no model", want: "no ladder model was given"},
		{
			name: "another preset", models: []*predict.Model{labModel(t, "slow", heights)},
			want: "the model is for h264 slow on 1080p sources, not h264 fast on 1080p",
		},
		{
			name: "other resolutions", models: []*predict.Model{labModel(t, "fast", heights[:3])},
			want: "the model is for the resolutions [1080 720 540], not [1080 720 540 360 270]",
		},
		{
			name: "10-bit encodes", models: []*predict.Model{labModel(t, "fast", heights)}, opts: Options{BitDepth: 10},
			want: "the model was trained on 8-bit SDR encodes without film grain synthesis",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
			opts := testCase.opts
			opts.Codec, opts.Probing, opts.Curves, opts.SkipVerify = "h264", ProbingModel, testCase.models, true

			res, err := labEngine(lab).Build(t.Context(), sourcePath, opts)
			require.NoError(t, err)

			assert.Equal(t, ProbingFixed, res.Probing.Mode)
			require.NotNil(t, res.Probing.Model)
			assert.Equal(t, testCase.want, res.Probing.Model.Fallback)

			for _, p := range res.Probes {
				assert.False(t, p.Predicted)
			}
		})
	}
}

func TestBuildWithModelTakesTheModelCoveringTheBuild(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec: "h264", Probing: ProbingModel, SkipVerify: true,
		Curves: []*predict.Model{labModel(t, "slow", heights), labModel(t, "fast", heights)},
	})
	require.NoError(t, err)

	assert.Equal(t, ProbingModel, res.Probing.Mode)
	assert.Empty(t, res.Probing.Model.Fallback)
}

func TestBuildWithModelMeasuresFewerAnchorsWithinTolerance(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	model := labModel(t, "fast", heights)
	lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec: "h264", Probing: ProbingModel, Curves: []*predict.Model{model}, Tolerance: 50, SkipVerify: true,
	})
	require.NoError(t, err)

	assert.Len(t, res.Probing.Model.Anchors, 1, "any design is within such a tolerance: the cheapest is taken")
}

func TestBuildPredicted(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	model := labModel(t, "fast", heights)
	lab := newFakeLab(rateModel{knee: 0.55}, sourceReport(1920, 1080, 8, 25, 600))
	featurer := &kneeFeaturer{knee: 0.55}
	engine := NewEngine(lab, lab, lab, WithFeatures(featurer))

	res, err := engine.Build(t.Context(), sourcePath, Options{
		Codec: "h264", Probing: ProbingPredicted, Curves: []*predict.Model{model}, SkipVerify: true,
	})
	require.NoError(t, err)

	// Nothing was encoded: the digest was described, and every probe
	// predicted from that.
	assert.Empty(t, lab.params)
	assert.Zero(t, res.ProbeEncodes())
	assert.Len(t, res.Probes, len(model.Heights)*len(model.CRFs))
	assert.Nil(t, res.Probing.Level, "no sampled measurement, no level to correct")

	assert.Equal(t, ProbingPredicted, res.Probing.Mode)
	require.NotNil(t, res.Probing.Model)
	assert.Empty(t, res.Probing.Model.Anchors)
	assert.Equal(t, 2, res.Probing.Model.Features)
	assert.Equal(t, "40 predicted from the analysis of the digest by a model of 3 titles, nothing measured, within 0.9 VMAF on average on titles it had not seen",
		roundedNote(res.Probing.Model, len(res.Probes)))

	require.Len(t, featurer.specs, 1)
	spec := featurer.specs[0]
	assert.Equal(t, 1920, spec.Width)
	assert.Equal(t, 1080, spec.Height)
	assert.Equal(t, 50, spec.Frames)
	assert.InDelta(t, 25, spec.Rate, 1e-9)
	assert.Equal(t, heights, spec.Heights)

	require.NotEmpty(t, res.Rungs)

	for _, p := range res.Probes {
		truth := lab.model.vmaf(encode.Params{Height: p.Height, CRF: p.CRF})
		assert.InDelta(t, truth, p.VMAF, 4, "%dp CRF %g", p.Height, p.CRF)
	}
}

// roundedNote is the note of a report whose error is set to a round value,
// the lab's being whatever the fit gives.
func roundedNote(
	m *ModelProbing,
	predicted int,
) string {
	rounded := *m
	rounded.MeanVMAFError = 0.9

	return rounded.Note(predicted)
}

func TestBuildPredictedVerifiesItsRungs(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	lab := newFakeLab(rateModel{knee: 0.55}, sourceReport(1920, 1080, 8, 25, 600))
	engine := NewEngine(lab, lab, lab, WithFeatures(&kneeFeaturer{knee: 0.55}))

	res, err := engine.Build(t.Context(), sourcePath, Options{
		Codec: "h264", Probing: ProbingPredicted, Curves: []*predict.Model{labModel(t, "fast", heights)},
	})
	require.NoError(t, err)

	// The only encodes are the verifications (and their corrections).
	assert.GreaterOrEqual(t, len(lab.params), len(res.Rungs))

	for _, r := range res.Rungs {
		require.NotNil(t, r.Measured)
	}
}

func TestBuildPredictedFallsBackToFixedProbing(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	withFeatures := labModel(t, "fast", heights)

	plain := *withFeatures
	plain.Designs = nil

	for _, d := range withFeatures.Designs {
		if len(d.Features) == 0 {
			plain.Designs = append(plain.Designs, d)
		}
	}

	testCases := []struct {
		name     string
		model    *predict.Model
		featurer Featurer
		want     string
	}{
		{name: "a model without features", model: &plain, featurer: &kneeFeaturer{knee: 0.4}, want: "the model cannot predict from the analysis alone: its corpus holds no features"},
		{name: "an engine without a featurer", model: withFeatures, want: "this engine cannot extract the features the model reads"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
			engine := NewEngine(lab, lab, lab)

			if testCase.featurer != nil {
				engine = NewEngine(lab, lab, lab, WithFeatures(testCase.featurer))
			}

			res, err := engine.Build(t.Context(), sourcePath, Options{
				Codec: "h264", Probing: ProbingPredicted, Curves: []*predict.Model{testCase.model}, SkipVerify: true,
			})
			require.NoError(t, err)

			assert.Equal(t, ProbingFixed, res.Probing.Mode)
			assert.Equal(t, testCase.want, res.Probing.Model.Fallback)
			assert.Positive(t, res.ProbeEncodes())
		})
	}
}

func TestBuildPredictedReportsAFeatureFailure(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
	engine := NewEngine(lab, lab, lab, WithFeatures(&kneeFeaturer{knee: 0.4, err: errFake}))

	_, err := engine.Build(t.Context(), sourcePath, Options{
		Codec: "h264", Probing: ProbingPredicted, Curves: []*predict.Model{labModel(t, "fast", heights)}, SkipVerify: true,
	})
	require.ErrorIs(t, err, errFake)
}

func TestBuildWithModelUsesFeaturesOnlyWithinTolerance(
	t *testing.T,
) {
	heights := DefaultHeights()[2:]
	model := labModel(t, "fast", heights)
	lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
	featurer := &kneeFeaturer{knee: 0.4}
	engine := NewEngine(lab, lab, lab, WithFeatures(featurer))

	// A tolerance any design meets: with a featurer, the design reading no
	// anchor is the cheapest.
	res, err := engine.Build(t.Context(), sourcePath, Options{
		Codec: "h264", Probing: ProbingModel, Curves: []*predict.Model{model}, Tolerance: 50, SkipVerify: true,
	})
	require.NoError(t, err)
	assert.Empty(t, res.Probing.Model.Anchors)
	assert.Len(t, featurer.specs, 1)
}
