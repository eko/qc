package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/quality"
)

// TestRenderComparisonGPU shows what ran on the GPU next to the score.
func TestRenderComparisonGPU(
	t *testing.T,
) {
	cmp := sampleComparison(quality.ModeSampled)
	cmp.VMAF.HWAccel, cmp.VMAF.Backend = "cuda", "cuda"

	assert.Contains(t, renderComparison(t, cmp, 100, ""), "gpu NVDEC decoding (cuda) · VMAF features on CUDA")
	assert.NotContains(t, renderComparison(t, sampleComparison(quality.ModeSampled), 100, ""), "gpu ")
}
