package libvmaf

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/vmaf"
)

func TestEngineResolveBackend(
	t *testing.T,
) {
	v0 := vmaf.ModelSpec{Name: builtinModel, Source: builtinModel}

	choice, err := NewEngine().ResolveBackend(vmaf.BackendCPU, []vmaf.ModelSpec{v0})
	require.NoError(t, err)
	assert.Equal(t, vmaf.BackendCPU, choice.Backend)

	if CUDABuilt {
		t.Skip("depends on the GPU of the machine: see cuda_test.go")
	}

	choice, err = NewEngine().ResolveBackend(vmaf.BackendAuto, []vmaf.ModelSpec{v0})
	require.NoError(t, err)
	assert.Equal(t, vmaf.BackendCPU, choice.Backend)
	assert.Contains(t, choice.Reason, vmaf.ErrCUDAUnavailable.Error(), "the device check runs on this build")
}

func TestEngineLoadModels(
	t *testing.T,
) {
	v0 := vmaf.ModelSpec{Name: builtinModel, Source: builtinModel}
	missing := vmaf.ModelSpec{Name: "missing", Source: filepath.Join(t.TempDir(), "missing.json"), FromPath: true}

	testCases := []struct {
		name       string
		specs      []vmaf.ModelSpec
		cfg        vmaf.ScorerConfig
		wantErr    bool
		wantScorer error
	}{
		{name: "one scorer per clip", specs: []vmaf.ModelSpec{v0, v0}, cfg: vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 8}},
		{name: "a model fails to load", specs: []vmaf.ModelSpec{v0, missing}, wantErr: true},
		{name: "scorer refused", specs: nil, wantScorer: ErrNoModel},
		{
			name:       "unknown extractor",
			specs:      []vmaf.ModelSpec{v0},
			cfg:        vmaf.ScorerConfig{Extractors: []vmaf.Extractor{"vif"}, Width: 64, Height: 64},
			wantScorer: vmaf.ErrUnknownExtractor,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			models, err := NewEngine().LoadModels(testCase.specs)
			if testCase.wantErr {
				require.Error(t, err)
				assert.Nil(t, models, "no typed nil")

				return
			}

			require.NoError(t, err)
			defer models.Close()

			scorer, err := models.NewScorer(testCase.cfg)
			if testCase.wantScorer != nil {
				require.ErrorIs(t, err, testCase.wantScorer)
				assert.Nil(t, scorer, "no typed nil")

				return
			}

			require.NoError(t, err)
			defer scorer.Close()

			pool := frame.NewPool(64, 64, frame.PoolOptions{Chroma: true})
			ref, dist := pool.Get(), pool.Get()
			texture(ref, false)
			texture(dist, true)
			require.NoError(t, scorer.Push(ref, dist))
			ref.Release()
			dist.Release()

			out, err := scorer.Collect()
			require.NoError(t, err)
			require.Len(t, out.VMAF, len(testCase.specs), "one series per model")
			assert.Equal(t, out.VMAF[0], out.VMAF[1])
		})
	}
}
