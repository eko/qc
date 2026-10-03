package htmlreport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/quality"
)

// sampleProgram is the sample ladder built for three videos: the top rung
// read on each (their VMAF), the second rung not verified.
func sampleProgram(
	t *testing.T,
	vmaf [3]float64,
) *ladder.Result {
	t.Helper()

	res := sampleLadder(t, "h264")

	for i, name := range []string{"/videos/episode-01.mov", "/videos/episode-02.mov", "/videos/episode-03.mov"} {
		report := sampleReport()
		report.Info.Path = name

		res.Sources = append(res.Sources, report)
		res.Digest.Titles = append(res.Digest.Titles, ladder.DigestTitle{Source: name, Segments: 7})
		res.Rungs[0].Measured.Titles = append(res.Rungs[0].Measured.Titles,
			ladder.TitleMeasurement{VMAF: vmaf[i], Bitrate: int64(1+i) * 1_000_000, ScoredFrames: 50})
	}

	res.Source = res.Sources[0]

	return res
}

// wordings are the texts of the findings of a ladder.
func wordings(
	r *ladder.Result,
) []string {
	var out []string
	for _, f := range ladderFindings(r) {
		out = append(out, f.Text)
	}

	return out
}

func TestProgramSection(
	t *testing.T,
) {
	res := sampleProgram(t, [3]float64{94, 94.6, 95.5})
	res.Rungs[0].Measured.Titles[2].ScoredFrames = 0

	assert.Equal(t, "episode-01.mov and 2 more", ladderSubject(res))
	assert.Equal(t, "3 videos, as many segments each", ladderScope(res))

	table := programTable(res)
	require.NotNil(t, table)
	assert.Equal(t, []string{"video", "duration", "segments", "#1 1080p", "#2 720p", "top rung"}, table.Head)
	assert.Equal(t, [][]string{
		{"episode-01.mov", "0:04.00", "7", "94.0", "–", "1.00 Mb/s"},
		{"episode-02.mov", "0:04.00", "7", "94.6", "–", "2.00 Mb/s"},
		{"episode-03.mov", "0:04.00", "7", "–", "–", "3.00 Mb/s"},
	}, table.Rows, "a video without a scored frame has its bitrate only, a rung not verified nothing")

	html := renderHTML(t, res)
	for _, want := range []string{"episode-01.mov and 2 more", "of the 3 videos, as many segments each", "Per-video quality", "1 segment of the 3 videos"} {
		assert.Contains(t, html, want)
	}

	// The ladder of one title has no such section.
	one := sampleLadder(t, "h264")
	assert.Nil(t, programTable(one))
	assert.Equal(t, "<clip>.mp4", ladderSubject(one))
	assert.Equal(t, "title", ladderScope(one))
	assert.Equal(t, "title", digestSubject(one))
	assert.NotContains(t, renderHTML(t, one), "Per-video quality")
}

func TestLadderFindingsProgram(
	t *testing.T,
) {
	even := sampleProgram(t, [3]float64{94, 94.6, 95.5})
	assert.Contains(t, wordings(even), "Every video within 0.9 VMAF of the program on the top rung")

	apart := sampleProgram(t, [3]float64{91.1, 94.6, 97.9})
	assert.Contains(t, wordings(apart), "episode-01.mov: VMAF 91.1 on the top rung for 94.6 over the program. "+
		"The shared ladder under-serves it: a ladder of its own would reach the target")
	assert.Contains(t, wordings(apart), "episode-03.mov: VMAF 97.9 on the top rung for 94.6 over the program. "+
		"It would reach the target with fewer bits on a ladder of its own")

	// Below the top, the rung where the videos differ most.
	apart.Rungs[1].Measured = &ladder.Measurement{VMAF: 80, Titles: []ladder.TitleMeasurement{
		{VMAF: 86, ScoredFrames: 50}, {VMAF: 80, ScoredFrames: 50}, {VMAF: 72.5, ScoredFrames: 50},
	}}
	assert.Contains(t, wordings(apart), "Rung 2 (720p): 13.5 VMAF between episode-03.mov (72.5) and episode-01.mov (86.0). "+
		"One CRF for all does not give the videos one quality down the ladder")

	// The renditions of a program are named after their video.
	apart.Renditions = []ladder.Rendition{{
		Rung: 0, Source: "/videos/episode-02.mov", Path: "out/h264/episode-02/01-1080p.mp4", Bitrate: 2_900_000,
		Checked: &ladder.Measurement{VMAF: 90, HalfWidth: 0.3},
	}}
	assert.Contains(t, wordings(apart), "episode-02/01-1080p.mp4 on the whole title: VMAF 90.0 ± 0.3, -4.6 from its prediction on the digest")
}

func TestRenderRunProgram(
	t *testing.T,
) {
	h264, av1 := sampleProgram(t, [3]float64{94, 94.6, 95.5}), sampleProgram(t, [3]float64{94, 94.6, 95.5})
	av1.Codec.Name = "av1"

	// The ladders of several codecs for several videos: the page is headed
	// by the program, not by the inspection of its first video.
	run := &pipeline.Report{Analysis: h264.Source, Ladders: []*ladder.Result{h264, av1}, Elapsed: "12m30s"}

	html := renderHTML(t, run)
	for _, want := range []string{"Encoding ladders", "episode-01.mov and 2 more", "<li>3 videos</li>", "<li>12m30s</li>", "h264 ladder", "av1 ladder"} {
		assert.Contains(t, html, want)
	}

	assert.NotContains(t, html, "Full run")

	cards, list := runSummary(run)
	require.Len(t, cards, 2, "one card per ladder")

	for _, f := range list {
		assert.NotEqual(t, "Source", f.Scope)
	}

	// The run of one video keeps its analysis.
	one := &pipeline.Report{Analysis: sampleReport(), Ladders: []*ladder.Result{sampleLadder(t, "h264")}}
	assert.Nil(t, program(one))
	assert.Nil(t, program(&pipeline.Report{Analysis: sampleReport()}))
	assert.Contains(t, renderHTML(t, one), "Full run")
}

func TestBandingInheritedFromTheSource(
	t *testing.T,
) {
	l := sampleLadder(t, "h264")
	l.Rungs[0].Measured.BandedFrames, l.Rungs[0].Measured.ScoredFrames = 30, 100
	assert.Contains(t, wordings(l), "Rung 1 (1080p): visible banding on 30% of the scored frames (CAMBI > 5): a 10-bit encode fixes it better than more bitrate")

	l.Rungs[0].Measured.SourceBandedFrames = 24
	assert.Contains(t, wordings(l), "Rung 1 (1080p): visible banding on 30% of the scored frames (CAMBI > 5), already in the source on 80% of them. "+
		"Neither bitrate nor a 10-bit encode removes it: deband the source")

	// A comparison says how many of its banded frames the reference has.
	banding := &quality.Banding{BandedFrames: 12, SourceFrames: 9, Segments: []quality.BandingSegment{{Frames: 12, Peak: 7}}}
	f := findings.Finding{Level: findings.Warn, Code: findings.Banding}

	list := bandingFindings(f, banding)
	require.Len(t, list, 2)
	assert.Equal(t, "9 of the 12 banded frames banded in the reference too: that banding is the source's, not the encode's", list[1].Text)

	banding.SourceFrames = 0
	assert.Len(t, bandingFindings(f, banding), 1)
}

func TestComparisonFindingDrops(
	t *testing.T,
) {
	v := &quality.Result{Mean: 88.4, HarmonicMean: 86.2, Drops: &quality.Drops{Margin: quality.DropMargin, Threshold: 78.4, Share: 0.081}}
	f := findings.Finding{Level: findings.Info, Code: findings.Drops, Value: 0.081, Limit: 78.4}

	list := comparisonFinding(f, v)
	require.Len(t, list, 1)
	assert.Equal(t, "8% of the frames score more than 10 VMAF under the mean (below 78.4): the mean hides them, the harmonic mean is 86.2", list[0].Text)
}
