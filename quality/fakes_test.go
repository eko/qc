package quality

import (
	"context"
	"sync/atomic"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/vmaf"
)

// modelOffset separates the fake scores of the models of one scorer: model
// m scores a pair with its reference index plus m×modelOffset.
const modelOffset = 1000

// fakeFeatures are the outputs the fake scorer reads back per extractor.
var fakeFeatures = map[vmaf.Extractor][]string{
	vmaf.ExtractorCAMBI: {vmaf.FeatureCAMBI},
	vmaf.ExtractorPSNR:  {vmaf.FeaturePSNRY, vmaf.FeaturePSNRCb, vmaf.FeaturePSNRCr},
}

// fakeEngine is a VMAF engine scoring every pair with the index of its
// reference frame, so that tests see which frames each clip kept without
// libvmaf. Its errors make the corresponding step fail.
type fakeEngine struct {
	choice     vmaf.BackendChoice
	resolveErr error
	loadErr    error
	scorerErr  error
	pushErr    error
	collectErr error
	// open counts the model sets and scorers not closed yet.
	open atomic.Int64
	// requested records the backend asked for.
	requested vmaf.Backend
}

func (e *fakeEngine) ResolveBackend(
	requested vmaf.Backend,
	_ []vmaf.ModelSpec,
) (vmaf.BackendChoice, error) {
	e.requested = requested

	return e.choice, e.resolveErr
}

func (e *fakeEngine) LoadModels(
	specs []vmaf.ModelSpec,
) (vmaf.Models, error) {
	if e.loadErr != nil {
		return nil, e.loadErr
	}

	e.open.Add(1)

	return &fakeModels{engine: e, count: len(specs)}, nil
}

type fakeModels struct {
	engine *fakeEngine
	count  int
}

func (m *fakeModels) NewScorer(
	cfg vmaf.ScorerConfig,
) (vmaf.Scorer, error) {
	if m.engine.scorerErr != nil {
		return nil, m.engine.scorerErr
	}

	m.engine.open.Add(1)

	return &fakeScorer{engine: m.engine, models: m.count, extractors: cfg.Extractors}, nil
}

func (m *fakeModels) Close() {
	m.engine.open.Add(-1)
}

type fakeScorer struct {
	engine     *fakeEngine
	models     int
	extractors []vmaf.Extractor
	indices    []int
}

func (s *fakeScorer) Push(
	ref, _ *frame.Frame,
) error {
	if s.engine.pushErr != nil {
		return s.engine.pushErr
	}

	s.indices = append(s.indices, ref.Index)

	return nil
}

func (s *fakeScorer) Collect() (vmaf.Scores, error) {
	if s.engine.collectErr != nil {
		return vmaf.Scores{}, s.engine.collectErr
	}

	out := vmaf.Scores{VMAF: make([][]float64, s.models), Features: map[string][]float64{}}

	for m := range out.VMAF {
		out.VMAF[m] = s.values(float64(m * modelOffset))
	}

	for _, extractor := range s.extractors {
		for _, name := range fakeFeatures[extractor] {
			out.Features[name] = s.values(0)
		}
	}

	return out, nil
}

// values are the pushed reference indices plus offset.
func (s *fakeScorer) values(
	offset float64,
) []float64 {
	out := make([]float64, len(s.indices))
	for i, index := range s.indices {
		out[i] = float64(index) + offset
	}

	return out
}

func (s *fakeScorer) Close() {
	s.engine.open.Add(-1)
}

// selectingSource decodes n blank frames and honours the frame selection
// of requests like ffmpeg: frames are numbered from FirstIndex, only the
// selected ones are output, at most MaxFrames of them.
func selectingSource(
	n int,
) decode.Source {
	return decodeFunc(func(ctx context.Context, req decode.Request, fn func(*frame.Frame) error) error {
		output := 0

		for i := 0; req.FirstIndex+i < n; i++ {
			if req.MaxFrames > 0 && output == req.MaxFrames {
				break
			}

			if !selected(req.Select, i) {
				continue
			}

			f := req.Pool.Get()
			f.Index = req.FirstIndex + i
			output++

			if err := fn(f); err != nil {
				return err
			}
		}

		return ctx.Err()
	})
}

// selected reports whether frame i of a decode is in selection (nil selects
// every frame).
func selected(
	selection [][2]int,
	i int,
) bool {
	if selection == nil {
		return true
	}

	for _, r := range selection {
		if i >= r[0] && i < r[1] {
			return true
		}
	}

	return false
}
