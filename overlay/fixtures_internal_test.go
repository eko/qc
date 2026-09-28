package overlay

import (
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/black"
	"github.com/eko/qc/analyze/crop"
	"github.com/eko/qc/analyze/freeze"
	"github.com/eko/qc/analyze/levels"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// testFont is the font of the golden scripts, whatever the platform.
const testFont = "DejaVu Sans Mono"

// frameStep is the frame interval of the synthetic title (25 fps).
const frameStep = 40 * time.Millisecond

// syntheticFrames is the frame count of the synthetic title.
const syntheticFrames = 12

// ptsAt returns n frame times every step.
func ptsAt(
	n int,
	step time.Duration,
) []media.Duration {
	pts := make([]media.Duration, n)
	for i := range pts {
		pts[i] = media.Duration(time.Duration(i) * step)
	}

	return pts
}

// inspection is the inspection of a synthetic 12-frame 25 fps 1920×1080
// title whose first frame is 40 ms into the container's timeline.
func inspection() *analysis.Report {
	pts := ptsAt(syntheticFrames, frameStep)
	sizes := []int{90_000, 800, 20_000, 21_000, 19_500, 22_000, 85_000, 30_000, 31_000, 30_500, 29_000, 28_000}
	keys := make([]bool, syntheticFrames)
	keys[0], keys[6] = true, true

	return &analysis.Report{
		Info: &media.Info{
			Path:      "/videos/my {clip}.mp4",
			StartTime: 0,
			Video:     []media.VideoStream{{Width: 1920, Height: 1080, Color: media.Color{Range: "tv"}}},
			Audio:     []media.AudioStream{{Codec: "aac"}},
		},
		Bitstream: &bitstream.Report{
			PacketCount: syntheticFrames,
			Duration:    media.Duration(syntheticFrames * frameStep),
			Start:       media.Duration(frameStep),
			PTS:         pts,
			FrameSizes:  sizes,
			KeyFlags:    keys,
		},
	}
}

// analysed is the inspection with a frame analysis: two shots (a pan then
// a shaky handheld shot), a black and a frozen segment, an out-of-range
// frame, letterboxing and HDR light levels.
func analysed() *analysis.Report {
	report := inspection()
	pts := report.Bitstream.PTS

	report.Video = &analysis.VideoReport{
		Levels: levels.Result{Levels: media.Levels{Black: 16, White: 235}, OutOfRange: []float64{0, 0, 0, 0.02}},
		Shots: []analysis.ShotReport{
			{
				Shot:   scene.Shot{Interval: media.Interval{Start: 0, End: pts[6]}, FirstFrame: 0, LastFrame: 5},
				Camera: &motion.Shot{Class: motion.ClassPan, Direction: motion.DirectionRight},
			},
			{
				Shot:   scene.Shot{Interval: media.Interval{Start: pts[6], End: media.Seconds(0.48)}, FirstFrame: 6, LastFrame: 11},
				Camera: &motion.Shot{Class: motion.ClassHandheld, Shaky: true},
			},
		},
		Black:  black.Result{Segments: []media.Interval{{Start: pts[1], End: pts[3]}}},
		Freeze: freeze.Result{Segments: []media.Interval{{Start: pts[8], End: pts[10]}}},
		Crop:   crop.Result{Letterbox: true},
	}

	report.Frames = &analysis.FrameSeries{
		PTS:              pts,
		SI:               []float64{52.4, 10, 48.6, 49, 50, 51, 30.2, 31, 31, 31, 32, 33},
		TI:               []float64{0, 40, 12.2, 11, 10, 9, 60, 5, 0, 0, 4, 3},
		LumaMean:         []float64{98, 16, 97, 96, 95, 94, 120, 121, 121, 121, 122, 123},
		LumaMin:          []float64{16, 16, 16, 4, 16, 16, 20, 20, 20, 20, 20, 20},
		LumaMax:          []float64{235, 16, 235, 250, 235, 235, 230, 230, 230, 230, 230, 230},
		PeakNits:         []float64{850, 0, 840, 830, 820, 810, 400, 400, 400, 400, 410, 420},
		AverageNits:      []float64{120, 0, 118, 117, 116, 115, 80, 80, 80, 80, 81, 82},
		MotionPan:        []float64{0, 8, 8, 8, 20, 20, 0, 0.2, -0.3, 0.1, 0, 0},
		MotionTilt:       []float64{0, 0, 3, 3, -1, 0, 0, 0.1, 0.2, -0.2, 0, 0},
		MotionZoom:       []float64{0, 0, 0, 1.25, 1.25, 0, 0, 0, 0, 0, 0, -0.04},
		MotionConfidence: []float64{0, 0.8, 0.8, 0.8, 0.9, 0.9, 0, 0.5, 0.5, 0.5, 0.5, 0.5},
	}

	return report
}

// exactQuality is an exact comparison scoring every frame, with XPSNR,
// CAMBI and a banded segment.
func exactQuality() *quality.Result {
	pts := ptsAt(syntheticFrames, frameStep)
	scores := []float64{95.2, 91, 88.4, 85, 79.9, 70, 96, 96.04, 96.1, 97, 98, 99}

	res := &quality.Result{
		Mode:         quality.ModeExact,
		FramesTotal:  syntheticFrames,
		FramesScored: syntheticFrames,
		Metrics:      []quality.MetricResult{{Name: quality.SeriesCAMBI}, {Name: quality.SeriesXPSNRY}},
		Banding: &quality.Banding{Threshold: 5, Segments: []quality.BandingSegment{
			{Interval: media.Interval{Start: pts[4], End: pts[6]}},
		}},
	}

	for i, score := range scores {
		res.Frames = append(res.Frames, quality.FrameScore{
			Index: i, PTS: pts[i], Score: score,
			Metrics: map[string]float64{quality.SeriesXPSNRY: 30 + score/10, quality.SeriesCAMBI: float64(i) / 2},
		})
	}

	return res
}
