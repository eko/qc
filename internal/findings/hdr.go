package findings

import (
	"slices"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/light"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// TopicLight links a finding to the light level chart.
const TopicLight Topic = "light"

// Codes of HDR signalling and light levels (technical analysis).
const (
	// DolbyVision: a Dolby Vision stream, reported, not processed. Value
	// is its profile, Other its base layer compatibility id; Limit is 1
	// when its base layer is measured (a compatible HDR10, HLG or SDR base
	// layer), 0 when nothing is (profile 5: IPTPQc2 without a compatible
	// base layer).
	DolbyVision Code = "dolby-vision"
	// HDR10Plus: SMPTE ST 2094-40 dynamic metadata, reported, not
	// processed: the static HDR10 layer is measured.
	HDR10Plus Code = "hdr10-plus"
	// HDRPrimaries: an HDR transfer with primaries other than BT.2020.
	// Text is the primaries.
	HDRPrimaries Code = "hdr-primaries"
	// HDRMatrix: an HDR transfer with a matrix other than BT.2020 (or
	// ICtCp). Text is the matrix.
	HDRMatrix Code = "hdr-matrix"
	// HDRBitDepth: an HDR transfer on 8-bit samples. Value is the depth.
	HDRBitDepth Code = "hdr-bit-depth"
	// HDRFullRange: an HDR stream in full range, where HDR10 and HLG
	// delivery specify narrow range.
	HDRFullRange Code = "hdr-full-range"
	// MissingMastering: PQ without SMPTE ST 2086 mastering display
	// metadata, required by HDR10.
	MissingMastering Code = "missing-mastering-display"
	// MissingContentLight: HDR10 without MaxCLL and MaxFALL (or 0, 0).
	// Value is the measured robust MaxCLL, Limit the measured MaxFALL,
	// when the light levels were measured.
	MissingContentLight Code = "missing-content-light"
	// ContentBrighter: the content is brighter than its signalled MaxCLL
	// (Text "maxcll") or MaxFALL (Text "maxfall") by more than the
	// tolerance: players and TVs tone map it as dimmer than it is. Value is
	// the measured level, Limit the signalled one.
	ContentBrighter Code = "content-brighter-than-signalled"
	// ContentDimmer: the signalled MaxCLL (Text "maxcll") or MaxFALL
	// (Text "maxfall") is well above anything measured: conservative tone
	// mapping dims the picture for nothing. Value is the measured level,
	// Limit the signalled one.
	ContentDimmer Code = "content-dimmer-than-signalled"
	// LightLevelsMatch: the signalled MaxCLL and MaxFALL agree with the
	// measured ones. Value is the measured robust MaxCLL, Limit the
	// measured MaxFALL.
	LightLevelsMatch Code = "light-levels-match"
)

// Tolerances of the light level checks. MaxFALL is an average, measured
// within 0.3% on a sample grid; MaxCLL is a maximum, which 4:2:0 chroma
// subsampling inflates (the strict one) or a percentile deflates (the
// robust one): its check allows more.
const (
	MaxFALLTolerance = 0.15
	MaxCLLTolerance  = 0.25
)

// hdrPrimaries and hdrMatrices are the colour descriptions of BT.2100.
var (
	hdrPrimaries = []string{"bt2020"}
	hdrMatrices  = []string{"bt2020nc", "bt2020c", "ictcp"}
)

// hdrFindings lists the HDR findings of a video stream and its measured
// light levels (nil when not measured).
func hdrFindings(
	v media.VideoStream,
	lr *light.Result,
) []Finding {
	var out []Finding

	if dv := v.HDR.DolbyVision; dv != nil {
		f := Finding{Level: Info, Code: DolbyVision, Value: float64(dv.Profile), Other: dv.CompatibilityID}
		if v.MeasurableHDR() {
			f.Limit = 1
		}

		out = append(out, f)
	}

	if v.HDR.HDR10Plus {
		out = append(out, Finding{Level: Info, Code: HDR10Plus})
	}

	if !v.Color.IsHDR() {
		return out
	}

	out = append(out, signalFindings(v)...)

	return append(out, lightFindings(v.HDR, lr)...)
}

// signalFindings check the colour description of an HDR stream against
// BT.2100 and the HDR10 metadata.
func signalFindings(
	v media.VideoStream,
) []Finding {
	var out []Finding

	c := v.Color

	if c.Primaries != "" && !slices.Contains(hdrPrimaries, c.Primaries) {
		out = append(out, Finding{Level: Warn, Code: HDRPrimaries, Text: c.Primaries})
	}

	if c.Space != "" && !slices.Contains(hdrMatrices, c.Space) {
		out = append(out, Finding{Level: Warn, Code: HDRMatrix, Text: c.Space})
	}

	if v.BitDepth > 0 && v.BitDepth < 10 {
		out = append(out, Finding{Level: Warn, Code: HDRBitDepth, Value: float64(v.BitDepth)})
	}

	if c.FullRange() {
		out = append(out, Finding{Level: Info, Code: HDRFullRange})
	}

	if c.Transfer == media.TransferPQ && v.HDR.MasteringDisplay == nil {
		out = append(out, Finding{Level: Warn, Code: MissingMastering})
	}

	return out
}

// lightFindings compare the signalled content light level of a PQ stream
// with the measured one.
func lightFindings(
	hdr media.HDR,
	lr *light.Result,
) []Finding {
	cll := hdr.ContentLightLevel
	if hdr.DynamicRange == media.DynamicRangeHLG || hdr.MasteringDisplay == nil {
		return nil
	}

	if cll == nil || (cll.MaxCLL == 0 && cll.MaxFALL == 0) {
		f := Finding{Level: Warn, Code: MissingContentLight, Topic: TopicLight}
		if lr != nil {
			f.Value, f.Limit = lr.MaxCLLRobust, lr.MaxFALL
		}

		return []Finding{f}
	}

	if lr == nil {
		return nil
	}

	var out []Finding

	out = appendLevelCheck(out, "maxcll", lr.MaxCLLRobust, lr.MaxCLL, float64(cll.MaxCLL), MaxCLLTolerance)
	out = appendLevelCheck(out, "maxfall", lr.MaxFALL, lr.MaxFALL, float64(cll.MaxFALL), MaxFALLTolerance)

	if len(out) == 0 {
		out = append(out, Finding{Level: OK, Code: LightLevelsMatch, Topic: TopicLight, Value: lr.MaxCLLRobust, Limit: lr.MaxFALL})
	}

	return out
}

// appendLevelCheck adds a finding when a signalled level (0: unknown, not
// checked) is below the measured one (low, a conservative measure) or
// above the highest measured (high) beyond the tolerance.
func appendLevelCheck(
	out []Finding,
	name string,
	low, high, signalled, tolerance float64,
) []Finding {
	switch {
	case signalled <= 0:
	case low > signalled*(1+tolerance):
		out = append(out, Finding{Level: Warn, Code: ContentBrighter, Topic: TopicLight, Text: name, Value: low, Limit: signalled})
	case high < signalled/(1+tolerance):
		out = append(out, Finding{Level: Info, Code: ContentDimmer, Topic: TopicLight, Text: name, Value: high, Limit: signalled})
	}

	return out
}

// Codes of HDR comparisons and ladders.
const (
	// HDRVMAF: how VMAF was scored on an HDR reference and how to read it.
	// Text is quality.HDRReport.Note; the level is Info when VMAF is not
	// calibrated for the signal (scored on PQ or HLG), OK when it scored an
	// SDR tone mapping.
	HDRVMAF Code = "hdr-vmaf"
	// HDRLadderSignal: the rungs of an HDR source carry its colour
	// description (Text is the transfer), and its HDR10 metadata when Value
	// is 1.
	HDRLadderSignal Code = "hdr-ladder-signal"
	// HDRLadderTarget: the rungs of an HDR source were placed on VMAF
	// scored on its PQ or HLG signal (Text is the transfer), which ranks
	// encodes but is not calibrated there: the quality targets of the
	// ladder (Value is the top one) do not mean what they do in SDR.
	HDRLadderTarget Code = "hdr-ladder-target"
	// HDRBitDepthUpgraded: 8-bit encodes were asked for an HDR source; the
	// ladder encodes 10-bit.
	HDRBitDepthUpgraded Code = "hdr-bit-depth-upgraded"
	// HDRPlayerSupport: an HDR ladder in a codec players seldom decode as
	// HDR (H.264 High 10). Text is the codec.
	HDRPlayerSupport Code = "hdr-player-support"
	// HDRNVENCMetadata: NVENC has no option for HDR10 metadata; its rungs
	// carry what ffmpeg forwards from the source's side data.
	HDRNVENCMetadata Code = "hdr-nvenc-metadata"
)

// hdrComparison is the finding of a comparison with an HDR reference.
func hdrComparison(
	h *quality.HDRReport,
) (Finding, bool) {
	if h == nil {
		return Finding{}, false
	}

	level := Info
	if h.Calibrated {
		level = OK
	}

	return Finding{Level: level, Code: HDRVMAF, Topic: TopicQuality, Text: h.Note}, true
}

// hdrLadderFindings are the findings of the ladder of an HDR source.
func hdrLadderFindings(
	r *ladder.Result,
) []Finding {
	h := r.HDR
	if h == nil {
		return nil
	}

	signal := Finding{Level: OK, Code: HDRLadderSignal, Text: h.Signal.Color.Transfer}
	if h.Signal.Mastering != nil {
		signal.Value = 1
	}

	out := []Finding{signal}

	// On the HDR signal VMAF orders the encodes of a title well (SROCC
	// 0.81-0.86 on LIVE-HDR) but is far from the opinion scores in absolute
	// terms (RMSE 17.7 where the best HDR models reach 9.4): a top rung
	// "at 95" is the right rung to compare, not a promise of that quality.
	if h.Metric != quality.HDRMetricToneMap {
		out = append(out, Finding{
			Level: Info, Code: HDRLadderTarget, Topic: TopicQuality,
			Text: h.Signal.Color.Transfer, Value: r.Constraints.WithDefaults().TopVMAF,
		})
	}

	if h.BitDepthUpgraded {
		out = append(out, Finding{Level: Info, Code: HDRBitDepthUpgraded})
	}

	if r.Codec.Name == codecH264 {
		out = append(out, Finding{Level: Warn, Code: HDRPlayerSupport, Text: r.Codec.Name})
	}

	if r.Codec.Hardware == encode.HardwareNVENC && h.Signal.Mastering != nil {
		out = append(out, Finding{Level: Info, Code: HDRNVENCMetadata})
	}

	return out
}

// videoHDRFindings are the HDR findings of the primary video of a report.
func videoHDRFindings(
	r *analysis.Report,
) []Finding {
	v, ok := r.Info.PrimaryVideo()
	if !ok {
		return nil
	}

	var lr *light.Result
	if r.Video != nil {
		lr = r.Video.Light
	}

	return hdrFindings(v, lr)
}
