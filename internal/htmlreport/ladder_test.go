package htmlreport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
)

func TestPredictedLabel(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   ladder.Rung
		want string
	}{
		{name: "fixed probing", in: ladder.Rung{PredictedVMAF: 93.04}, want: "93.0"},
		{name: "adaptive probing", in: ladder.Rung{PredictedVMAF: 93.04, PredictionError: 0.44}, want: "93.0 ± 0.4"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, predictedLabel(testCase.in))
		})
	}
}

func TestProbingNote(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   ladder.ProbingReport
		want string
	}{
		{name: "fixed", in: ladder.ProbingReport{Mode: ladder.ProbingFixed}, want: ""},
		{name: "converged", in: ladder.ProbingReport{Mode: ladder.ProbingAdaptive, Rounds: 3, Converged: true}, want: " (adaptive, 3 rounds, converged)"},
		{name: "budget", in: ladder.ProbingReport{Mode: ladder.ProbingAdaptive, Rounds: 5}, want: " (adaptive, 5 rounds, budget reached)"},
		{name: "fixed with challengers", in: ladder.ProbingReport{Mode: ladder.ProbingFixed, Challengers: 4}, want: " (4 challenger probes)"},
		{name: "adaptive with challengers", in: ladder.ProbingReport{Mode: ladder.ProbingAdaptive, Rounds: 4, Converged: true, Challengers: 2}, want: " (adaptive, 4 rounds, converged, 2 challenger probes)"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, probingNote(testCase.in))
		})
	}
}

func TestPerShotSubtitle(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   *ladder.ShotProbing
		want []string
	}{
		{name: "older report", want: []string{"one CRF per shot"}},
		{name: "per-shot CRF", in: &ladder.ShotProbing{Probes: 6}, want: []string{"one CRF per shot", " · 6 exact probes of the digest"}},
		{
			name: "per-shot resolution", in: &ladder.ShotProbing{Resolution: true, Probes: 9, Extra: 3},
			want: []string{"one resolution and CRF per shot", "declare their largest one", "9 exact probes of the digest (3 to reach the neighbouring resolutions)"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := perShotSubtitle(testCase.in)
			for _, want := range testCase.want {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestLadderExtras(
	t *testing.T,
) {
	measured := &ladder.Measurement{Bitrate: 2_000_000, VMAF: 90}
	res := &ladder.Result{
		Rungs: []ladder.Rung{
			{
				Width: 1280, Height: 720, Measured: measured,
				PerShot: &ladder.PerShot{
					Chunks:   []encode.Chunk{{CRF: 25}, {CRF: 22.5}},
					Measured: &ladder.Measurement{Bitrate: 1_800_000, VMAF: 90}, Gain: 0.1, Command: "pershot",
				},
				Grain: &ladder.GrainCheck{Source: grain.Stats{Sigma: 4}, Output: grain.Stats{Sigma: 4}, Ratio: 1, OK: true},
			},
			{
				Width: 640, Height: 360,
				PerShot: &ladder.PerShot{Chunks: []encode.Chunk{{CRF: 30}}, Width: 1280, Height: 720},
				Grain:   &ladder.GrainCheck{Ratio: 0.5},
			},
			{Width: 480, Height: 270},
		},
	}

	testCases := []struct {
		name   string
		grain  *ladder.GrainReport
		titles []string
		want   string
	}{
		{name: "per-shot only", titles: []string{"Per-shot"}},
		{
			name:   "grain synthesised",
			grain:  &ladder.GrainReport{Level: 50, Trials: []ladder.GrainTrial{{Level: 50, Ratio: 0.9}}},
			titles: []string{"Per-shot", "Film grain"},
			want:   "synthesis level 50; fidelity scored against SVT-AV1's denoised reference · level 50 gives back 90%",
		},
		{name: "grain detected, off", grain: &ladder.GrainReport{Detected: true}, titles: []string{"Per-shot", "Film grain"}, want: "grain detected, synthesis off"},
		{name: "clean", grain: &ladder.GrainReport{}, titles: []string{"Per-shot", "Film grain"}, want: "no grain detected"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res.Grain = testCase.grain
			sections := ladderExtras(res)
			require.Len(t, sections, len(testCase.titles))

			for i, s := range sections {
				assert.Equal(t, testCase.titles[i], s.Title)
			}

			shots := sections[0].Table
			require.Len(t, shots.Rows, 2)
			assert.Equal(t, []string{"1", "1280×720", "2", "22.5–25.0", "2.00 Mb/s · VMAF 90.0", "1.80 Mb/s · VMAF 90.0", "10.0%"}, shots.Rows[0])
			assert.Equal(t, []string{"–", "–", "–"}, shots.Rows[1][4:])
			assert.Equal(t, "640×360 (declares 1280×720)", shots.Rows[1][1])

			if testCase.grain != nil {
				assert.Contains(t, sections[1].Subtitle, testCase.want)
				require.Len(t, sections[1].Table.Rows, 2)
				assert.Equal(t, "matches", sections[1].Table.Rows[0][5])
				assert.Equal(t, "off", sections[1].Table.Rows[1][5])
			}
		})
	}

	assert.Nil(t, perShotTable(res.Rungs[2:]))
	assert.Nil(t, grainTable(res.Rungs[2:]))
	assert.Equal(t, []string{"", "", "", "pershot", ""}, rungCommands(res.Rungs))
}
