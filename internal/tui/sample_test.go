package tui

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
	"github.com/eko/qc/sample"
)

// sampleResult is a mixed sample of a film and of a trailer taken whole.
func sampleResult() *sample.Result {
	scene := func(start, end float64, kind sample.Scenes) sample.Segment {
		return sample.Segment{
			Interval: media.Interval{Start: media.Seconds(start), End: media.Seconds(end)},
			Frames:   int((end - start) * 25), SI: 41.2, TI: 18.3, Kind: kind,
		}
	}

	film := []sample.Segment{scene(12, 16.4, sample.ScenesAverage), scene(300, 308, sample.ScenesTop), scene(580, 588, sample.ScenesEasy)}
	trailer := sample.Segment{Interval: media.Interval{End: media.Seconds(10)}, Frames: 250}

	return &sample.Result{
		Order: []sample.Scene{
			{Source: 0, Segment: film[1]}, {Source: 0, Segment: film[0]}, {Source: 0, Segment: film[2]}, {Source: 1, Segment: trailer},
		},
		Path: "out/sample.mkv", Scenes: sample.ScenesMixed, TopShare: 0.3,
		Asked: media.Seconds(30), Duration: media.Seconds(30.4), Frames: 760,
		Sources: []sample.SourceResult{
			{
				Path: "/videos/film.mp4", Duration: media.Seconds(600), Taken: media.Seconds(20.4), Frames: 510,
				Segments:   film,
				Complexity: &sample.Complexity{SI: 41.2, TI: 18.3, VideoSI: 30.4, VideoTI: 12},
			},
			{
				Path: "/videos/trailer.mp4", Duration: media.Seconds(10), Taken: media.Seconds(10), Frames: 250, Whole: true,
				Segments: []sample.Segment{trailer},
			},
		},
		Check:   sample.Check{Frames: 760, Decoded: true},
		Elapsed: media.Seconds(19.6),
	}
}

func renderSample(
	t *testing.T,
	res *sample.Result,
) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, RenderSample(&buf, res))

	return plain(buf.String())
}

func TestRenderSample(
	t *testing.T,
) {
	res := sampleResult()

	out := renderSample(t, res)
	for _, want := range []string{
		"◆ qc  ·  sample",
		"sample      out/sample.mkv · 0:30.400 for 0:30.000 asked · 4 scenes · 760 frames",
		"scenes      mixed: the most complex scenes and representative ones (30% complex asked)",
		"copy        whole GOPs of the sources, not re-encoded · video only",
		"film.mp4                           10:00      0:20         3      41.2   18.3  (30.4, 12.0)",
		"trailer.mp4                         0:10      0:10         1   taken whole: no longer than its share",
		"Scenes  (as the sample plays them: the complex ones first, the most complex leading, then the representative ones)",
		" 1  film.mp4                       5:00.000 → 5:08.000  complex         SI 41.2 · TI 18.3 · SI×TI 754 · 200 frames",
		" 2  film.mp4                       0:12.000 → 0:16.400  representative  SI 41.2 · TI 18.3 · SI×TI 754 · 109 frames",
		" 3  film.mp4                       9:40.000 → 9:48.000  easy            SI 41.2 · TI 18.3 · SI×TI 754 · 200 frames",
		" 4  trailer.mp4                    0:00.000 → 0:10.000  the whole video · 250 frames",
		"✓ sample read back: 760 frames, every one decodes; they are the sources' own frames",
		"⚡ total 19.6s",
	} {
		assert.Contains(t, out, want)
	}

	assert.Equal(t, "0:30.400 · 4 scenes · 760 frames", SampleSummary(res))

	// What the check found is told in place of the good news.
	res.Check = sample.Check{Frames: 758, Note: "the sample holds 758 frames where its scenes have 760"}
	out = renderSample(t, res)
	assert.Contains(t, out, "▲ the sample holds 758 frames where its scenes have 760")
	assert.NotContains(t, out, "every one decodes")

	require.Error(t, RenderSample(failingWriter{}, res))
}

func TestRenderSampleScenes(
	t *testing.T,
) {
	// Other scenes than a mix tell no share; long lists are cut.
	res := sampleResult()
	res.Scenes, res.TopShare = sample.ScenesTop, 0

	for i := range 40 {
		start := float64(20 * i)
		res.Order = append(res.Order, sample.Scene{Segment: sample.Segment{
			Interval: media.Interval{Start: media.Seconds(start), End: media.Seconds(start + 2)}, Frames: 50, Kind: sample.ScenesTop,
		}})
	}

	out := renderSample(t, res)
	assert.Contains(t, out, "scenes      top: the most complex scenes\n")
	assert.Contains(t, out, "Scenes  (as the sample plays them: the most complex first)")
	assert.Contains(t, out, "  14 more")
}
