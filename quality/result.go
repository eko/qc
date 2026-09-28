package quality

import (
	"slices"
	"strings"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
	"github.com/eko/qc/vmaf"
)

// Result is a VMAF measurement.
type Result struct {
	Model      vmaf.ModelSpec `json:"model"`
	BitDepth   int            `json:"bitDepth"`
	Mode       string         `json:"mode"`
	Mean       float64        `json:"mean"`
	Low        float64        `json:"low"`
	High       float64        `json:"high"`
	HalfWidth  float64        `json:"halfWidth"`
	Confidence float64        `json:"confidence"`
	// HarmonicMean is only computed in exact mode.
	HarmonicMean float64 `json:"harmonicMean,omitempty"`
	// Fallback explains why an exact measurement replaced sampling.
	Fallback string `json:"fallback,omitempty"`
	// Sample describes the fixed budget of a sampled measurement, nil when
	// sampling was driven by a precision.
	Sample *SampleReport `json:"sample,omitempty"`
	// Plans counts the decoding plans used per round (sweep or seek runs).
	Plans map[string]int `json:"plans,omitempty"`
	// Scored summarises the frames actually scored. In sampled mode its low
	// percentiles are estimates and may miss isolated bad frames.
	Scored        stats.Summary   `json:"scored"`
	FramesTotal   int             `json:"framesTotal"`
	FramesScored  int             `json:"framesScored"`
	FramesDecoded int             `json:"framesDecoded"`
	Rounds        int             `json:"rounds"`
	Strata        []StratumResult `json:"strata"`
	Frames        []FrameScore    `json:"frames"`
	// Metrics holds the other metrics, one entry per series (psnr_y...).
	Metrics []MetricResult `json:"metrics,omitempty"`
	// Devices holds the VMAF of each requested viewing device.
	Devices []DeviceResult `json:"devices,omitempty"`
	// Banding lists the banded segments when CAMBI is measured.
	Banding *Banding `json:"banding,omitempty"`
	// HDR tells how VMAF was scored on a PQ or HLG reference, and how to
	// read it; nil for SDR.
	HDR *HDRReport `json:"hdr,omitempty"`
	// Backend is "cuda" when the model features were extracted on an
	// NVIDIA GPU, empty on the CPU.
	Backend string `json:"backend,omitempty"`
	// BackendNote explains why a GPU measurement ran on the CPU instead.
	BackendNote string `json:"backendNote,omitempty"`
	// HWAccel is the hardware decoding mode of the decoder ("cuda",
	// "cuda-scale", "videotoolbox"), empty on the CPU. Files the hardware
	// cannot decode still fall back to the CPU.
	HWAccel string         `json:"hwaccel,omitempty"`
	Elapsed media.Duration `json:"elapsed"`
}

// StratumResult describes one stratum of the sampling plan.
type StratumResult struct {
	media.Interval
	Frames      int     `json:"frames"`
	Clips       int     `json:"clips"`
	ClipsScored int     `json:"clipsScored"`
	Mean        float64 `json:"mean"`
}

// FrameScore is the VMAF of one frame, and the values of the other metrics
// and devices by series name (psnr_y, vmaf_phone...).
type FrameScore struct {
	Index   int                `json:"index"`
	PTS     media.Duration     `json:"pts"`
	Score   float64            `json:"score"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
}

// GPUSummary says in a few words what ran on a GPU: NVDEC or VideoToolbox
// decoding, CUDA feature extraction, or a CUDA request that fell back to
// the CPU (BackendNote says why). It is empty for a CPU-only measurement.
func (r *Result) GPUSummary() string {
	var parts []string

	switch r.HWAccel {
	case "":
	case string(decode.HWAccelVideoToolbox):
		parts = append(parts, "VideoToolbox decoding")
	default:
		parts = append(parts, "NVDEC decoding ("+r.HWAccel+")")
	}

	switch {
	case r.Backend != "":
		parts = append(parts, "VMAF features on "+strings.ToUpper(r.Backend))
	case r.BackendNote != "":
		parts = append(parts, "VMAF on the CPU (CUDA fallback, see backendNote)")
	}

	return strings.Join(parts, " · ")
}

// backendName is the reported name of a backend: empty on the CPU, so
// that CPU reports are unchanged.
func backendName(
	backend vmaf.Backend,
) string {
	if backend == vmaf.BackendCPU {
		return ""
	}

	return backend.String()
}

// hwAccel is the hardware decoding mode of decoders that report one.
func hwAccel(
	decoder decode.Source,
) string {
	if hw, ok := decoder.(decode.HWAccelReporter); ok && hw.HWAccel() != decode.HWAccelNone {
		return hw.HWAccel().String()
	}

	return ""
}

// result builds the report common to both modes: per-frame scores in index
// order, their summary and one entry per stratum.
func (r *run) result(
	strata []*stratum,
	results []clipResult,
	mode string,
) *Result {
	res := &Result{
		Model:         r.spec,
		BitDepth:      r.bitDepth,
		Mode:          mode,
		Confidence:    r.opts.Confidence,
		FramesTotal:   r.n,
		FramesScored:  r.scored,
		FramesDecoded: r.decoded,
		Backend:       backendName(r.backend.Backend),
		BackendNote:   r.backend.Reason,
		HWAccel:       hwAccel(r.meter.decoder),
		HDR:           hdrReport(r.ref.Video, r.opts.HDRMetric),
	}

	var all []float64

	pts := r.ref.Bitstream.PTS

	for _, cr := range results {
		for i, s := range cr.scores {
			idx := cr.clip.from + i
			res.Frames = append(res.Frames, FrameScore{Index: idx, PTS: pts[idx], Score: s, Metrics: r.frameMetrics(cr, i)})
			all = append(all, s)
		}
	}

	slices.SortFunc(res.Frames, func(a, b FrameScore) int { return a.Index - b.Index })
	res.Scored = stats.Summarize(all)

	for _, s := range strata {
		end := r.ref.Bitstream.Duration
		if s.last < len(pts) {
			end = pts[s.last]
		}

		sr := StratumResult{
			Interval:    media.Interval{Start: pts[s.first], End: end},
			Frames:      s.frames(),
			Clips:       len(s.slots),
			ClipsScored: len(s.clipMeans),
		}

		if len(s.clipMeans) > 0 {
			sr.Mean = s.mean()
		}

		res.Strata = append(res.Strata, sr)
	}

	return res
}

// harmonicMean follows libvmaf's convention: scores are shifted by 1 so that
// zero scores stay finite.
func harmonicMean(
	scores []float64,
) float64 {
	var sum float64
	for _, s := range scores {
		sum += 1 / (s + 1)
	}

	return float64(len(scores))/sum - 1
}
