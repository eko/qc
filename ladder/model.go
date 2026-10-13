package ladder

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
)

// ProbingModel measures a few encodes of the digest, one per resolution at
// most (the anchors of a design of Options.Curves), and lets the model
// complete the curve of every resolution: the bitrate and the VMAF of one
// encoder at the other CRFs follow closely from them (see package
// predict). Probes then are mostly predictions (Probe.Predicted), and the
// stages after them are unchanged: the level of the probes is corrected,
// and every rung is verified by a real encode.
//
// A model that does not cover the build (another codec or preset, a source
// of another height, other candidate resolutions) leaves fixed probing,
// and ProbingReport.Model says why.
const ProbingModel Probing = "model"

// ProbingPredicted measures nothing: the model predicts every curve from
// the features of the digest (package features: its frame analysis, a
// statistics pass of x264 at its fastest and the loss of scaling), with
// the design of Options.Curves that reads no anchor. A ladder then costs
// the digest and its analysis, a few seconds; what it is worth is what the
// validation of that design says (ModelProbing.MeanVMAFError), which
// depends on how many titles the model was trained on. Verification still
// runs unless Options.SkipVerify.
//
// A model without such a design (its corpus held no features), or an
// engine without a Featurer, leaves fixed probing.
const ProbingPredicted Probing = "predicted"

// ErrNoEncode is returned by a build asked to predict without an encode
// (Options.NoEncode) when no model can: it does not fall back on probes.
var ErrNoEncode = errors.New("cannot predict without an encode")

// ModelProbing reports how a model completed the probes.
type ModelProbing struct {
	// Anchors are the encodes measured; the other probes are predicted.
	Anchors []predict.Point `json:"anchors,omitempty"`
	// Features is the number of features of the digest the prediction
	// read; zero when it read the anchors alone.
	Features int `json:"features,omitempty"`
	// Titles is the number of titles the model was trained on.
	Titles int `json:"titles,omitempty"`
	// MeanVMAFError and WorstVMAFError are the errors of the design on the
	// titles it was validated on (see predict.Validation); zero when it
	// was not validated.
	MeanVMAFError  float64 `json:"meanVmafError,omitempty"`
	WorstVMAFError float64 `json:"worstVmafError,omitempty"`
	// Fallback explains why the model was not used, and fixed probing was.
	Fallback string `json:"fallback,omitempty"`
}

// probeModel measures the anchors of the design the tolerance asks of the
// model covering the build and returns the grid it predicts from them, or
// the probes of fixed probing when no model covers the build.
func (b *build) probeModel(
	ctx context.Context,
) ([]Probe, error) {
	model, reason := b.coveringModel()

	var design predict.Design
	if model != nil {
		design, reason = b.design(model)
	}

	if reason != "" {
		// Asked not to encode, a build does not answer with the encodes
		// of fixed probing, which take minutes: it says what is missing.
		if b.opts.NoEncode {
			return nil, fmt.Errorf("ladder: %w: %s", ErrNoEncode, reason)
		}

		probes, err := b.probeFixed(ctx)
		b.probing.Model = &ModelProbing{Fallback: reason}

		return probes, err
	}

	b.probing = ProbingReport{Mode: b.opts.Probing, Rounds: 1, Model: modelProbing(model, design)}
	in := predict.Input{Width: func(height int) int {
		width, _ := b.geometry(height)

		return width
	}}

	if len(design.Features) > 0 {
		b.reportProgress(Progress{Stage: StageProbe})

		set, err := b.engine.featurer.Extract(ctx, features.Spec{
			Path: b.digest, Width: b.video.Width, Height: b.video.Height, Rate: b.video.AvgFrameRate.Float(),
			Frames: b.gop(), Heights: b.heights(), Inspection: b.digestReport, WorkDir: b.workDir,
			Names: design.Features, Quantisers: b.quantisers(),
		})
		if err != nil {
			return nil, fmt.Errorf("ladder: %w", err)
		}

		in.Features, in.Segments = set.Mean(), set.Values
	}

	jobs := make([]Probe, len(design.Anchors))
	for i, a := range design.Anchors {
		width, height := b.geometry(a.Height)
		jobs[i] = Probe{Width: width, Height: height, CRF: a.CRF}
	}

	b.resetProgress()

	anchors, err := b.measureAll(ctx, jobs, StageProbe, len(jobs), b.opts.ProbePreset)
	if err != nil {
		return nil, err
	}

	return predictProbes(model, design, in, anchors)
}

// design returns the design of model the build predicts with: the one
// reading no anchor for ProbingPredicted, the one the tolerance asks for
// otherwise, among those the engine can feed (designs reading features
// need a Featurer). It returns why when there is none.
func (b *build) design(
	model *predict.Model,
) (predict.Design, string) {
	if b.engine.featurer == nil {
		if b.opts.Probing == ProbingPredicted {
			return predict.Design{}, "this engine cannot extract the features the model reads"
		}

		return model.Design(b.opts.Tolerance, nil), ""
	}

	quantisers := b.quantisers() != nil

	if b.opts.Probing != ProbingPredicted {
		return model.Design(b.opts.Tolerance, features.Usable(true, quantisers)), ""
	}

	design, ok := model.Instant(features.Usable(!b.opts.NoEncode, quantisers))

	switch {
	case ok:
		return design, ""
	case b.opts.NoEncode:
		if _, any := model.Instant(nil); any {
			return predict.Design{}, "the model has no design predicting without an encode: train it again with this version of qc"
		}
	}

	return predict.Design{}, "the model cannot predict from the analysis alone: its corpus holds no features"
}

// quantisers returns the quantisers of the source's own encode, one per
// frame of the digest, when the digest came with them (see
// sourceQuantisers); nil otherwise.
func (b *build) quantisers() []float64 {
	if b.digest == "" {
		return nil
	}

	quantisers, err := encode.ReadQuantisers(b.digest + quantisersSuffix)
	if err != nil || len(quantisers) == 0 {
		return nil
	}

	return quantisers
}

// coveringModel returns the first model of Options.Curves that can
// complete the probes of the build, or why the first of them (or none)
// cannot.
func (b *build) coveringModel() (*predict.Model, string) {
	reason := "no ladder model was given"

	for i, model := range b.opts.Curves {
		gap := b.modelGap(model)
		if gap == "" {
			return model, ""
		}

		if i == 0 {
			reason = gap
		}
	}

	return nil, reason
}

// modelProbing describes the design of model a build probes with.
func modelProbing(
	model *predict.Model,
	design predict.Design,
) *ModelProbing {
	report := &ModelProbing{Anchors: design.Anchors, Features: len(design.Features), Titles: model.Titles}
	if v := design.Validation; v != nil {
		report.MeanVMAFError, report.WorstVMAFError = v.MeanVMAFError, v.WorstVMAFError
	}

	return report
}

// modelGap tells why model cannot complete the probes of the build, or
// returns "" when it can: it must be the model of the encoder setting and
// of the source's height, and of exactly the build's resolutions.
func (b *build) modelGap(
	model *predict.Model,
) string {
	switch {
	case !model.Covers(b.codec.Name, b.opts.ProbePreset, b.video.Height):
		return fmt.Sprintf("the model is for %s %s on %dp sources, not %s %s on %dp",
			model.Codec, model.Preset, model.SourceHeight, b.codec.Name, b.opts.ProbePreset, b.video.Height)
	case b.opts.BitDepth > 8 || b.grain > 0 || !b.signal.IsZero():
		return "the model was trained on 8-bit SDR encodes without film grain synthesis"
	case !slices.Equal(b.heights(), model.Heights):
		return fmt.Sprintf("the model is for the resolutions %v, not %v", model.Heights, b.heights())
	}

	return ""
}

// predictProbes returns the probes of the grid of model: the anchors as
// measured, the other points as design predicts them from the anchors and
// from the features of in, with the error of the design there as their
// half-width.
func predictProbes(
	model *predict.Model,
	design predict.Design,
	in predict.Input,
	anchors []Probe,
) ([]Probe, error) {
	in.Anchors = make([]predict.Measurement, len(anchors))
	for i, a := range anchors {
		in.Anchors[i] = predict.Measurement{
			Point: predict.Point{Height: a.Height, CRF: a.CRF}, Width: a.Width,
			Bitrate: float64(a.Bitrate), VMAF: a.VMAF,
		}
	}

	grid, err := model.Predict(design, in)
	if err != nil {
		return nil, fmt.Errorf("ladder: %w", err)
	}

	probes := make([]Probe, 0, len(grid))

	for _, g := range grid {
		if i := slices.IndexFunc(anchors, func(a Probe) bool { return a.Height == g.Height && a.CRF == g.CRF }); i >= 0 {
			probes = append(probes, anchors[i])

			continue
		}

		probes = append(probes, Probe{
			Width: g.Width, Height: g.Height, CRF: g.CRF,
			Bitrate: int64(g.Bitrate), VMAF: g.VMAF, HalfWidth: g.VMAFError, Predicted: true,
		})
	}

	return probes, nil
}

// ProbeEncodes is the number of probes of the result that were encoded
// and measured: all of them, but those a model predicted.
func (r *Result) ProbeEncodes() int {
	encoded := 0

	for _, p := range r.Probes {
		if !p.Predicted {
			encoded++
		}
	}

	return encoded
}

// Note describes in a few words what the model did for the probes of a
// result with predicted probes ("35 more predicted by a model of 3
// titles, within 0.9 VMAF on average"), or why it was not used.
func (m *ModelProbing) Note(
	predicted int,
) string {
	if m.Fallback != "" {
		return "ladder model not used: " + m.Fallback
	}

	note := fmt.Sprintf("%d more predicted by a model of %d titles", predicted, m.Titles)
	if len(m.Anchors) == 0 && m.Features > 0 {
		note = fmt.Sprintf("%d predicted from the analysis of the digest by a model of %d titles, nothing measured", predicted, m.Titles)
	}

	if m.MeanVMAFError > 0 {
		note += fmt.Sprintf(", within %.1f VMAF on average on titles it had not seen", m.MeanVMAFError)
	}

	return note
}
