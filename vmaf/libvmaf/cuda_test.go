//go:build cuda

package libvmaf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/vmaf"
)

// cudaTolerance is the largest per-frame VMAF difference accepted between
// the CUDA and CPU integer extractors. libvmaf's CUDA kernels port the
// integer (fixed-point) CPU code, so scores are expected to agree to a few
// thousandths; the GPU validation kit measures the real figure.
const cudaTolerance = 0.01

// requireGPU skips the test when no NVIDIA GPU is usable: the cuda build is
// compiled and vetted without one (e.g. in the CUDA devel image).
func requireGPU(
	t *testing.T,
) {
	t.Helper()

	if err := InitCUDA(); err != nil {
		t.Skipf("no usable NVIDIA GPU: %v", err)
	}
}

// scoreOn scores three textured pairs on backend and returns the scores and
// features.
func scoreOn(
	t *testing.T,
	backend vmaf.Backend,
	bitDepth int,
	extractors []vmaf.Extractor,
) vmaf.Scores {
	t.Helper()

	model, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer model.Close()

	const w, h = 256, 144

	pool := frame.NewPool(w, h, frame.PoolOptions{Chroma: true, HighBitDepth: bitDepth > 8})

	scorer, err := New([]*Model{model}, vmaf.ScorerConfig{
		Extractors: extractors,
		Width:      w, Height: h, BitDepth: bitDepth, Threads: 2, Backend: backend,
	})
	require.NoError(t, err)
	defer scorer.Close()

	for i := range 3 {
		ref, dist := pool.Get(), pool.Get()
		texture(ref, false)
		texture(dist, i%2 == 1)
		require.NoError(t, scorer.Push(ref, dist))
		ref.Release()
		dist.Release()
	}

	out, err := scorer.Collect()
	require.NoError(t, err)

	return out
}

func TestCUDAMatchesCPU(
	t *testing.T,
) {
	requireGPU(t)

	testCases := []struct {
		name       string
		bitDepth   int
		extractors []vmaf.Extractor
	}{
		{name: "8 bits", bitDepth: 8},
		{name: "10 bits", bitDepth: 10},
		{name: "CPU extractors next to CUDA features", bitDepth: 8, extractors: []vmaf.Extractor{vmaf.ExtractorPSNR, vmaf.ExtractorCAMBI}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cpu := scoreOn(t, vmaf.BackendCPU, testCase.bitDepth, testCase.extractors)
			gpu := scoreOn(t, vmaf.BackendCUDA, testCase.bitDepth, testCase.extractors)

			require.Len(t, gpu.VMAF[0], len(cpu.VMAF[0]))

			for i := range cpu.VMAF[0] {
				assert.InDelta(t, cpu.VMAF[0][i], gpu.VMAF[0][i], cudaTolerance, "frame %d", i)
			}

			// Extractors run on the CPU in both cases: identical values.
			assert.Equal(t, cpu.Features, gpu.Features)
		})
	}
}

func TestCUDARejectsV1Models(
	t *testing.T,
) {
	requireGPU(t)

	model, err := LoadModel(vmaf.ModelSpec{Source: "vmaf_v1.0.16_3d0h"})
	require.NoError(t, err)
	defer model.Close()

	s, err := New([]*Model{model}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 8, Backend: vmaf.BackendCUDA})
	require.ErrorIs(t, err, vmaf.ErrCUDAModel)
	assert.Nil(t, s)
}

func TestResolveBackendOnGPU(
	t *testing.T,
) {
	requireGPU(t)

	choice, err := NewEngine().ResolveBackend(vmaf.BackendAuto, []vmaf.ModelSpec{{Name: builtinModel, Source: builtinModel}})
	require.NoError(t, err)
	assert.Equal(t, vmaf.BackendChoice{Backend: vmaf.BackendCUDA}, choice)
}
