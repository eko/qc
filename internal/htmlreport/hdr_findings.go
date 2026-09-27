package htmlreport

import (
	"fmt"

	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/quality"
)

// hdrFindingText words an HDR finding for the page, or "" for another
// code.
func hdrFindingText(
	f findings.Finding,
) string {
	switch f.Code {
	case findings.DolbyVision:
		if f.Limit > 0 {
			return fmt.Sprintf("Dolby Vision profile %.0f (base layer compatibility %d): reported only, its base layer is measured", f.Value, f.Other)
		}

		return fmt.Sprintf("Dolby Vision profile %.0f without a compatible base layer: reported only, not measured", f.Value)
	case findings.HDR10Plus:
		return "HDR10+ dynamic metadata: reported only, the static HDR10 layer is measured"
	case findings.HDRPrimaries:
		return fmt.Sprintf("HDR transfer with %s primaries: BT.2100 HDR uses BT.2020", f.Text)
	case findings.HDRMatrix:
		return fmt.Sprintf("HDR transfer with a %s matrix: BT.2100 HDR uses BT.2020 (non-constant luminance)", f.Text)
	case findings.HDRBitDepth:
		return fmt.Sprintf("HDR on %.0f-bit samples: PQ and HLG band visibly below 10 bits", f.Value)
	case findings.HDRFullRange:
		return "HDR in full range: HDR10 and HLG delivery use narrow (limited) range"
	case findings.MissingMastering:
		return "PQ without mastering display metadata (SMPTE ST 2086): not HDR10, TVs guess how to tone map it"
	case findings.MissingContentLight:
		if f.Value > 0 {
			return fmt.Sprintf("HDR10 without MaxCLL/MaxFALL: measured %s / %s cd/m²", nits(f.Value), nits(f.Limit))
		}

		return "HDR10 without MaxCLL/MaxFALL"
	case findings.ContentBrighter:
		return fmt.Sprintf("Content brighter than its signalled %s: measured %s vs %s cd/m²", lightName(f.Text), nits(f.Value), nits(f.Limit))
	case findings.ContentDimmer:
		return fmt.Sprintf("Signalled %s well above the content: %s vs measured %s cd/m²", lightName(f.Text), nits(f.Limit), nits(f.Value))
	case findings.LightLevelsMatch:
		return fmt.Sprintf("Signalled MaxCLL and MaxFALL match the content (measured %s / %s cd/m²)", nits(f.Value), nits(f.Limit))
	}

	return hdrMeasurementText(f)
}

// hdrMeasurementText words the HDR findings of comparisons and ladders.
func hdrMeasurementText(
	f findings.Finding,
) string {
	switch f.Code {
	case findings.HDRVMAF:
		return f.Text
	case findings.HDRLadderSignal:
		if f.Value > 0 {
			return fmt.Sprintf("HDR rungs: every encode carries the %s colour description and the HDR10 metadata", transferName(f.Text))
		}

		return fmt.Sprintf("HDR rungs: every encode carries the %s colour description", transferName(f.Text))
	case findings.HDRBitDepthUpgraded:
		return "HDR source: rungs encoded in 10 bits (8 bits was asked for)"
	case findings.HDRPlayerSupport:
		return "HDR in H.264 (High 10): few players decode it as HDR; prefer HEVC or AV1 for HDR renditions"
	case findings.HDRNVENCMetadata:
		return "NVENC has no HDR10 metadata option: rendered commands carry what ffmpeg forwards from the source"
	}

	return ""
}

// lightName names a light level of a finding ("maxcll", "maxfall").
func lightName(
	name string,
) string {
	if name == "maxfall" {
		return "MaxFALL"
	}

	return "MaxCLL"
}

// transferName names an HDR transfer characteristic.
func transferName(
	transfer string,
) string {
	switch transfer {
	case "smpte2084":
		return "PQ"
	case "arib-std-b67":
		return "HLG"
	}

	return transfer
}

// nits formats a light level in cd/m², to the unit.
func nits(
	v float64,
) string {
	return fmt.Sprintf("%.0f", v)
}

// hdrVMAFLabel says on what VMAF was scored for an HDR reference.
func hdrVMAFLabel(
	h *quality.HDRReport,
) string {
	if h.Metric == quality.HDRMetricToneMap {
		return "SDR tone mapped"
	}

	return transferName(h.Transfer) + ", not HDR-calibrated"
}
