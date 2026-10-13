// Package predict completes the rate-quality curves of a title from a few
// measured encodes.
//
// A ladder needs the bitrate and the VMAF of a title at every resolution
// over a range of CRFs: fixed probing measures three or four encodes per
// resolution. The curves of one encoder are much alike from a title to the
// next, though: once a title is placed by one measured encode per
// resolution (an anchor), the rest of its grid follows closely. A Model is
// that relation, learnt on a corpus of titles measured on a whole grid
// (Train): one linear model per grid point, of the bitrate and of the VMAF
// there, on the anchors' bitrates and VMAFs.
//
// A Model holds several designs, each a set of anchors: more anchors cost
// more encodes and predict better. A design may also read the features of
// the title (package features: what its analysis says of it), and a design
// reading them alone, without any anchor, predicts a ladder from the
// analysis of the video: nothing is encoded. Train validates every design
// by leaving each title of the corpus out in turn, and a design carries
// the errors it made on the titles it had not seen: what a ladder built on
// it may trust. How few anchors a model needs is a matter of how many
// titles it has seen.
package predict

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
)

// SchemaVersion is the version of the JSON form of a Model.
const SchemaVersion = 1

// Errors of a Model.
var (
	// ErrModel is returned for a model file that cannot be used.
	ErrModel = errors.New("invalid ladder model")
	// ErrAnchors is returned when the measurements given to a design are
	// not those of its anchors.
	ErrAnchors = errors.New("measurements do not match the anchors of the design")
	// ErrFeatures is returned when a feature a design reads is not given.
	ErrFeatures = errors.New("a feature of the design is missing")
)

// Point is a point of the grid of a model: a resolution and a CRF.
type Point struct {
	Height int     `json:"height"`
	CRF    float64 `json:"crf"`
}

// Measurement is an encode as measured: what an anchor gives a design, and
// what a design predicts of the other points.
type Measurement struct {
	Point
	Width int `json:"width"`
	// Bitrate is in bits per second.
	Bitrate float64 `json:"bitrate"`
	VMAF    float64 `json:"vmaf"`
	// VMAFError and BitrateError (a fraction of the bitrate) are the 95th
	// percentiles of the errors the design made at this point on the
	// titles it was validated on; zero for a measured anchor, and for a
	// design that could not be validated.
	VMAFError    float64 `json:"vmafError,omitempty"`
	BitrateError float64 `json:"bitrateError,omitempty"`
}

// Model predicts the grid of a title for one encoder setting.
type Model struct {
	SchemaVersion int `json:"schemaVersion"`
	// Codec and Preset are the encoder setting the corpus was encoded
	// with: the model says nothing of another.
	Codec  string `json:"codec"`
	Preset string `json:"preset"`
	// SourceHeight is the height of the titles of the corpus. Encodes are
	// scored at the source's resolution, so the curves of a 1080p source
	// are not those of a 720p one.
	SourceHeight int `json:"sourceHeight"`
	// Heights (highest first) and CRFs (lowest first) span the grid.
	Heights []int     `json:"heights"`
	CRFs    []float64 `json:"crfs"`
	// Titles and Segments count the corpus the model was trained on.
	Titles   int `json:"titles"`
	Segments int `json:"segments"`
	// Designs are the sets of anchors the model can predict from, the
	// ones with the most anchors first.
	Designs []Design `json:"designs"`
}

// Design is what a prediction reads (anchors, features, or both) and the
// models predicting the grid from it.
type Design struct {
	// Anchors are the encodes measured; none for a design predicting from
	// the features alone.
	Anchors []Point `json:"anchors"`
	// Features are the names of the features of the title the design
	// reads (see package features); none for a design of anchors alone.
	Features []string `json:"features,omitempty"`
	// Rate and VMAF hold, for each point of the grid (heights then CRFs),
	// the coefficients of its linear model: a constant, then for each
	// anchor the logarithm of its bitrate per pixel and its VMAF on the
	// logistic scale (see link), then each feature. They predict the
	// logarithm of the bitrate per pixel and the VMAF on the same scale.
	Rate [][]float64 `json:"rate"`
	VMAF [][]float64 `json:"vmaf"`
	// Validation is what the design did on titles left out of its
	// training; nil when the corpus had a single title.
	Validation *Validation `json:"validation,omitempty"`
}

// Validation is the leave-one-title-out check of a design: each title of
// the corpus predicted by a model trained on the others, on digests drawn
// from it.
type Validation struct {
	// Clips is the number of digests predicted.
	Clips int `json:"clips"`
	// VMAFError and BitrateError are, for each point of the grid, the 95th
	// percentile of the absolute error (VMAF points, fraction of the
	// bitrate).
	VMAFError    []float64 `json:"vmafError"`
	BitrateError []float64 `json:"bitrateError"`
	// MeanVMAFError and MeanBitrateError average the absolute errors over
	// the points whose VMAF a ladder uses (see ladderRange).
	MeanVMAFError    float64 `json:"meanVmafError"`
	MeanBitrateError float64 `json:"meanBitrateError"`
	// WorstVMAFError is the largest error on those points.
	WorstVMAFError float64 `json:"worstVmafError"`
	// Titles are the mean errors of each title on those points, in the
	// order of the corpus: which kinds of content the design reads well.
	// Nil in models trained before it was kept.
	Titles []TitleValidation `json:"titles,omitempty"`
}

// TitleValidation is the validation of a design on one title, predicted
// by a model trained on the others.
type TitleValidation struct {
	Name string `json:"name"`
	// VMAFError and BitrateError average the absolute errors over the
	// points whose VMAF a ladder uses (VMAF points, fraction of the
	// bitrate).
	VMAFError    float64 `json:"vmafError"`
	BitrateError float64 `json:"bitrateError"`
}

// Load reads the model at path.
func Load(
	path string,
) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ladder model: %w", err)
	}

	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w: %w", path, ErrModel, err)
	}

	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &m, nil
}

// Save writes the model to path.
func (m *Model) Save(
	path string,
) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode ladder model: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write ladder model: %w", err)
	}

	return nil
}

// validate checks that the model is one this version can predict with.
func (m *Model) validate() error {
	points := len(m.Heights) * len(m.CRFs)

	switch {
	case m.SchemaVersion != SchemaVersion:
		return fmt.Errorf("%w: schema version %d, expected %d", ErrModel, m.SchemaVersion, SchemaVersion)
	case points == 0 || len(m.Designs) == 0:
		return fmt.Errorf("%w: no grid or no design", ErrModel)
	}

	for _, d := range m.Designs {
		if len(d.Rate) != points || len(d.VMAF) != points {
			return fmt.Errorf("%w: a design has %d rate and %d VMAF models for %d points", ErrModel, len(d.Rate), len(d.VMAF), points)
		}

		for i := range points {
			if want := d.regressors(); len(d.Rate[i]) != want || len(d.VMAF[i]) != want {
				return fmt.Errorf("%w: a model of %d anchors and %d features has not %d coefficients", ErrModel, len(d.Anchors), len(d.Features), want)
			}
		}
	}

	return nil
}

// Covers tells whether the model predicts the encodes of codec at preset
// of a source of the given height.
func (m *Model) Covers(
	codec, preset string,
	sourceHeight int,
) bool {
	return m.Codec == codec && m.Preset == preset && m.SourceHeight == sourceHeight
}

// regressors is the number of coefficients of each model of the design.
func (d Design) regressors() int {
	return 1 + 2*len(d.Anchors) + len(d.Features)
}

// Design returns the design with the fewest anchors whose validation
// stayed within tolerance VMAF on average (MeanVMAFError), the best
// validated of them when several have as few. When none did, or with a
// tolerance of zero, it returns the design whose validation was the best,
// and the design with the most anchors when none was validated. A design
// reading features is left out unless usable accepts every one of them:
// it needs those features of the title (nil accepts none).
func (m *Model) Design(
	tolerance float64,
	usable func(feature string) bool,
) Design {
	var (
		best   Design
		found  bool
		within bool
	)

	better := func(d Design) bool {
		switch {
		case !found:
			return true
		case d.Validation == nil:
			return best.Validation == nil && len(d.Anchors) > len(best.Anchors)
		case best.Validation == nil:
			return true
		}

		fits := tolerance > 0 && d.Validation.MeanVMAFError <= tolerance

		switch {
		case fits != within:
			return fits
		case fits && len(d.Anchors) != len(best.Anchors):
			return len(d.Anchors) < len(best.Anchors)
		default:
			return d.Validation.MeanVMAFError < best.Validation.MeanVMAFError
		}
	}

	for _, d := range m.Designs {
		if !d.reads(usable) {
			continue
		}

		if better(d) {
			best, found = d, true
			within = d.Validation != nil && tolerance > 0 && d.Validation.MeanVMAFError <= tolerance
		}
	}

	return best
}

// reads tells whether the design can be fed: it reads no feature, or
// features usable accepts, all of them.
func (d Design) reads(
	usable func(feature string) bool,
) bool {
	if len(d.Features) == 0 {
		return true
	}

	return usable != nil && !slices.ContainsFunc(d.Features, func(name string) bool { return !usable(name) })
}

// Instant returns the design predicting from features of the title alone,
// without any anchor: of those reading only features usable accepts (nil
// accepts them all), the one reading the most. A model holds several,
// each for what can be read of a title (see TrainOptions.FeatureSets), and
// a build reads all it can: which design it takes does not depend on how
// the validation of a small corpus happened to fall.
func (m *Model) Instant(
	usable func(feature string) bool,
) (Design, bool) {
	if usable == nil {
		usable = func(string) bool { return true }
	}

	var (
		best  Design
		found bool
	)

	for _, d := range m.Designs {
		if len(d.Anchors) > 0 || len(d.Features) == 0 || !d.reads(usable) {
			continue
		}

		if !found || len(d.Features) > len(best.Features) {
			best, found = d, true
		}
	}

	return best, found
}

// Points returns the points of the grid, heights then CRFs: the order of
// the models of a design.
func (m *Model) Points() []Point {
	points := make([]Point, 0, len(m.Heights)*len(m.CRFs))

	for _, h := range m.Heights {
		for _, crf := range m.CRFs {
			points = append(points, Point{Height: h, CRF: crf})
		}
	}

	return points
}

// Input is what a design predicts the grid of a title from.
type Input struct {
	// Anchors are the measurements of the anchors of the design, in any
	// order.
	Anchors []Measurement
	// Features are the features of the title (the means of those of its
	// segments), by name.
	Features map[string]float64
	// Segments are the features of each segment of the title, by name. A
	// design reading features alone predicts every segment and averages
	// them, as the bitrate and the VMAF of a title are the means of those
	// of its segments: the means of the features would give the geometric
	// mean of the bitrates, under the real one by as much as the segments
	// differ (13% on average on 39 real titles). Without Segments, and for
	// a design also reading anchors, measured on the whole title, Features
	// are read.
	Segments map[string][]float64
	// Width gives the width of the encodes at a height; nil keeps the
	// aspect ratio of the first anchor.
	Width func(height int) int
}

// Predict returns the grid of a title as design d predicts it from in:
// every point of the grid, the anchors as they were measured.
func (m *Model) Predict(
	d Design,
	in Input,
) ([]Measurement, error) {
	xs, err := inputs(d, in)
	if err != nil {
		return nil, err
	}

	points := m.Points()
	out := make([]Measurement, len(points))

	for i, p := range points {
		width := in.widthAt(p.Height)
		if width <= 0 {
			return nil, fmt.Errorf("%w: no width at %dp", ErrAnchors, p.Height)
		}

		out[i] = Measurement{Point: p, Width: width}

		for _, x := range xs {
			out[i].Bitrate += math.Exp(dot(d.Rate[i], x)) * float64(width*p.Height) / float64(len(xs))
			out[i].VMAF += unlink(dot(d.VMAF[i], x)) / float64(len(xs))
		}

		if v := d.Validation; v != nil {
			out[i].VMAFError, out[i].BitrateError = v.VMAFError[i], v.BitrateError[i]
		}

		if k := slices.IndexFunc(in.Anchors, func(a Measurement) bool { return a.Point == p }); k >= 0 {
			out[i] = in.Anchors[k]
			out[i].VMAFError, out[i].BitrateError = 0, 0
		}
	}

	return out, nil
}

// inputs are the regressor vectors design d predicts from, whose
// predictions are averaged: one per segment of in for a design reading
// features alone (see Input.Segments), one for the title otherwise.
func inputs(
	d Design,
	in Input,
) ([][]float64, error) {
	count := in.segments(d)
	if count == 0 {
		x, err := regressors(d, in)

		return [][]float64{x}, err
	}

	out := make([][]float64, count)

	for s := range out {
		segment := Input{Features: make(map[string]float64, len(d.Features))}

		for _, name := range d.Features {
			if values := in.Segments[name]; s < len(values) {
				segment.Features[name] = values[s]
			}
		}

		x, err := regressors(d, segment)
		if err != nil {
			return nil, err
		}

		out[s] = x
	}

	return out, nil
}

// segments is the number of segments design d predicts one by one from
// in: none when it reads anchors or no features, or when in holds no
// segments.
func (in Input) segments(
	d Design,
) int {
	if len(d.Anchors) > 0 || len(d.Features) == 0 {
		return 0
	}

	return len(in.Segments[d.Features[0]])
}

// regressors is the regressor vector of a title: a constant, then the
// logarithm of the bitrate per pixel and the linked VMAF of each anchor
// of the design, in its order, then each of its features.
func regressors(
	d Design,
	in Input,
) ([]float64, error) {
	if len(in.Anchors) != len(d.Anchors) {
		return nil, fmt.Errorf("%w: %d measurements for %d anchors", ErrAnchors, len(in.Anchors), len(d.Anchors))
	}

	x := make([]float64, 1, d.regressors())
	x[0] = 1

	for _, p := range d.Anchors {
		k := slices.IndexFunc(in.Anchors, func(a Measurement) bool { return a.Point == p })
		if k < 0 || in.Anchors[k].Bitrate <= 0 || in.Anchors[k].Width <= 0 {
			return nil, fmt.Errorf("%w: no usable measurement at %dp CRF %g", ErrAnchors, p.Height, p.CRF)
		}

		a := in.Anchors[k]
		x = append(x, math.Log(a.Bitrate/float64(a.Width*a.Height)), link(a.VMAF))
	}

	for _, name := range d.Features {
		value, ok := in.Features[name]
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%w: %s", ErrFeatures, name)
		}

		x = append(x, value)
	}

	return x, nil
}

// widthAt is the width of the encodes at height: what Width says, that of
// the anchor measured there, or the first anchor's aspect ratio, rounded
// to even; zero when nothing tells.
func (in Input) widthAt(
	height int,
) int {
	if in.Width != nil {
		return in.Width(height)
	}

	for _, a := range in.Anchors {
		if a.Height == height {
			return a.Width
		}
	}

	if len(in.Anchors) == 0 {
		return 0
	}

	a := in.Anchors[0]

	return int(math.Round(float64(height)*float64(a.Width)/float64(a.Height)/2)) * 2
}

// linkMargin keeps VMAF away from 0 and 100, where the logistic scale has
// no value.
const linkMargin = 0.5

// link puts a VMAF on an unbounded scale, the logarithm of v/(100−v): a
// linear model then never predicts above 100, and a point of VMAF weighs
// more near the top, where it costs the most bitrate.
func link(
	v float64,
) float64 {
	v = min(max(v, linkMargin), 100-linkMargin)

	return math.Log(v / (100 - v))
}

// unlink is the inverse of link.
func unlink(
	z float64,
) float64 {
	return 100 / (1 + math.Exp(-z))
}

func dot(
	a, b []float64,
) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}

	return s
}
