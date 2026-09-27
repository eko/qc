package media

// Transfer characteristics of the HDR signals of ITU-R BT.2100, as ffmpeg
// names them (Color.Transfer).
const (
	// TransferPQ is the perceptual quantizer of SMPTE ST 2084: absolute
	// display light, up to 10 000 cd/m².
	TransferPQ = "smpte2084"
	// TransferHLG is hybrid log-gamma (ARIB STD-B67): relative scene light,
	// displayed at the peak luminance of the display.
	TransferHLG = "arib-std-b67"
)

// HLGNominalPeak is the display peak luminance (cd/m²) HLG light levels
// and colour differences are computed for: the reference display of ITU-R
// BT.2100 and BT.2124 (conversion 4).
const HLGNominalPeak = 1000

// IsHDR reports whether the transfer characteristic is PQ or HLG.
func (c Color) IsHDR() bool {
	return c.Transfer == TransferPQ || c.Transfer == TransferHLG
}

// FullRange reports whether samples use the full quantisation range
// ("pc"); limited ("tv") is the default for Y′CbCr video.
func (c Color) FullRange() bool {
	return c.Range == "pc" || c.Range == "jpeg"
}

// MeasurableHDR reports whether the decoded frames of the stream are a
// BT.2100 PQ or HLG Y′CbCr signal that light levels and HDR metrics can be
// computed on. A Dolby Vision stream without a compatible base layer
// (profile 5, compatibility id 0) codes IPTPQc2, which only its RPU turns
// into a picture: it is reported, not measured.
func (v VideoStream) MeasurableHDR() bool {
	if !v.Color.IsHDR() {
		return false
	}

	return v.HDR.DolbyVision == nil || v.HDR.DolbyVision.CompatibilityID != 0
}
