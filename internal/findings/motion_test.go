package findings

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/media"
)

func shotWithCamera(
	from, to float64,
	camera *motion.Shot,
) analysis.ShotReport {
	return analysis.ShotReport{Shot: scene.Shot{Interval: interval(from, to)}, Camera: camera}
}

func TestMotionFindings(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		video *analysis.VideoReport
		want  []Finding
	}{
		{
			name:  "motion analysis skipped",
			video: &analysis.VideoReport{Shots: []analysis.ShotReport{shotWithCamera(0, 1, nil)}},
		},
		{
			name: "no shaky shot",
			video: &analysis.VideoReport{
				Motion: &motion.Summary{},
				Shots:  []analysis.ShotReport{shotWithCamera(0, 1, &motion.Shot{Class: motion.ClassPan}), shotWithCamera(1, 2, nil)},
			},
		},
		{
			name: "shaky shots",
			video: &analysis.VideoReport{
				Motion: &motion.Summary{ShakyShots: 2},
				Shots: []analysis.ShotReport{
					shotWithCamera(0, 1, &motion.Shot{Class: motion.ClassHandheld, Shaky: true, Shake: 0.4}),
					shotWithCamera(1, 2, &motion.Shot{Class: motion.ClassStatic}),
					shotWithCamera(2, 3, &motion.Shot{Class: motion.ClassPan, Shaky: true, Shake: 0.9}),
				},
			},
			want: []Finding{{Level: Info, Code: ShakyShots, Topic: TopicMotion, Spans: []media.Interval{interval(0, 1), interval(2, 3)}, Value: 0.9}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, motionFindings(testCase.video))
		})
	}
}

func TestAnalysisWithShakyShots(
	t *testing.T,
) {
	r := report()
	r.Video.Motion = &motion.Summary{ShakyShots: 1}
	r.Video.Shots = []analysis.ShotReport{shotWithCamera(0, 1, &motion.Shot{Class: motion.ClassHandheld, Shaky: true})}

	assert.Equal(t, [][2]string{{"ok", string(NoBlackOrFrozen)}, {"info", string(ShakyShots)}}, codes(Analysis(r)))
}
