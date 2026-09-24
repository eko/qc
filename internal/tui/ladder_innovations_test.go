package tui

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
)

// innovativeLadder is the sample ladder with per-shot rungs and film grain.
func innovativeLadder(
	t *testing.T,
) *ladder.Result {
	t.Helper()

	res := sampleLadder(t)
	res.Grain = &ladder.GrainReport{
		Source: grain.Stats{Sigma: 5.8}, Detected: true, Level: 50,
		Trials: []ladder.GrainTrial{{Level: 10, Ratio: 0.3}, {Level: 50, Ratio: 0.95}},
	}
	res.Timings["shots"] = "2m"

	for i := range res.Rungs {
		r := &res.Rungs[i]
		r.PerShot = &ladder.PerShot{
			Chunks:        []encode.Chunk{{Start: 0, Frames: 50, CRF: r.CRF - 1}, {Start: 50, Frames: 50, CRF: r.CRF + 2}},
			PredictedVMAF: r.PredictedVMAF, PredictedBitrate: r.Bitrate * 9 / 10,
			Gain: 0.08 - 0.1*float64(i), Command: "ffmpeg -ss 0 -i source.mov pershot",
		}

		if i == 1 {
			r.PerShot.Width, r.PerShot.Height = 1920, 1080
		}

		if r.Measured != nil {
			r.PerShot.Measured = &ladder.Measurement{Bitrate: r.Bitrate * 9 / 10, VMAF: r.Measured.VMAF}
		}

		r.Grain = &ladder.GrainCheck{Source: grain.Stats{Sigma: 4}, Output: grain.Stats{Sigma: 4 * (1 - 0.3*float64(i))}, Ratio: 1 - 0.3*float64(i), OK: i == 0}
	}

	return res
}

func TestRenderLadderInnovations(
	t *testing.T,
) {
	out := renderLadder(t, innovativeLadder(t), 110, "", true)

	for _, want := range []string{
		"grain       source noise σ 5.80 · film grain synthesis level 50, fidelity scored against the denoised reference (grain given back: 10→30%, 50→95%)",
		"Per-shot  (one CRF per shot at equal rate-quality slope, same pooled quality)",
		"2 chunks  CRF 23.5–26.5",
		"-8.0% at equal VMAF",
		"declares 1080p",
		"720p rung: synthesised grain at 70% of the source's",
		"ffmpeg -ss 0 -i source.mov pershot",
		"shots 2m",
	} {
		assert.Contains(t, out, want)
	}

	assert.NotContains(t, out, "1080p rung: synthesised grain")
}

func TestGrainLine(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   *ladder.GrainReport
		want string
	}{
		{name: "not requested", in: nil, want: ""},
		{name: "clean", in: &ladder.GrainReport{Source: grain.Stats{Sigma: 0.3}}, want: "no grain detected, synthesis off"},
		{name: "grainy but off", in: &ladder.GrainReport{Source: grain.Stats{Sigma: 3}, Detected: true}, want: "grain detected, synthesis off"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := plain(grainLine(testCase.in))
			if testCase.want == "" {
				assert.Empty(t, got)

				return
			}

			assert.Contains(t, got, testCase.want)
		})
	}
}

func TestPerShotTableWithoutPerShot(
	t *testing.T,
) {
	assert.Empty(t, perShotTable(sampleRungs(), nil))
}

func TestPerShotScope(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   *ladder.ShotProbing
		want string
	}{
		{name: "older report", want: "one CRF per shot at equal rate-quality slope, same pooled quality"},
		{name: "per-shot CRF", in: &ladder.ShotProbing{Probes: 6}, want: "one CRF per shot at equal rate-quality slope, same pooled quality; 6 exact probes of the digest"},
		{
			name: "per-shot resolution", in: &ladder.ShotProbing{Resolution: true, Probes: 9, Extra: 3},
			want: "one resolution and CRF per shot at equal rate-quality slope, same pooled quality; 9 exact probes of the digest, 3 of them to reach the neighbouring resolutions",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, perShotScope(testCase.in))
		})
	}
}

func TestLadderPanelShots(
	t *testing.T,
) {
	res := innovativeLadder(t)
	rungs := slices.Concat(sampleRungs()[:1], res.Rungs)
	out := plain(LadderPanel{Stage: ladder.StageShots, Done: 3, Rungs: rungs}.View(100))

	assert.Contains(t, out, "per-shot  (every frame scored: 3 measurement(s) done)")
	assert.Contains(t, out, "-8.0% at equal VMAF")
	assert.Contains(t, out, "+2.0% at equal VMAF")

	assert.Contains(t, plain(LadderPanel{Stage: ladder.StageGrain}.View(100)), "calibrating film grain synthesis")
}
