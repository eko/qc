package quality

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality/xpsnr"
	"github.com/eko/qc/vmaf"
)

// Metrics measured next to VMAF, as named in Options.Metrics. Every metric
// is computed on the frames VMAF already decodes (the sampled clips, or
// every frame), at the evaluation resolution of the model.
const (
	// MetricVMAF is always measured; naming it is allowed and changes nothing.
	MetricVMAF = "vmaf"
	// MetricXPSNR is XPSNR per plane (Fraunhofer HHI), computed in Go and
	// identical to ffmpeg's xpsnr filter.
	MetricXPSNR = "xpsnr"
	// MetricCAMBI is Netflix's banding index, with the options of the VMAF
	// v1 models (free when a v1 model is scored).
	MetricCAMBI = "cambi"
	// MetricPSNR is PSNR per plane and the AV2 CTC weighted PSNR-YUV.
	MetricPSNR = "psnr"
	// MetricPSNRHVS is PSNR-HVS (libvmaf).
	MetricPSNRHVS = "psnr-hvs"
	// MetricSSIM is SSIM on luma (libvmaf float_ssim).
	MetricSSIM = "ssim"
	// MetricMSSSIM is multi-scale SSIM on luma (libvmaf float_ms_ssim).
	MetricMSSSIM = "ms-ssim"
	// MetricCIEDE2000 is the CIEDE2000 colour difference (libvmaf), as a
	// dB-like score: higher is better.
	MetricCIEDE2000 = "ciede2000"
)

// metricNames lists the metric names, in report order.
var metricNames = []string{
	MetricVMAF, MetricXPSNR, MetricCAMBI, MetricPSNR, MetricPSNRHVS, MetricSSIM, MetricMSSSIM, MetricCIEDE2000,
}

// av2CTCMetrics is the metric set of the AOM AV2 common test conditions.
var av2CTCMetrics = []string{
	MetricVMAF, MetricCAMBI, MetricPSNR, MetricPSNRHVS, MetricSSIM, MetricMSSSIM, MetricCIEDE2000,
}

// Metrics returns the metric names, in report order (VMAF first).
func Metrics() []string {
	return slices.Clone(metricNames)
}

// AV2CTCMetrics returns the metric set of the AOM AV2 common test conditions
// (arXiv:2605.15800): PSNR per plane and weighted PSNR-YUV, PSNR-HVS, SSIM,
// MS-SSIM, CIEDE2000, VMAF and CAMBI, all computed by libvmaf.
func AV2CTCMetrics() []string {
	return slices.Clone(av2CTCMetrics)
}

// Series names: the keys of MetricResult.Name and FrameScore.Metrics.
const (
	SeriesXPSNRY = "xpsnr_y"
	SeriesXPSNRU = "xpsnr_u"
	SeriesXPSNRV = "xpsnr_v"
	SeriesCAMBI  = "cambi"
	// SeriesCAMBISource is CAMBI on the reference frame, measured on the
	// banded frames only (FrameScore.Metrics): the banding the encode
	// inherited.
	SeriesCAMBISource = "cambi_source"
	SeriesPSNRY       = "psnr_y"
	SeriesPSNRCb      = "psnr_cb"
	SeriesPSNRCr      = "psnr_cr"
	SeriesPSNRYUV     = "psnr_yuv"
	SeriesPSNRHVS     = "psnr_hvs"
	SeriesSSIM        = "ssim"
	SeriesMSSSIM      = "ms_ssim"
	SeriesCIEDE2000   = "ciede2000"
	// seriesDevice prefixes the per-frame VMAF of a device ("vmaf_phone").
	seriesDevice = "vmaf_"
)

// AV2 CTC weights of the 4:2:0 planes in PSNR-YUV: 14:1:1 (7/8, 1/16, 1/16).
const (
	psnrWeightY      = 14.0
	psnrWeightChroma = 1.0
	psnrWeightTotal  = psnrWeightY + 2*psnrWeightChroma
)

const (
	// maxDecibels caps dB-like values that are infinite on identical frames
	// (PSNR-HVS, CIEDE2000, XPSNR), so that means stay finite and reports
	// encode. libvmaf's PSNR is already capped (60 dB at 8 bits).
	maxDecibels = 100
	// BandingThreshold is the CAMBI score above which banding is visible
	// (Netflix: below 5 it is imperceptible).
	BandingThreshold = 5.0
	// bandingJoin is the largest gap between two banded scored frames of
	// one segment: in sampled mode, clips of a banded shot a few frames
	// apart form one segment, clips far apart do not.
	bandingJoin = media.Duration(time.Second)
)

var (
	// ErrUnknownMetric is returned for a metric name not in Metrics.
	ErrUnknownMetric = errors.New("unknown metric")
	// ErrUnknownDevice is returned for a device not in vmaf.Devices.
	ErrUnknownDevice = errors.New("unknown device")
)

// ParseMetrics validates metric names (case-insensitive) and returns them
// without duplicates, in report order. "vmaf" is dropped: it is always
// measured.
func ParseMetrics(
	names []string,
) ([]string, error) {
	return parseNames(names, metricNames, ErrUnknownMetric, MetricVMAF)
}

// ParseDevices validates device names (case-insensitive) and returns them
// without duplicates, in display order.
func ParseDevices(
	names []string,
) ([]string, error) {
	return parseNames(names, vmaf.Devices(), ErrUnknownDevice, "")
}

// parseNames normalises names against the known ones, sorted like known.
func parseNames(
	names, known []string,
	unknown error,
	implicit string,
) ([]string, error) {
	var out []string

	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || name == implicit {
			continue
		}

		if !slices.Contains(known, name) {
			return nil, fmt.Errorf("%w %q (supported: %s)", unknown, name, strings.Join(known, ", "))
		}

		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}

	slices.SortFunc(out, func(a, b string) int { return slices.Index(known, a) - slices.Index(known, b) })

	return out, nil
}

// series is a per-frame quantity pooled like VMAF: the mean of its raw
// per-frame values is estimated with the stratified estimator, then
// converted into the reported value.
type series struct {
	name string
	// value converts a raw value, or a mean of raw values, into the
	// reported one. nil is the identity.
	value func(raw float64) float64
	// decreasing is set when value decreases as raw grows: the bounds of
	// the interval swap.
	decreasing bool
}

// report converts a raw value.
func (s series) report(
	raw float64,
) float64 {
	if s.value == nil {
		return raw
	}

	return s.value(raw)
}

// headlineSeries are the series that sum a metric up in one number.
var headlineSeries = []string{
	SeriesXPSNRY, SeriesPSNRY, SeriesPSNRHVS, SeriesSSIM, SeriesMSSSIM, SeriesCIEDE2000, SeriesCAMBI,
	SeriesWPSNRY, SeriesDeltaEITP,
}

// HeadlineSeries returns the series that sum a metric up in one number, in
// report order: the luma series of per-plane metrics. Reports short on room,
// such as the rungs of a ladder, show only these.
func HeadlineSeries() []string {
	return slices.Clone(headlineSeries)
}

// metricSeries lists the series of the requested metrics, in report order.
// XPSNR is pooled like ffmpeg: its raw value is the per-frame distortion
// √WSSE, whose mean converts into the XPSNR of the sequence.
func metricSeries(
	metrics []string,
	width, height int,
	bitDepth int,
) []series {
	var out []series

	for _, metric := range metrics {
		switch metric {
		case MetricXPSNR:
			cw, ch := (width+1)/2, (height+1)/2
			out = append(out,
				xpsnrSeries(SeriesXPSNRY, width, height, bitDepth),
				xpsnrSeries(SeriesXPSNRU, cw, ch, bitDepth),
				xpsnrSeries(SeriesXPSNRV, cw, ch, bitDepth))
		case MetricCAMBI:
			out = append(out, series{name: SeriesCAMBI})
		case MetricPSNR:
			out = append(out, series{name: SeriesPSNRY}, series{name: SeriesPSNRCb}, series{name: SeriesPSNRCr},
				series{name: SeriesPSNRYUV})
		case MetricPSNRHVS:
			out = append(out, series{name: SeriesPSNRHVS})
		case MetricSSIM:
			out = append(out, series{name: SeriesSSIM})
		case MetricMSSSIM:
			out = append(out, series{name: SeriesMSSSIM})
		case MetricCIEDE2000:
			out = append(out, series{name: SeriesCIEDE2000})
		}
	}

	return out
}

// xpsnrSeries converts the mean distortion of a plane into XPSNR, capped
// at maxDecibels.
func xpsnrSeries(
	name string,
	width, height int,
	bitDepth int,
) series {
	return series{
		name: name,
		value: func(distortion float64) float64 {
			return min(maxDecibels, xpsnr.Decibels(distortion, width, height, bitDepth))
		},
		decreasing: true,
	}
}

// extractors lists the libvmaf extractors the metrics need.
func extractors(
	metrics []string,
) []vmaf.Extractor {
	byMetric := map[string]vmaf.Extractor{
		MetricCAMBI:     vmaf.ExtractorCAMBI,
		MetricPSNR:      vmaf.ExtractorPSNR,
		MetricPSNRHVS:   vmaf.ExtractorPSNRHVS,
		MetricSSIM:      vmaf.ExtractorSSIM,
		MetricMSSSIM:    vmaf.ExtractorMSSSIM,
		MetricCIEDE2000: vmaf.ExtractorCIEDE2000,
	}

	var out []vmaf.Extractor

	for _, metric := range metrics {
		if e, ok := byMetric[metric]; ok {
			out = append(out, e)
		}
	}

	return out
}

// featureValues maps the libvmaf features of a scored clip to raw series
// values, adding the weighted PSNR-YUV and capping infinite values.
func featureValues(
	features map[string][]float64,
	values map[string][]float64,
) {
	names := map[string]string{
		vmaf.FeatureCAMBI:       SeriesCAMBI,
		vmaf.FeatureCAMBISource: SeriesCAMBISource,
		vmaf.FeaturePSNRY:       SeriesPSNRY,
		vmaf.FeaturePSNRCb:      SeriesPSNRCb,
		vmaf.FeaturePSNRCr:      SeriesPSNRCr,
		vmaf.FeaturePSNRHVS:     SeriesPSNRHVS,
		vmaf.FeatureSSIM:        SeriesSSIM,
		vmaf.FeatureMSSSIM:      SeriesMSSSIM,
		vmaf.FeatureCIEDE2000:   SeriesCIEDE2000,
	}

	for feature, frames := range features {
		out := make([]float64, len(frames))
		for i, v := range frames {
			out[i] = min(v, maxDecibels)
		}

		values[names[feature]] = out
	}

	y, cb, cr := values[SeriesPSNRY], values[SeriesPSNRCb], values[SeriesPSNRCr]
	if y == nil {
		return
	}

	yuv := make([]float64, len(y))
	for i := range yuv {
		yuv[i] = (psnrWeightY*y[i] + psnrWeightChroma*(cb[i]+cr[i])) / psnrWeightTotal
	}

	values[SeriesPSNRYUV] = yuv
}

// Estimate is a mean with its confidence interval. Low and High are equal
// to Mean in exact mode.
type Estimate struct {
	Mean      float64 `json:"mean"`
	Low       float64 `json:"low"`
	High      float64 `json:"high"`
	HalfWidth float64 `json:"halfWidth"`
}

// MetricResult is the measurement of one series of a metric (e.g. psnr_y).
type MetricResult struct {
	Name string `json:"name"`
	Estimate
	// Scored summarises the per-frame values of the scored frames.
	Scored stats.Summary `json:"scored"`
}

// DeviceResult is the VMAF of one viewing condition, with its own model.
type DeviceResult struct {
	Device string         `json:"device"`
	Model  vmaf.ModelSpec `json:"model"`
	Estimate
	Scored stats.Summary `json:"scored"`
}

// Banding reports where CAMBI exceeds BandingThreshold among the scored
// frames. In sampled mode, unscored frames are not known to be clean.
type Banding struct {
	Threshold    float64 `json:"threshold"`
	BandedFrames int     `json:"bandedFrames"`
	// SourceFrames counts the banded frames whose reference frame is
	// banded too (SeriesCAMBISource above the threshold): banding the
	// encode inherited rather than made.
	SourceFrames int              `json:"sourceFrames,omitempty"`
	Segments     []BandingSegment `json:"segments,omitempty"`
}

// Inherited is the share of the banded frames whose reference is banded
// too, 0 without banded frames.
func (b *Banding) Inherited() float64 {
	if b == nil || b.BandedFrames == 0 {
		return 0
	}

	return float64(b.SourceFrames) / float64(b.BandedFrames)
}

// BandingSegment is a run of banded scored frames.
type BandingSegment struct {
	media.Interval
	Frames int     `json:"frames"`
	Mean   float64 `json:"mean"`
	Peak   float64 `json:"peak"`
}

// pooledEstimate estimates the mean of a series: over every frame in exact
// mode, with the stratified estimator over the clip means otherwise. The
// estimator is the one of VMAF, applied to the same strata and clips.
func pooledEstimate(
	s series,
	strata []*stratum,
	results []clipResult,
	exact bool,
	estimateOf estimator,
	confidence float64,
) Estimate {
	if exact {
		var all []float64
		for _, cr := range results {
			all = append(all, cr.values[s.name]...)
		}

		v := s.report(stats.Mean(all))

		return Estimate{Mean: v, Low: v, High: v}
	}

	est := estimateOf(seriesStrata(strata, results, s.name), confidence)
	mean := s.report(est.mean)
	low, high := s.report(est.mean-est.halfWidth), s.report(est.mean+est.halfWidth)

	if s.decreasing {
		low, high = high, low
	}

	return Estimate{Mean: mean, Low: low, High: high, HalfWidth: (high - low) / 2}
}

// seriesStrata returns copies of strata holding the clip means of a series
// instead of the primary VMAF, so that every metric gets the estimator of
// VMAF on the same strata and clips.
func seriesStrata(
	strata []*stratum,
	results []clipResult,
	name string,
) []*stratum {
	copies := make(map[*stratum]*stratum, len(strata))
	out := make([]*stratum, len(strata))

	for i, s := range strata {
		out[i] = &stratum{first: s.first, last: s.last, slots: s.slots}
		copies[s] = out[i]
	}

	for _, cr := range results {
		if values := cr.values[name]; len(values) > 0 {
			copies[cr.clip.stratum].addClip(stats.Mean(values), len(values))
		}
	}

	return out
}

// bandingOf finds the banded segments among the scored frames (in index
// order).
func bandingOf(
	frames []FrameScore,
	frameDuration media.Duration,
) *Banding {
	b := &Banding{Threshold: BandingThreshold}

	var (
		seg  *BandingSegment
		last media.Duration
	)

	for _, f := range frames {
		cambi, ok := f.Metrics[SeriesCAMBI]
		if !ok {
			continue
		}

		if cambi <= BandingThreshold {
			seg = nil

			continue
		}

		b.BandedFrames++

		if f.Metrics[SeriesCAMBISource] > BandingThreshold {
			b.SourceFrames++
		}

		if seg == nil || f.PTS-last > bandingJoin {
			b.Segments = append(b.Segments, BandingSegment{Interval: media.Interval{Start: f.PTS}})
			seg = &b.Segments[len(b.Segments)-1]
		}

		seg.Mean = (seg.Mean*float64(seg.Frames) + cambi) / float64(seg.Frames+1)
		seg.Frames++
		seg.Peak = max(seg.Peak, cambi)
		seg.End = f.PTS + frameDuration
		last = f.PTS
	}

	return b
}

// frameDuration is the duration of one frame at rate (0 when unknown).
func frameDuration(
	rate media.Rational,
) media.Duration {
	fps := rate.Float()
	if fps <= 0 {
		return 0
	}

	return media.Seconds(1 / fps)
}

// RawSeries returns the per-frame raw values of a series of a result: the
// quantity whose mean is estimated. It is the reported value, except for
// XPSNR whose distortion √WSSE is recovered from its dB value (exact below
// the maxDecibels cap). ok is false when no frame carries the series.
func RawSeries(
	res *Result,
	name string,
) ([]float64, bool) {
	w, h := res.Model.Width, res.Model.Height
	if name != SeriesXPSNRY {
		w, h = (w+1)/2, (h+1)/2
	}

	peak := float64(int(1)<<res.BitDepth - 1)
	isXPSNR := strings.HasPrefix(name, "xpsnr_")

	out := make([]float64, 0, len(res.Frames))

	for _, f := range res.Frames {
		v, ok := f.Metrics[name]
		if !ok {
			continue
		}

		if isXPSNR {
			v = math.Sqrt(float64(w*h) * peak * peak / math.Pow(10, v/10))
		}

		out = append(out, v)
	}

	return out, len(out) > 0
}

// SeriesInfo tells how to read a series.
type SeriesInfo struct {
	// Label is the display name.
	Label string
	// Unit is "dB" for decibel-like scores, "" otherwise.
	Unit string
	// LowerIsBetter is set for CAMBI: its worst frames are the highest.
	LowerIsBetter bool
	// Decimals is the precision worth displaying.
	Decimals int
}

// seriesInfo describes the known series.
var seriesInfo = map[string]SeriesInfo{
	SeriesXPSNRY:       {Label: "XPSNR Y", Unit: "dB", Decimals: 2},
	SeriesXPSNRU:       {Label: "XPSNR U", Unit: "dB", Decimals: 2},
	SeriesXPSNRV:       {Label: "XPSNR V", Unit: "dB", Decimals: 2},
	SeriesCAMBI:        {Label: "CAMBI (banding)", LowerIsBetter: true, Decimals: 2},
	SeriesCAMBISource:  {Label: "CAMBI of the reference", LowerIsBetter: true, Decimals: 2},
	SeriesPSNRY:        {Label: "PSNR Y", Unit: "dB", Decimals: 2},
	SeriesPSNRCb:       {Label: "PSNR Cb", Unit: "dB", Decimals: 2},
	SeriesPSNRCr:       {Label: "PSNR Cr", Unit: "dB", Decimals: 2},
	SeriesPSNRYUV:      {Label: "PSNR YUV 14:1:1", Unit: "dB", Decimals: 2},
	SeriesPSNRHVS:      {Label: "PSNR-HVS", Unit: "dB", Decimals: 2},
	SeriesSSIM:         {Label: "SSIM", Decimals: 4},
	SeriesMSSSIM:       {Label: "MS-SSIM", Decimals: 4},
	SeriesCIEDE2000:    {Label: "CIEDE2000", Unit: "dB", Decimals: 2},
	SeriesWPSNRY:       {Label: "wPSNR Y", Unit: "dB", Decimals: 2},
	SeriesWPSNRCb:      {Label: "wPSNR Cb", Unit: "dB", Decimals: 2},
	SeriesWPSNRCr:      {Label: "wPSNR Cr", Unit: "dB", Decimals: 2},
	SeriesDeltaEITP:    {Label: "ΔE ITP", LowerIsBetter: true, Decimals: 2},
	SeriesDeltaEITPP99: {Label: "ΔE ITP p99", LowerIsBetter: true, Decimals: 2},
}

// DescribeSeries returns how to read a series; unknown names are shown as
// is.
func DescribeSeries(
	name string,
) SeriesInfo {
	if info, ok := seriesInfo[name]; ok {
		return info
	}

	return SeriesInfo{Label: name, Decimals: 2}
}

// Worst is the value of the worst 5% of the scored frames: p5, or p95 when
// lower is better.
func (i SeriesInfo) Worst(
	s stats.Summary,
) float64 {
	if i.LowerIsBetter {
		return s.P95
	}

	return s.P5
}

// ShortLabel is the label without its parenthesised hint ("CAMBI
// (banding)" is "CAMBI"), for column headers.
func (i SeriesInfo) ShortLabel() string {
	label, _, _ := strings.Cut(i.Label, " (")

	return label
}

// Format prints a value at the precision of the series.
func (i SeriesInfo) Format(
	v float64,
) string {
	return strconv.FormatFloat(v, 'f', i.Decimals, 64)
}

// Metric returns the measurement of a series (e.g. SeriesXPSNRY), if it was
// measured.
func (r *Result) Metric(
	name string,
) (MetricResult, bool) {
	for _, m := range r.Metrics {
		if m.Name == name {
			return m, true
		}
	}

	return MetricResult{}, false
}

// Device returns the VMAF of a device (e.g. vmaf.DevicePhone), if it was
// measured.
func (r *Result) Device(
	name string,
) (DeviceResult, bool) {
	for _, d := range r.Devices {
		if d.Device == name {
			return d, true
		}
	}

	return DeviceResult{}, false
}
