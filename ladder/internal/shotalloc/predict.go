package shotalloc

import (
	"slices"

	"github.com/eko/qc/internal/linalg"
)

// ridgePenalty regularises the per-shot regressions: few shots, correlated
// features.
const ridgePenalty = 1e-3

// predictedFields are the model parameters regressed on shot features. crf0
// is not (every shot is fitted at the same probe CRFs), nor q (the title's
// curvature).
var predictedFields = []func(*Model) *float64{
	func(m *Model) *float64 { return &m.x0 },
	func(m *Model) *float64 { return &m.beta },
	func(m *Model) *float64 { return &m.xMid },
	func(m *Model) *float64 { return &m.vMid },
	func(m *Model) *float64 { return &m.k },
}

// Predict fills in the models of the shots the digest does not cover
// (measured false) by weighted least squares on the shot features, fitted
// on the measured shots (ridge-regularised). With fewer measured shots than
// features, their average model is used. weights are the shots' frame
// shares; measured models are kept as they are.
func Predict(
	models []Model,
	measured []bool,
	weights []float64,
	features [][]float64,
) []Model {
	var xs [][]float64

	var ys []Model

	var ws []float64

	for i := range models {
		if measured[i] {
			xs, ys, ws = append(xs, features[i]), append(ys, models[i]), append(ws, weights[i])
		}
	}

	out := slices.Clone(models)
	if len(ys) == 0 {
		return out
	}

	total := 0.0
	for _, w := range ws {
		total += w
	}

	norm := make([]float64, len(ws))
	for i, w := range ws {
		norm[i] = w / total
	}

	mean := Blend(ys, norm)
	fits := make([][]float64, len(predictedFields))

	for f, field := range predictedFields {
		values := make([]float64, len(ys))
		for i := range ys {
			values[i] = *field(&ys[i])
		}

		fits[f] = linalg.Ridge(xs, values, norm, ridgePenalty)
	}

	for i := range out {
		if measured[i] {
			continue
		}

		m := mean
		if len(ys) >= len(features[i]) {
			for f, field := range predictedFields {
				*field(&m) = linalg.Dot(fits[f], features[i])
			}
		}

		out[i] = m
	}

	return out
}
