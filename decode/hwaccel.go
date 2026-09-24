package decode

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
)

// HWAccel selects hardware (NVIDIA NVDEC) decoding.
type HWAccel string

// Hardware decoding modes, from the most to the least offloaded.
const (
	// HWAccelNone decodes on the CPU (the zero value).
	HWAccelNone HWAccel = ""
	// HWAccelCUDA decodes with NVDEC (ffmpeg -hwaccel cuda) and lets ffmpeg
	// download each frame to system memory: selection, scaling and format
	// conversion stay on the CPU, so frames are identical to a CPU decode
	// (decoders are bit-exact by specification) and VMAF is unchanged.
	HWAccelCUDA HWAccel = "cuda"
	// HWAccelCUDAScale also scales and converts on the GPU (scale_cuda,
	// bicubic) before downloading the frames: the least CPU, but the GPU
	// scaler does not round like swscale, so scores differ slightly from a
	// CPU decode (see docs/gpu.md).
	HWAccelCUDAScale HWAccel = "cuda-scale"
)

// hwAccelNoneName is how HWAccelNone is written on the command line.
const hwAccelNoneName = "none"

// ErrHWAccel is returned for an unknown hardware decoding mode.
var ErrHWAccel = errors.New("unknown hwaccel")

// ParseHWAccel reads a hardware decoding mode: none (or empty), cuda or
// cuda-scale.
func ParseHWAccel(
	s string,
) (HWAccel, error) {
	switch h := HWAccel(strings.ToLower(strings.TrimSpace(s))); h {
	case HWAccelNone, hwAccelNoneName:
		return HWAccelNone, nil
	case HWAccelCUDA, HWAccelCUDAScale:
		return h, nil
	}

	return HWAccelNone, fmt.Errorf("%w %q (supported: none, cuda, cuda-scale)", ErrHWAccel, s)
}

// String returns the mode name, "none" for the zero value.
func (h HWAccel) String() string {
	if h == HWAccelNone {
		return hwAccelNoneName
	}

	return string(h)
}

// fallback is the next mode to try when h fails: scaling moves back to the
// CPU first, then decoding.
func (h HWAccel) fallback() HWAccel {
	if h == HWAccelCUDAScale {
		return HWAccelCUDA
	}

	return HWAccelNone
}

// rank orders modes by how much they offload.
func (h HWAccel) rank() int {
	switch h {
	case HWAccelCUDA:
		return 1
	case HWAccelCUDAScale:
		return 2
	}

	return 0
}

// nvdecCodecs are the ffprobe codec names NVDEC can decode (profiles and
// chroma formats vary by GPU generation; ffmpeg falls back to software for
// the others in HWAccelCUDA mode). Other codecs (ProRes, DNxHR, FFV1, raw
// video...) are decoded on the CPU without trying.
var nvdecCodecs = []string{"av1", "h264", "hevc", "mjpeg", "mpeg1video", "mpeg2video", "mpeg4", "vc1", "vp8", "vp9"}

// Option configures an FFmpeg source.
type Option func(*FFmpeg)

// WithHWAccel decodes with NVDEC in the given mode. When hardware decoding
// of a file fails before its first frame (no device, an unsupported
// profile), the file is decoded again with the next mode down and later
// decodes of that file start there; a warning is logged.
func WithHWAccel(
	mode HWAccel,
) Option {
	return func(d *FFmpeg) {
		d.hwaccel = mode
	}
}

// WithLogger logs hardware decoding fallbacks to logger.
func WithLogger(
	logger *slog.Logger,
) Option {
	return func(d *FFmpeg) {
		d.logger = logger
	}
}

// HWAccelReporter is implemented by sources that decode with a hardware
// decoding mode (*FFmpeg): measurements report it next to their results.
type HWAccelReporter interface {
	HWAccel() HWAccel
}

var _ HWAccelReporter = (*FFmpeg)(nil)

// HWAccel returns the configured hardware decoding mode.
func (d *FFmpeg) HWAccel() HWAccel {
	return d.hwaccel
}

// modeFor returns the mode to decode req with: none for codecs NVDEC does
// not decode, and never above a mode that already failed on the file.
func (d *FFmpeg) modeFor(
	req Request,
) HWAccel {
	if d.hwaccel == HWAccelNone || (req.Codec != "" && !slices.Contains(nvdecCodecs, req.Codec)) {
		return HWAccelNone
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if failed, ok := d.degraded[req.Path]; ok && failed.rank() < d.hwaccel.rank() {
		return failed
	}

	return d.hwaccel
}

// degrade records that mode failed on path: its later decodes use next.
func (d *FFmpeg) degrade(
	path string,
	mode, next HWAccel,
	cause error,
) {
	d.mu.Lock()
	if d.degraded == nil {
		d.degraded = map[string]HWAccel{}
	}

	d.degraded[path] = next
	d.mu.Unlock()

	if d.logger != nil {
		d.logger.Warn("hardware decoding failed, falling back",
			"path", path, "hwaccel", mode.String(), "fallback", next.String(), "error", cause)
	}
}

// hwInputArgs are the input options of mode, placed before -i.
func hwInputArgs(
	mode HWAccel,
) []string {
	switch mode {
	case HWAccelCUDA:
		return []string{"-hwaccel", "cuda"}
	case HWAccelCUDAScale:
		return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
	}

	return nil
}

// gpuFilters is the filter chain of HWAccelCUDAScale: scale_cuda resizes
// and converts the decoded surfaces to planar 4:2:0 at the pool's depth,
// then frames are downloaded and go through the CPU part of the chain
// (selection, luma extraction). Selection runs after the download: select
// is not aware of hardware frames, and moving every scaled frame over PCIe
// costs less than a CPU scale.
func gpuFilters(
	req Request,
) string {
	pool := req.Pool

	format := "yuv420p"
	if pool.HighBitDepth() {
		format = "yuv420p10le"
	}

	chain := []string{
		fmt.Sprintf("scale_cuda=%d:%d:interp_algo=bicubic:format=%s", pool.Width(), pool.Height(), format),
		"hwdownload",
		"format=" + format,
	}

	if len(req.Select) > 0 {
		chain = append(chain, selectFilter(req.Select))
	}

	if !pool.Chroma() {
		chain = append(chain, "extractplanes=y")
	}

	return strings.Join(chain, ",")
}
