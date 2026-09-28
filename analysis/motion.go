package analysis

import "github.com/eko/qc/analyze/scene"

// addMotion classifies the camera work of every shot and adds it to the
// report: the title summary, each shot's camera work and the per-frame
// columns. It does nothing when the motion analysis was skipped.
func (v videoAnalyzers) addMotion(
	shots []scene.Shot,
	video *VideoReport,
	frames *FrameSeries,
) {
	if v.motion == nil {
		return
	}

	res := v.motion.Result(shots)
	video.Motion = &res.Summary

	for i := range video.Shots {
		if i < len(res.Shots) {
			video.Shots[i].Camera = &res.Shots[i]
		}
	}

	f := res.Frames
	n := min(len(frames.PTS), len(f.Pan))
	frames.MotionPan, frames.MotionTilt, frames.MotionZoom = f.Pan[:n], f.Tilt[:n], f.Zoom[:n]
	frames.MotionRoll, frames.MotionShake, frames.MotionConfidence = f.Roll[:n], f.Shake[:n], f.Confidence[:n]
}
