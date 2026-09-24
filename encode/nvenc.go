package encode

import (
	"errors"
	"fmt"
	"strings"
)

// Hardware selects the implementation of a codec's encoder.
type Hardware string

// Encoder implementations.
const (
	// HardwareCPU encodes with x264, x265 and SVT-AV1 (the zero value).
	HardwareCPU Hardware = ""
	// HardwareNVENC encodes with NVIDIA's hardware encoders (h264_nvenc,
	// hevc_nvenc, av1_nvenc). AV1 needs an Ada Lovelace or newer GPU.
	HardwareNVENC Hardware = "nvenc"
)

// hardwareCPUName is how HardwareCPU is written on the command line.
const hardwareCPUName = "cpu"

// ErrUnknownHardware is returned for an unknown encoder implementation
// (Hardware).
var ErrUnknownHardware = errors.New("unknown encoder implementation")

// ParseHardware reads an encoder implementation: cpu (or empty) or nvenc.
func ParseHardware(
	s string,
) (Hardware, error) {
	switch h := Hardware(strings.ToLower(strings.TrimSpace(s))); h {
	case HardwareCPU, hardwareCPUName:
		return HardwareCPU, nil
	case HardwareNVENC:
		return h, nil
	}

	return HardwareCPU, fmt.Errorf("%w %q (supported: cpu, nvenc)", ErrUnknownHardware, s)
}

// String returns the implementation name, "cpu" for the zero value.
func (h Hardware) String() string {
	if h == HardwareCPU {
		return hardwareCPUName
	}

	return string(h)
}

// NVENC settings shared by every codec (see docs/gpu.md for the sources).
const (
	// nvencPreset balances speed and quality: p5 ("slow, good quality") is
	// still several hundred 1080p frames per second, and NVIDIA recommends
	// p4–p7 for latency-tolerant transcoding.
	nvencPreset = "p5"
	// nvencTune is the high-quality tuning NVIDIA recommends for OTT
	// streaming and archiving. uhq exists for HEVC and AV1 on Ada and
	// newer only, so it is left to --preset experiments.
	nvencTune = "hq"
	// nvencLookahead enables rate-control lookahead (frames): it improves
	// bit distribution in constant-quality mode. With lookahead NVENC may
	// insert I-frames at scene cuts, which -no-scenecut disables.
	nvencLookahead = "20"
	// nvencCRFStep is the granularity used for -cq: NVENC takes fractions
	// (8.8 fixed point), integers keep every driver on known ground.
	nvencCRFStep = 1.0
	// nvencMinCQ bounds the scale away from 0, which means "automatic".
	nvencMinCQ = 10
)

// Pixel formats NVENC is fed: it takes planar 8-bit 4:2:0 but only
// semi-planar 10-bit (P010), not yuv420p10le.
const (
	nvencPixelFormat8  = "yuv420p"
	nvencPixelFormat10 = "p010le"
)

// nvencCodecs lists the codecs encoded with NVENC (read-only, see codecs). Probe CQs are defaults
// derived from the CPU sets (NVENC's CQ is a QP-like scale close to x264's
// CRF, and av1_nvenc's 0–63 scale is SVT-AV1's), to be calibrated on a GPU
// with bench/gpu (make gpu-validate): together they must span VMAF ~97 to
// ~40 like the CPU sets.
var nvencCodecs = map[string]Codec{
	"h264": {
		Name: "h264", Encoder: "h264_nvenc", Hardware: HardwareNVENC, DefaultPreset: nvencPreset,
		MinCRF: nvencMinCQ, MaxCRF: 51, ProbeCRFs: []float64{21, 28, 35}, CRFStep: nvencCRFStep,
		family: nvenc{},
	},
	"hevc": {
		Name: "hevc", Encoder: "hevc_nvenc", Hardware: HardwareNVENC, DefaultPreset: nvencPreset,
		MinCRF: nvencMinCQ, MaxCRF: 51, ProbeCRFs: []float64{23, 30, 37}, CRFStep: nvencCRFStep,
		family: nvenc{},
	},
	"av1": {
		Name: "av1", Encoder: "av1_nvenc", Hardware: HardwareNVENC, DefaultPreset: nvencPreset,
		MinCRF: nvencMinCQ, MaxCRF: 63, ProbeCRFs: []float64{28, 38, 48}, CRFStep: nvencCRFStep,
		family: nvenc{},
	},
}

// nvenc drives NVIDIA's hardware encoders. It supports no Feature: film
// grain synthesis is SVT-AV1's, and joining chunks without re-encoding is
// only verified for the CPU encoders.
type nvenc struct{}

// qualityArgs select NVENC's constant-quality mode: VBR rate control with a
// target quality and no average bitrate. -b:v 0 matters: ffmpeg's NVENC
// default is 2 Mb/s, which older releases kept as an average target next to
// -cq (9.x drops it in CQ mode, together with the VBV buffer size: only
// -maxrate caps a CQ encode there).
func (nvenc) qualityArgs(
	cq string,
) []string {
	return []string{"-tune", nvencTune, "-rc", "vbr", "-cq", cq, "-b:v", "0"}
}

// gopArgs fix the keyframe interval. NVENC ignores -keyint_min: its GOP
// length is also its IDR period.
func (nvenc) gopArgs(
	gop string,
) []string {
	return []string{"-g", gop}
}

// privateArgs are the NVENC-private options. With a fixed GOP every GOP
// starts with an IDR (NVENC's IDR period is its GOP length), lookahead
// scene-cut I-frames are disabled and forced keyframes are IDRs, as ABR
// segmenting requires. -strict_gop is not used: it only smooths the rate
// per GOP towards a bitrate target, which constant quality does not have.
func (nvenc) privateArgs(
	p Params,
) []string {
	args := []string{"-rc-lookahead", nvencLookahead}
	if p.GOP > 0 {
		args = append(args, "-no-scenecut", "1", "-forced-idr", "1")
	}

	return args
}

// pixelFormat is the 4:2:0 format NVENC is fed at bitDepth. 10-bit input
// makes hevc_nvenc and av1_nvenc encode Main10; h264_nvenc then needs High
// 10 support (Blackwell GPUs, Video Codec SDK 13).
func (nvenc) pixelFormat(
	bitDepth int,
) string {
	if bitDepth > 8 {
		return nvencPixelFormat10
	}

	return nvencPixelFormat8
}

// inputArgs decode the source with NVDEC too (see Codec.InputArgs).
func (nvenc) inputArgs() []string {
	return []string{"-hwaccel", "cuda"}
}

func (nvenc) qualityOption() string {
	return "cq"
}

func (nvenc) supports(
	Feature,
) bool {
	return false
}

// InputArgs are the input options of the commands rendered for the codec:
// NVENC commands decode the source with NVDEC too (frames are downloaded
// before the CPU scaler, so the encoder sees exactly what the ladder
// measured; ffmpeg falls back to software decoding for codecs NVDEC does
// not decode). Encodes run by the ladder read the raw digest and need no
// decoder.
func (c Codec) InputArgs() []string {
	return c.impl().inputArgs()
}

// Step returns the granularity of the quality scale, half a step when the
// codec does not say.
func (c Codec) Step() float64 {
	if c.CRFStep > 0 {
		return c.CRFStep
	}

	return crfStepX26x
}

// QualityOption is the ffmpeg option of the constant-quality value: -crf,
// or -cq for NVENC.
func (c Codec) QualityOption() string {
	return c.impl().qualityOption()
}
