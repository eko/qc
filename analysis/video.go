package analysis

import (
	"context"

	"github.com/eko/qc/analyze"
	"github.com/eko/qc/analyze/black"
	"github.com/eko/qc/analyze/crop"
	"github.com/eko/qc/analyze/freeze"
	"github.com/eko/qc/analyze/levels"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/analyze/siti"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
)

// thumbMaxWidth bounds the width of the thumbnails used by cheap analyzers.
const thumbMaxWidth = 240

// VideoOptions tunes the decoded-frame analyzers. The zero value is valid.
type VideoOptions struct {
	Scene  scene.Options
	Black  black.Options
	Freeze freeze.Options
	Crop   crop.Options
	// Workers is the SI/TI worker count (0 = NumCPU).
	Workers int
}

// VideoReport summarises the decoded-frame analysis.
type VideoReport struct {
	FramesDecoded int            `json:"framesDecoded"`
	Levels        levels.Result  `json:"luma"`
	SITI          siti.Result    `json:"siti"`
	Shots         []ShotReport   `json:"shots"`
	Black         black.Result   `json:"black"`
	Freeze        freeze.Result  `json:"freeze"`
	Crop          crop.Result    `json:"crop"`
	Complexity    ComplexityHint `json:"complexity"`
}

// ShotReport enriches a shot with its complexity and cost. Shots are the
// strata used later to sample frames for quality measurement.
type ShotReport struct {
	scene.Shot
	Frames  int     `json:"frames"`
	SIMean  float64 `json:"siMean"`
	TIMean  float64 `json:"tiMean"`
	Bitrate int64   `json:"bitrate"`
}

// ComplexityHint is a coarse encoding difficulty classification from SI/TI.
type ComplexityHint struct {
	Spatial  string `json:"spatial"`
	Temporal string `json:"temporal"`
}

// FrameSeries holds per-frame values as columns, which keeps the JSON compact
// and ready for charting.
type FrameSeries struct {
	PTS        []media.Duration `json:"pts"`
	Size       []int            `json:"size"`
	Keyframe   []bool           `json:"keyframe"`
	SI         []float64        `json:"si"`
	TI         []float64        `json:"ti"`
	SceneScore []float64        `json:"sceneScore"`
	LumaMean   []float64        `json:"lumaMean"`
	LumaMin    []float64        `json:"lumaMin"`
	LumaMax    []float64        `json:"lumaMax"`
}

// videoAnalyzers are the analyzers fed by the single decode of stage 2.
type videoAnalyzers struct {
	siti   *siti.Analyzer
	levels *levels.Analyzer
	scene  *scene.Analyzer
	black  *black.Analyzer
	freeze *freeze.Analyzer
	crop   *crop.Analyzer
}

func newVideoAnalyzers(
	video media.VideoStream,
	opts VideoOptions,
) videoAnalyzers {
	lv := media.LevelsFor(video.Color.Range)

	return videoAnalyzers{
		siti:   siti.New(opts.Workers),
		levels: levels.New(lv),
		scene:  scene.New(opts.Scene),
		black:  black.New(lv, opts.Black),
		freeze: freeze.New(opts.Freeze),
		crop:   crop.New(video.Width, video.Height, lv, opts.Crop),
	}
}

func (v videoAnalyzers) list() []analyze.Analyzer {
	return []analyze.Analyzer{v.siti, v.levels, v.scene, v.black, v.freeze, v.crop}
}

// analyzeVideo runs stage 2: it decodes the primary video once, fans the
// frames out to every analyzer and fills report.Video and report.Frames.
func (a *Analyzer) analyzeVideo(
	ctx context.Context,
	path string,
	opts Options,
	report *Report,
	progress func(Progress),
) error {
	video, _ := report.Info.PrimaryVideo()
	bs := report.Bitstream
	analyzers := newVideoAnalyzers(video, opts.Video)

	req := decode.Request{
		Path:         path,
		SourceWidth:  video.Width,
		SourceHeight: video.Height,
		Pool:         frame.NewPool(video.Width, video.Height, frame.PoolOptions{ThumbMaxWidth: thumbMaxWidth}),
		PTS:          bs.PTS,
		FrameRate:    video.AvgFrameRate,
		Codec:        video.Codec,
	}

	decoded := 0
	onFrame := func(frames int) {
		decoded = frames
		progress(Progress{Stage: StageDecode, Done: frames, Total: bs.PacketCount})
	}

	if err := analyze.Run(ctx, a.decoder, req, analyzers.list(), onFrame); err != nil {
		return err
	}

	end := bs.Duration
	sitiResult := analyzers.siti.Result()
	levelsResult := analyzers.levels.Result()
	sceneResult := analyzers.scene.Result(end)

	report.Video = &VideoReport{
		FramesDecoded: decoded,
		Levels:        levelsResult,
		SITI:          sitiResult,
		Shots:         shotReports(sceneResult.Shots, sitiResult, bs),
		Black:         analyzers.black.Result(end),
		Freeze:        analyzers.freeze.Result(end),
		Crop:          analyzers.crop.Result(),
		Complexity:    classify(sitiResult),
	}

	report.Frames = frameSeries(decoded, bs, sitiResult, sceneResult, levelsResult)

	return nil
}

// shotReports enriches shots with their SI/TI means and bitrate. Frames
// beyond the SI/TI series (never expected: both come from the same decode)
// are ignored rather than trusted.
func shotReports(
	shots []scene.Shot,
	s siti.Result,
	bs *bitstream.Report,
) []ShotReport {
	out := make([]ShotReport, len(shots))

	for i, shot := range shots {
		out[i] = ShotReport{Shot: shot}

		first, last := shot.FirstFrame, min(shot.LastFrame, len(s.SI)-1)
		if last < first {
			continue
		}

		// The first TI of a shot measures the cut itself, not the shot motion.
		tiFrom := min(first+1, last)

		out[i].Frames = last - first + 1
		out[i].SIMean = stats.Mean(s.SI[first : last+1])
		out[i].TIMean = stats.Mean(s.TI[tiFrom : last+1])
		out[i].Bitrate = shotBitrate(shot, bs.FrameSizes)
	}

	return out
}

// shotBitrate is the bitrate of the packets of a shot, in bits per second.
func shotBitrate(
	shot scene.Shot,
	frameSizes []int,
) int64 {
	length := shot.Length().Seconds()
	if length <= 0 {
		return 0
	}

	var bytes int64
	for j := shot.FirstFrame; j <= shot.LastFrame && j < len(frameSizes); j++ {
		bytes += int64(frameSizes[j])
	}

	return int64(float64(bytes*8) / length)
}

// Complexity buckets of the SI/TI means. They follow the usual P.910 SI/TI
// plane reading on 8-bit luma: indicative, not normative.
const (
	siMediumFrom = 30
	siHighFrom   = 70
	tiMediumFrom = 8
	tiHighFrom   = 25
)

// Complexity levels of a ComplexityHint.
const (
	complexityLow    = "low"
	complexityMedium = "medium"
	complexityHigh   = "high"
)

// classify buckets SI/TI means into a ComplexityHint.
func classify(
	s siti.Result,
) ComplexityHint {
	return ComplexityHint{
		Spatial:  bucket(s.SISummary.Mean, siMediumFrom, siHighFrom),
		Temporal: bucket(s.TISummary.Mean, tiMediumFrom, tiHighFrom),
	}
}

func bucket(
	v, mediumFrom, highFrom float64,
) string {
	switch {
	case v < mediumFrom:
		return complexityLow
	case v < highFrom:
		return complexityMedium
	default:
		return complexityHigh
	}
}

// frameSeries builds the per-frame columns, truncated to the shortest series
// so every column has one value per frame.
func frameSeries(
	n int,
	bs *bitstream.Report,
	s siti.Result,
	sc scene.Result,
	lv levels.Result,
) *FrameSeries {
	n = min(n, len(s.SI), len(sc.Scores), len(lv.Mean))
	packets := min(n, len(bs.PTS))

	series := &FrameSeries{
		PTS:        make([]media.Duration, n),
		Size:       make([]int, n),
		Keyframe:   make([]bool, n),
		SI:         s.SI[:n],
		TI:         s.TI[:n],
		SceneScore: sc.Scores[:n],
		LumaMean:   lv.Mean[:n],
		LumaMin:    lv.Min[:n],
		LumaMax:    lv.Max[:n],
	}

	copy(series.PTS, bs.PTS[:packets])
	copy(series.Size, bs.FrameSizes[:packets])
	copy(series.Keyframe, bs.KeyFlags[:packets])

	return series
}
