package findings

import (
	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
)

// Codes of the technical analysis.
const (
	// PeakBitrate: the peak bitrate is too far above the average. Value is
	// the peak / average ratio, Limit MaxPeakToAverage.
	PeakBitrate Code = "peak-bitrate"
	// KeyframeInterval: keyframes are too far apart. Value is the longest
	// interval, in seconds, Limit MaxKeyframeInterval.
	KeyframeInterval Code = "keyframe-interval"
	// BlackSegments and FrozenSegments: Spans are every black (frozen)
	// segment.
	BlackSegments  Code = "black-segments"
	FrozenSegments Code = "frozen-segments"
	// BlackBars: letterbox or pillarbox bars around the picture content
	// (analysis.VideoReport.Crop).
	BlackBars Code = "black-bars"
	// LevelsOutOfRange: luma samples outside the nominal range. Value is
	// their share on the worst frames (95th percentile), Limit
	// MaxOutOfRange.
	LevelsOutOfRange Code = "levels-out-of-range"
	// NoBlackOrFrozen: no black and no frozen segment.
	NoBlackOrFrozen Code = "no-black-or-frozen"
	// Interlaced: the source is interlaced. Text is its field order.
	Interlaced Code = "interlaced"
)

const (
	// MaxPeakToAverage is the HLS authoring spec limit on the peak bitrate
	// of a rendition over its average.
	MaxPeakToAverage = 2
	// MaxKeyframeInterval (seconds) is the longest comfortable keyframe
	// interval: seeking and ABR segments get coarse beyond it.
	MaxKeyframeInterval = 4
	// MaxOutOfRange is the share of out-of-range luma samples, on the
	// worst frames, above which levels are worth checking.
	MaxOutOfRange = 0.01
)

// fieldProgressive is the field order of a progressive stream.
const fieldProgressive = "progressive"

// Analysis lists the findings of a technical analysis: bitstream, then
// decoded frames (when analysed), then the stream's field order.
func Analysis(
	r *analysis.Report,
) []Finding {
	var out []Finding

	if bs := r.Bitstream; bs != nil {
		out = append(out, bitstreamFindings(bs)...)
	}

	v, _ := r.Info.PrimaryVideo()
	if vr := r.Video; vr != nil {
		out = append(out, videoFindings(vr)...)
	}

	if v.FieldOrder != "" && v.FieldOrder != fieldProgressive {
		out = append(out, Finding{Level: Warn, Code: Interlaced, Text: v.FieldOrder})
	}

	return append(out, videoHDRFindings(r)...)
}

func bitstreamFindings(
	bs *bitstream.Report,
) []Finding {
	var out []Finding

	if bs.PeakToAverage > MaxPeakToAverage {
		out = append(out, Finding{Level: Warn, Code: PeakBitrate, Topic: TopicBitrate, Value: bs.PeakToAverage, Limit: MaxPeakToAverage})
	}

	// A single keyframe has no interval.
	if interval := bs.GOP.MaxInterval.Seconds(); bs.GOP.KeyframeCount > 1 && interval > MaxKeyframeInterval {
		out = append(out, Finding{Level: Warn, Code: KeyframeInterval, Topic: TopicBitrate, Value: interval, Limit: MaxKeyframeInterval})
	}

	return out
}

func videoFindings(
	v *analysis.VideoReport,
) []Finding {
	var out []Finding

	if segments := v.Black.Segments; len(segments) > 0 {
		out = append(out, Finding{Level: Warn, Code: BlackSegments, Topic: TopicComplexity, Spans: segments})
	}

	if segments := v.Freeze.Segments; len(segments) > 0 {
		out = append(out, Finding{Level: Warn, Code: FrozenSegments, Topic: TopicComplexity, Spans: segments})
	}

	if c := v.Crop; c.Letterbox || c.Pillarbox {
		out = append(out, Finding{Level: Warn, Code: BlackBars})
	}

	if share := v.Levels.OutOfRangeSummary.P95; share > MaxOutOfRange {
		out = append(out, Finding{Level: Warn, Code: LevelsOutOfRange, Value: share, Limit: MaxOutOfRange})
	}

	if len(v.Black.Segments)+len(v.Freeze.Segments) == 0 {
		out = append(out, Finding{Level: OK, Code: NoBlackOrFrozen})
	}

	return append(out, motionFindings(v)...)
}
