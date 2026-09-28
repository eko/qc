package findings

import (
	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

// TopicMotion links a finding to the camera motion chart.
const TopicMotion Topic = "motion"

// Codes of the camera motion analysis.
const (
	// ShakyShots: shots whose camera path jitters (motion.Shot.Shaky), a
	// handheld or unstabilised camera. Spans are the shaky shots, Value
	// the highest shake (% of the picture width, median jitter). It is a
	// note, not an issue: shaky footage is often intended, but it costs
	// bits to encode and may call for stabilisation.
	ShakyShots Code = "shaky-shots"
)

// motionFindings lists the findings of the camera motion analysis.
func motionFindings(
	v *analysis.VideoReport,
) []Finding {
	if v.Motion == nil {
		return nil
	}

	var (
		spans []media.Interval
		worst float64
	)

	for _, s := range v.Shots {
		if s.Camera != nil && s.Camera.Shaky {
			spans = append(spans, s.Interval)
			worst = max(worst, s.Camera.Shake)
		}
	}

	if len(spans) == 0 {
		return nil
	}

	return []Finding{{Level: Info, Code: ShakyShots, Topic: TopicMotion, Spans: spans, Value: worst}}
}
