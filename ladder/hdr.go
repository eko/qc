package ladder

import (
	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// hdrBitDepth is the depth of every HDR encode: HDR10 requires 10 bits
// (8-bit PQ bands visibly), and HLG ladders follow the same rule.
const hdrBitDepth = 10

// HDRLadder describes how the ladder of an HDR (PQ or HLG) source keeps its
// signal.
type HDRLadder struct {
	// Signal is the colour description and HDR10 metadata every probe,
	// rung and rendered command carries.
	Signal encode.Signal `json:"signal"`
	// BitDepthUpgraded is set when 8-bit encodes were asked for: HDR
	// encodes are 10-bit.
	BitDepthUpgraded bool `json:"bitDepthUpgraded,omitempty"`
	// Metric is how VMAF scored the probes and rungs.
	Metric quality.HDRMetric `json:"metric"`
}

// hdrLadder plans the ladder of an HDR source: its signal on every encode,
// and 10-bit encodes, asked for or not (an 8-bit request is upgraded rather
// than refused: the default depth is 8, and the ladder of an HDR source
// should work with the defaults). It returns nil for SDR, whose encodes are
// unchanged. Options.ContentLight stands in for a content light level the
// source does not signal.
func hdrLadder(
	opts *Options,
	video media.VideoStream,
) *HDRLadder {
	if !video.Color.IsHDR() {
		return nil
	}

	metric, _ := quality.ParseHDRMetric(string(opts.HDRMetric))
	h := &HDRLadder{Signal: encode.SignalOf(video), Metric: metric}

	if h.Signal.ContentLight == nil && opts.ContentLight != nil {
		h.Signal.ContentLight = opts.ContentLight
	}

	if opts.BitDepth < hdrBitDepth {
		h.BitDepthUpgraded = true
		opts.BitDepth = hdrBitDepth
	}

	return h
}

// withSignal returns the inspection of the digest with the colour
// description and HDR metadata of its source: the raw digest (NUT) carries
// no colour tags, and its measurements must know they score an HDR signal.
func withSignal(
	report *analysis.Report,
	video media.VideoStream,
) *analysis.Report {
	if report == nil || report.Info == nil || len(report.Info.Video) == 0 || !video.Color.IsHDR() {
		return report
	}

	out := *report
	info := *report.Info
	info.Video = append([]media.VideoStream(nil), info.Video...)
	info.Video[0].Color, info.Video[0].HDR = video.Color, video.HDR
	out.Info = &info

	return &out
}
