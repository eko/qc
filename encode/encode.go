// Package encode runs ffmpeg encoders with codec-specific settings: x264,
// x265 and SVT-AV1 on the CPU, or NVIDIA NVENC (see Hardware).
package encode

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ErrUnknownCodec is returned for an unsupported codec name.
var ErrUnknownCodec = errors.New("unknown codec")

// svtMaxRate is SVT-AV1's highest maxrate (100 000 kb/s).
const svtMaxRate = 100_000_000

// Pixel formats of 8-bit and 10-bit 4:2:0 encodes.
const (
	pixelFormat8  = "yuv420p"
	pixelFormat10 = "yuv420p10le"
)

// Codec describes how to drive one encoder.
type Codec struct {
	// Name is the user-facing name: h264, hevc or av1.
	Name string `json:"name"`
	// Encoder is the ffmpeg encoder.
	Encoder string `json:"encoder"`
	// Hardware is the encoder implementation: the CPU (empty) or NVENC.
	Hardware Hardware `json:"hardware,omitempty"`
	// DefaultPreset favours speed: the ladder is estimated from many encodes.
	DefaultPreset string `json:"-"`
	// MinCRF and MaxCRF bound the constant-quality scale.
	MinCRF float64 `json:"-"`
	MaxCRF float64 `json:"-"`
	// ProbeCRFs are the constant-quality values probed per resolution, from
	// high to low quality; together they span VMAF ~97 to ~40 at the right
	// resolution for most content.
	ProbeCRFs []float64 `json:"-"`
	// CRFStep is the granularity of the constant-quality scale.
	CRFStep float64 `json:"-"`
	// MaxRate is the highest VBV rate the encoder accepts (bits/s, 0 for no
	// limit): SVT-AV1 refuses maxrate above 100 Mb/s, which the 2× cap of a
	// top rung of grainy content can exceed.
	MaxRate int64 `json:"-"`

	// family drives the encoder (options and features). A codec built by
	// hand or decoded from JSON has none; impl finds it from Encoder.
	family family
}

// CRF granularity of the encoders: x264 and x265 take half steps, SVT-AV1
// only integers.
const (
	crfStepX26x = 0.5
	crfStepSVT  = 1.0
)

// codecs lists the supported codecs, encoded on the CPU. It is read-only:
// Lookup and LookupFor hand out copies, so that no caller can change a
// codec for every other.
var codecs = map[string]Codec{
	"h264": {
		Name: "h264", Encoder: "libx264", DefaultPreset: "fast",
		MinCRF: 10, MaxCRF: 51, ProbeCRFs: []float64{20, 27, 34}, CRFStep: crfStepX26x,
		family: x264{},
	},
	"hevc": {
		Name: "hevc", Encoder: "libx265", DefaultPreset: "veryfast",
		MinCRF: 10, MaxCRF: 51, ProbeCRFs: []float64{22, 29, 36}, CRFStep: crfStepX26x,
		family: x265{},
	},
	"av1": {
		Name: "av1", Encoder: "libsvtav1", DefaultPreset: "8",
		MinCRF: 10, MaxCRF: 63, ProbeCRFs: []float64{28, 40, 52}, CRFStep: crfStepSVT, MaxRate: svtMaxRate,
		family: svtAV1{},
	},
}

// Lookup returns the CPU codec named name.
func Lookup(
	name string,
) (Codec, error) {
	return LookupFor(name, HardwareCPU)
}

// LookupFor returns the codec named name, encoded on hw. The codec is a
// copy the caller may change.
func LookupFor(
	name string,
	hw Hardware,
) (Codec, error) {
	table := codecs

	switch hw {
	case HardwareCPU:
	case HardwareNVENC:
		table = nvencCodecs
	default:
		return Codec{}, fmt.Errorf("%w %q", ErrUnknownHardware, hw)
	}

	codec, ok := table[strings.ToLower(name)]
	if !ok {
		return Codec{}, fmt.Errorf("%w %q (supported: h264, hevc, av1)", ErrUnknownCodec, name)
	}

	codec.ProbeCRFs = slices.Clone(codec.ProbeCRFs)

	return codec, nil
}

// Params are the settings of one encode.
type Params struct {
	Width, Height int
	CRF           float64
	Preset        string
	// GOP is the keyframe interval in frames (fixed, no scene-cut keyframes,
	// as ABR segmenting requires). 0 lets the encoder decide.
	GOP int
	// MaxRate and BufSize (bits/s, bits) cap the bitrate when set.
	MaxRate int64
	BufSize int64
	// BitDepth is 8 (default) or 10 (Main10 / 10-bit profiles).
	BitDepth int
	// FilmGrain is the SVT-AV1 film grain synthesis level (1–50; 0 off,
	// ignored by the other encoders).
	FilmGrain int
}

// Args returns the ffmpeg output arguments for p, without input or output path.
func (c Codec) Args(
	p Params,
) []string {
	preset := p.Preset
	if preset == "" {
		preset = c.DefaultPreset
	}

	impl := c.impl()
	args := []string{
		"-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=bicubic,format=%s", p.Width, p.Height, impl.pixelFormat(p.BitDepth)),
		"-c:v", c.Encoder,
		"-preset", preset,
	}
	args = append(args, impl.qualityArgs(strconv.FormatFloat(p.CRF, 'f', -1, 64))...)

	if p.GOP > 0 {
		args = append(args, impl.gopArgs(strconv.Itoa(p.GOP))...)
	}

	if p.MaxRate > 0 {
		maxRate, bufSize := p.MaxRate, p.BufSize
		if c.MaxRate > 0 && maxRate > c.MaxRate {
			// Keep the buffer's length in seconds of peak rate.
			bufSize = int64(float64(bufSize) * float64(c.MaxRate) / float64(maxRate))
			maxRate = c.MaxRate
		}

		args = append(args,
			"-maxrate", strconv.FormatInt(maxRate, 10),
			"-bufsize", strconv.FormatInt(bufSize, 10))
	}

	args = append(args, impl.privateArgs(p)...)
	if c.Name == "hevc" {
		// hvc1 is the sample entry Apple players require for HEVC in MP4,
		// whichever encoder produced it.
		args = append(args, "-tag:v", "hvc1")
	}

	return args
}

// impl returns the family driving the codec's encoder.
func (c Codec) impl() family {
	if c.family != nil {
		return c.family
	}

	return familyOf(c)
}

// Supports reports whether the codec's encoder has feature f.
func (c Codec) Supports(
	f Feature,
) bool {
	return c.impl().supports(f)
}

// CommandLine renders a copy-pasteable ffmpeg command for p. NVENC
// commands also decode the source on the GPU (see InputArgs).
func (c Codec) CommandLine(
	src, dst string,
	p Params,
) string {
	parts := append([]string{"ffmpeg"}, c.InputArgs()...)
	parts = append(parts, "-i", quote(src))
	for _, a := range c.Args(p) {
		parts = append(parts, quote(a))
	}

	return strings.Join(append(parts, quote(dst)), " ")
}

// pixelFormat is the planar 4:2:0 pixel format of bitDepth.
func pixelFormat(
	bitDepth int,
) string {
	if bitDepth > 8 {
		return pixelFormat10
	}

	return pixelFormat8
}

// quote single-quotes s for a POSIX shell unless every character is safe
// unquoted, so that commands stay copy-pasteable whatever the file names.
func quote(
	s string,
) string {
	if s != "" && strings.IndexFunc(s, needsQuoting) < 0 {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func needsQuoting(
	r rune,
) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case strings.ContainsRune("-_./=+@%,:", r):
		return false
	}

	return true
}
