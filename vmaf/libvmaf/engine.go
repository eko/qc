package libvmaf

import "github.com/eko/qc/vmaf"

// Engine scores with libvmaf: it resolves backends against this build and
// the machine, and loads models for scorers. It implements quality.Engine.
type Engine struct{}

// NewEngine returns the libvmaf Engine.
func NewEngine() Engine {
	return Engine{}
}

// ResolveBackend picks the backend scoring specs (see vmaf.ResolveBackend),
// initialising the CUDA device when the models can run on it.
func (Engine) ResolveBackend(
	requested vmaf.Backend,
	specs []vmaf.ModelSpec,
) (vmaf.BackendChoice, error) {
	return vmaf.ResolveBackend(requested, specs, InitCUDA)
}

// LoadModels loads the models of specs, in order. On failure, the models
// already loaded are released.
func (Engine) LoadModels(
	specs []vmaf.ModelSpec,
) (vmaf.Models, error) {
	set := &ModelSet{models: make([]*Model, 0, len(specs))}

	for _, spec := range specs {
		model, err := LoadModel(spec)
		if err != nil {
			set.Close()

			return nil, err
		}

		set.models = append(set.models, model)
	}

	return set, nil
}

// ModelSet is a set of loaded models whose scorers score them all, in
// order. It implements vmaf.Models.
type ModelSet struct {
	models []*Model
}

var _ vmaf.Models = (*ModelSet)(nil)

// NewScorer returns a Scorer measuring every model of the set and cfg.
func (m *ModelSet) NewScorer(
	cfg vmaf.ScorerConfig,
) (vmaf.Scorer, error) {
	scorer, err := New(m.models, cfg)
	if err != nil {
		// A nil *Scorer in the interface would not compare equal to nil.
		return nil, err
	}

	return scorer, nil
}

// Close releases the models.
func (m *ModelSet) Close() {
	for _, model := range m.models {
		model.Close()
	}

	m.models = nil
}
