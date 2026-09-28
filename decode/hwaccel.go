package decode

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strings"
)

// HWAccel selects hardware decoding: NVIDIA NVDEC or Apple VideoToolbox.
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
	// HWAccelVideoToolbox decodes with Apple VideoToolbox (ffmpeg -hwaccel
	// videotoolbox) and lets ffmpeg download each frame: like HWAccelCUDA,
	// frames are identical to a CPU decode. Only the codecs whose decoding
	// is bit-exact by specification, in the 4:2:0 formats VideoToolbox
	// outputs as they are, are decoded by it (see vtCodecs). One session
	// decodes slower than ffmpeg's multithreaded CPU decoder but uses
	// almost no CPU, and concurrent sessions add up: it pays when a video
	// is decoded in concurrent segments (Request.Segment).
	HWAccelVideoToolbox HWAccel = "videotoolbox"
	// HWAccelAuto uses the platform's exact hardware decoder where it pays:
	// on macOS, VideoToolbox for concurrent decodes (Request.Segment: the
	// segments of a frame analysis, the runs and segments of a VMAF
	// measurement), the CPU for every other decode. Elsewhere it is the
	// CPU (NVDEC needs an explicit cuda mode, which the GPU preflight
	// checks).
	HWAccelAuto HWAccel = "auto"
)

// hwAccelNoneName is how HWAccelNone is written on the command line.
const hwAccelNoneName = "none"

// ErrHWAccel is returned for an unknown hardware decoding mode.
var ErrHWAccel = errors.New("unknown hwaccel")

// ParseHWAccel reads a hardware decoding mode: none (or empty), auto,
// videotoolbox, cuda or cuda-scale.
func ParseHWAccel(
	s string,
) (HWAccel, error) {
	switch h := HWAccel(strings.ToLower(strings.TrimSpace(s))); h {
	case HWAccelNone, hwAccelNoneName:
		return HWAccelNone, nil
	case HWAccelAuto, HWAccelVideoToolbox, HWAccelCUDA, HWAccelCUDAScale:
		return h, nil
	}

	return HWAccelNone, fmt.Errorf("%w %q (supported: none, auto, videotoolbox, cuda, cuda-scale)", ErrHWAccel, s)
}

// darwin is the GOOS of macOS, where VideoToolbox is.
const darwin = "darwin"

// Resolve returns the mode h stands for on the operating system goos
// (runtime.GOOS): HWAccelAuto stays automatic on macOS and becomes none
// elsewhere; other modes are returned as they are.
func (h HWAccel) Resolve(
	goos string,
) HWAccel {
	if h == HWAccelAuto && goos != darwin {
		return HWAccelNone
	}

	return h
}

// CUDA reports whether h decodes on an NVIDIA GPU, which the GPU preflight
// must check.
func (h HWAccel) CUDA() bool {
	return h == HWAccelCUDA || h == HWAccelCUDAScale
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
	case HWAccelCUDA, HWAccelVideoToolbox:
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

// vtCodecs are the codecs every Apple silicon Mac decodes with
// VideoToolbox and whose decoding is bit-exact by specification.
// VideoToolbox also decodes MPEG-2, MPEG-4 part 2 and ProRes, whose inverse
// transforms are not specified to the bit (their frames could differ from
// ffmpeg's), and VP9 and AV1 on some machines only: ffmpeg would silently
// decode them in software, one full decoder per segment.
var vtCodecs = []string{"h264", "hevc"}

// vtPixelFormats are the ffprobe pixel formats VideoToolbox outputs as
// they are (NV12, P010): 4:2:0 in 8 or 10 bits. Frames are then converted
// exactly into the pools' formats, as a CPU decode's.
var vtPixelFormats = []string{"yuv420p", "yuvj420p", "yuv420p10le"}

// Option configures an FFmpeg source.
type Option func(*FFmpeg)

// WithHWAccel decodes with hardware in the given mode (HWAccelAuto is
// resolved for the running system). When hardware decoding of a file fails
// before its first frame (no device, an unsupported profile), the file is
// decoded again with the next mode down and later decodes of that file
// start there; a warning is logged.
func WithHWAccel(
	mode HWAccel,
) Option {
	return func(d *FFmpeg) {
		d.hwaccel = mode.Resolve(runtime.GOOS)
	}
}

// WithVideoToolboxSessions lets at most n decodes use VideoToolbox at once
// (NumCPU by default): the others decode on the CPU, with identical
// frames. On an M2 Max, a dozen sessions reach the throughput of the
// decoding engine; a CPU decode next to them only pays on an idle machine
// (docs/analysis.md).
func WithVideoToolboxSessions(
	n int,
) Option {
	return func(d *FFmpeg) {
		if n > 0 {
			d.sessions = make(chan struct{}, n)
		}
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

// SegmentDecoders is implemented by sources that know how many segments of
// a video are worth decoding at once (*FFmpeg).
type SegmentDecoders interface {
	// SegmentDecoders returns how many segments of the video of req to
	// decode concurrently: 1 when a single decode is the fastest.
	SegmentDecoders(req Request) int
}

var _ SegmentDecoders = (*FFmpeg)(nil)

// SegmentDecoders implements SegmentDecoders. When segments decode on
// VideoToolbox, it is its sessions (see WithVideoToolboxSessions).
// Otherwise it is 1: ffmpeg's multithreaded CPU decoder already keeps every
// core busy, and segments would only add the GOP each one decodes before
// its first frame.
func (d *FFmpeg) SegmentDecoders(
	req Request,
) int {
	if d.SegmentHWAccel(req) != HWAccelVideoToolbox {
		return 1
	}

	return d.vtSessions()
}

// SegmentHWAccelReporter is implemented by sources whose segment decodes
// (Request.Segment) may use another hardware decoding mode than their
// other decodes (*FFmpeg with HWAccelAuto): measurements decoding segments
// report it.
type SegmentHWAccelReporter interface {
	SegmentHWAccel(req Request) HWAccel
}

var _ SegmentHWAccelReporter = (*FFmpeg)(nil)

// SegmentHWAccel returns the mode the segments of the video of req start
// decoding in, before any fallback: with HWAccelAuto, VideoToolbox on
// macOS for the codecs and formats it decodes exactly.
func (d *FFmpeg) SegmentHWAccel(
	req Request,
) HWAccel {
	req.Segment = true

	return d.modeFor(req)
}

// vtSessions is how many decodes may use VideoToolbox at once.
func (d *FFmpeg) vtSessions() int {
	if d.sessions == nil {
		return runtime.NumCPU()
	}

	return cap(d.sessions)
}

// acquire returns the mode a decode of req starts in, taking a
// VideoToolbox session when the mode uses one: none when every session is
// busy. The returned function gives the session back.
func (d *FFmpeg) acquire(
	req Request,
) (HWAccel, func()) {
	mode := d.modeFor(req)
	if mode != HWAccelVideoToolbox {
		return mode, func() {}
	}

	d.once.Do(func() {
		if d.sessions == nil {
			d.sessions = make(chan struct{}, d.vtSessions())
		}
	})

	select {
	case d.sessions <- struct{}{}:
		return mode, func() { <-d.sessions }
	default:
		return HWAccelNone, func() {}
	}
}

// HWAccel returns the hardware decoding mode of decodes that are not
// segments (Request.Segment): the configured one, none for HWAccelAuto.
func (d *FFmpeg) HWAccel() HWAccel {
	if d.hwaccel == HWAccelAuto {
		return HWAccelNone
	}

	return d.hwaccel
}

// configured returns the mode req is decoded with before any fallback:
// HWAccelAuto uses VideoToolbox for segments only.
func (d *FFmpeg) configured(
	req Request,
) HWAccel {
	if d.hwaccel != HWAccelAuto {
		return d.hwaccel
	}

	if req.Segment {
		return HWAccelVideoToolbox
	}

	return HWAccelNone
}

// modeFor returns the mode to decode req with: none for codecs (and
// pixel formats) the hardware does not decode as the CPU does, and never
// above a mode that already failed on the file.
func (d *FFmpeg) modeFor(
	req Request,
) HWAccel {
	mode := d.configured(req)
	if !hardwareDecodes(mode, req) {
		return HWAccelNone
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if failed, ok := d.degraded[req.Path]; ok && failed.rank() < mode.rank() {
		return failed
	}

	return mode
}

// hardwareDecodes reports whether mode decodes req on the hardware
// (unknown codecs are tried).
func hardwareDecodes(
	mode HWAccel,
	req Request,
) bool {
	known := func(value string, supported []string) bool {
		return value == "" || slices.Contains(supported, value)
	}

	switch mode {
	case HWAccelNone:
		return false
	case HWAccelVideoToolbox:
		// The pixel format must be known: filter graphs convert to it.
		return known(req.Codec, vtCodecs) && slices.Contains(vtPixelFormats, req.PixelFormat)
	}

	return known(req.Codec, nvdecCodecs)
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
	case HWAccelVideoToolbox:
		return []string{"-hwaccel", "videotoolbox"}
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

	if req.ToneMap != nil && pool.Chroma() {
		// scale_cuda cannot tone map: the CPU scaler does, at the same size.
		chain = append(chain, scaleFilter(req))
	}

	return strings.Join(chain, ",")
}
