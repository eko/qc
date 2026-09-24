package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/nvidia"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// gpuFFmpeg stands for an ffmpeg with NVIDIA support on a machine with a
// GPU: listings gain cuda, the NVENC encoders and scale_cuda, the device
// opens (only h264_nvenc is listed, even when the real ffmpeg has NVENC),
// and commands run on the real ffmpeg with the NVIDIA options
// translated (hardware decoding dropped, NVENC mapped to x264 with -cq as
// -crf). failOn makes command lines containing it fail.
func gpuFFmpeg(
	t *testing.T,
	failOn string,
) string {
	t.Helper()

	testutil.RequireFFmpeg(t)

	fail := ""
	if failOn != "" {
		fail = `case "$*" in *"` + failOn + `"*) echo "failing on ` + failOn + `" >&2; exit 1;; esac`
	}

	return testutil.FakeFFmpeg(t, fail+`
case "$*" in
*-hwaccels*) printf 'Hardware acceleration methods:\ncuda\n'; exit 0;;
*-encoders*) "$REAL_FFMPEG" "$@" | grep -v _nvenc; printf ' V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)\n'; exit 0;;
*-filters*) "$REAL_FFMPEG" "$@"; printf ' .. scale_cuda        V->V       GPU accelerated video resizer\n'; exit 0;;
*-init_hw_device*) exit 0;;
esac
skip=0
for a; do
  shift
  if [ "$skip" = 1 ]; then skip=0; continue; fi
  case "$a" in
    -hwaccel|-hwaccel_output_format|-tune|-rc|-b:v|-rc-lookahead|-no-scenecut|-forced-idr) skip=1; continue;;
    -cq) a=-crf;;
    h264_nvenc) a=libx264;;
    p5) a=fast;;
  esac
  set -- "$@" "$a"
done
exec "$REAL_FFMPEG" "$@"`)
}

func TestWithGPU(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		config Config
		want   Config
	}{
		{name: "off", config: Config{Ladder: LadderConfig{Codec: "h264"}}, want: Config{Ladder: LadderConfig{Codec: "h264"}}},
		{
			name:   "everything on a ladder",
			config: Config{Ladder: LadderConfig{Codec: "av1"}, GPU: GPUConfig{GPU: true}},
			want:   Config{Ladder: LadderConfig{Codec: "av1"}, GPU: GPUConfig{GPU: true, HWAccel: "cuda", Encoder: "nvenc", VMAFBackend: "auto"}},
		},
		{
			name:   "no ladder, no encoder",
			config: Config{GPU: GPUConfig{GPU: true}},
			want:   Config{GPU: GPUConfig{GPU: true, HWAccel: "cuda", VMAFBackend: "auto"}},
		},
		{
			name:   "explicit settings win",
			config: Config{Run: RunConfig{Codecs: []string{"hevc"}}, GPU: GPUConfig{GPU: true, HWAccel: "cuda-scale", Encoder: "cpu", VMAFBackend: "cpu"}},
			want:   Config{Run: RunConfig{Codecs: []string{"hevc"}}, GPU: GPUConfig{GPU: true, HWAccel: "cuda-scale", Encoder: "cpu", VMAFBackend: "cpu"}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.config.withGPU())
		})
	}
}

func TestValidateGPU(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "defaults", config: Config{}},
		{name: "all set", config: Config{GPU: GPUConfig{HWAccel: "cuda-scale", Encoder: "nvenc", VMAFBackend: "auto"}}},
		{name: "hwaccel", config: Config{GPU: GPUConfig{HWAccel: "qsv"}}, wantErr: "invalid --hwaccel"},
		{name: "encoder", config: Config{GPU: GPUConfig{Encoder: "amf"}}, wantErr: "invalid --encoder"},
		{name: "vmaf backend", config: Config{GPU: GPUConfig{VMAFBackend: "rocm"}}, wantErr: "invalid --vmaf-backend"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.GPU.validate()
			if testCase.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

func TestGPUSettings(
	t *testing.T,
) {
	s := gpuSettingsOf(Config{
		Ladder: LadderConfig{Codec: "av1", EncodeBitDepth: 10},
		Run:    RunConfig{Codecs: []string{"h264", "hevc"}},
		GPU:    GPUConfig{HWAccel: "cuda", Encoder: "nvenc", VMAFBackend: "cuda"},
	})

	assert.Equal(t, gpuSettings{
		hwaccel:     decode.HWAccelCUDA,
		encoder:     encode.HardwareNVENC,
		backend:     vmaf.BackendCUDA,
		nvencCodecs: []string{"av1", "h264", "hevc"},
		bitDepth:    10,
	}, s)
}

func TestGPUTitle(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		config Config
		want   string
	}{
		{name: "cpu", config: Config{Ladder: LadderConfig{Codec: "h264"}}, want: "run"},
		{name: "gpu", config: Config{Ladder: LadderConfig{Codec: "h264"}, GPU: GPUConfig{GPU: true}}.withGPU(), want: "run  ·  GPU: NVDEC, NVENC, CUDA VMAF (auto)"},
		{name: "gpu scaling and strict vmaf", config: Config{GPU: GPUConfig{HWAccel: "cuda-scale", VMAFBackend: "cuda"}}, want: "run  ·  GPU: NVDEC+scale, CUDA VMAF"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, gpuTitle("run", gpuSettingsOf(testCase.config)))
		})
	}
}

func TestCheckGPU(
	t *testing.T,
) {
	cpu := testutil.FakeFFmpeg(t, `case "$*" in
*-hwaccels*) printf 'Hardware acceleration methods:\nvideotoolbox\n';;
*-encoders*|*-filters*) printf '';;
*) exit 1;;
esac`)

	testCases := []struct {
		name    string
		config  Config
		wantErr error
		wantMsg string
	}{
		{name: "nothing asked", config: Config{Tools: ToolsConfig{FFmpeg: "no-such-ffmpeg"}}},
		{name: "nvdec and nvenc", config: Config{Tools: ToolsConfig{FFmpeg: gpuFFmpeg(t, "")}, Ladder: LadderConfig{Codec: "h264"}, GPU: GPUConfig{HWAccel: "cuda", Encoder: "nvenc"}}},
		{name: "nvenc without ladders probes nothing", config: Config{Tools: ToolsConfig{FFmpeg: "no-such-ffmpeg"}, GPU: GPUConfig{Encoder: "nvenc"}}},
		{name: "probe fails", config: Config{Tools: ToolsConfig{FFmpeg: "no-such-ffmpeg"}, GPU: GPUConfig{HWAccel: "cuda"}}, wantErr: ffexec.ErrNotFound, wantMsg: "gpu: nvidia"},
		{name: "ffmpeg without cuda", config: Config{Tools: ToolsConfig{FFmpeg: cpu}, GPU: GPUConfig{HWAccel: "cuda"}}, wantErr: nvidia.ErrMissing, wantMsg: "--hwaccel cuda"},
		{name: "ffmpeg without av1_nvenc", config: Config{Tools: ToolsConfig{FFmpeg: gpuFFmpeg(t, "")}, Ladder: LadderConfig{Codec: "av1"}, GPU: GPUConfig{Encoder: "nvenc"}}, wantErr: nvidia.ErrMissing, wantMsg: "--encoder nvenc"},
		{name: "no device", config: Config{Tools: ToolsConfig{FFmpeg: gpuFFmpeg(t, "-init_hw_device")}, GPU: GPUConfig{HWAccel: "cuda-scale"}}, wantErr: nvidia.ErrDevice},
		{name: "encoder unusable", config: Config{Tools: ToolsConfig{FFmpeg: gpuFFmpeg(t, "testsrc2")}, Ladder: LadderConfig{Codec: "h264"}, GPU: GPUConfig{Encoder: "nvenc"}}, wantErr: nvidia.ErrEncoder},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := checkGPU(t.Context(), testCase.config.Tools, gpuSettingsOf(testCase.config))

			if testCase.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, testCase.wantErr)
			assert.Contains(t, err.Error(), testCase.wantMsg)
		})
	}
}

func TestCheckGPUStrictVMAF(
	t *testing.T,
) {
	if libvmaf.CUDABuilt {
		t.Skip("depends on the GPU of the machine")
	}

	require.ErrorIs(t, checkGPU(t.Context(), ToolsConfig{}, gpuSettings{backend: vmaf.BackendCUDA}), vmaf.ErrCUDAUnavailable)
}

// TestRunOnGPU runs qc run --gpu end to end against the GPU fake: the
// ladder is built with NVENC settings, its commands decode and encode on
// the GPU, and the VMAF report says it stayed on the CPU (VMAF v1) and how
// frames were decoded.
func TestRunOnGPU(
	t *testing.T,
) {
	source, encoded := clips(t)
	fake := gpuFFmpeg(t, "")

	code, stdout, stderr := execute(t, testEnv, "run", encoded, "-r", source, "--gpu", "--ffmpeg", fake,
		"--codecs", "h264", "--heights", "180", "--max-rungs", "2", "--no-verify", "--skip-analysis", "--metrics=", "-f", "json")
	require.Equal(t, 0, code, stderr)

	var report pipeline.Report
	require.NoError(t, json.Unmarshal([]byte(stdout), &report))

	require.Len(t, report.Ladders, 1)
	l := report.Ladders[0]
	assert.Equal(t, "h264_nvenc", l.Codec.Encoder)
	assert.Equal(t, encode.HardwareNVENC, l.Codec.Hardware)

	for _, r := range l.Rungs {
		assert.True(t, strings.HasPrefix(r.Command, "ffmpeg -hwaccel cuda -i "), r.Command)
	}

	require.NotNil(t, report.Comparison)
	assert.Equal(t, "cuda", report.Comparison.VMAF.HWAccel)
	assert.Empty(t, report.Comparison.VMAF.Backend)
	assert.Contains(t, report.Comparison.VMAF.BackendNote, "no CUDA extractor")
}

func TestGPUFlagsPerCommand(
	t *testing.T,
) {
	testCases := []struct {
		command string
		flags   []string
		absent  []string
	}{
		{command: "analyze", flags: []string{"gpu", "hwaccel"}, absent: []string{"encoder", "vmaf-backend"}},
		{command: "vmaf", flags: []string{"gpu", "hwaccel", "vmaf-backend"}, absent: []string{"encoder"}},
		{command: "ladder", flags: []string{"gpu", "hwaccel", "encoder", "vmaf-backend"}},
		{command: "run", flags: []string{"gpu", "hwaccel", "encoder", "vmaf-backend"}},
	}

	root := newRootCommand(testEnv)

	for _, testCase := range testCases {
		t.Run(testCase.command, func(t *testing.T) {
			cmd, _, err := root.Find([]string{testCase.command})
			require.NoError(t, err)

			for _, name := range testCase.flags {
				assert.NotNil(t, cmd.Flags().Lookup(name), name)
			}

			for _, name := range testCase.absent {
				assert.Nil(t, cmd.Flags().Lookup(name), name)
			}
		})
	}
}

func TestWizardOffersGPU(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		available  func(ctx context.Context, ffmpeg string) bool
		env        map[string]string
		want       bool
		wantFFmpeg string
		wantErr    string
	}{
		{name: "no detection"},
		{
			name:       "usable gpu, ffmpeg from the environment",
			available:  func(context.Context, string) bool { return true },
			env:        map[string]string{"QC_FFMPEG": "/opt/ffmpeg"},
			want:       true,
			wantFFmpeg: "/opt/ffmpeg",
		},
		{
			name:       "no gpu, ffmpeg from PATH",
			available:  func(context.Context, string) bool { return false },
			wantFFmpeg: "ffmpeg",
		},
		{
			name:      "invalid environment",
			available: func(context.Context, string) bool { return true },
			env:       map[string]string{"QC_LOG_LEVEL": "chatty"},
			wantErr:   "invalid log level",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			for key, value := range testCase.env {
				t.Setenv(key, value)
			}

			var gotFFmpeg string

			env := testEnv
			if testCase.available != nil {
				env.gpuAvailable = func(ctx context.Context, ffmpeg string) bool {
					gotFFmpeg = ffmpeg

					deadline, ok := ctx.Deadline()
					assert.True(t, ok, "the detection is bounded")
					assert.WithinDuration(t, time.Now().Add(wizardProbeTimeout), deadline, time.Second)

					return testCase.available(ctx, ffmpeg)
				}
			}

			root := newRootCommand(env)
			root.SetContext(t.Context())
			require.NoError(t, root.ParseFlags(nil))

			got, err := wizardOffersGPU(root, env)
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.wantFFmpeg, gotFFmpeg)
		})
	}
}

func TestWizardGPUArgs(
	t *testing.T,
) {
	answers := wizardAnswers{Source: "source.mov", Actions: []string{actionAnalysis}}
	assert.NotContains(t, answers.runArgs(), "--gpu")

	answers.GPU = true
	assert.Contains(t, answers.runArgs(), "--gpu")
}

// TestGPUCommandErrors checks that GPU problems stop a command before any
// work, with the reason.
func TestGPUCommandErrors(
	t *testing.T,
) {
	source, _ := clips(t)
	noCUDA := testutil.FakeFFmpeg(t, `printf 'Hardware acceleration methods:\n'`)

	testCases := []struct {
		name string
		args []string
		want string
	}{
		{name: "invalid mode", args: []string{"analyze", source, "--hwaccel", "vaapi"}, want: "invalid --hwaccel"},
		{name: "ffmpeg without nvdec", args: []string{"analyze", source, "--gpu", "--ffmpeg", noCUDA}, want: "--hwaccel cuda: ffmpeg lacks NVIDIA support"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			code, _, stderr := execute(t, testEnv, testCase.args...)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, testCase.want)
		})
	}
}

// TestExplicitCPUSettings maps explicit "cpu" settings to the CPU zero
// values the libraries expect.
func TestExplicitCPUSettings(
	t *testing.T,
) {
	config := Config{Ladder: LadderConfig{Codec: "h264"}, GPU: GPUConfig{Encoder: "cpu", VMAFBackend: "cpu"}}

	assert.Equal(t, encode.HardwareCPU, ladderOptions(config).Encoder)
	assert.Equal(t, vmaf.BackendCPU, ladderOptions(config).Backend)
	assert.Equal(t, vmaf.BackendCPU, qualityOptions(config).Backend)
	assert.Equal(t, vmaf.BackendAuto, qualityOptions(Config{GPU: GPUConfig{VMAFBackend: "AUTO"}}).Backend)
}
