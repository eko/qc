package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/nvidia"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// gpuUse says which GPU flags a command has.
type gpuUse int

const (
	// gpuDecode: frames are decoded (NVDEC).
	gpuDecode gpuUse = 1 << iota
	// gpuEncode: ladders are encoded (NVENC).
	gpuEncode
	// gpuVMAF: VMAF is measured (CUDA feature extraction).
	gpuVMAF
)

// gpuFlagHelp describes --gpu: what it turns on, and what stays on the CPU.
const gpuFlagHelp = "use an NVIDIA GPU where available: NVDEC decoding (--hwaccel cuda), NVENC ladders (--encoder nvenc), " +
	"CUDA VMAF when the model allows it (--vmaf-backend auto); fails early without a usable GPU"

// addGPUFlags registers --gpu and the fine-grained flags of what the
// command uses. Their defaults are empty, so that --gpu only fills what was
// not set explicitly.
func addGPUFlags(
	cmd *cobra.Command,
	use gpuUse,
) {
	flags := cmd.Flags()
	flags.Bool("gpu", false, gpuFlagHelp)

	if use&gpuDecode != 0 {
		flags.String("hwaccel", "", "hardware decoding: none (default), cuda (NVDEC, identical frames) or cuda-scale (NVDEC + GPU scaling, not bit-exact)")
	}

	if use&gpuEncode != 0 {
		flags.String("encoder", "", "ladder encoders: cpu (default: x264, x265, SVT-AV1) or nvenc (h264_nvenc, hevc_nvenc, av1_nvenc)")
	}

	if use&gpuVMAF != 0 {
		flags.String("vmaf-backend", "", "VMAF feature extraction: cpu (default), cuda (fails for models without CUDA features, e.g. VMAF v1) or auto")
	}
}

// withGPU expands --gpu into the fine-grained settings left unset: NVDEC
// decoding that keeps frames identical, NVENC for ladders, and CUDA VMAF
// when the model can run on it (VMAF v1 cannot: auto keeps it on the CPU).
func (c Config) withGPU() Config {
	if !c.GPU.GPU {
		return c
	}

	if c.GPU.HWAccel == "" {
		c.GPU.HWAccel = string(decode.HWAccelCUDA)
	}

	if c.GPU.Encoder == "" && len(c.ladderCodecs()) > 0 {
		c.GPU.Encoder = string(encode.HardwareNVENC)
	}

	if c.GPU.VMAFBackend == "" {
		c.GPU.VMAFBackend = string(vmaf.BackendAuto)
	}

	return c
}

// validate rejects unknown GPU settings.
func (c GPUConfig) validate() error {
	if _, err := decode.ParseHWAccel(c.HWAccel); err != nil {
		return fmt.Errorf("invalid --hwaccel: %w", err)
	}

	if _, err := encode.ParseHardware(c.Encoder); err != nil {
		return fmt.Errorf("invalid --encoder: %w", err)
	}

	if _, err := vmaf.ParseBackend(c.VMAFBackend); err != nil {
		return fmt.Errorf("invalid --vmaf-backend: %w", err)
	}

	return nil
}

// gpuSettings are the parsed GPU settings of a validated configuration.
type gpuSettings struct {
	hwaccel decode.HWAccel
	encoder encode.Hardware
	backend vmaf.Backend
	// nvencCodecs are the codecs of the ladders encoded with NVENC.
	nvencCodecs []string
	// bitDepth is the bit depth of the ladder encodes.
	bitDepth int
}

// gpuSettingsOf parses the GPU settings of config (validated already).
func gpuSettingsOf(
	config Config,
) gpuSettings {
	s := gpuSettings{bitDepth: config.Ladder.EncodeBitDepth}

	s.hwaccel, _ = decode.ParseHWAccel(config.GPU.HWAccel)
	s.encoder, _ = encode.ParseHardware(config.GPU.Encoder)
	s.backend, _ = vmaf.ParseBackend(config.GPU.VMAFBackend)

	if s.encoder == encode.HardwareNVENC {
		s.nvencCodecs = config.ladderCodecs()
	}

	return s
}

// checkGPU verifies, before any work, that ffmpeg and the machine can do
// what the GPU settings ask (see nvidia.Check), and that libvmaf's CUDA
// state initialises when CUDA VMAF is explicitly required. It probes
// nothing when no GPU is asked for. Its errors name the flag to change.
func checkGPU(
	ctx context.Context,
	tools ToolsConfig,
	s gpuSettings,
) error {
	if s.backend == vmaf.BackendCUDA {
		if err := libvmaf.InitCUDA(); err != nil {
			return fmt.Errorf("--vmaf-backend cuda: %w", err)
		}
	}

	err := nvidia.Check(ctx, tools.FFmpeg, nvidia.Requirements{
		HWAccel:  s.hwaccel,
		Codecs:   s.nvencCodecs,
		BitDepth: s.bitDepth,
	})

	var checkErr *nvidia.CheckError

	switch {
	case err == nil:
		return nil
	case !errors.As(err, &checkErr):
		return fmt.Errorf("gpu: %w", err)
	case checkErr.Part == nvidia.PartDecoding:
		return fmt.Errorf("--hwaccel %s: %w", s.hwaccel, checkErr.Err)
	case checkErr.Part == nvidia.PartEncoding:
		return fmt.Errorf("--encoder nvenc: %w", checkErr.Err)
	}

	return fmt.Errorf("gpu: %w", checkErr.Err)
}

// gpuTitle appends what runs on the GPU to the dashboard title, e.g.
// "run · GPU: NVDEC, NVENC, CUDA VMAF (auto)".
func gpuTitle(
	name string,
	s gpuSettings,
) string {
	var parts []string

	switch s.hwaccel {
	case decode.HWAccelCUDA:
		parts = append(parts, "NVDEC")
	case decode.HWAccelCUDAScale:
		parts = append(parts, "NVDEC+scale")
	}

	if len(s.nvencCodecs) > 0 {
		parts = append(parts, "NVENC")
	}

	switch s.backend {
	case vmaf.BackendCUDA:
		parts = append(parts, "CUDA VMAF")
	case vmaf.BackendAuto:
		parts = append(parts, "CUDA VMAF (auto)")
	}

	if len(parts) == 0 {
		return name
	}

	return name + "  ·  GPU: " + strings.Join(parts, ", ")
}

// wizardProbeTimeout bounds the GPU detection of the wizard: a slow driver
// must not delay the form.
const wizardProbeTimeout = 5 * time.Second
