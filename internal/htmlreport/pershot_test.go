package htmlreport

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/ladder"
)

// shotLadder is the sample ladder cut into two shots of 50 frames (2 s at
// 25 fps): the second, harder one costs three times the bits. Only the
// first is in the digest, and only the top rung was verified.
func shotLadder(
	t *testing.T,
) *ladder.Result {
	t.Helper()

	res := sampleLadder(t, "h264")
	res.Shots = []ladder.Shot{
		{Start: 0, Frames: 50, Measured: true, SourceBitrate: 20e6, TI: 4.5},
		{Start: 50, Frames: 50},
	}

	for i := range res.Rungs {
		r := &res.Rungs[i]
		r.PerShot = &ladder.PerShot{
			Chunks: []encode.Chunk{{Frames: 50, CRF: r.CRF + 1}, {Start: 50, Frames: 50, CRF: r.CRF - 1}},
			Shots: []ladder.ShotAllocation{
				{CRF: r.CRF + 1, PredictedBitrate: r.Bitrate / 2, PredictedVMAF: r.PredictedVMAF + 1},
				{CRF: r.CRF - 1, PredictedBitrate: r.Bitrate * 3 / 2, PredictedVMAF: r.PredictedVMAF - 1},
			},
		}
	}

	top := &res.Rungs[0].PerShot.Shots[0]
	top.Measured = &ladder.ShotMeasurement{Bitrate: 2.6e6, VMAF: 95.8, ScoredFrames: 12}
	res.Rungs[1].PerShot.Shots[0].Measured = &ladder.ShotMeasurement{Bitrate: 510_000}

	return res
}

func TestShotLadderSection(
	t *testing.T,
) {
	s, ok := shotLadderSection(shotLadder(t))
	require.True(t, ok)

	assert.Equal(t, "Per-shot ladder", s.Title)
	assert.Contains(t, s.Subtitle, "2 shots × 2 rungs")
	assert.Contains(t, s.Subtitle, "1 shots measured on the digest")
	require.Len(t, s.Charts, 1)

	tbl := s.Table
	require.NotNil(t, tbl)
	assert.Equal(t, []string{"#", "start", "end", "model", "source bitrate", "TI", "cost", "#1 1080p", "#2 720p"}, tbl.Head)
	assert.Equal(t, []span{{0, 2}, {2, 4}}, tbl.Spans)
	assert.Equal(t, 1, tbl.LinkCol)
	assert.Equal(t, []string{
		"1", "0:00.00", "0:02.00", "measured", "20.00 Mb/s", "4.5", "0.50",
		"2.50 Mb/s · CRF 23.0 · VMAF 96.0", "500 kb/s · CRF 31.0 · VMAF 81.0",
	}, tbl.Rows[0])
	assert.Equal(t, []string{"2", "0:02.00", "0:04.00", "predicted", "–", "–", "1.50"}, tbl.Rows[1][:7])

	all := payloads(t, string(s.Charts[0]))
	require.Len(t, all, 1)

	d := all[0]
	assert.True(t, d.LogY)
	assert.Equal(t, svg.UnitTime, d.X)
	assert.Equal(t, svg.UnitBitrate, d.Y)
	require.Len(t, d.Series, 2)

	top := d.Series[0]
	assert.Equal(t, "#1 1080p", top.Name)
	assert.Equal(t, "step", top.Style)
	assert.Equal(t, []float64{0, 2, 2, 4}, top.X.values())
	assert.Equal(t, []float64{2.5e6, 2.5e6, 7.5e6, 7.5e6}, top.Y.values())
	require.Len(t, top.Details, 4)
	assert.Equal(t, "shot 1 · 0:00.00 – 0:02.00 · measured", top.Details[0].Title)
	assert.Equal(t, [][2]string{{"CRF", "23.0"}, {"VMAF", "96.0"}, {"measured", "2.60 Mb/s, VMAF 95.8"}}, top.Details[1].Fields)
	assert.Equal(t, "shot 2 · 0:02.00 – 0:04.00 · predicted", top.Details[2].Title)
	assert.Equal(t, [][2]string{{"CRF", "31.0"}, {"VMAF", "81.0"}, {"measured", "510 kb/s"}}, d.Series[1].Details[0].Fields,
		"no VMAF when no frame of the shot was scored")
}

func TestShotLadderResolution(
	t *testing.T,
) {
	res := shotLadder(t)
	for r := range res.Rungs {
		for i := range res.Rungs[r].PerShot.Shots {
			a := &res.Rungs[r].PerShot.Shots[i]
			a.Width, a.Height = res.Rungs[r].Width, res.Rungs[r].Height
		}
	}

	moved := &res.Rungs[1].PerShot.Shots[1]
	moved.Width, moved.Height = 1920, 1080

	s, ok := shotLadderSection(res)
	require.True(t, ok)
	assert.Equal(t, "2.50 Mb/s · CRF 23.0 · VMAF 96.0", s.Table.Rows[0][7], "the rung's own resolution is implied")
	assert.Equal(t, "1.50 Mb/s · CRF 29.0 · VMAF 79.0 · 1080p", s.Table.Rows[1][8])

	d := payloads(t, string(s.Charts[0]))[0]
	assert.Equal(t, [2]string{"at", "1080p"}, d.Series[1].Details[2].Fields[0])
}

func TestShotLadderSectionWithout(
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
			res := shotLadder(t)
			testCase.mutate(res)

			_, ok := shotLadderSection(res)
			assert.False(t, ok)
		})
	}
}

func TestRenderShotLadder(
	t *testing.T,
) {
	html := renderHTML(t, shotLadder(t))

	assert.Contains(t, html, "Per-shot ladder")
	assert.Equal(t, 2, strings.Count(html, `<figure class="chart"`), "ladder and per-shot charts")
	assert.Contains(t, html, `<tr data-t0="2.000" data-t1="4.000" data-link="1"><td>2</td>`, "shot rows zoom the per-shot chart")
}
