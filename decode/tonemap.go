package decode

import (
	"fmt"
	"strings"

	"github.com/eko/qc/media"
)

// ToneMap asks the decoder for SDR frames tone mapped from an HDR (PQ or
// HLG) signal: ffmpeg's scaler converts them to BT.709 primaries, transfer
// and matrix with its perceptual intent, a tone and gamut mapping ported
// from libplacebo (FFmpeg ≥ 8). Both sides of a comparison go through the
// same mapping.
type ToneMap struct {
	// Input is the colour description of the frames. It is given
	// explicitly: raw digests carry no colour tags, and a wrong guess
	// would silently skip the mapping.
	Input media.Color
}

// toneMapOptions are the scale filter options of tone mapping to SDR
// BT.709, limited range.
func (t *ToneMap) options() string {
	in := t.Input

	opts := []string{
		"in_transfer=" + in.Transfer,
		"in_primaries=" + nonEmpty(in.Primaries, "bt2020"),
		"in_color_matrix=" + nonEmpty(in.Space, "bt2020nc"),
		"in_range=" + nonEmpty(in.Range, "tv"),
		"out_transfer=bt709", "out_primaries=bt709", "out_color_matrix=bt709", "out_range=tv",
		"intent=perceptual",
	}

	return strings.Join(opts, ":")
}

// scaleFilter is the scale of the filter chain: bicubic to the pool's size,
// with tone mapping when req asks for it.
func scaleFilter(
	req Request,
) string {
	scale := fmt.Sprintf("scale=%d:%d:flags=bicubic", req.Pool.Width(), req.Pool.Height())
	if req.ToneMap != nil && req.Pool.Chroma() {
		scale += ":" + req.ToneMap.options()
	}

	return scale
}

func nonEmpty(
	v, fallback string,
) string {
	if v == "" {
		return fallback
	}

	return v
}
