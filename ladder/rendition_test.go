package ladder

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/quality"
)

func TestRungParamsMatchTheCommands(
	t *testing.T,
) {
	testCases := []struct {
		name string
		opts Options
	}{
		{name: "h264", opts: Options{Codec: "h264"}},
		{name: "10-bit hevc", opts: Options{Codec: "hevc", BitDepth: 10, Preset: "medium"}},
		{name: "av1 with film grain", opts: Options{Codec: "av1", FilmGrain: 20}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))
			lab.grain = 6

			res, err := labEngine(lab).Build(t.Context(), sourcePath, testCase.opts)
			require.NoError(t, err)

			for i, r := range res.Rungs {
				want := res.Codec.CommandLine(sourcePath, fmt.Sprintf("%02d-%dp.mp4", i+1, r.Height), res.RungParams(r))
				assert.Equal(t, want, r.Command, "rung %d: what is encoded is what the command shows", i)
			}
		})
	}
}

func TestEncode(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		perShot     bool
		skipPerShot bool
		check       bool
		want        []string
	}{
		{name: "rungs", want: []string{"01-1080p.mp4"}},
		{name: "rungs checked against the source", check: true, want: []string{"01-1080p.mp4"}},
		{name: "per-shot versions too", perShot: true, want: []string{"01-1080p.mp4", "01-1080p-pershot.mp4"}},
		{name: "per-shot versions left out", perShot: true, skipPerShot: true, want: []string{"01-1080p.mp4"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, shotSource(60, 4))
			engine := labEngine(lab)

			res, err := engine.Build(t.Context(), sourcePath, Options{Codec: "h264", PerShot: testCase.perShot})
			require.NoError(t, err)

			opts := RenditionOptions{Dir: t.TempDir(), SkipPerShot: testCase.skipPerShot}
			if testCase.check {
				opts.Check = &quality.Options{Precision: 0.5}
			}

			var last RenditionProgress

			completed := 0
			opts.Progress = func(p RenditionProgress) {
				assert.GreaterOrEqual(t, p.Done, last.Done, "progress only grows")

				last = p
				if p.Rendition != nil {
					completed++
				}
			}

			renditions, err := engine.Encode(t.Context(), sourcePath, res, opts)
			require.NoError(t, err)

			assert.Equal(t, renditions, res.Renditions, "recorded in the result")
			assert.Len(t, renditions, completed, "each rendition reported once done")
			assert.Equal(t, last.Total, last.Done, "progress reaches the total")

			perRung := len(testCase.want)
			require.Len(t, renditions, perRung*len(res.Rungs))

			for i, want := range testCase.want {
				assert.Equal(t, filepath.Join(opts.Dir, want), renditions[i].Path)
			}

			for _, r := range renditions {
				assert.Positive(t, r.Bitrate)
				assert.Equal(t, res.Rungs[r.Rung].Height, r.Height)

				if testCase.check {
					require.NotNil(t, r.Checked)
					assert.Equal(t, r.Bitrate, r.Checked.Bitrate)
				} else {
					assert.Nil(t, r.Checked)
				}

				if r.PerShot {
					assert.Equal(t, res.Rungs[r.Rung].PerShot.Chunks, lab.chunks[r.Path], "the per-shot chunks over the title")
				}
			}
		})
	}
}

func TestEncodeErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		failOn  func(op, target string, seen int) bool
		engine  func(lab *fakeLab) *Engine
		wantErr error
		wantMsg string
	}{
		{
			name:    "no rendition encoder",
			engine:  func(lab *fakeLab) *Engine { return NewEngine(lab, lab, lab) },
			wantErr: ErrNoRenditionEncoder,
		},
		{
			name:    "a rendition fails",
			failOn:  failing(opEncode, "01-1080p.mp4", 0),
			wantErr: errFake,
			wantMsg: "01-1080p.mp4",
		},
		{
			name:    "a rendition cannot be inspected",
			failOn:  failing(opAnalyze, "01-1080p.mp4", 0),
			wantErr: errFake,
			wantMsg: "inspect",
		},
		{
			name:    "a rendition cannot be checked",
			failOn:  failing(opCompare, "01-1080p.mp4", 0),
			wantErr: errFake,
			wantMsg: "check",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))

			res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "h264"})
			require.NoError(t, err)

			engine := labEngine(lab)
			if testCase.engine != nil {
				engine = testCase.engine(lab)
			}

			lab.failOn = testCase.failOn

			_, err = engine.Encode(t.Context(), sourcePath, res, RenditionOptions{Dir: t.TempDir(), Check: &quality.Options{Precision: 0.5}})
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Contains(t, err.Error(), testCase.wantMsg)
		})
	}
}
