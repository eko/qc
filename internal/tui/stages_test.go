package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/siti"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
)

func TestStageSummary(
	t *testing.T,
) {
	inspection := &analysis.Report{Info: &media.Info{
		Duration: media.Seconds(61.4),
		Video: []media.VideoStream{{
			Codec: "h264", Width: 1920, Height: 1080, AvgFrameRate: media.Rational{Num: 25, Den: 1},
		}},
	}}
	frames := &analysis.Report{Info: inspection.Info, Video: &analysis.VideoReport{
		Shots: make([]analysis.ShotReport, 3),
		SITI:  siti.Result{SISummary: stats.Summary{Mean: 42.4}, TISummary: stats.Summary{Mean: 7.6}},
	}}
	rungs := []ladder.Rung{{Height: 1080, Bitrate: 4_500_000}, {Height: 720, Bitrate: 2_000_000}}

	nvencH264, err := encode.LookupFor("h264", encode.HardwareNVENC)
	require.NoError(t, err)

	stage := func(kind string) pipeline.Stage { return pipeline.Stage{Kind: kind} }

	testCases := []struct {
		name   string
		result pipeline.StageResult
		want   string
	}{
		{
			name:   "inspection",
			result: pipeline.StageResult{Stage: stage(pipeline.KindInspect), Analysis: inspection},
			want:   "h264 · 1920×1080 · 25 fps · 1m1s",
		},
		{
			name:   "frame analysis",
			result: pipeline.StageResult{Stage: stage(pipeline.KindAnalysis), Analysis: frames},
			want:   "3 shots · SI 42 · TI 8",
		},
		{
			name: "single shot",
			result: pipeline.StageResult{Stage: stage(pipeline.KindAnalysis), Analysis: &analysis.Report{Info: inspection.Info, Video: &analysis.VideoReport{
				Shots: make([]analysis.ShotReport, 1),
			}}},
			want: "1 shot · SI 0 · TI 0",
		},
		{
			name: "sampled vmaf",
			result: pipeline.StageResult{Stage: stage(pipeline.KindVMAF), Comparison: &analysis.Comparison{
				VMAF: &quality.Result{Mean: 93.456, HalfWidth: 0.41},
			}},
			want: "93.46 ± 0.41",
		},
		{
			name: "exact vmaf",
			result: pipeline.StageResult{Stage: stage(pipeline.KindVMAF), Comparison: &analysis.Comparison{
				VMAF: &quality.Result{Mean: 91},
			}},
			want: "91.00 (exact)",
		},
		{
			name: "vmaf on the gpu",
			result: pipeline.StageResult{Stage: stage(pipeline.KindVMAF), Comparison: &analysis.Comparison{
				VMAF: &quality.Result{Mean: 91, Backend: "cuda"},
			}},
			want: "91.00 (exact) · CUDA",
		},
		{
			name:   "ladder",
			result: pipeline.StageResult{Stage: stage(pipeline.KindLadder), Ladder: &ladder.Result{Rungs: rungs}},
			want:   "2 rungs · top 1080p @ 4.50 Mb/s",
		},
		{
			name:   "ladder on the gpu",
			result: pipeline.StageResult{Stage: stage(pipeline.KindLadder), Ladder: &ladder.Result{Rungs: rungs, Codec: nvencH264}},
			want:   "2 rungs · top 1080p @ 4.50 Mb/s · h264_nvenc",
		},
		{
			name:   "ladder without rungs",
			result: pipeline.StageResult{Stage: stage(pipeline.KindLadder), Ladder: &ladder.Result{}},
			want:   "no rung",
		},
		{
			name:   "overlay",
			result: pipeline.StageResult{Stage: stage(pipeline.KindOverlay), Overlay: "out/annotated.mp4"},
			want:   "annotated copy · annotated.mp4",
		},
		{
			name:   "unknown stage",
			result: pipeline.StageResult{Stage: stage("teleport")},
			want:   "",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, StageSummary(testCase.result))
		})
	}
}

// TestFindingWordingUnknownCode checks that a finding a report does not
// know is left out rather than worded wrongly.
func TestFindingWordingUnknownCode(
	t *testing.T,
) {
	unknown := findings.Finding{Code: "teleport"}

	assert.Nil(t, analysisLines(unknown, sampleReport()))
	assert.Empty(t, comparisonLine(unknown, sampleVMAF(quality.ModeExact)))
	assert.Empty(t, ladderLine(unknown, sampleRungs()))
}
