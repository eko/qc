package libvmaf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/vmaf"
)

func TestNewBackendErrors(
	t *testing.T,
) {
	model, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer model.Close()

	models := []*Model{model}
	cfg := vmaf.ScorerConfig{Width: 64, Height: 64}

	t.Run("unresolved auto", func(t *testing.T) {
		cfg := cfg
		cfg.Backend = vmaf.BackendAuto

		s, err := New(models, cfg)
		require.ErrorIs(t, err, vmaf.ErrBackend)
		assert.Nil(t, s)
	})

	t.Run("cuda without the cuda build", func(t *testing.T) {
		if CUDABuilt {
			t.Skip("see cuda_test.go")
		}

		cfg := cfg
		cfg.Backend = vmaf.BackendCUDA

		s, err := New(models, cfg)
		require.ErrorIs(t, err, vmaf.ErrCUDAUnavailable)
		assert.Nil(t, s)
		require.ErrorIs(t, InitCUDA(), vmaf.ErrCUDAUnavailable)
	})
}

func TestModelError(
	t *testing.T,
) {
	s := &Scorer{}
	cause := libvmafError(-22)

	require.ErrorIs(t, s.modelError(vmaf.BackendCUDA, cause), vmaf.ErrCUDAModel)
	require.ErrorIs(t, s.modelError(vmaf.BackendCUDA, cause), cause)
	require.NotErrorIs(t, s.modelError(vmaf.BackendCPU, cause), vmaf.ErrCUDAModel)
	require.ErrorIs(t, s.modelError(vmaf.BackendCPU, cause), vmaf.ErrModelFeatures)
	require.ErrorIs(t, s.modelError(vmaf.BackendCPU, cause), cause)
}
