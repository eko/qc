package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestWizardFormSteps(
	t *testing.T,
) {
	answers := newWizardAnswers()

	for _, offerGPU := range []bool{false, true} {
		steps := answers.formSteps(testWizardContext(t, offerGPU))
		require.Len(t, steps, 22)

		var sections []section

		for i, step := range steps {
			assert.NotEmpty(t, step.fields, "step %d", i)
			sections = append(sections, step.section)
		}

		assert.IsNonDecreasing(t, sections, "the sections follow each other")
		assert.Equal(t, sectionOutputs, steps[len(steps)-1].section)
		assert.Equal(t, offerGPU, !steps[16].isHidden(), "the GPU is asked only when usable")
	}

	assert.Equal(t, []string{actionAnalysis, actionLadder}, answers.Actions, "the defaults the form shows")
	assert.Equal(t, defaultMetrics, answers.Metrics)
	assert.NotSame(t, &defaultMetrics[0], &answers.Metrics[0], "the form edits a copy of the defaults")
}

func TestWizardHiddenGroups(
	t *testing.T,
) {
	type hidden struct {
		vmaf, ladder, metrics, advanced, precision, share, count, resolutions, filmGrain bool
	}

	testCases := []struct {
		name    string
		answers wizardAnswers
		want    hidden
	}{
		{
			name:    "analysis only",
			answers: wizardAnswers{Actions: []string{actionAnalysis}, VMAFMode: vmafPrecision},
			want:    hidden{vmaf: true, ladder: true, metrics: true, advanced: true, precision: true, share: true, count: true, resolutions: true, filmGrain: true},
		},
		{
			name:    "vmaf with a share budget",
			answers: wizardAnswers{Actions: []string{actionVMAF}, VMAFMode: vmafShare},
			want:    hidden{ladder: true, advanced: true, precision: true, count: true, resolutions: true, filmGrain: true},
		},
		{
			name:    "automatic ladder",
			answers: wizardAnswers{Actions: []string{actionLadder}, VMAFMode: vmafPrecision},
			want:    hidden{vmaf: true, advanced: true, precision: true, share: true, count: true, resolutions: true, filmGrain: true},
		},
		{
			name:    "customised AV1 ladder with a rung count",
			answers: wizardAnswers{Actions: []string{actionLadder}, Advanced: true, Shape: shapeCount, Codecs: []string{av1Codec}},
			want:    hidden{vmaf: true, precision: true, share: true, resolutions: true},
		},
		{
			name:    "customised per-shot AV1 ladder with resolutions",
			answers: wizardAnswers{Actions: []string{actionLadder}, Advanced: true, Shape: shapeResolutions, Codecs: []string{av1Codec}, PerShot: true},
			want:    hidden{vmaf: true, precision: true, share: true, count: true, filmGrain: true},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := testCase.answers

			assert.Equal(t, testCase.want, hidden{
				vmaf:        a.vmafHidden(),
				ladder:      a.ladderHidden(),
				metrics:     a.metricsHidden(),
				advanced:    a.advancedHidden(),
				precision:   a.vmafModeHidden(vmafPrecision)(),
				share:       a.vmafModeHidden(vmafShare)(),
				count:       a.shapeHidden(shapeCount)(),
				resolutions: a.shapeHidden(shapeResolutions)(),
				filmGrain:   a.filmGrainHidden(),
			})
		})
	}
}

func TestWizardOverlayHidden(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		answers      wizardAnswers
		wantAsked    bool
		wantPathAsks bool
	}{
		{name: "ladder only", answers: wizardAnswers{Actions: []string{actionLadder}, Overlay: true}},
		{name: "analysis, declined", answers: wizardAnswers{Actions: []string{actionAnalysis}}, wantAsked: true},
		{name: "vmaf, accepted", answers: wizardAnswers{Actions: []string{actionVMAF}, Overlay: true}, wantAsked: true, wantPathAsks: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := testCase.answers
			assert.Equal(t, testCase.wantAsked, !a.overlayHidden())
			assert.Equal(t, testCase.wantPathAsks, !a.overlayPathHidden())
		})
	}

	require.NoError(t, requireName("annotated.mp4"))
	require.Error(t, requireName("  "))
}

func TestWizardHDRDetected(
	t *testing.T,
) {
	detect := func(path string) string {
		return map[string]string{"hdr.mov": "HDR10", "hlg.mov": "HLG"}[path]
	}

	testCases := []struct {
		name    string
		answers wizardAnswers
		detect  func(string) string
		want    string
	}{
		{name: "hdr ladder source", answers: wizardAnswers{Actions: []string{actionLadder}, Source: "hdr.mov"}, detect: detect, want: "HDR10"},
		{name: "hdr reference", answers: wizardAnswers{Actions: []string{actionVMAF}, Source: "enc.mp4", Reference: "hlg.mov"}, detect: detect, want: "HLG"},
		{name: "sdr", answers: wizardAnswers{Actions: []string{actionLadder}, Source: "sdr.mov"}, detect: detect},
		{name: "nothing measures vmaf", answers: wizardAnswers{Actions: []string{actionAnalysis}, Source: "hdr.mov"}, detect: detect},
		{name: "no detector", answers: wizardAnswers{Actions: []string{actionLadder}, Source: "hdr.mov"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.answers.hdrDetected(testCase.detect))
		})
	}
}

func TestVideoCacheDynamicRange(
	t *testing.T,
) {
	clip := testutil.HDRClip(media.TransferHLG)
	clip.Seconds = 0.2
	hlg := testutil.Generate(t, clip)
	sdr := testutil.Generate(t, testutil.Clip{Seconds: 0.2})

	videos := newVideoCache(t.Context(), "ffprobe")

	assert.Equal(t, "HLG", videos.dynamicRange(hlg))
	assert.Equal(t, "HLG", videos.dynamicRange(hlg), "cached")
	assert.Empty(t, videos.dynamicRange(sdr))
	assert.Empty(t, videos.dynamicRange(""))
	assert.Empty(t, videos.dynamicRange(filepath.Join(t.TempDir(), "missing.mov")))
}
