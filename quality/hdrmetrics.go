package quality

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eko/qc/media"
)

// HDRMetric selects how VMAF is scored on an HDR (PQ or HLG) reference.
// VMAF's models were trained on SDR: there is no public HDR model.
type HDRMetric string

// HDR metric modes.
const (
	// HDRMetricPQ scores VMAF on the HDR signal as decoded (PQ or HLG code
	// values), the default: no extra work, fine for ranking encodes of one
	// title (a ladder), not calibrated as an absolute score on PQ. The HDR
	// metrics (wPSNR, ΔE ITP) are measured on the same frames.
	HDRMetricPQ HDRMetric = "pq"
	// HDRMetricToneMap scores VMAF on an SDR (BT.709) tone mapping of both
	// videos, done by ffmpeg while decoding: VMAF then sees what it was
	// trained on, as an SDR display would show the HDR picture. Slower:
	// tone mapping costs decoding time, and the HDR metrics need a second
	// decode of the scored clips.
	HDRMetricToneMap HDRMetric = "tonemap"
)

// ErrUnknownHDRMetric is returned for an HDR metric mode other than pq and
// tonemap.
var ErrUnknownHDRMetric = errors.New("unknown HDR metric")

// ParseHDRMetric reads an HDR metric mode: pq (or empty) or tonemap.
func ParseHDRMetric(
	s string,
) (HDRMetric, error) {
	switch m := HDRMetric(strings.ToLower(strings.TrimSpace(s))); m {
	case "", HDRMetricPQ:
		return HDRMetricPQ, nil
	case HDRMetricToneMap:
		return m, nil
	}

	return HDRMetricPQ, fmt.Errorf("%w %q (supported: pq, tonemap)", ErrUnknownHDRMetric, s)
}

// Series of the HDR metrics, measured on PQ and HLG references (see
// package quality/hdr).
const (
	SeriesWPSNRY       = "wpsnr_y"
	SeriesWPSNRCb      = "wpsnr_cb"
	SeriesWPSNRCr      = "wpsnr_cr"
	SeriesDeltaEITP    = "deltae_itp"
	SeriesDeltaEITPP99 = "deltae_itp_p99"
)

// hdrSeries are the series of the HDR metrics, in report order: wPSNR in
// dB (per-frame values averaged, like the JVET tools), ΔE ITP means.
var hdrSeries = []string{SeriesWPSNRY, SeriesWPSNRCb, SeriesWPSNRCr, SeriesDeltaEITP, SeriesDeltaEITPP99}

// hdrSeriesOf are the HDR series of a transfer: wPSNR is defined on PQ code
// values (the JVET conditions convert HLG to PQ first, a per-pixel
// conversion qc does not pay for), ΔE ITP on both (HLG on the 1000 cd/m²
// reference display of BT.2124).
func hdrSeriesOf(
	transfer string,
) []string {
	if transfer == media.TransferPQ {
		return hdrSeries
	}

	return hdrSeries[3:]
}

// HDRReport tells how a comparison with an HDR reference was measured.
type HDRReport struct {
	// Transfer is the reference's: media.TransferPQ or TransferHLG.
	Transfer string `json:"transfer"`
	// Metric is how VMAF was scored.
	Metric HDRMetric `json:"metric"`
	// Calibrated is false when VMAF was scored on the HDR signal, which its
	// models were not trained for.
	Calibrated bool `json:"vmafCalibrated"`
	// Note explains in a sentence how to read VMAF here.
	Note string `json:"note"`
}

// Notes of HDRReport.
const (
	notePQ = "VMAF was scored on the PQ signal: its models were trained on SDR and are not calibrated " +
		"for PQ. Use it to rank encodes of this title, not as an absolute score; wPSNR and ΔE ITP are HDR metrics."
	noteHLG = "VMAF was scored on the HLG signal: HLG follows a gamma curve over the SDR range, so VMAF " +
		"is closer to meaningful than on PQ, but it is not calibrated for HDR highlights."
	noteToneMap = "VMAF was scored on an SDR (BT.709) tone mapping of both videos: the quality an SDR " +
		"display would show. Highlights compressed by the tone mapping weigh less than on an HDR display."
)

// hdrReport describes the measurement of an HDR reference, nil for SDR.
func hdrReport(
	video media.VideoStream,
	metric HDRMetric,
) *HDRReport {
	if !video.MeasurableHDR() {
		return nil
	}

	r := &HDRReport{Transfer: video.Color.Transfer, Metric: metric, Note: notePQ}

	switch {
	case metric == HDRMetricToneMap:
		r.Calibrated, r.Note = true, noteToneMap
	case video.Color.Transfer == media.TransferHLG:
		r.Note = noteHLG
	}

	return r
}
