package ladder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrainOptions(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		opts      Options
		codec     string
		wantGrain int
		wantMsg   string
	}{
		{name: "off", opts: Options{}, codec: "h264"},
		{name: "auto on AV1", opts: Options{FilmGrain: FilmGrainAuto}, codec: "av1", wantGrain: FilmGrainAuto},
		{name: "level 50 on AV1", opts: Options{FilmGrain: 50}, codec: "av1", wantGrain: 50},
		{name: "other codecs ignore it", opts: Options{FilmGrain: 10, PerShot: true}, codec: "hevc"},
		{name: "level out of range", opts: Options{FilmGrain: 51}, codec: "av1", wantMsg: "level 51"},
		{name: "with per-shot", opts: Options{FilmGrain: 10, PerShot: true}, codec: "av1", wantMsg: "per-shot"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := grainOptions(testCase.opts, mustCodec(t, testCase.codec))
			if testCase.wantMsg == "" {
				require.NoError(t, err)
				assert.Equal(t, testCase.wantGrain, got.FilmGrain)

				return
			}

			require.ErrorIs(t, err, ErrFilmGrain)
			assert.Contains(t, err.Error(), testCase.wantMsg)
		})
	}
}

func TestBuildFilmGrain(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		grain float64
		level int
		check func(t *testing.T, res *Result, lab *fakeLab)
	}{
		{
			name:  "grainy title: detected, calibrated, scored against the denoised reference",
			grain: 6,
			level: FilmGrainAuto,
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				require.NotNil(t, res.Grain)
				assert.True(t, res.Grain.Detected)
				assert.Equal(t, 50, res.Grain.Level, "the level giving the grain back")
				assert.Len(t, res.Grain.Trials, len(calibrationLevels))
				assert.InDelta(t, 6, res.Grain.Source.Sigma, 1e-9)

				references := 0

				for i, p := range lab.params {
					if i < len(calibrationLevels) {
						assert.Equal(t, calibrationLevels[i], p.FilmGrain, "calibration %d", i)

						continue
					}

					assert.Equal(t, 50, p.FilmGrain, "every encode synthesises grain")

					if p.CRF == referenceCRF {
						references++
					}
				}

				assert.Equal(t, 1, references, "one denoised reference")

				for _, r := range res.Rungs {
					require.NotNil(t, r.Grain)
					assert.True(t, r.Grain.OK)
					assert.InDelta(t, 1, r.Grain.Ratio, 1e-9)
					assert.Contains(t, r.Command, "film-grain=50:film-grain-denoise=1")
				}
			},
		},
		{
			name:  "clean title: nothing synthesised",
			grain: 0.3,
			level: FilmGrainAuto,
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				require.NotNil(t, res.Grain)
				assert.False(t, res.Grain.Detected)
				assert.Zero(t, res.Grain.Level)
				assert.Empty(t, res.Grain.Trials)

				for _, p := range lab.params {
					assert.Zero(t, p.FilmGrain)
				}

				for _, r := range res.Rungs {
					assert.Nil(t, r.Grain)
					assert.False(t, strings.Contains(r.Command, "film-grain"))
				}
			},
		},
		{
			name:  "imposed level: no calibration, grain checked",
			grain: 6,
			level: 20,
			check: func(t *testing.T, res *Result, _ *fakeLab) {
				assert.Equal(t, 20, res.Grain.Level)
				assert.Empty(t, res.Grain.Trials)

				for _, r := range res.Rungs {
					require.NotNil(t, r.Grain)
					assert.False(t, r.Grain.OK, "level 20 gives back too little grain")
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
			lab.grain = testCase.grain

			res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "av1", FilmGrain: testCase.level})
			require.NoError(t, err)

			testCase.check(t, res, lab)
		})
	}
}

func TestBuildFilmGrainErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		level   int
		failOn  func(op, target string, seen int) bool
		wantErr error
		wantMsg string
	}{
		{name: "invalid level", level: 99, wantErr: ErrFilmGrain},
		{name: "digest noise", level: FilmGrainAuto, failOn: failing(opNoise, "digest.nut", 0), wantErr: errFake, wantMsg: "ladder: grain"},
		{name: "calibration encode", level: FilmGrainAuto, failOn: failing(opEncode, "grain-25.mp4", 0), wantErr: errFake},
		{name: "calibration noise", level: FilmGrainAuto, failOn: failing(opNoise, "grain-10.mp4", 0), wantErr: errFake},
		{name: "reference encode", level: 20, failOn: failing(opEncode, "reference.mp4", 0), wantErr: errFake, wantMsg: "grain reference"},
		{name: "reference decode", level: 20, failOn: failing(opDecode, "reference.nut", 0), wantErr: errFake, wantMsg: "grain reference"},
		{name: "reference inspection", level: 20, failOn: failing(opAnalyze, "reference.nut", 0), wantErr: errFake, wantMsg: "inspect"},
		{name: "probe inspection", level: 20, failOn: failing(opAnalyze, "probe-2.mp4", 0), wantErr: errFake, wantMsg: "probe-2: measure: inspect"},
		{name: "probe grain-free decode", level: 20, failOn: failing(opDecode, "probe-1.mp4.nut", 0), wantErr: errFake, wantMsg: "grain-free decode"},
		{name: "probe measurement", level: 20, failOn: failing(opCompare, "probe-0.mp4.nut", 0), wantErr: errFake, wantMsg: "probe-0: measure"},
		{name: "rung grain of the source", level: 20, failOn: failing(opNoise, "digest.nut", 1), wantErr: errFake, wantMsg: "verify-0: grain"},
		{name: "rung grain of the output", level: 20, failOn: failing(opNoise, "verify-1.mp4", 0), wantErr: errFake, wantMsg: "verify-1: grain"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
			lab.grain = 6
			lab.failOn = testCase.failOn

			_, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "av1", FilmGrain: testCase.level, Parallel: 1})
			require.ErrorIs(t, err, testCase.wantErr)

			if testCase.wantMsg != "" {
				assert.Contains(t, err.Error(), testCase.wantMsg)
			}
		})
	}
}

func TestBuildWithoutGrainLab(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		opts    Options
		wantErr error
	}{
		{name: "AV1 film grain needs the lab", opts: Options{Codec: "av1", FilmGrain: FilmGrainAuto}, wantErr: ErrNoGrainLab},
		{name: "other codecs ignore film grain", opts: Options{Codec: "h264", FilmGrain: FilmGrainAuto, SkipVerify: true}},
		{name: "AV1 without film grain", opts: Options{Codec: "av1", SkipVerify: true}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))

			_, err := NewEngine(lab, lab, lab).Build(t.Context(), sourcePath, testCase.opts)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.Contains(t, err.Error(), "ladder: film grain synthesis needs a grain lab")

				return
			}

			require.NoError(t, err)
		})
	}
}
