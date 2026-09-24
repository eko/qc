package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
)

// withShotLadder cuts the sample ladder into n shots of 100 frames (4 s at
// 25 fps) and gives every rung their allocation: odd shots are harder and
// cost twice the bits at a lower CRF.
func withShotLadder(
	t *testing.T,
	n int,
) *ladder.Result {
	t.Helper()

	res := sampleLadder(t)

	for i := range n {
		res.Shots = append(res.Shots, ladder.Shot{Start: 100 * i, Frames: 100, Measured: i%3 == 0, SourceBitrate: int64(20e6 * (1 + i%2)), TI: float64(5 + 10*(i%2))})
	}

	for r := range res.Rungs {
		rung := &res.Rungs[r]
		rung.PerShot = &ladder.PerShot{Command: "pershot", Chunks: []encode.Chunk{{Frames: 100 * n, CRF: rung.CRF}}}

		for i := range n {
			scale := int64(1 + i%2)
			rung.PerShot.Shots = append(rung.PerShot.Shots, ladder.ShotAllocation{
				CRF: rung.CRF - float64(i%2), PredictedBitrate: rung.Bitrate * scale * int64(10+i) / 20, PredictedVMAF: rung.PredictedVMAF - float64(i%2),
			})
		}
	}

	return res
}

func TestPerShotLadder(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		shots      int
		width      int
		resolution bool
		want       []string
		wantNot    []string
	}{
		{
			name:  "short title, wide terminal",
			shots: 3,
			width: 120,
			want: []string{
				"Per-shot ladder  (3 shots × rungs)",
				"#1 1080p",
				"    1 ● 0:00–0:04  20.0M   5.0  ×0.68  24.5  1.6M  95.0  27.0  575k  86.6  27.0  180k  64.1",
				"    2 ○ 0:04–0:08  40.0M  15.0  ×1.50  23.5  3.4M  94.0  26.0  1.3M  85.6  26.0  396k  63.1",
				"per rung: CRF · predicted bitrate · predicted VMAF; ● shot measured",
			},
			wantNot: []string{"⋯"},
		},
		{
			name:    "narrow terminal: no VMAF",
			shots:   3,
			width:   80,
			want:    []string{"    1 ● 0:00–0:04  20.0M   5.0  ×0.68  24.5  1.6M  27.0  575k  27.0  180k"},
			wantNot: []string{"predicted VMAF"},
		},
		{
			name:    "very narrow terminal: no complexity",
			shots:   3,
			width:   70,
			want:    []string{"    1 ● 0:00–0:04  ×0.68  24.5  1.6M  27.0  575k  27.0  180k"},
			wantNot: []string{"source", "predicted VMAF"},
		},
		{
			name:       "per-shot resolution",
			shots:      2,
			width:      140,
			resolution: true,
			want: []string{
				"    1 ● 0:00–0:04  20.0M   5.0  ×0.62  24.5  1.6M    ·  95.0",
				"    2 ○ 0:04–0:08  40.0M  15.0  ×1.38  23.5  3.4M  720  94.0  26.0  1.3M    ·  85.6  26.0  396k  720  63.1",
				"per rung: CRF · predicted bitrate · resolution · predicted VMAF",
			},
		},
		{
			name:  "long title: the most and least expensive shots",
			shots: 31,
			width: 140,
			want: []string{
				"(16 of 31 shots: the 8 most and 8 least expensive; all of them in the JSON and HTML reports)",
				"    3 ○ 0:08–0:12", "    ⋯ 1 shot(s)\n    5 ○", "   13 ● 0:48–0:52", "    ⋯ 2 shot(s)", "   30 ○ 1:56–2:00",
				"    ⋯ 1 shot(s)\n  per rung",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := withShotLadder(t, testCase.shots)
			if testCase.resolution {
				res.ShotProbing = &ladder.ShotProbing{Resolution: true}

				for r := range res.Rungs {
					for i := range res.Rungs[r].PerShot.Shots {
						a := &res.Rungs[r].PerShot.Shots[i]
						a.Width, a.Height = res.Rungs[r].Width, res.Rungs[r].Height
						if i%2 == 1 {
							a.Width, a.Height = 1280, 720
						}
					}
				}
			}

			out := plain(perShotLadder(res, testCase.width))

			for _, want := range testCase.want {
				assert.Contains(t, out, want)
			}

			for _, not := range testCase.wantNot {
				assert.NotContains(t, out, not)
			}

			for line := range strings.SplitSeq(out, "\n") {
				if !strings.HasPrefix(line, "  per rung") {
					assert.LessOrEqual(t, len([]rune(line)), testCase.width, line)
				}
			}
		})
	}
}

func TestPerShotLadderWithout(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		mutate func(res *ladder.Result)
	}{
		{name: "no per-shot rung", mutate: func(res *ladder.Result) {
			for i := range res.Rungs {
				res.Rungs[i].PerShot = nil
			}
		}},
		{name: "older report without allocations", mutate: func(res *ladder.Result) {
			for i := range res.Rungs {
				res.Rungs[i].PerShot.Shots = nil
			}
		}},
		{name: "no shot", mutate: func(res *ladder.Result) { res.Shots = nil }},
		{name: "zero bitrates", mutate: func(res *ladder.Result) {
			for i := range res.Rungs {
				for s := range res.Rungs[i].PerShot.Shots {
					res.Rungs[i].PerShot.Shots[s].PredictedBitrate = 0
				}
			}
		}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := withShotLadder(t, 3)
			testCase.mutate(res)
			assert.Empty(t, perShotLadder(res, 120))
		})
	}
}

func TestPerShotLadderUnknownComplexity(
	t *testing.T,
) {
	res := withShotLadder(t, 2)
	res.Shots[0].SourceBitrate, res.Shots[0].TI = 0, 0

	out := plain(perShotLadder(res, 120))
	require.NotEmpty(t, out)
	assert.Contains(t, out, "    1 ● 0:00–0:04      –     –")
}

func TestRenderLadderPerShot(
	t *testing.T,
) {
	out := renderLadder(t, withShotLadder(t, 3), 120, "", false)

	assert.Contains(t, out, "Per-shot ladder")
}
