package main

import (
	"testing"

	"github.com/eko/qc/quality"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWizardAdvancedArgs(
	t *testing.T,
) {
	base := wizardAnswers{
		Source:  "in.mp4",
		Actions: []string{actionLadder},
		Codecs:  []string{"av1"},
	}

	testCases := []struct {
		name   string
		mutate func(a *wizardAnswers)
		want   []string
	}{
		{
			name:   "advanced off: defaults only",
			mutate: func(a *wizardAnswers) { a.TopVMAF = "90" },
			want:   []string{"in.mp4", "--codecs=av1", "--skip-analysis"},
		},
		{
			name: "defaults typed back produce no flag",
			mutate: func(a *wizardAnswers) {
				a.Advanced, a.Shape, a.TopVMAF, a.MinVMAF, a.BitDepth = true, shapeAuto, "95", "30", "8"
			},
			want: []string{"in.mp4", "--codecs=av1", "--skip-analysis"},
		},
		{
			name: "rung count and quality range",
			mutate: func(a *wizardAnswers) {
				a.Advanced, a.Shape, a.RungCount, a.TopVMAF, a.MinVMAF = true, shapeCount, " 5 ", "93", "45"
			},
			want: []string{"in.mp4", "--codecs=av1", "--rungs", "5", "--top-vmaf", "93", "--min-vmaf", "45", "--skip-analysis"},
		},
		{
			name: "resolutions, cap, preset, 10-bit, no verification",
			mutate: func(a *wizardAnswers) {
				a.Advanced, a.Shape, a.Resolutions = true, shapeResolutions, "1080, 720p,540"
				a.MaxBitrate, a.Preset, a.BitDepth, a.SkipVerify = "6000", "6", "10", true
			},
			want: []string{
				"in.mp4", "--codecs=av1",
				"--rungs", "1080p,720p,540p", "--max-bitrate", "6000000", "--preset", "6",
				"--encode-bit-depth", "10", "--no-verify", "--skip-analysis",
			},
		},
		{
			name: "adaptive probing and AV1 film grain",
			mutate: func(a *wizardAnswers) {
				a.Advanced, a.Probing, a.FilmGrain = true, "adaptive", "auto"
			},
			want: []string{"in.mp4", "--codecs=av1", "--probing", "adaptive", "--film-grain", "auto", "--skip-analysis"},
		},
		{
			name: "per-shot rungs exclude film grain",
			mutate: func(a *wizardAnswers) {
				a.Advanced, a.PerShot, a.FilmGrain = true, true, "auto"
			},
			want: []string{"in.mp4", "--codecs=av1", "--per-shot", "--skip-analysis"},
		},
		{
			name: "digest of the most complex scenes",
			mutate: func(a *wizardAnswers) {
				a.Digest = "top"
			},
			want: []string{"in.mp4", "--codecs=av1", "--digest", "top", "--skip-analysis"},
		},
		{
			name: "film grain only for AV1",
			mutate: func(a *wizardAnswers) {
				a.Advanced, a.Codecs, a.Probing, a.FilmGrain = true, []string{"h264"}, "fixed", "auto"
			},
			want: []string{"in.mp4", "--codecs=h264", "--skip-analysis"},
		},
		{
			name: "a single resolution is not read as a count",
			mutate: func(a *wizardAnswers) {
				a.Advanced, a.Shape, a.Resolutions = true, shapeResolutions, "720"
			},
			want: []string{"in.mp4", "--codecs=av1", "--rungs", "720p", "--skip-analysis"},
		},
		{
			name: "advanced answers ignored without a ladder",
			mutate: func(a *wizardAnswers) {
				a.Actions, a.Advanced, a.Shape, a.RungCount = []string{actionAnalysis}, true, shapeCount, "4"
			},
			want: []string{"in.mp4", "--codecs="},
		},
		{
			name: "VMAF precision when sampled",
			mutate: func(a *wizardAnswers) {
				a.Actions, a.Reference, a.Precision = []string{actionVMAF}, "ref.mov", "0.25"
			},
			want: []string{"in.mp4", "-r", "ref.mov", "--precision", "0.25", "--codecs=", "--skip-analysis"},
		},
		{
			name: "exact wins over precision",
			mutate: func(a *wizardAnswers) {
				a.Actions, a.Reference, a.Precision, a.VMAFMode = []string{actionVMAF}, "ref.mov", "0.25", vmafExact
			},
			want: []string{"in.mp4", "-r", "ref.mov", "--exact", "--codecs=", "--skip-analysis"},
		},
		{
			name: "fixed budget: share of frames",
			mutate: func(a *wizardAnswers) {
				a.Actions, a.Reference, a.Precision, a.VMAFMode, a.Share = []string{actionVMAF}, "ref.mov", "0.25", vmafShare, " 2.5% "
			},
			want: []string{"in.mp4", "-r", "ref.mov", "--sample", "2.5%", "--codecs=", "--skip-analysis"},
		},
		{
			name: "fixed budget: clips per scene",
			mutate: func(a *wizardAnswers) {
				a.Actions, a.Reference, a.VMAFMode, a.PerScene = []string{actionVMAF}, "ref.mov", vmafPerScene, "1"
			},
			want: []string{"in.mp4", "-r", "ref.mov", "--sample", "1/scene", "--codecs=", "--skip-analysis"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			answers := base
			testCase.mutate(&answers)

			assert.Equal(t, testCase.want, answers.runArgs())
		})
	}
}

func TestWizardMetricArgs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		metrics []string
		devices []string
		want    []string
	}{
		{
			name: "unset metrics are the defaults",
			want: []string{"in.mp4", "-r", "ref.mov", "--codecs=", "--skip-analysis"},
		},
		{
			name:    "defaults in another order produce no flag",
			metrics: []string{"psnr", "cambi", "xpsnr"},
			want:    []string{"in.mp4", "-r", "ref.mov", "--codecs=", "--skip-analysis"},
		},
		{
			name:    "nothing selected: VMAF only",
			metrics: []string{},
			want:    []string{"in.mp4", "-r", "ref.mov", "--metrics=", "--codecs=", "--skip-analysis"},
		},
		{
			name:    "picked one by one, with devices",
			metrics: []string{"cambi", "ms-ssim"},
			devices: []string{"phone", "4k"},
			want: []string{
				"in.mp4", "-r", "ref.mov", "--metrics=cambi,ms-ssim", "--devices=phone,4k",
				"--codecs=", "--skip-analysis",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := wizardAnswers{
				Source: "in.mp4", Reference: "ref.mov", Actions: []string{actionVMAF},
				Precision: defaultPrecision, Metrics: testCase.metrics, Devices: testCase.devices,
			}

			args := a.runArgs()
			assert.Equal(t, testCase.want, args)

			run, _, err := newRootCommand(testEnv).Find([]string{"run"})
			require.NoError(t, err)
			require.NoError(t, run.ParseFlags(args))

			config, err := loadConfig(run)
			require.NoError(t, err)
			require.NoError(t, config.validate())
		})
	}
}

func TestMetricOptions(
	t *testing.T,
) {
	var values []string
	for _, o := range metricOptions() {
		values = append(values, o.Value)
	}

	// Every metric but VMAF itself is offered, once.
	assert.ElementsMatch(t, quality.Metrics()[1:], values)
}

func TestWizardValidators(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		validate func(string) error
		in       string
		wantErr  bool
	}{
		{name: "count", validate: validateCount, in: "6"},
		{name: "count zero", validate: validateCount, in: "0", wantErr: true},
		{name: "count text", validate: validateCount, in: "six", wantErr: true},
		{name: "heights", validate: validateHeights, in: "1080,720p, 540"},
		{name: "heights bad", validate: validateHeights, in: "1080,hd", wantErr: true},
		{name: "heights empty entry", validate: validateHeights, in: "1080,,720", wantErr: true},
		{name: "range inside", validate: validateRange(1, 110, false), in: "93.5"},
		{name: "range above", validate: validateRange(1, 110, false), in: "120", wantErr: true},
		{name: "range not a number", validate: validateRange(1, 110, false), in: "high", wantErr: true},
		{name: "required range empty", validate: validateRange(1, 110, false), in: "", wantErr: true},
		{name: "optional range empty", validate: validateRange(1, 10, true), in: " "},
		{name: "share", validate: validateShare, in: "2.5"},
		{name: "share with its sign", validate: validateShare, in: " 100% "},
		{name: "share zero", validate: validateShare, in: "0", wantErr: true},
		{name: "share above 100", validate: validateShare, in: "150", wantErr: true},
		{name: "share text", validate: validateShare, in: "half", wantErr: true},
		{name: "clips per scene", validate: validatePerScene, in: " 2 "},
		{name: "clips per scene zero", validate: validatePerScene, in: "0", wantErr: true},
		{name: "clips per scene fraction", validate: validatePerScene, in: "1.5", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.validate(testCase.in)
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestWizardAdvancedArgsParse(
	t *testing.T,
) {
	// The generated arguments must be accepted by the run command itself.
	answers := wizardAnswers{
		Source: "in.mp4", Actions: []string{actionLadder}, Codecs: []string{"h264"},
		Advanced: true, Shape: shapeResolutions, Resolutions: "1080,720",
		TopVMAF: "92", MaxBitrate: "4500", BitDepth: "10", SkipVerify: true,
	}

	root := newRootCommand(testEnv)
	run, _, err := root.Find([]string{"run"})
	require.NoError(t, err)
	require.NoError(t, run.ParseFlags(answers.runArgs()))

	config, err := loadConfig(run)
	require.NoError(t, err)
	require.NoError(t, config.validate())

	opts := ladderOptions(config)
	assert.Equal(t, []int{1080, 720}, opts.Constraints.Resolutions)
	assert.InDelta(t, 92, opts.Constraints.TopVMAF, 1e-9)
	assert.Equal(t, int64(4_500_000), opts.Constraints.MaxBitrate)
	assert.Equal(t, 10, opts.BitDepth)
	assert.True(t, opts.SkipVerify)
}

func TestWizardVMAFModeArgsParse(
	t *testing.T,
) {
	// Every VMAF mode produces arguments the run command accepts.
	testCases := []struct {
		name        string
		mode        string
		wantExact   bool
		wantSample  quality.Sample
		wantPrecise float64
	}{
		{name: "precision", mode: vmafPrecision, wantPrecise: 0.5},
		{name: "share of frames", mode: vmafShare, wantSample: quality.Sample{Share: 0.05}, wantPrecise: 0.5},
		{name: "clips per scene", mode: vmafPerScene, wantSample: quality.Sample{PerScene: 2}, wantPrecise: 0.5},
		{name: "exact", mode: vmafExact, wantExact: true, wantPrecise: 0.5},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			answers := wizardAnswers{
				Source: "in.mp4", Reference: "ref.mov", Actions: []string{actionVMAF},
				VMAFMode: testCase.mode, Precision: defaultPrecision, Share: defaultShare, PerScene: defaultPerScene,
			}

			run, _, err := newRootCommand(testEnv).Find([]string{"run"})
			require.NoError(t, err)
			require.NoError(t, run.ParseFlags(answers.runArgs()))

			config, err := loadConfig(run)
			require.NoError(t, err)

			opts := qualityOptions(config)
			assert.Equal(t, testCase.wantExact, opts.Exact)
			assert.Equal(t, testCase.wantSample, opts.Sample)
			assert.InDelta(t, testCase.wantPrecise, opts.Precision, 1e-9)
		})
	}
}

func TestWizardMetricArgsForLadders(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		actions []string
		want    []string
	}{
		{
			name:    "ladder rungs get the chosen metrics",
			actions: []string{actionLadder},
			want:    []string{"src.mov", "--metrics=cambi", "--devices=phone", "--codecs=av1", "--skip-analysis"},
		},
		{
			name:    "analysis only: no metric flag",
			actions: []string{actionAnalysis},
			want:    []string{"src.mov", "--codecs="},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := wizardAnswers{
				Source: "src.mov", Actions: testCase.actions, Codecs: []string{"av1"},
				Metrics: []string{"cambi"}, Devices: []string{"phone"},
			}

			assert.Equal(t, testCase.want, a.runArgs())
		})
	}
}
