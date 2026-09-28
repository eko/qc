package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/internal/findings"
)

// withMotion adds the camera motion analysis to a report: its fourth shot (among the hardest)

func withMotion(
	r *analysis.Report,
) *analysis.Report {
	r.Video.Shots[3].Camera = &motion.Shot{Class: motion.ClassPan, Direction: motion.DirectionLeft, Shaky: true, Shake: 0.7}
	r.Video.Motion = &motion.Summary{
		Classes: []motion.ClassShare{
			{Class: motion.ClassStatic, Share: 0.6}, {Class: motion.ClassPan, Share: 0.3}, {Class: motion.ClassZoom, Share: 0.1},
		},
		ShakyShots: 1,
	}

	f := r.Frames
	f.MotionPan, f.MotionTilt, f.MotionZoom, f.MotionShake = f.SI, f.TI, f.SI, f.TI

	return r
}

func TestCameraSection(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		report  *analysis.Report
		want    []string
		wantNot []string
	}{
		{
			name:   "motion",
			report: withMotion(sampleReport()),
			want: []string{
				"Camera", "static 60% · pan 30% · zoom 10%", "shaky 1", "pan/tilt", "shake",
				"pan ← ~", "1 shaky shot(s) (camera jitter up to 0.7% of the width)",
			},
		},
		{name: "motion skipped", report: sampleReport(), wantNot: []string{"pan/tilt", "shaky shot"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := strings.Join(plainLines(renderReport(t, testCase.report, 120, "")), "\n")

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, not := range testCase.wantNot {
				assert.NotContains(t, out, not)
			}
		})
	}
}

func TestCameraLabel(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		camera *motion.Shot
		want   string
	}{
		{name: "not analysed", want: "–"},
		{name: "tilt up", camera: &motion.Shot{Class: motion.ClassTilt, Direction: motion.DirectionUp}, want: "tilt ↑"},
		{name: "zoom out", camera: &motion.Shot{Class: motion.ClassZoom, Direction: motion.DirectionOut}, want: "zoom out"},
		{name: "handheld", camera: &motion.Shot{Class: motion.ClassHandheld, Shaky: true}, want: "handheld"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, cameraLabel(testCase.camera))
		})
	}
}

func TestMotionFindingTextOther(
	t *testing.T,
) {
	assert.Empty(t, motionFindingText(findings.Finding{Code: findings.BlackBars}))
}
