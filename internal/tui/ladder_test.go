package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

func renderLadder(
	t *testing.T,
	res *ladder.Result,
	width int,
	jsonPath string,
	commands bool,
) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, RenderLadder(&buf, res, width, jsonPath, commands))

	return plain(buf.String())
}

func TestRenderLadder(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		jsonPath string
		commands bool
		want     []string
		wantNot  []string
	}{
		{
			name: "report",
			want: []string{
				"◆ qc  ·  ladder",
				"source      /videos/source.mov",
				"            h264 · 1920×1080 · 25.000 fps · 1:00.000",
				"target      h264 (libx264, preset fast)",
				"digest      2 segment(s) · 0:04.000 · 6.7% of the title · 8 probe encodes",
				"Rate / quality  (VMAF vs bitrate, log scale)", "━━ 1080p", "━━ 720p", "━━ 360p", "⣿ rungs",
				"Ladder", "measured VMAF",
				"⚡ digest 1.2s · probe 40s · verify 12s · total 53.4s",
			},
			wantNot: []string{"Encoding commands", "written to", "4.00 Mb/s"},
		},
		{
			name:     "with commands and JSON",
			jsonPath: "ladder.json",
			commands: true,
			want: []string{
				"Encoding commands\nffmpeg -i source.mov -c:v libx264 -crf 24.5 1080p.mp4\n",
				"✓ full report written to ladder.json",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := renderLadder(t, sampleLadder(t), 100, testCase.jsonPath, testCase.commands)

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, unwanted := range testCase.wantNot {
				assert.NotContains(t, out, unwanted)
			}
		})
	}
}

func TestRenderLadderLayout(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		width int
		want  int
	}{
		{name: "regular", width: 100, want: 100},
		{name: "clamped to the table width", width: 20, want: minLadderWidth},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := strings.Split(renderLadder(t, sampleLadder(t), testCase.width, "", false), "\n")

			plot := linesWith(lines, "┤")
			require.Len(t, plot, rqChartHeight)

			axis := linesStarting(lines, "360k ")
			require.Len(t, axis, 1)
			assert.Equal(t, []string{"360k", "1.4M", "5.6M"}, strings.Fields(axis[0]), "the middle label is the geometric mean")

			for _, w := range widths(append(plot, axis...)) {
				assert.Equal(t, testCase.want, w)
			}
		})
	}
}

// TestLadderTableAlignment checks that coloured cells do not shift columns:
// fmt widths count escape sequences, display widths do not.
func TestLadderTableAlignment(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		rungs    func() []ladder.Rung
		wantHead string
	}{
		{name: "verified", rungs: sampleRungs, wantHead: "measured VMAF"},
		{
			name: "adaptive predictions carry their half-width",
			rungs: func() []ladder.Rung {
				rungs := sampleRungs()
				for i := range rungs {
					rungs[i].PredictionError = 0.4
				}

				return rungs
			},
			wantHead: "measured VMAF",
		},
		{
			name: "not verified",
			rungs: func() []ladder.Rung {
				rungs := sampleRungs()
				for i := range rungs {
					rungs[i].Measured, rungs[i].Calibrated = nil, false
				}

				return rungs
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := plainLines(ladderTable(testCase.rungs()))
			require.Len(t, lines, 2+3)

			head := lines[1]
			if testCase.wantHead != "" {
				assert.Contains(t, head, testCase.wantHead)
			} else {
				assert.NotContains(t, head, "measured")
			}

			for _, row := range lines[2:] {
				row = strings.TrimSuffix(row, " ✱")
				assert.Equal(t, len([]rune(head)), len([]rune(row)), "%q\n%q", head, row)
			}

			assert.True(t, strings.HasPrefix(lines[2], "  1   1920×1080     3.10 Mb/s   24.5   6.20 Mb/s"), lines[2])
			assert.Contains(t, lines[2], "95.0")
		})
	}

	assert.Empty(t, ladderTable(nil))
}

func TestRungRowCalibrated(
	t *testing.T,
) {
	row := plain(rungRow(1, sampleRungs()[1]))

	assert.True(t, strings.HasSuffix(row, "84.1 ± 0.7   1.10 Mb/s ✱"), row)
}

func TestLadderFindings(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(r *ladder.Result)
		want   []string
	}{
		{
			name:   "verified h264 ladder with a calibrated rung",
			mutate: func(*ladder.Result) {},
			want: []string{
				"✓ VMAF 95 reached at 3.10 Mb/s (1080p)",
				"✓ top rung 60% lighter than Apple's static 1080p rung (7.8 Mb/s)",
				"✓ verification: measured VMAF within 2.5 of the prediction on every rung (VBV-capped encodes of the digest)",
				"✱ 1 rung(s) missed the prediction by more than 1.5 and had their CRF corrected (secant step) and re-measured",
			},
		},
		{
			name: "title never reaching the top VMAF, verification off by more than half a step",
			mutate: func(r *ladder.Result) {
				r.Rungs[0].PredictedVMAF = 91
				r.Rungs[0].Bitrate = 9_000_000
				r.Rungs[1].Calibrated = false
				r.Rungs[2].Measured.VMAF = 70
			},
			want: []string{
				"▲ the title never reaches VMAF 95 at 1080p: top rung at the best probed quality",
				"▲ verification: measured VMAF within 5.9 of the prediction on every rung (VBV-capped encodes of the digest)",
			},
		},
		{
			name: "unverified AV1 ladder",
			mutate: func(r *ladder.Result) {
				r.Codec = encode.Codec{Name: "av1", Encoder: "libsvtav1"}
				for i := range r.Rungs {
					r.Rungs[i].Measured, r.Rungs[i].Calibrated = nil, false
				}
			},
			want: []string{"✓ VMAF 95 reached at 3.10 Mb/s (1080p)"},
		},
		{
			name: "rung findings grouped by kind, calibrated rungs last",
			mutate: func(r *ladder.Result) {
				r.Rungs[0].Grain = &ladder.GrainCheck{Ratio: 0.5}
				r.Rungs[0].Grain.Source.Sigma, r.Rungs[0].Grain.Output.Sigma = 2, 1
				r.Rungs[1].Extrapolated = true
				r.Rungs[2].Extrapolated, r.Rungs[2].Calibrated = true, true
				r.Rungs[2].Measured.BandedFrames, r.Rungs[2].Measured.ScoredFrames = 10, 20
				r.Rungs[1].Measured.Metrics = map[string]float64{quality.SeriesXPSNRY: 35}
				r.Rungs[2].Measured.Metrics = map[string]float64{quality.SeriesXPSNRY: 36}
			},
			want: []string{
				"✓ VMAF 95 reached at 3.10 Mb/s (1080p)",
				"✓ top rung 60% lighter than Apple's static 1080p rung (7.8 Mb/s)",
				"✓ verification: measured VMAF within 2.5 of the prediction on every rung (VBV-capped encodes of the digest)",
				"▲ 720p rung targets a quality outside the probed range of that resolution: its prediction is extrapolated, trust the measured value",
				"▲ 360p rung targets a quality outside the probed range of that resolution: its prediction is extrapolated, trust the measured value",
				"▲ 1080p rung: synthesised grain at 50% of the source's (σ 1.00 vs 2.00)",
				"▲ rung 3 (360p): visible banding on 50% of the scored frames (CAMBI > 5): a 10-bit encode fixes it better than more bitrate",
				"▲ rungs 2 (720p) and 3 (360p): VMAF ranks 720p higher (84.1 vs 64.5) but XPSNR ranks it lower (35.00 vs 36.00 dB)",
				"✱ 2 rung(s) missed the prediction by more than 1.5 and had their CRF corrected (secant step) and re-measured",
			},
		},
		{
			name:   "no rung",
			mutate: func(r *ladder.Result) { r.Rungs = nil },
			want:   []string{"▲ no rung could be selected"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := sampleLadder(t)
			testCase.mutate(res)

			var got []string
			for _, line := range ladderFindings(res) {
				got = append(got, plain(line))
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestRenderLadderWithoutProbes(
	t *testing.T,
) {
	res := sampleLadder(t)
	res.Probes, res.Timings = nil, nil

	out := renderLadder(t, res, 80, "", false)

	assert.Contains(t, out, "⣿ rungs", "rungs are still plotted")
	assert.Contains(t, out, "⚡ total 53.4s")
}

func TestProbeSeries(
	t *testing.T,
) {
	series, legend, lowest := probeSeries(sampleProbes())

	require.Len(t, series, 3)
	assert.InDelta(t, 64.1, lowest, 1e-9)

	var got []string
	for _, l := range legend {
		got = append(got, plain(l))
	}

	assert.Equal(t, []string{"━━ 1080p", "━━ 720p", "━━ 360p"}, got, "highest resolution first")

	for _, s := range series {
		assert.True(t, s.Line)

		for i := 1; i < len(s.Points); i++ {
			assert.Less(t, s.Points[i-1][0], s.Points[i][0], "points are sorted by bitrate")
		}
	}
}

func TestVMAFFloor(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		lowest float64
		want   float64
	}{
		{name: "rounds down to ten", lowest: 64.1, want: 60},
		{name: "never below zero", lowest: -3, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, vmafFloor(testCase.lowest), 1e-9)
		})
	}
}

func TestRenderWritten(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		jsonPath string
		htmlPath string
		want     string
	}{
		{name: "nothing written", want: ""},
		{name: "JSON", jsonPath: "a.json", want: "✓ report written to a.json\n"},
		{name: "both", jsonPath: "a.json", htmlPath: "a.html", want: "✓ report written to a.json\n✓ report written to a.html\n"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, RenderWritten(&buf, testCase.jsonPath, testCase.htmlPath))
			assert.Equal(t, testCase.want, plain(buf.String()))
		})
	}
}

func TestRenderWrittenVideo(
	t *testing.T,
) {
	testCases := []struct {
		name string
		path string
		want string
	}{
		{name: "no copy", want: ""},
		{name: "copy", path: "a.mp4", want: "✓ annotated video written to a.mp4\n"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, RenderWrittenVideo(&buf, testCase.path))
			assert.Equal(t, testCase.want, plain(buf.String()))
		})
	}
}

func TestLadderShape(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(res *ladder.Result)
		want   []string
	}{
		{
			name:   "automatic",
			mutate: func(res *ladder.Result) { res.Shape = ladder.ShapeAuto },
			want:   []string{"shape       automatic: from VMAF 95 down by 6 points"},
		},
		{
			name: "rung count",
			mutate: func(res *ladder.Result) {
				res.Shape, res.Constraints.Rungs = ladder.ShapeCount, 5
			},
			want: []string{"shape       5 rungs requested, VMAF 95 down to the title's floor, resolutions automatic"},
		},
		{
			name: "imposed resolutions with an extrapolated rung",
			mutate: func(res *ladder.Result) {
				res.Shape, res.Constraints.Resolutions = ladder.ShapeResolutions, []int{1080, 720, 360}
				res.Rungs[len(res.Rungs)-1].Extrapolated = true
			},
			want: []string{
				"shape       resolutions requested (1080p, 720p, 360p), VMAF 95 down to the title's floor",
				"rung targets a quality outside the probed range of that resolution",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := sampleLadder(t)
			res.Constraints = res.Constraints.WithDefaults()
			testCase.mutate(res)

			out := renderLadder(t, res, 100, "", false)
			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}
		})
	}
}

func TestProbingLabel(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   ladder.ProbingReport
		want string
	}{
		{name: "fixed", in: ladder.ProbingReport{Mode: ladder.ProbingFixed, Rounds: 1}, want: ""},
		{name: "adaptive converged", in: ladder.ProbingReport{Mode: ladder.ProbingAdaptive, Rounds: 4, Budget: 14, Converged: true}, want: " (adaptive: 4 rounds, budget 14, converged)"},
		{name: "adaptive out of budget", in: ladder.ProbingReport{Mode: ladder.ProbingAdaptive, Rounds: 5, Budget: 14}, want: " (adaptive: 5 rounds, budget 14, budget reached)"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, probingLabel(testCase.in))
		})
	}
}
