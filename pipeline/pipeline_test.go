package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

func TestStages(
	t *testing.T,
) {
	testCases := []struct {
		name string
		opts Options
		want []string
	}{
		{name: "analysis only", opts: Options{Source: "a.mp4"}, want: []string{KindInspect, KindAnalysis}},
		{
			name: "everything",
			opts: Options{Source: "a.mp4", Reference: "r.mp4", Codecs: []string{"h264", "av1"}},
			want: []string{KindInspect, KindAnalysis, KindVMAF, KindLadder, KindLadder},
		},
		{
			name: "vmaf only",
			opts: Options{Source: "a.mp4", Reference: "r.mp4", SkipAnalysis: true},
			want: []string{KindInspect, KindVMAF},
		},
		{
			name: "renditions after each ladder",
			opts: Options{Source: "a.mp4", Codecs: []string{"h264", "av1"}, Renditions: ladder.RenditionOptions{Dir: "out"}},
			want: []string{KindInspect, KindAnalysis, KindLadder, KindRenditions, KindLadder, KindRenditions},
		},
		{
			name: "the annotated copy comes before the ladders",
			opts: Options{Source: "a.mp4", Reference: "r.mp4", Codecs: []string{"h264"}, Overlay: OverlayOptions{Output: "o.mp4"}},
			want: []string{KindInspect, KindAnalysis, KindVMAF, KindOverlay, KindLadder},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var kinds []string
			for _, s := range Stages(testCase.opts) {
				kinds = append(kinds, s.Kind)
			}

			assert.Equal(t, testCase.want, kinds)
		})
	}
}

func TestStagesVMAFLabel(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		sample quality.Sample
		want   string
	}{
		{name: "precision-driven", want: "VMAF"},
		{name: "share of frames", sample: quality.Sample{Share: 0.05}, want: "VMAF · 5%"},
		{name: "clips per scene", sample: quality.Sample{PerScene: 2}, want: "VMAF · 2/scene"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			stages := Stages(Options{Source: "a.mp4", Reference: "r.mp4", SkipAnalysis: true, Quality: quality.Options{Sample: testCase.sample}})

			require.Len(t, stages, 2)
			assert.Equal(t, testCase.want, stages[1].Label)
		})
	}
}
