package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWizardFormGroups(
	t *testing.T,
) {
	answers := newWizardAnswers()

	for _, offerGPU := range []bool{false, true} {
		groups := answers.formGroups(offerGPU)
		require.Len(t, groups, 15)

		for i, group := range groups {
			assert.NotNil(t, group, "group %d", i)
		}
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
