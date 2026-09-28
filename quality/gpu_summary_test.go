package quality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGPUSummary(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		result Result
		want   string
	}{
		{name: "cpu", result: Result{}, want: ""},
		{name: "nvdec", result: Result{HWAccel: "cuda"}, want: "NVDEC decoding (cuda)"},
		{name: "videotoolbox", result: Result{HWAccel: "videotoolbox"}, want: "VideoToolbox decoding"},
		{name: "cuda features", result: Result{Backend: "cuda"}, want: "VMAF features on CUDA"},
		{
			name:   "nvdec and a cuda fallback",
			result: Result{HWAccel: "cuda-scale", BackendNote: "no CUDA extractor"},
			want:   "NVDEC decoding (cuda-scale) · VMAF on the CPU (CUDA fallback, see backendNote)",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.result.GPUSummary())
		})
	}
}
