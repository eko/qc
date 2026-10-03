package sample

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

// series builds a frame series at 25 fps from the indices of its
// keyframes, with SI = index and TI = 1.
func series(
	n int,
	keys ...int,
) *analysis.FrameSeries {
	frames := &analysis.FrameSeries{}

	for i := range n {
		frames.PTS = append(frames.PTS, media.Seconds(float64(i)/fps))
		frames.SI = append(frames.SI, float64(i))
		frames.TI = append(frames.TI, 1)
		frames.Keyframe = append(frames.Keyframe, false)
	}

	for _, k := range keys {
		frames.Keyframe[k] = true
	}

	return frames
}

func TestPieces(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		frames *analysis.FrameSeries
		least  float64
		// want are the first frame and the frame count of each piece.
		want [][2]int
	}{
		{name: "no keyframe", frames: series(100)},
		{name: "one GOP", frames: series(100, 0), least: 2, want: [][2]int{{0, 100}}},
		{name: "GOPs of two seconds", frames: series(150, 0, 50, 100), least: 2, want: [][2]int{{0, 50}, {50, 50}, {100, 50}}},
		{
			name:   "short GOPs are joined until they last enough",
			frames: series(200, 0, 25, 50, 75, 100, 125, 150, 175), least: 2,
			want: [][2]int{{0, 50}, {50, 50}, {100, 50}, {150, 50}},
		},
		{
			name:   "irregular GOPs: a piece ends at the first keyframe late enough",
			frames: series(200, 0, 30, 60, 140), least: 2,
			want: [][2]int{{0, 60}, {60, 80}, {140, 60}},
		},
		{
			name:   "a short tail joins the piece before it",
			frames: series(120, 0, 50, 100), least: 2,
			want: [][2]int{{0, 50}, {50, 70}},
		},
		{name: "a short video is one piece", frames: series(30, 0, 10, 20), least: 2, want: [][2]int{{0, 30}}},
		{
			name:   "frames before the first keyframe are left out",
			frames: series(110, 10, 60), least: 2,
			want: [][2]int{{10, 50}, {60, 50}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			n := len(testCase.frames.PTS)
			end := media.Seconds(float64(n) / fps)

			got := pieces(testCase.frames, end, media.Seconds(testCase.least))
			require.Len(t, got, len(testCase.want))

			for i, want := range testCase.want {
				p := got[i]
				assert.Equal(t, want, [2]int{p.first, p.frames}, "piece %d", i)
				assert.InDelta(t, float64(want[0])/fps, p.start.Seconds(), 1e-9)
				assert.InDelta(t, float64(want[0]+want[1])/fps, p.end.Seconds(), 1e-9, "to the next keyframe, or the end")
				assert.InDelta(t, float64(want[0])+float64(want[1]-1)/2, p.si, 1e-9, "the mean of its frames")
				assert.InDelta(t, 1, p.ti, 1e-9)
				assert.InDelta(t, p.si, p.score(), 1e-9)
			}
		})
	}
}

// pool builds consecutive pieces of the given lengths in seconds, whose SI
// and TI are those given.
func pool(
	lengths []float64,
	si, ti []float64,
) []piece {
	var (
		out   []piece
		start float64
	)

	for i, l := range lengths {
		out = append(out, piece{
			first: int(start * fps), frames: int(l * fps),
			start: media.Seconds(start), end: media.Seconds(start + l), si: si[i], ti: ti[i],
		})
		start += l
	}

	return out
}

func firsts(
	list []piece,
) []int {
	var out []int
	for _, p := range list {
		out = append(out, p.first/fps)
	}

	return out
}

func TestRanked(
	t *testing.T,
) {
	scenes := pool([]float64{2, 10, 2, 4, 2}, []float64{5, 4, 3, 2, 1}, []float64{1, 1, 1, 1, 1})

	testCases := []struct {
		name   string
		budget float64
		want   []int
	}{
		{name: "a scene that would take the total further is passed over", budget: 5, want: []int{0, 12}},
		{name: "a long scene is taken when it brings the total closer", budget: 8, want: []int{0, 2}},
		{name: "in order, up to the budget", budget: 14, want: []int{0, 2, 12}},
		{name: "at least one scene", budget: 0.5, want: []int{0}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, firsts(ranked(scenes, media.Seconds(testCase.budget))))
		})
	}
}

func TestExtreme(
	t *testing.T,
) {
	scenes := pool([]float64{2, 2, 2, 2}, []float64{10, 40, 20, 30}, []float64{2, 2, 2, 2})

	assert.Equal(t, []int{2, 6}, firsts(extreme(scenes, media.Seconds(4), false)), "the highest SI × TI")
	assert.Equal(t, []int{0, 4}, firsts(extreme(scenes, media.Seconds(4), true)), "the lowest")
	assert.Equal(t, []int{0, 2, 4, 6}, firsts(scenes), "the pool is left in order")
}

func TestRepresentative(
	t *testing.T,
) {
	// Four runs of three scenes: the middle one of each is not the one
	// that balances the choice.
	si := []float64{10, 90, 50, 50, 10, 90, 90, 50, 10, 50, 90, 10}
	ti := make([]float64, len(si))
	lengths := make([]float64, len(si))

	for i := range si {
		ti[i], lengths[i] = 5, 2
	}

	scenes := pool(lengths, si, ti)
	target := meansOf(scenes)
	assert.InDelta(t, 50, target[0], 1e-9)

	got := representative(scenes, media.Seconds(8), target)
	require.Len(t, got, 4, "one scene per run")

	for i, p := range got {
		assert.GreaterOrEqual(t, p.first/fps, 6*i)
		assert.Less(t, p.first/fps, 6*(i+1), "scene %d comes from its own run", i)
	}

	assert.InDelta(t, 50, meansOf(got)[0], 1e-9, "the spatial information of the video")
	assert.InDelta(t, 8, total(got).Seconds(), 1e-9)

	// Nothing to take from, or nothing asked.
	assert.Nil(t, representative(nil, media.Seconds(8), target))
	assert.Nil(t, representative(scenes, 0, target))

	// A budget beyond the pool takes every scene; a tiny one, one scene.
	assert.Len(t, representative(scenes, media.Seconds(100), target), len(scenes))
	assert.Len(t, representative(scenes, media.Seconds(0.1), target), 1)

	// Scenes that do not vary are measured against a spread of one.
	flat := pool([]float64{2, 2}, []float64{7, 7}, []float64{3, 3})
	assert.Equal(t, [2]float64{1, 1}, spreadOf(flat))
	assert.Equal(t, [2]float64{}, meansOf(nil))
}

func TestChooseMixed(
	t *testing.T,
) {
	// Ten scenes of two seconds, more and more complex.
	var si, ti, lengths []float64
	for i := range 10 {
		si, ti, lengths = append(si, 10+10*float64(i)), append(ti, 5), append(lengths, 2)
	}

	scenes := pool(lengths, si, ti)

	got := choose(scenes, media.Seconds(8), Options{Scenes: ScenesMixed, TopShare: 0.5, Piece: media.Seconds(2)})
	require.Len(t, got, 4)

	var top, average []float64

	for i, seg := range got {
		if i > 0 {
			assert.Greater(t, seg.Start, got[i-1].Start, "in the order of the video")
		}

		if seg.Kind == ScenesTop {
			top = append(top, seg.Start.Seconds())
		} else {
			average = append(average, seg.Start.Seconds())
		}
	}

	assert.Equal(t, []float64{16, 18}, top, "the two most complex scenes")
	assert.Len(t, average, 2, "and two of the others, which stand for the video")

	for _, start := range average {
		assert.Less(t, start, 16.0, "a scene is not taken twice")
	}
}

func TestOriginAndTimestamps(
	t *testing.T,
) {
	// Without the packets of a video, its stream tells where it starts.
	v := source{inspection: &analysis.Report{}, video: media.VideoStream{StartTime: media.Seconds(1.4)}}
	assert.InDelta(t, 1.4, origin(v).Seconds(), 1e-12)

	_, shared := sharedTimestamp(&analysis.Report{})
	assert.False(t, shared, "nothing to read")
}
