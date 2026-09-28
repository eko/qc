package quality

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// TestMeasureBackend checks how the VMAF backend is chosen and reported.
// The default model (VMAF v1) has no CUDA features: auto falls back to the
// CPU with a note, cuda fails before decoding anything. The GPU path itself
// is covered by vmaf/cuda_test.go and the GPU validation kit.
func TestMeasureBackend(
	t *testing.T,
) {
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.28}))

	testCases := []struct {
		name        string
		opts        Options
		wantErr     error
		wantNote    bool
		wantBackend string
	}{
		{name: "cpu", opts: Options{Model: testModel}},
		{name: "auto with VMAF v1", opts: Options{Backend: vmaf.BackendAuto, Devices: []string{vmaf.DevicePhone}}, wantNote: true},
		{name: "auto with a 4K device pass", opts: Options{Model: testModel, Backend: vmaf.BackendAuto, Devices: []string{vmaf.Device4K}}, wantNote: true},
		{name: "cuda with VMAF v1", opts: Options{Backend: vmaf.BackendCUDA}, wantErr: vmaf.ErrCUDAModel},
		{name: "unknown backend", opts: Options{Model: testModel, Backend: "metal"}, wantErr: vmaf.ErrBackend},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ref, ref, testCase.opts)
			require.ErrorIs(t, err, testCase.wantErr)

			if testCase.wantErr != nil {
				return
			}

			assert.Equal(t, testCase.wantBackend, res.Backend)
			assert.Equal(t, testCase.wantNote, res.BackendNote != "", res.BackendNote)
			assert.Empty(t, res.HWAccel)
		})
	}
}

// TestMeasureCUDAWithoutBuild asks for CUDA with a CUDA-capable model from
// a binary built without the cuda tag.
func TestMeasureCUDAWithoutBuild(
	t *testing.T,
) {
	if libvmaf.CUDABuilt {
		t.Skip("cuda build: see vmaf/cuda_test.go")
	}

	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.28}))

	_, err := measureWithin(t, decodedMeter(), ref, ref, Options{Model: testModel, Backend: vmaf.BackendCUDA})
	require.ErrorIs(t, err, vmaf.ErrCUDAUnavailable)
}

func TestHWAccelReported(
	t *testing.T,
) {
	h264 := fakeInput("in.mp4", 10, 5)
	h264.Video.Codec, h264.Video.PixelFormat = "h264", "yuv420p"
	prores := fakeInput("in.mov", 10, 5)
	prores.Video.Codec, prores.Video.PixelFormat = "prores", "yuv422p10le"

	testCases := []struct {
		name     string
		decoder  decode.Source
		decoders int
		inputs   []Input
		want     string
	}{
		{name: "cpu decoder", decoder: decode.NewFFmpeg("ffmpeg", 0), want: ""},
		{name: "nvdec", decoder: decode.NewFFmpeg("ffmpeg", 0, decode.WithHWAccel(decode.HWAccelCUDA)), want: "cuda"},
		{name: "decoder without modes", decoder: decodeFunc(nil), want: ""},
		{
			name: "segments decoded with videotoolbox", decoder: decode.NewFFmpeg("ffmpeg", 0, decode.WithHWAccel(decode.HWAccelVideoToolbox)),
			decoders: 12, inputs: []Input{prores, h264}, want: "videotoolbox",
		},
		{
			name: "explicit videotoolbox, codecs it does not decode", decoder: decode.NewFFmpeg("ffmpeg", 0, decode.WithHWAccel(decode.HWAccelVideoToolbox)),
			decoders: 12, inputs: []Input{prores}, want: "videotoolbox",
		},
		{
			name: "automatic mode without segments", decoder: decode.NewFFmpeg("ffmpeg", 0, decode.WithHWAccel(decode.HWAccelAuto)),
			decoders: 1, inputs: []Input{h264}, want: "",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, hwAccel(testCase.decoder, testCase.decoders, testCase.inputs...))
		})
	}
}

func TestBackendName(
	t *testing.T,
) {
	assert.Empty(t, backendName(vmaf.BackendCPU))
	assert.Equal(t, "cuda", backendName(vmaf.BackendCUDA))
}
