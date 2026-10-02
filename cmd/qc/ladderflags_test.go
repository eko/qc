package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
)

func TestParseFilmGrain(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "default", in: "", want: 0},
		{name: "off", in: "Off", want: 0},
		{name: "auto", in: " auto ", want: ladder.FilmGrainAuto},
		{name: "level", in: "25", want: 25},
		{name: "zero is off", in: "0", want: 0},
		{name: "above 50", in: "51", wantErr: true},
		{name: "negative", in: "-1", wantErr: true},
		{name: "not a level", in: "strong", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := parseFilmGrain(testCase.in)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidFilmGrain)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestLadderInnovationFlags(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		config  Config
		want    ladder.Options
		wantErr error
	}{
		{
			name:   "adaptive probing, per-shot and grain",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Probing: "adaptive", PerShot: true, FilmGrain: "auto"}},
			want:   ladder.Options{Probing: ladder.ProbingAdaptive, PerShot: true, FilmGrain: ladder.FilmGrainAuto},
		},
		{
			name:   "digest of the most complex scenes",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Digest: "top"}},
			want:   ladder.Options{DigestSampling: ladder.DigestTop},
		},
		{
			name:   "digest length",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{DigestDuration: 80}},
			want:   ladder.Options{DigestDuration: media.Seconds(80)},
		},
		{
			name:   "uniform digest",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Digest: "uniform"}},
			want:   ladder.Options{DigestSampling: ladder.DigestUniform},
		},
		{
			name:   "per-shot resolution",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{PerShotResolution: true}},
			want:   ladder.Options{PerShotResolution: true},
		},
		{
			name:    "invalid film grain",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{FilmGrain: "99"}},
			wantErr: ErrInvalidFilmGrain,
		},
		{
			name:    "film grain and per-shot rungs on an AV1 ladder",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Codec: "av1", PerShot: true, FilmGrain: "auto"}},
			wantErr: ErrFilmGrainPerShot,
		},
		{
			name:    "film grain and per-shot resolution in a run with AV1",
			config:  Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{PerShotResolution: true, FilmGrain: "25"}, Run: RunConfig{Codecs: []string{"h264", "av1"}}},
			wantErr: ErrFilmGrainPerShot,
		},
		{
			name:   "per-shot rungs without AV1: film grain does not apply",
			config: Config{Tools: ToolsConfig{LogLevel: "warn"}, Ladder: LadderConfig{Codec: "h264", PerShot: true, FilmGrain: "auto"}},
			want:   ladder.Options{PerShot: true, FilmGrain: ladder.FilmGrainAuto},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.validate()
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)

			got := ladderOptions(testCase.config)
			assert.Equal(t, testCase.want.Probing, got.Probing)
			assert.Equal(t, testCase.want.DigestSampling, got.DigestSampling)
			assert.Equal(t, testCase.want.DigestDuration, got.DigestDuration)
			assert.Equal(t, testCase.want.PerShot, got.PerShot)
			assert.Equal(t, testCase.want.PerShotResolution, got.PerShotResolution)
			assert.Equal(t, testCase.want.FilmGrain, got.FilmGrain)
		})
	}
}
