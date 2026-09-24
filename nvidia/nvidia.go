// Package nvidia checks, before any long job, that an ffmpeg binary and the
// machine can do the NVIDIA GPU work a run asks for: NVDEC decoding
// (decode.WithHWAccel), and NVENC encoding (ladder.Options.Encoder set to
// encode.HardwareNVENC).
//
// The two fail differently, which is why this package exists: a hardware
// decode that fails falls back to the CPU on its own, while an NVENC ladder
// has no fallback and fails at its first probe encode, possibly after the
// analysis and VMAF stages of a run. Check turns both into an error in a
// second, with the reason (ffmpeg built without NVIDIA support, no usable
// device, an encoder this GPU lacks) and how to fix it.
//
// CUDA VMAF is checked by libvmaf itself: see libvmaf.InitCUDA in package
// vmaf/libvmaf. This package needs no cgo.
package nvidia

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/ffexec"
)

// Names of the ffmpeg components the GPU paths use.
const (
	// hwaccelCUDA is ffmpeg's NVDEC hardware decoding method.
	hwaccelCUDA = "cuda"
	// filterScaleCUDA scales and converts frames on the GPU.
	filterScaleCUDA = "scale_cuda"
	// filterHWDownload copies GPU frames to system memory.
	filterHWDownload = "hwdownload"
)

var (
	// ErrMissing is returned when ffmpeg was built without a component.
	ErrMissing = errors.New("ffmpeg lacks NVIDIA support")
	// ErrDevice is returned when the CUDA device cannot be opened.
	ErrDevice = errors.New("no usable NVIDIA GPU")
	// ErrEncoder is returned when an NVENC encoder fails on this GPU.
	ErrEncoder = errors.New("NVENC encoder unusable")
)

// buildHint tells how to get an ffmpeg with NVIDIA support.
const buildHint = "use the qc CUDA image (Dockerfile.cuda) or an ffmpeg configured with --enable-ffnvcodec --enable-nvdec --enable-nvenc --enable-cuda-llvm"

// Capabilities are the components an ffmpeg binary was built with: its
// hardware decoding methods, encoders and filters. Being built in does not
// mean usable: the GPU, its driver and its generation decide (see Check).
type Capabilities struct {
	HWAccels []string
	Encoders []string
	Filters  []string
}

// Probe lists the hardware decoding methods, encoders and filters of the
// ffmpeg binary.
func Probe(
	ctx context.Context,
	ffmpeg string,
) (Capabilities, error) {
	var caps Capabilities

	lists := []struct {
		flag  string
		parse func([]byte) []string
		into  *[]string
	}{
		{flag: "-hwaccels", parse: parseHWAccels, into: &caps.HWAccels},
		{flag: "-encoders", parse: parseEncoders, into: &caps.Encoders},
		{flag: "-filters", parse: parseFilters, into: &caps.Filters},
	}

	for _, list := range lists {
		out, err := ffexec.Output(ctx, ffmpeg, []string{"-hide_banner", list.flag})
		if err != nil {
			return Capabilities{}, fmt.Errorf("nvidia: ffmpeg %s: %w", list.flag, err)
		}

		*list.into = list.parse(out)
	}

	return caps, nil
}

// RequireDecoding checks that ffmpeg can decode in mode: NVDEC for
// decode.HWAccelCUDA, plus the GPU scaler for decode.HWAccelCUDAScale.
// decode.HWAccelNone requires nothing.
func (c Capabilities) RequireDecoding(
	mode decode.HWAccel,
) error {
	if mode == decode.HWAccelNone {
		return nil
	}

	if !slices.Contains(c.HWAccels, hwaccelCUDA) {
		return fmt.Errorf("%w: no cuda hwaccel (NVDEC): %s", ErrMissing, buildHint)
	}

	if mode != decode.HWAccelCUDAScale {
		return nil
	}

	for _, filter := range []string{filterScaleCUDA, filterHWDownload} {
		if !slices.Contains(c.Filters, filter) {
			return fmt.Errorf("%w: no %s filter: %s", ErrMissing, filter, buildHint)
		}
	}

	return nil
}

// RequireEncoders checks that ffmpeg has every encoder (h264_nvenc...).
func (c Capabilities) RequireEncoders(
	encoders []string,
) error {
	for _, encoder := range encoders {
		if !slices.Contains(c.Encoders, encoder) {
			return fmt.Errorf("%w: no %s encoder: %s", ErrMissing, encoder, buildHint)
		}
	}

	return nil
}

// parseHWAccels reads "ffmpeg -hwaccels": a header line, then one method
// per line.
func parseHWAccels(
	out []byte,
) []string {
	var methods []string

	for line := range bytes.Lines(out) {
		name := strings.TrimSpace(string(line))
		if name == "" || strings.HasSuffix(name, ":") {
			continue
		}

		methods = append(methods, name)
	}

	return methods
}

// parseEncoders reads "ffmpeg -encoders": after the legend, which ends with
// a dashed line, each line is capability flags, the name and a description.
func parseEncoders(
	out []byte,
) []string {
	var names []string

	legend := true

	for line := range bytes.Lines(out) {
		fields := strings.Fields(string(line))

		switch {
		case len(fields) > 0 && strings.HasPrefix(fields[0], "---"):
			legend = false
		case legend || len(fields) < 2:
		default:
			names = append(names, fields[1])
		}
	}

	return names
}

// parseFilters reads "ffmpeg -filters": filter lines are flags, the name,
// then the pads as "V->V" (legend lines use "->" alone or come first).
func parseFilters(
	out []byte,
) []string {
	var names []string

	for line := range bytes.Lines(out) {
		fields := strings.Fields(string(line))
		if len(fields) >= 3 && strings.Contains(fields[2], "->") && len(fields[2]) > len("->") {
			names = append(names, fields[1])
		}
	}

	return names
}
