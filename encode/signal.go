package encode

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/eko/qc/media"
)

// Signal is the colour signal an encode carries: its colour description,
// written in the bitstream (VUI, AV1 sequence header), and for HDR10 the
// static metadata of SMPTE ST 2086 and CTA-861.3 (SEI, AV1 metadata OBUs).
// The zero value carries nothing: the encoder writes whatever its input
// frames say, which is how SDR ladders have always been encoded.
type Signal struct {
	Color        media.Color              `json:"color"`
	Mastering    *media.MasteringDisplay  `json:"masteringDisplay,omitempty"`
	ContentLight *media.ContentLightLevel `json:"contentLightLevel,omitempty"`
}

// SignalOf is the signal of an HDR (PQ or HLG) video stream, to carry on
// its encodes, and the zero Signal for SDR ones. Unknown content light
// levels (0, 0) are left out.
func SignalOf(
	v media.VideoStream,
) Signal {
	if !v.Color.IsHDR() {
		return Signal{}
	}

	s := Signal{Color: v.Color, Mastering: v.HDR.MasteringDisplay}
	if cll := v.HDR.ContentLightLevel; cll != nil && (cll.MaxCLL > 0 || cll.MaxFALL > 0) {
		s.ContentLight = cll
	}

	return s
}

// IsZero reports whether the signal carries nothing.
func (s Signal) IsZero() bool {
	return s.Color == media.Color{} && s.Mastering == nil && s.ContentLight == nil
}

// hdr10 reports whether the encode is HDR10: PQ with a mastering display.
func (s Signal) hdr10() bool {
	return s.Color.Transfer == media.TransferPQ && s.Mastering != nil
}

// setParams is the filter tagging every frame with the colour description,
// or "" when there is none. ffmpeg's encoders take the colour of their
// input frames: -color_trc and the like are ignored when the frames say
// otherwise (or are untagged, as frames read from a raw digest are).
func (s Signal) setParams() string {
	c := s.Color

	var params []string

	for _, p := range [][2]string{
		{"color_primaries", c.Primaries}, {"color_trc", c.Transfer}, {"colorspace", c.Space}, {"range", c.Range},
	} {
		if p[1] != "" {
			params = append(params, p[0]+"="+p[1])
		}
	}

	if len(params) == 0 {
		return ""
	}

	return "setparams=" + strings.Join(params, ":")
}

// Units of x265's master-display: chromaticities in 0.00002, luminances in
// 0.0001 cd/m² (SMPTE ST 2086 / HEVC SEI units).
const (
	x265Chromaticity = 50000
	x265Luminance    = 10000
)

// x265Params are the x265 parameters of the signal: for PQ, block-level QP
// optimisation for HDR10 (hdr10-opt), and for HDR10 the mastering display
// and content light level SEI (hdr10, master-display, max-cll). x265 writes
// them with every keyframe (checked with ffmpeg's trace_headers), so every
// segment of an ABR rendition carries them; repeat-headers is not needed,
// and would put parameter sets in-band, which an hvc1 track must not.
func (s Signal) x265Params() []string {
	if s.Color.Transfer != media.TransferPQ {
		return nil
	}

	params := []string{"hdr10-opt=1"}

	if s.hdr10() {
		m := s.Mastering
		xy := func(c media.Chromaticity) string {
			return fmt.Sprintf("(%d,%d)", scaled(c.X, x265Chromaticity), scaled(c.Y, x265Chromaticity))
		}

		params = append(params, "hdr10=1", fmt.Sprintf("master-display=G%sB%sR%sWP%sL(%d,%d)",
			xy(m.Green), xy(m.Blue), xy(m.Red), xy(m.WhitePoint),
			scaled(m.MaxLuminance, x265Luminance), scaled(m.MinLuminance, x265Luminance)))
	}

	if cll := s.ContentLight; cll != nil && s.Color.Transfer == media.TransferPQ {
		params = append(params, fmt.Sprintf("max-cll=%d,%d", cll.MaxCLL, cll.MaxFALL))
	}

	return params
}

// svtParams are the SVT-AV1 parameters of an HDR10 signal: its mastering
// display (chromaticities and luminances as decimals, SVT-AV1 4.x syntax)
// and content light level, written as AV1 metadata OBUs.
func (s Signal) svtParams() []string {
	var params []string

	if s.hdr10() {
		m := s.Mastering
		xy := func(c media.Chromaticity) string {
			return "(" + decimal(c.X) + "," + decimal(c.Y) + ")"
		}

		params = append(params, "mastering-display=G"+xy(m.Green)+"B"+xy(m.Blue)+"R"+xy(m.Red)+"WP"+xy(m.WhitePoint)+
			"L("+decimal(m.MaxLuminance)+","+decimal(m.MinLuminance)+")")
	}

	if cll := s.ContentLight; cll != nil && s.Color.Transfer == media.TransferPQ {
		params = append(params, fmt.Sprintf("content-light=%d,%d", cll.MaxCLL, cll.MaxFALL))
	}

	return params
}

// scaled converts v to an integer number of 1/unit.
func scaled(
	v float64,
	unit int,
) int {
	return int(v*float64(unit) + 0.5)
}

// decimal formats v with four decimals, the precision of the SMPTE ST 2086
// luminance (0.0001 cd/m²).
func decimal(
	v float64,
) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}
