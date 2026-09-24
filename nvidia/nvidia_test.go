package nvidia

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/testutil"
)

// gpuFFmpeg is the listing of an ffmpeg built with NVIDIA support, trimmed
// to a few entries of each list.
const gpuFFmpeg = `case "$*" in
*-hwaccels*) printf 'Hardware acceleration methods:\nvdpau\ncuda\n\n';;
*-encoders*) printf 'Encoders:\n V..... = Video\n ------\n V....D libx264              libx264 H.264 (codec h264)\n V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)\n V....D hevc_nvenc           NVIDIA NVENC hevc encoder (codec hevc)\n';;
*-filters*) printf 'Filters:\n  T.. = Timeline support\n  N = Dynamic number and/or type of input/output\n  ------\n .. hwdownload        V->V       Download a hardware frame to a normal frame\n .. scale_cuda        V->V       GPU accelerated video resizer\n .. buffersink        V->|       Buffer video frames\n';;
*-init_hw_device*) exit 0;;
*h264_nvenc*) exit 0;;
*p010le*hevc_nvenc*) echo "10 bit encode not supported" >&2; exit 1;;
*hevc_nvenc*) exit 0;;
*) echo "unexpected: $*" >&2; exit 1;;
esac`

// cpuFFmpeg is an ffmpeg without NVIDIA support on a machine without GPU.
const cpuFFmpeg = `case "$*" in
*-hwaccels*) printf 'Hardware acceleration methods:\nvideotoolbox\n';;
*-encoders*) printf 'Encoders:\n ------\n V....D libx264              libx264 H.264 (codec h264)\n';;
*-filters*) printf 'Filters:\n  ------\n .. scale             V->V       Scale the input video size\n';;
*-init_hw_device*) echo "Device creation failed: -542398533." >&2; exit 1;;
*) echo "Unknown encoder" >&2; exit 1;;
esac`

func TestProbe(
	t *testing.T,
) {
	gpu := testutil.FakeFFmpeg(t, gpuFFmpeg)
	cpu := testutil.FakeFFmpeg(t, cpuFFmpeg)

	caps, err := Probe(t.Context(), gpu)
	require.NoError(t, err)
	assert.Equal(t, Capabilities{
		HWAccels: []string{"vdpau", "cuda"},
		Encoders: []string{"libx264", "h264_nvenc", "hevc_nvenc"},
		Filters:  []string{"hwdownload", "scale_cuda", "buffersink"},
	}, caps)

	none, err := Probe(t.Context(), cpu)
	require.NoError(t, err)

	testCases := []struct {
		name     string
		caps     Capabilities
		mode     decode.HWAccel
		encoders []string
		wantErr  error
	}{
		{name: "nvdec and nvenc", caps: caps, mode: decode.HWAccelCUDAScale, encoders: []string{"h264_nvenc", "hevc_nvenc"}},
		{name: "no av1_nvenc", caps: caps, mode: decode.HWAccelCUDA, encoders: []string{"av1_nvenc"}, wantErr: ErrMissing},
		{name: "no cuda hwaccel", caps: none, mode: decode.HWAccelCUDA, wantErr: ErrMissing},
		{name: "no decoding asked", caps: none},
		{name: "no scale_cuda", caps: Capabilities{HWAccels: []string{"cuda"}, Filters: []string{"hwdownload"}}, mode: decode.HWAccelCUDAScale, wantErr: ErrMissing},
		{name: "decoding without gpu scaling", caps: Capabilities{HWAccels: []string{"cuda"}}, mode: decode.HWAccelCUDA},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.caps.RequireDecoding(testCase.mode)
			if err == nil {
				err = testCase.caps.RequireEncoders(testCase.encoders)
			}

			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestProbeFailure(
	t *testing.T,
) {
	_, err := Probe(t.Context(), testutil.FakeFFmpeg(t, "exit 1"))
	require.ErrorContains(t, err, "ffmpeg -hwaccels")
}

func TestDeviceAndEncoderChecks(
	t *testing.T,
) {
	gpu := testutil.FakeFFmpeg(t, gpuFFmpeg)
	cpu := testutil.FakeFFmpeg(t, cpuFFmpeg)

	require.NoError(t, checkDevice(t.Context(), gpu))
	require.ErrorIs(t, checkDevice(t.Context(), cpu), ErrDevice)
	require.ErrorContains(t, checkDevice(t.Context(), cpu), "--gpus all")

	testCases := []struct {
		name     string
		bin      string
		encoder  string
		bitDepth int
		wantErr  error
	}{
		{name: "h264 8-bit", bin: gpu, encoder: "h264_nvenc", bitDepth: 8},
		{name: "hevc 8-bit", bin: gpu, encoder: "hevc_nvenc", bitDepth: 8},
		{name: "hevc 10-bit unsupported by the gpu", bin: gpu, encoder: "hevc_nvenc", bitDepth: 10, wantErr: ErrEncoder},
		{name: "no gpu", bin: cpu, encoder: "av1_nvenc", bitDepth: 8, wantErr: ErrEncoder},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.ErrorIs(t, checkEncoder(t.Context(), testCase.bin, testCase.encoder, testCase.bitDepth), testCase.wantErr)
		})
	}
}

// TestProbeRealFFmpeg parses the listings of the installed ffmpeg: whatever
// its build, it has the software scaler and x264 (a qc requirement).
func TestProbeRealFFmpeg(
	t *testing.T,
) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	caps, err := Probe(t.Context(), "ffmpeg")
	require.NoError(t, err)
	assert.Contains(t, caps.Encoders, "libx264")
	assert.Contains(t, caps.Filters, "scale")
	assert.Contains(t, caps.Filters, "hwdownload")
	assert.NotContains(t, caps.Encoders, "------")
}

func TestCheck(
	t *testing.T,
) {
	gpu := testutil.FakeFFmpeg(t, gpuFFmpeg)
	cpu := testutil.FakeFFmpeg(t, cpuFFmpeg)
	noDevice := testutil.FakeFFmpeg(t, `case "$*" in *-init_hw_device*) exit 1;; esac
`+gpuFFmpeg)

	testCases := []struct {
		name     string
		ffmpeg   string
		req      Requirements
		wantPart Part
		wantErr  error
		wantMsg  string
	}{
		{name: "nothing asked", ffmpeg: "no-such-ffmpeg"},
		{name: "nvdec and nvenc", ffmpeg: gpu, req: Requirements{HWAccel: decode.HWAccelCUDAScale, Codecs: []string{"h264", "HEVC"}}},
		{name: "encoding only", ffmpeg: gpu, req: Requirements{Codecs: []string{"h264"}, BitDepth: 8}},
		{name: "unknown codec", ffmpeg: gpu, req: Requirements{Codecs: []string{"vp9"}}, wantPart: PartEncoding, wantMsg: "vp9"},
		{name: "ffmpeg missing", ffmpeg: "no-such-ffmpeg", req: Requirements{HWAccel: decode.HWAccelCUDA}, wantMsg: "ffmpeg -hwaccels"},
		{name: "no nvdec", ffmpeg: cpu, req: Requirements{HWAccel: decode.HWAccelCUDA}, wantPart: PartDecoding, wantErr: ErrMissing},
		{name: "encoder not built in", ffmpeg: gpu, req: Requirements{Codecs: []string{"av1"}}, wantPart: PartEncoding, wantErr: ErrMissing},
		{name: "no device", ffmpeg: noDevice, req: Requirements{HWAccel: decode.HWAccelCUDA}, wantPart: PartDevice, wantErr: ErrDevice},
		{name: "encoder unusable at 10 bits", ffmpeg: gpu, req: Requirements{Codecs: []string{"hevc"}, BitDepth: 10}, wantPart: PartEncoding, wantErr: ErrEncoder},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := Check(t.Context(), testCase.ffmpeg, testCase.req)
			if testCase.wantPart == "" && testCase.wantMsg == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.wantMsg)

			var checkErr *CheckError
			if testCase.wantPart == "" {
				assert.NotErrorAs(t, err, &checkErr)

				return
			}

			require.ErrorAs(t, err, &checkErr)
			assert.Equal(t, testCase.wantPart, checkErr.Part)
			assert.True(t, strings.HasPrefix(err.Error(), string(testCase.wantPart)+": "), err.Error())

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			}
		})
	}
}

func TestAvailable(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		script string
		want   bool
	}{
		{name: "usable gpu", script: gpuFFmpeg, want: true},
		{name: "no device", script: `case "$*" in *-init_hw_device*) exit 1;; esac
` + gpuFFmpeg},
		{name: "no nvdec", script: cpuFFmpeg},
		{name: "no nvenc", script: `case "$*" in *-hwaccels*) printf 'Hardware acceleration methods:\ncuda\n';; esac`},
		{name: "no ffmpeg", script: "exit 1"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Available(t.Context(), testutil.FakeFFmpeg(t, testCase.script)))
		})
	}
}
