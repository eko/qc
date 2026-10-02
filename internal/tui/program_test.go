package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/ladder"
)

// sampleProgram is the sample ladder built for three videos, every rung
// read on each: their VMAF on the three rungs, top first.
func sampleProgram(
	t *testing.T,
	vmaf [3][3]float64,
) *ladder.Result {
	t.Helper()

	res := sampleLadder(t)
	res.Constraints = res.Constraints.WithDefaults()

	names := []string{"/videos/episode-01.mov", "/videos/episode-02.mov", "/videos/a-very-long-episode-name-number-03.mov"}
	for _, name := range names {
		res.Sources = append(res.Sources, &analysis.Report{Info: sampleInfo(name)})
		res.Digest.Titles = append(res.Digest.Titles, ladder.DigestTitle{Source: name, Segments: 7})
	}

	res.Source = res.Sources[0]

	for i := range res.Rungs {
		for v := range names {
			res.Rungs[i].Measured.Titles = append(res.Rungs[i].Measured.Titles,
				ladder.TitleMeasurement{VMAF: vmaf[i][v], Bitrate: int64(1+v) * 1_000_000, ScoredFrames: 50})
		}
	}

	return res
}

func TestRenderLadderProgram(
	t *testing.T,
) {
	even := [3][3]float64{{94.5, 95.3, 96}, {85, 84, 83.5}, {66, 64, 63}}
	apart := [3][3]float64{{92, 95.5, 98.1}, {86, 84, 79}, {75, 64, 55.5}}

	testCases := []struct {
		name    string
		vmaf    [3][3]float64
		mutate  func(res *ladder.Result)
		want    []string
		wantNot []string
	}{
		{
			name: "videos served alike",
			vmaf: even,
			want: []string{
				"program     3 videos · h264 · 1920×1080 · 25.000 fps",
				"            /videos/episode-01.mov · 1:00.000",
				"            /videos/a-very-long-episode-name-number-03.mov · 1:00.000",
				"6.7% of the program, as many segments per video",
				"Per-video quality  (VMAF of each rung on the frames of each video)",
				"video                         1·1080p   2·720p   3·360p    top rung",
				"episode-01.mov                   94.5     85.0     66.0   1.00 Mb/s",
				"…-episode-name-number-03.mov     96.0     83.5     63.0   3.00 Mb/s",
				"every video within 0.8 VMAF of the program on the top rung",
			},
			wantNot: []string{"source      ", "ladder of its own", "down the ladder"},
		},
		{
			name: "videos apart",
			vmaf: apart,
			want: []string{
				"episode-01.mov: VMAF 92.0 on the top rung for 95.3 over the program: the shared ladder under-serves it, a ladder of its own would reach the target",
				"a-very-long-episode-name-number-03.mov: VMAF 98.1 on the top rung for 95.3 over the program: it would reach the target with fewer bits on a ladder of its own",
				"rung 3 (360p): 19.5 VMAF between a-very-long-episode-name-number-03.mov (55.5) and episode-01.mov (75.0): " +
					"one CRF for all does not give the videos one quality down the ladder",
			},
			wantNot: []string{"every video within"},
		},
		{
			name: "a video without a scored frame on a rung, a rung not verified",
			vmaf: even,
			mutate: func(res *ladder.Result) {
				res.Rungs[1].Measured.Titles[1].ScoredFrames = 0
				res.Rungs[2].Measured = nil
			},
			want: []string{"episode-02.mov                   95.3        –        –   2.00 Mb/s"},
		},
		{
			name:    "not verified: nothing to read video by video",
			vmaf:    even,
			mutate:  func(res *ladder.Result) { res.Rungs[0].Measured = nil },
			want:    []string{"program     3 videos"},
			wantNot: []string{"Per-video quality"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := sampleProgram(t, testCase.vmaf)
			if testCase.mutate != nil {
				testCase.mutate(res)
			}

			text := renderLadder(t, res, 120, "", false)

			for _, want := range testCase.want {
				assert.Contains(t, text, want)
			}

			for _, wantNot := range testCase.wantNot {
				assert.NotContains(t, text, wantNot)
			}
		})
	}

	// The ladder of one title has no per-video table.
	assert.Empty(t, programTable(sampleLadder(t)))
	assert.Equal(t, "short.mov", truncate("short.mov", 28))
}

func TestRenderLadderProgramRenditions(
	t *testing.T,
) {
	res := sampleProgram(t, [3][3]float64{{95, 95, 95}, {84, 84, 84}, {64, 64, 64}})
	res.Renditions = []ladder.Rendition{
		{Rung: 0, Source: "/videos/episode-02.mov", Path: "out/h264/episode-02/01-1080p.mp4", Width: 1920, Height: 1080, Bitrate: 2_050_000,
			Checked: &ladder.Measurement{VMAF: 91.2, HalfWidth: 0.3}},
	}

	text := renderLadder(t, res, 120, "", false)

	// Named after its video, and compared with what the rung measured on
	// the segments of that video (95.0 at 2.00 Mb/s), not over the program.
	assert.Contains(t, text, "episode-02/01-1080p.mp4 on the whole title: VMAF 91.2 ± 0.3, -3.8 from its prediction on the digest")
	assert.NotContains(t, text, "bitrate over the whole title")
}
