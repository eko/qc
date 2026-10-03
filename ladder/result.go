package ladder

import (
	"fmt"
	"iter"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

// Result is a ladder and the measurements behind it.
type Result struct {
	SchemaVersion int              `json:"schemaVersion"`
	GeneratedAt   time.Time        `json:"generatedAt"`
	Source        *analysis.Report `json:"source"`
	// Sources are the videos of a program (Engine.BuildProgram), the first
	// being Source; nil for the ladder of one title.
	Sources []*analysis.Report `json:"sources,omitempty"`
	Codec   encode.Codec       `json:"codec"`
	Preset  string             `json:"preset"`
	// GOP is the keyframe interval of every encode, in frames; BitDepth
	// their depth (0 or 8, or 10). With Preset, the rungs' resolution and
	// rate cap and the ladder's grain and signal, they are the settings of
	// the rungs' commands (see RungParams).
	GOP         int           `json:"gop"`
	BitDepth    int           `json:"bitDepth,omitempty"`
	Constraints Constraints   `json:"constraints"`
	Shape       Shape         `json:"shape"`
	Digest      Digest        `json:"digest"`
	Probing     ProbingReport `json:"probing"`
	// Grain describes AV1 film grain synthesis, when requested.
	Grain *GrainReport `json:"grain,omitempty"`
	// HDR describes how the ladder of an HDR source keeps its signal; nil
	// for SDR.
	HDR    *HDRLadder  `json:"hdr,omitempty"`
	Probes []Probe     `json:"probes"`
	Hull   []HullPoint `json:"hull"`
	Rungs  []Rung      `json:"rungs"`
	// Shots are the shots of the title per-shot rungs allocate (PerShot).
	Shots []Shot `json:"shots,omitempty"`
	// ShotProbing is the cost of the per-shot rungs in exact probes.
	ShotProbing *ShotProbing `json:"shotProbing,omitempty"`
	// Renditions are the rungs encoded on the whole title (Engine.Encode).
	Renditions []Rendition       `json:"renditions,omitempty"`
	Timings    map[string]string `json:"timings"`
	Elapsed    media.Duration    `json:"elapsed"`
}

// Digest describes the representative extract the ladder is estimated on.
type Digest struct {
	Segments []media.Interval `json:"segments"`
	Duration media.Duration   `json:"duration"`
	Share    float64          `json:"share"`
	// Titles describes the part of the digest of each video of a program,
	// in the order of Result.Sources: its Segments come one video after
	// the other. Empty for one title.
	Titles []DigestTitle `json:"titles,omitempty"`
	// Sampling is how the segments were placed; empty for a title used
	// whole. A balanced or top digest whose source analysis had no SI and
	// TI is uniform.
	Sampling DigestSampling `json:"sampling,omitempty"`
	// Complexity compares the digest with its title, when the frame
	// analysis of the source was read.
	Complexity *DigestComplexity `json:"complexity,omitempty"`
}

// DigestTitle is the part of the digest of a program taken in one video.
type DigestTitle struct {
	Source string `json:"source"`
	// Segments counts its segments, and Duration is their length.
	Segments int            `json:"segments"`
	Duration media.Duration `json:"duration"`
	// Complexity compares them with the video they come from, when its
	// frame analysis was read.
	Complexity *DigestComplexity `json:"complexity,omitempty"`
}

// DigestComplexity is the mean spatial and temporal information (ITU-T
// P.910) of the frames of the digest and of the whole title: a balanced
// digest has the title's, a top digest is above them.
type DigestComplexity struct {
	TitleSI float64 `json:"titleSi"`
	TitleTI float64 `json:"titleTi"`
	SI      float64 `json:"si"`
	TI      float64 `json:"ti"`
}

// Rung is one rendition of the ladder.
type Rung struct {
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Bitrate int64   `json:"bitrate"`
	CRF     float64 `json:"crf"`
	MaxRate int64   `json:"maxRate"`
	BufSize int64   `json:"bufSize"`
	// PredictedVMAF is read on the rate-quality curve of the resolution.
	PredictedVMAF float64 `json:"predictedVmaf"`
	// PredictionError is the 95% half-width of PredictedVMAF under the
	// fitted curve model, measurement noise included (adaptive probing).
	PredictionError float64 `json:"predictionError,omitempty"`
	// Measured is the verification encode of the digest, when run.
	Measured *Measurement `json:"measured,omitempty"`
	// Calibrated is set when verification missed the prediction and the CRF
	// was corrected (Measured is then the corrected encode).
	Calibrated bool `json:"calibrated,omitempty"`
	// Extrapolated is set when an imposed resolution had to reach a quality
	// outside its probed range: verification then matters most.
	Extrapolated bool   `json:"extrapolated,omitempty"`
	Command      string `json:"command"`
	// Grain compares the rung's synthesised grain with the source's.
	Grain *GrainCheck `json:"grain,omitempty"`
	// PerShot is the per-shot version of the rung (Options.PerShot).
	PerShot *PerShot `json:"perShot,omitempty"`
	// PerShotRejected is the per-shot version the verification rejected:
	// encoded on the digest, it cost more than the rung itself at equal
	// VMAF (its Gain is not positive). The rung then has no per-shot
	// version: the shot models were wrong for it, most often where quality
	// saturates, at the top of the ladder.
	PerShotRejected *PerShot `json:"perShotRejected,omitempty"`
}

// Measurement is a measured encode.
type Measurement struct {
	Bitrate   int64   `json:"bitrate"`
	VMAF      float64 `json:"vmaf"`
	HalfWidth float64 `json:"vmafHalfWidth"`
	// Metrics are the means of the extra metric series of a verified rung
	// (quality.SeriesXPSNRY, quality.SeriesCAMBI...), keyed by series.
	Metrics map[string]float64 `json:"metrics,omitempty"`
	// Devices is the VMAF of each viewing device measured on a verified rung.
	Devices map[string]float64 `json:"devices,omitempty"`
	// Titles reads the measurement video by video, for the ladder of a
	// program (Engine.BuildProgram): one entry per video, in the order of
	// Result.Sources.
	Titles []TitleMeasurement `json:"titles,omitempty"`
	// BandedFrames counts the scored frames with visible banding (CAMBI
	// above quality.BandingThreshold) when CAMBI is measured, out of
	// ScoredFrames.
	BandedFrames int `json:"bandedFrames,omitempty"`
	ScoredFrames int `json:"scoredFrames,omitempty"`
	// SourceBandedFrames counts the banded frames whose source frame is
	// banded too: banding the rung inherited, which no encoding setting
	// removes.
	SourceBandedFrames int `json:"sourceBandedFrames,omitempty"`
}

// TitleMeasurement is the part of a measurement of an encode of the digest
// that falls in one video of a program.
type TitleMeasurement struct {
	// Bitrate is measured on every frame of the video's segments.
	Bitrate int64 `json:"bitrate"`
	// VMAF is the mean of the scored frames among them: a sampled
	// measurement scores some only (ScoredFrames, 0 when none fell there).
	VMAF         float64 `json:"vmaf,omitempty"`
	ScoredFrames int     `json:"scoredFrames,omitempty"`
}

// VMAFLabel is the measured VMAF with its 95% confidence interval, or
// "exact" when every frame was scored.
func (m Measurement) VMAFLabel() string {
	if m.HalfWidth == 0 {
		return fmt.Sprintf("%.1f (exact)", m.VMAF)
	}

	return fmt.Sprintf("%.1f ± %.1f", m.VMAF, m.HalfWidth)
}

// BandedShare is the share of the scored frames with visible banding.
func (m Measurement) BandedShare() float64 {
	if m.ScoredFrames == 0 {
		return 0
	}

	return float64(m.BandedFrames) / float64(m.ScoredFrames)
}

// InheritedBanding is the share of the banded frames whose source frame is
// banded too.
func (m Measurement) InheritedBanding() float64 {
	if m.BandedFrames == 0 {
		return 0
	}

	return float64(m.SourceBandedFrames) / float64(m.BandedFrames)
}

// ShotInterval is the time range of a shot of the title, at the frame rate
// of the source that placed it (zero when the source has none).
func (r *Result) ShotInterval(
	s Shot,
) media.Interval {
	if r.Source == nil || r.Source.Info == nil {
		return media.Interval{}
	}

	video, _ := r.Source.Info.PrimaryVideo()

	rate := video.AvgFrameRate.Float()
	if rate <= 0 {
		rate = video.FrameRate.Float()
	}

	if rate <= 0 {
		return media.Interval{}
	}

	return media.Interval{Start: media.Seconds(float64(s.Start) / rate), End: media.Seconds(float64(s.Start+s.Frames) / rate)}
}

// ShotLadder iterates over the shots of the title with the allocation the
// per-shot version of rung (an index of Rungs) gives each: one column of the
// per-shot ladder, without matching Shots and PerShot.Shots by index. It
// yields nothing when the rung has no per-shot allocation of every shot.
func (r *Result) ShotLadder(
	rung int,
) iter.Seq2[Shot, ShotAllocation] {
	return func(yield func(Shot, ShotAllocation) bool) {
		if rung < 0 || rung >= len(r.Rungs) {
			return
		}

		ps := r.Rungs[rung].PerShot
		if ps == nil || len(ps.Shots) != len(r.Shots) {
			return
		}

		for i, s := range r.Shots {
			if !yield(s, ps.Shots[i]) {
				return
			}
		}
	}
}
