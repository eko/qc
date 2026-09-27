package encode

import (
	"fmt"
	"strings"
)

// Feature is an encoder capability callers such as the ladder engine
// depend on: they ask the codec (Codec.Supports) instead of guessing from
// its hardware or encoder name.
type Feature int

// Encoder features.
const (
	// FeatureFilmGrain is film grain synthesis (Params.FilmGrain): the
	// encoder denoises its input, codes the clean picture and signals grain
	// parameters the decoder adds back. SVT-AV1 only: av1_nvenc has none
	// reachable from ffmpeg.
	FeatureFilmGrain Feature = iota + 1
	// FeatureChunkJoin is chunked encoding (FFmpeg.EncodeChunks): chunks
	// encoded separately and joined without re-encoding decode exactly as
	// the separate chunks. Verified for x264, x265 and SVT-AV1 (see Chunk).
	FeatureChunkJoin
)

// family drives one family of encoders: each takes its own ffmpeg options
// for the same settings, and supports its own features. Adding an encoder
// is adding a family, not a branch in every method of Codec.
//
//nolint:interfacebloat // one strategy: every method maps the same settings to one encoder's options; splitting it would make each family implement several interfaces for nothing
type family interface {
	// qualityArgs select constant-quality rate control at crf.
	qualityArgs(crf string) []string
	// gopArgs fix the keyframe interval to gop frames.
	gopArgs(gop string) []string
	// privateArgs are the encoder-private options of p.
	privateArgs(p Params) []string
	// pixelFormat is the 4:2:0 pixel format the encoder is fed at bitDepth.
	pixelFormat(bitDepth int) string
	// inputArgs are the input options of the commands rendered for the
	// family (see Codec.InputArgs).
	inputArgs() []string
	// qualityOption is the ffmpeg option of the constant-quality value.
	qualityOption() string
	supports(f Feature) bool
}

// cpuFamily holds the options every CPU encoder shares: -crf, a fixed GOP
// through -g and -keyint_min, planar 4:2:0 input. It also drives encoders
// no family knows, with no private option and no feature.
type cpuFamily struct{}

func (cpuFamily) qualityArgs(
	crf string,
) []string {
	return []string{"-crf", crf}
}

func (cpuFamily) gopArgs(
	gop string,
) []string {
	return []string{"-g", gop, "-keyint_min", gop}
}

func (cpuFamily) privateArgs(
	Params,
) []string {
	return nil
}

func (cpuFamily) pixelFormat(
	bitDepth int,
) string {
	return pixelFormat(bitDepth)
}

func (cpuFamily) inputArgs() []string {
	return nil
}

func (cpuFamily) qualityOption() string {
	return "crf"
}

func (cpuFamily) supports(
	Feature,
) bool {
	return false
}

// x264 is libx264. With a fixed GOP, scene-cut keyframes are disabled so
// that every segment boundary is a keyframe and nothing else is.
type x264 struct{ cpuFamily }

func (x264) privateArgs(
	p Params,
) []string {
	if p.GOP > 0 {
		return []string{"-sc_threshold", "0"}
	}

	return nil
}

func (x264) supports(
	f Feature,
) bool {
	return f == FeatureChunkJoin
}

// x265 is libx265, quiet, without scene-cut keyframes under a fixed GOP.
type x265 struct{ cpuFamily }

func (x265) privateArgs(
	p Params,
) []string {
	params := []string{"log-level=error"}
	if p.GOP > 0 {
		params = append(params, "scenecut=0")
	}

	params = append(params, p.Signal.x265Params()...)

	return []string{"-x265-params", strings.Join(params, ":")}
}

func (x265) supports(
	f Feature,
) bool {
	return f == FeatureChunkJoin
}

// svtAV1 is libsvtav1, the only family synthesising film grain.
type svtAV1 struct{ cpuFamily }

// privateArgs gather film grain synthesis and HDR10 metadata in one
// -svtav1-params: ffmpeg keeps only the last of repeated options.
func (svtAV1) privateArgs(
	p Params,
) []string {
	params := p.Signal.svtParams()
	if p.FilmGrain > 0 {
		params = append(grainParams(p.FilmGrain), params...)
	}

	if len(params) == 0 {
		return nil
	}

	return []string{"-svtav1-params", strings.Join(params, ":")}
}

func (svtAV1) supports(
	f Feature,
) bool {
	return f == FeatureChunkJoin || f == FeatureFilmGrain
}

// grainParams are the SVT-AV1 parameters synthesising film grain of level
// (1–50): the encoder denoises its input, codes the clean picture and
// signals grain parameters the decoder adds back.
func grainParams(
	level int,
) []string {
	return []string{fmt.Sprintf("film-grain=%d", level), "film-grain-denoise=1"}
}

// familyOf finds the family of a codec that carries none, built by hand or
// decoded from JSON: the family of the known codec with its encoder, else
// the common options of its hardware.
func familyOf(
	c Codec,
) family {
	for _, table := range []map[string]Codec{codecs, nvencCodecs} {
		for _, known := range table {
			if known.Encoder == c.Encoder {
				return known.family
			}
		}
	}

	if c.Hardware == HardwareNVENC {
		return nvenc{}
	}

	return cpuFamily{}
}
