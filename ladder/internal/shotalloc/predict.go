package shotalloc

import (
	"math"
	"slices"

	"github.com/eko/qc/internal/linalg"
)

// ridgePenalty regularises the per-shot regressions, on standardised
// features: a few dozen measured shots, whose models are noisy (each is
// read on a 2 s piece of its shot), must not be followed to the letter. In
// replays of the allocation on a title encoded whole, 0.3 gained as much as
// or more than a penalty a hundred times smaller or ten times larger.
const ridgePenalty = 0.3

// predictedFields are the model parameters regressed on shot features:
// the shot's ln(bitrate) and VMAF at both probes. The probe CRFs are not
// (every shot is fitted at the same ones), nor the curvature (the
// title's).
var predictedFields = []func(*Model) *float64{
	func(m *Model) *float64 { return &m.xA },
	func(m *Model) *float64 { return &m.xB },
	func(m *Model) *float64 { return &m.vA },
	func(m *Model) *float64 { return &m.vB },
}

// Predict fills in the models of the shots the digest does not cover
// (measured false) by weighted least squares on the shot features, fitted
// on the measured shots (ridge-regularised). The first feature is the
// constant; the others are standardised on the measured shots, so that the
// penalty weighs them alike whatever their units. With fewer measured shots
// than features, their average model is used. weights are the shots' frame
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

	norm := Normalise(ws)
	mean := Blend(ys, norm)
	fits := make([][]float64, len(predictedFields))
	scale := newScaler(xs, norm)
	xs = scale.all(xs)

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
			x := scale.one(features[i])
			for f, field := range predictedFields {
				*field(&m) = linalg.Dot(fits[f], x)
			}
		}

		out[i] = m
	}

	return out
}

// scaler standardises features: every feature but the first, the constant,
// is centred on its weighted mean and divided by its weighted standard
// deviation over the observations it was built on. A feature that does not
// vary there is left centred: it predicts nothing.
type scaler struct {
	mean, deviation []float64
}

func newScaler(
	xs [][]float64,
	weights []float64,
) scaler {
	n := len(xs[0])
	s := scaler{mean: make([]float64, n), deviation: make([]float64, n)}

	for k := 1; k < n; k++ {
		for i, x := range xs {
			s.mean[k] += weights[i] * x[k]
		}

		for i, x := range xs {
			d := x[k] - s.mean[k]
			s.deviation[k] += weights[i] * d * d
		}

		s.deviation[k] = math.Sqrt(s.deviation[k])
	}

	return s
}

// one standardises the features of one observation.
func (s scaler) one(
	x []float64,
) []float64 {
	out := slices.Clone(x)

	for k := 1; k < len(out); k++ {
		out[k] -= s.mean[k]
		if s.deviation[k] > 0 {
			out[k] /= s.deviation[k]
		}
	}

	return out
}

// all standardises the features of every observation.
func (s scaler) all(
	xs [][]float64,
) [][]float64 {
	out := make([][]float64, len(xs))
	for i, x := range xs {
		out[i] = s.one(x)
	}

	return out
}
