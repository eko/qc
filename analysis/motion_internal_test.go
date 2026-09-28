package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeMotion(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		skip       bool
		wantMotion bool
	}{
		{name: "on by default", wantMotion: true},
		{name: "skipped", skip: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			report, err := world{info: fakeVideo()}.analyzer().Analyze(t.Context(), "clip.mp4", Options{
				Video: VideoOptions{SkipMotion: testCase.skip},
			})
			require.NoError(t, err)

			v, f := report.Video, report.Frames
			require.NotNil(t, v)
			require.NotNil(t, f)

			if !testCase.wantMotion {
				assert.Nil(t, v.Motion)
				assert.Nil(t, v.Shots[0].Camera)
				assert.Empty(t, f.MotionPan)

				return
			}

			require.NotNil(t, v.Motion)
			assert.NotEmpty(t, v.Motion.Classes)

			for i, s := range v.Shots {
				assert.NotNil(t, s.Camera, "shot %d", i)
			}

			for _, column := range [][]float64{f.MotionPan, f.MotionTilt, f.MotionZoom, f.MotionRoll, f.MotionShake, f.MotionConfidence} {
				assert.Len(t, column, len(f.PTS))
			}
		})
	}
}
