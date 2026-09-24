package nvidia

import (
	"context"
	"fmt"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/ffexec"
)

// Requirements are the GPU work a run asks for. The zero value asks for
// nothing, and Check then runs nothing.
type Requirements struct {
	// HWAccel is the hardware decoding mode (decode.WithHWAccel).
	HWAccel decode.HWAccel
	// Codecs are the codecs encoded with NVENC (h264, hevc, av1): the
	// codecs of the ladders built with encode.HardwareNVENC.
	Codecs []string
	// BitDepth is the bit depth of the NVENC encodes: 8 (the default) or
	// 10, which older GPUs lack for some codecs.
	BitDepth int
}

// encoders are the NVENC encoders of the required codecs.
func (r Requirements) encoders() ([]string, error) {
	encoders := make([]string, 0, len(r.Codecs))

	for _, name := range r.Codecs {
		codec, err := encode.LookupFor(name, encode.HardwareNVENC)
		if err != nil {
			return nil, err
		}

		encoders = append(encoders, codec.Encoder)
	}

	return encoders, nil
}

// minBitDepth is the bit depth of encodes that do not say theirs.
const minBitDepth = 8

// Part is the part of the GPU work a failed check concerns.
type Part string

// Parts of the GPU work.
const (
	// PartDecoding is NVDEC decoding and GPU scaling (Requirements.HWAccel).
	PartDecoding Part = "decoding"
	// PartEncoding is NVENC encoding (Requirements.Codecs).
	PartEncoding Part = "encoding"
	// PartDevice is the CUDA device every part needs.
	PartDevice Part = "device"
)

// CheckError is a failed check: the part of the GPU work that cannot run,
// and why (wrapping ErrMissing, ErrDevice or ErrEncoder). A caller can use
// the part to fall back, e.g. to the CPU encoders when only NVENC fails.
type CheckError struct {
	Part Part
	Err  error
}

// Error implements error.
func (e *CheckError) Error() string {
	return string(e.Part) + ": " + e.Err.Error()
}

// Unwrap returns the reason of the failure.
func (e *CheckError) Unwrap() error {
	return e.Err
}

// Check verifies that the ffmpeg binary and this machine can do the GPU
// work of req, in a few short ffmpeg runs: ffmpeg was built with NVDEC (and
// the GPU scaler) and the NVENC encoders, the CUDA device opens, and each
// NVENC encoder encodes a frame at the bit depth (AV1 needs an Ada Lovelace
// GPU or newer, 10-bit H.264 a Blackwell, and every encoder a free NVENC
// session). A failed check is a *CheckError; failing to run ffmpeg at all
// is returned as is.
func Check(
	ctx context.Context,
	ffmpeg string,
	req Requirements,
) error {
	encoders, err := req.encoders()
	if err != nil {
		return &CheckError{Part: PartEncoding, Err: err}
	}

	if req.HWAccel == decode.HWAccelNone && len(encoders) == 0 {
		return nil
	}

	caps, err := Probe(ctx, ffmpeg)
	if err != nil {
		return err
	}

	if err := caps.RequireDecoding(req.HWAccel); err != nil {
		return &CheckError{Part: PartDecoding, Err: err}
	}

	if err := caps.RequireEncoders(encoders); err != nil {
		return &CheckError{Part: PartEncoding, Err: err}
	}

	if err := checkDevice(ctx, ffmpeg); err != nil {
		return &CheckError{Part: PartDevice, Err: err}
	}

	for _, encoder := range encoders {
		if err := checkEncoder(ctx, ffmpeg, encoder, max(req.BitDepth, minBitDepth)); err != nil {
			return &CheckError{Part: PartEncoding, Err: err}
		}
	}

	return nil
}

// Available reports whether the ffmpeg binary decodes with NVDEC, has
// NVENC (H.264) and opens the CUDA device: whether offering the GPU makes
// sense. It encodes nothing, so it is cheaper than Check, which a run should
// still call with its actual requirements.
func Available(
	ctx context.Context,
	ffmpeg string,
) bool {
	caps, err := Probe(ctx, ffmpeg)
	if err != nil || caps.RequireDecoding(decode.HWAccelCUDA) != nil || caps.RequireEncoders([]string{availableEncoder}) != nil {
		return false
	}

	return checkDevice(ctx, ffmpeg) == nil
}

// availableEncoder is the NVENC encoder every NVENC-capable GPU has.
const availableEncoder = "h264_nvenc"

// deviceHint tells what usually stands between a machine and its GPU.
const deviceHint = "check nvidia-smi, the driver, and --gpus all (NVIDIA container toolkit) in containers"

// checkDevice opens the CUDA device through ffmpeg, which fails without a
// GPU, a driver, or the container runtime's GPU access.
func checkDevice(
	ctx context.Context,
	ffmpeg string,
) error {
	args := []string{
		"-hide_banner", "-v", "error", "-init_hw_device", "cuda=gpu",
		"-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.04", "-frames:v", "1", "-f", "null", "-",
	}

	if _, err := ffexec.Output(ctx, ffmpeg, args); err != nil {
		return fmt.Errorf("%w (%s): %w", ErrDevice, deviceHint, err)
	}

	return nil
}

// checkSize is the frame size of the encoder checks: above every NVENC
// minimum (AV1 and HEVC refuse tiny frames).
const checkSize = "256x256"

// checkEncoder encodes one frame with encoder at bitDepth (8 or 10): a
// session that opens proves the GPU has that encoder and a free NVENC
// session.
func checkEncoder(
	ctx context.Context,
	ffmpeg, encoder string,
	bitDepth int,
) error {
	format := "yuv420p"
	if bitDepth > minBitDepth {
		format = "p010le"
	}

	args := []string{
		"-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=" + checkSize + ":d=0.04",
		"-frames:v", "1", "-vf", "format=" + format, "-c:v", encoder, "-f", "null", "-",
	}

	if _, err := ffexec.Output(ctx, ffmpeg, args); err != nil {
		return fmt.Errorf("%w: %s at %d bits: %w", ErrEncoder, encoder, bitDepth, err)
	}

	return nil
}
