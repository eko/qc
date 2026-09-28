package htmlreport

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/internal/findings"
)

// motionDecodedReport adds the camera motion analysis to
// sampleDecodedReport: one shaky pan.
func motionDecodedReport() *analysis.Report {
	r := sampleDecodedReport()
	r.Video.Shots[0].Camera = &motion.Shot{Class: motion.ClassPan, Direction: motion.DirectionRight, Shaky: true, Shake: 0.6, Confidence: 0.9}
	r.Video.Motion = &motion.Summary{
		Classes:    []motion.ClassShare{{Class: motion.ClassPan, Shots: 1, Share: 1}},
		ShakyShots: 1, Reliable: 0.97,
	}

	f := r.Frames
	f.MotionPan, f.MotionTilt, f.MotionZoom, f.MotionRoll = f.SI, f.SI, f.SI, f.SI
	f.MotionShake, f.MotionConfidence = f.SI, f.SI

	return r
}

func TestRenderAnalysisMotion(
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
			report: motionDecodedReport(),
			want: []string{
				"Camera motion", "pan (%W/s)", "shake (%W)", "<b>100% · 1 shot</b>", "<b>97%</b>",
				"pan right, shaky", "100% <small>pan</small>", "1 shaky shot: camera jitter up to 0.6% of the width",
			},
		},
		{name: "motion skipped", report: sampleDecodedReport(), wantNot: []string{"Camera motion", "shaky"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, RenderAnalysis(&buf, testCase.report))

			out := buf.String()
			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, not := range testCase.wantNot {
				assert.NotContains(t, out, not)
			}
		})
	}
}

func TestCameraCell(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		camera *motion.Shot
		want   string
	}{
		{name: "not analysed", want: "–"},
		{name: "static", camera: &motion.Shot{Class: motion.ClassStatic}, want: "static"},
		{name: "zoom", camera: &motion.Shot{Class: motion.ClassZoom, Direction: motion.DirectionIn}, want: "zoom in"},
		{name: "handheld is shaky by nature", camera: &motion.Shot{Class: motion.ClassHandheld, Shaky: true}, want: "handheld"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, cameraCell(testCase.camera))
		})
	}
}

func TestMotionFindingOther(
	t *testing.T,
) {
	_, ok := motionFinding(findings.Finding{Code: findings.BlackBars})
	assert.False(t, ok)
	assert.Empty(t, capitalize(""))
}

func TestSymmetricLimit(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		want   float64
	}{
		{name: "empty", want: 1},
		{name: "negative peak", values: []float64{3, -17}, want: 20},
		{name: "exact bound", values: []float64{50}, want: 50},
		{name: "above five", values: []float64{-0.7, 64}, want: 100},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, symmetricLimit(testCase.values), 1e-9)
		})
	}
}
