package balance_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/ladder/internal/balance"
	"github.com/eko/qc/media"
)

const (
	frameRate = 25
	// slotFrames is the length of a slot of the titles below: 20 segments
	// over 600 s.
	slotFrames = 750
)

// title returns the frames of a 25 fps title of the given length whose
// features are computed frame by frame.
func title(
	seconds int,
	features ...func(frame int) float64,
) balance.Frames {
	n := seconds * frameRate
	frames := balance.Frames{PTS: make([]media.Duration, n), Features: make([][]float64, len(features))}

	for i := range frames.PTS {
		frames.PTS[i] = media.Seconds(float64(i) / frameRate)
	}

	for k, feature := range features {
		frames.Features[k] = make([]float64, n)
		for i := range frames.Features[k] {
			frames.Features[k][i] = feature(i)
		}
	}

	return frames
}

// flat is a feature that never varies.
func flat(int) float64 { return 7 }

// bursts is busy during the first 150 frames of every slot, which centred
// segments never see.
func bursts(frame int) float64 {
	if frame%slotFrames < 150 {
		return 11
	}

	return 1
}

// ramp grows along the title.
func ramp(frame int) float64 { return float64(frame) / 1000 }

// centred are the evenly spaced segments of a 600 s title.
func centred() []media.Interval {
	out := make([]media.Interval, 20)
	for i := range out {
		start := media.Seconds(float64(i)*30 + 14)
		out[i] = media.Interval{Start: start, End: start + media.Seconds(2)}
	}

	return out
}

func TestSegments(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		frames balance.Frames
		// tolerance bounds the gap between the digest's mean features and
		// the title's, as a share of the title's.
		tolerance float64
		// centredGap is that gap for evenly spaced segments, per feature.
		centredGap []float64
	}{
		{
			name:       "bursts the centred segments miss",
			frames:     title(600, bursts),
			tolerance:  0.01,
			centredGap: []float64{-2.0 / 3},
		},
		{
			name:       "two features at once",
			frames:     title(600, bursts, ramp),
			tolerance:  0.01,
			centredGap: []float64{-2.0 / 3, 0},
		},
		{
			name:       "a flat feature weighs nothing",
			frames:     title(600, flat, bursts),
			tolerance:  0.01,
			centredGap: []float64{0, -2.0 / 3},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			segments := balance.Segments(testCase.frames, media.Seconds(600), media.Seconds(2), 20)
			require.Len(t, segments, 20)

			for i, s := range segments {
				assert.Equal(t, media.Seconds(2), s.Length())
				assert.GreaterOrEqual(t, s.Start, media.Seconds(float64(i)*30), "segment %d starts in its slot", i)
				assert.LessOrEqual(t, s.End, media.Seconds(float64(i+1)*30), "segment %d ends in its slot", i)
			}

			want, ok := testCase.frames.Means()
			require.True(t, ok)

			got, ok := testCase.frames.Means(segments...)
			require.True(t, ok)

			even, ok := testCase.frames.Means(centred()...)
			require.True(t, ok)

			for k := range want {
				assert.InDelta(t, 0, got[k]/want[k]-1, testCase.tolerance, "feature %d of the digest", k)
				assert.InDelta(t, testCase.centredGap[k], even[k]/want[k]-1, 0.01, "feature %d of centred segments", k)
			}
		})
	}
}

func TestSegmentsKeepsCentredSegmentsOfFlatContent(
	t *testing.T,
) {
	segments := balance.Segments(title(600, flat, flat), media.Seconds(600), media.Seconds(2), 20)
	require.Len(t, segments, 20)

	// Each starts a quarter of a frame before the frame the centred segment
	// starts on.
	for i, want := range centred() {
		assert.Equal(t, want.Start-media.Seconds(0.01), segments[i].Start)
		assert.Equal(t, media.Seconds(2), segments[i].Length())
	}
}

func TestSegmentsHoldWholeFrames(
	t *testing.T,
) {
	frames := title(600, bursts, ramp)

	for i, s := range balance.Segments(frames, media.Seconds(600), media.Seconds(2), 20) {
		held := 0

		for _, pts := range frames.PTS {
			if pts >= s.Start && pts < s.End {
				held++

				// A seek a millisecond off either way finds the same frames.
				assert.Greater(t, pts-s.Start, media.Seconds(0.001), "segment %d", i)
				assert.Greater(t, s.End-pts, media.Seconds(0.001), "segment %d", i)
			}
		}

		assert.Equal(t, 2*frameRate, held, "segment %d", i)

		// Its start times the frame rate rounds to its first frame.
		first := math.Round(s.Start.Seconds() * frameRate)
		assert.InDelta(t, 0.25, first-s.Start.Seconds()*frameRate, 1e-6, "segment %d", i)
	}
}

func TestSegmentsIsDeterministic(
	t *testing.T,
) {
	frames := title(600, bursts, ramp)

	assert.Equal(t,
		balance.Segments(frames, media.Seconds(600), media.Seconds(2), 20),
		balance.Segments(frames, media.Seconds(600), media.Seconds(2), 20),
	)
}

func TestSegmentsOnAStride(
	t *testing.T,
) {
	const gop = 2 * frameRate

	testCases := []struct {
		name string
		make func(balance.Frames) []media.Interval
	}{
		{
			name: "balanced",
			make: func(f balance.Frames) []media.Interval {
				return balance.Segments(f, media.Seconds(600), media.Seconds(2), 20)
			},
		},
		{
			name: "top",
			make: func(f balance.Frames) []media.Interval {
				return balance.Top(f, cutsEvery(30, 600), media.Seconds(2), 20, first)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			frames := title(600, bursts, ramp)
			frames.Stride = gop

			segments := testCase.make(frames)
			require.Len(t, segments, 20)

			for i, s := range segments {
				// The first frame of a segment starts a GOP of the title.
				first := int(math.Round(s.Start.Seconds() * frameRate))
				assert.Zero(t, first%gop, "segment %d starts on frame %d", i, first)
				assert.Equal(t, media.Seconds(2), s.Length())
			}

			// The bursts hold three GOPs in fifteen: the balanced digest
			// still finds the title's share of them, the top one only them.
			busy := 0

			for _, s := range segments {
				if int(math.Round(s.Start.Seconds()*frameRate))%slotFrames < 150 {
					busy++
				}
			}

			if testCase.name == "balanced" {
				assert.Equal(t, 4, busy, "a fifth of the title is busy")
			} else {
				assert.Equal(t, 20, busy)
			}
		})
	}

	t.Run("a slot without a GOP start leaves no digest", func(t *testing.T) {
		frames := title(60, bursts)
		frames.Stride = 20 * frameRate

		assert.Nil(t, balance.Segments(frames, media.Seconds(60), media.Seconds(2), 20))
	})
}

func TestSegmentsUnusable(
	t *testing.T,
) {
	short := title(600, bursts)
	short.Features[0] = short.Features[0][:10]

	testCases := []struct {
		name     string
		frames   balance.Frames
		duration media.Duration
		segment  media.Duration
		count    int
	}{
		{name: "no frame", frames: balance.Frames{}, duration: media.Seconds(600), segment: media.Seconds(2), count: 20},
		{name: "no feature", frames: title(600), duration: media.Seconds(600), segment: media.Seconds(2), count: 20},
		{name: "feature shorter than the frames", frames: short, duration: media.Seconds(600), segment: media.Seconds(2), count: 20},
		{name: "no segment", frames: title(600, bursts), duration: media.Seconds(600), segment: media.Seconds(2)},
		{name: "empty segment", frames: title(600, bursts), duration: media.Seconds(600), count: 20},
		{name: "slot shorter than a segment", frames: title(600, bursts), duration: media.Seconds(600), segment: media.Seconds(40), count: 20},
		{name: "slot beyond the last frame", frames: title(300, bursts), duration: media.Seconds(600), segment: media.Seconds(2), count: 20},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Nil(t, balance.Segments(testCase.frames, testCase.duration, testCase.segment, testCase.count))
		})
	}
}

func TestMeans(
	t *testing.T,
) {
	frames := title(4, ramp, flat)

	testCases := []struct {
		name      string
		frames    balance.Frames
		intervals []media.Interval
		want      []float64
		wantOK    bool
	}{
		{name: "every frame", frames: frames, want: []float64{0.0495, 7}, wantOK: true},
		{
			name:      "frames of the intervals",
			frames:    frames,
			intervals: []media.Interval{{Start: 0, End: media.Seconds(1)}, {Start: media.Seconds(3), End: media.Seconds(4)}},
			want:      []float64{0.0495, 7},
			wantOK:    true,
		},
		{
			name:      "one interval",
			frames:    frames,
			intervals: []media.Interval{{Start: media.Seconds(2), End: media.Seconds(3)}},
			want:      []float64{0.062, 7},
			wantOK:    true,
		},
		{name: "interval without a frame", frames: frames, intervals: []media.Interval{{Start: media.Seconds(9), End: media.Seconds(10)}}},
		{name: "no feature", frames: title(4), intervals: nil},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := testCase.frames.Means(testCase.intervals...)
			require.Equal(t, testCase.wantOK, ok)

			if !ok {
				assert.Nil(t, got)

				return
			}

			assert.InDeltaSlice(t, testCase.want, got, 1e-9)
		})
	}
}

// product scores a segment by the product of its two features.
func product(
	means []float64,
) float64 {
	return means[0] * means[1]
}

// first scores a segment by its first feature.
func first(
	means []float64,
) float64 {
	return means[0]
}

// cutsEvery returns the shot cuts of a title whose shots all last the same.
func cutsEvery(
	seconds, title int,
) []media.Duration {
	var cuts []media.Duration

	for t := seconds; t < title; t += seconds {
		cuts = append(cuts, media.Seconds(float64(t)))
	}

	return cuts
}

func TestTop(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		frames balance.Frames
		cuts   []media.Duration
		count  int
		score  func([]float64) float64
		// wantBusy is the share of the segments' frames in a burst.
		wantBusy float64
		// wantShots is the number of shots the segments show.
		wantShots int
	}{
		{
			name:      "one burst per shot: every segment in a burst, each in its shot",
			frames:    title(600, bursts),
			cuts:      cutsEvery(30, 600),
			count:     20,
			score:     first,
			wantBusy:  1,
			wantShots: 20,
		},
		{
			name:      "fewer shots than segments: shots give several segments",
			frames:    title(600, bursts),
			cuts:      cutsEvery(120, 600),
			count:     20,
			score:     first,
			wantBusy:  1,
			wantShots: 5,
		},
		{
			name:      "no cut: the title is one shot",
			frames:    title(600, bursts),
			count:     20,
			score:     first,
			wantBusy:  1,
			wantShots: 1,
		},
		{
			name:      "the product of two features: the end of the ramp, in bursts",
			frames:    title(600, bursts, ramp),
			cuts:      cutsEvery(30, 600),
			count:     4,
			score:     product,
			wantBusy:  1,
			wantShots: 4,
		},
		{
			name:      "more segments than the bursts hold: the calm fills the digest",
			frames:    title(60, bursts),
			cuts:      cutsEvery(30, 60),
			count:     10,
			score:     first,
			wantBusy:  0.6,
			wantShots: 2,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			segments := balance.Top(testCase.frames, testCase.cuts, media.Seconds(2), testCase.count, testCase.score)
			require.Len(t, segments, testCase.count)

			shots := map[int]bool{}

			for i, s := range segments {
				assert.Equal(t, media.Seconds(2), s.Length())

				if i > 0 {
					assert.GreaterOrEqual(t, s.Start, segments[i-1].End, "segments in the order of the title, never overlapping")
				}

				shot := 0
				for _, cut := range testCase.cuts {
					if cut <= s.Start+media.Seconds(1) {
						shot++
					}
				}

				shots[shot] = true
			}

			assert.Len(t, shots, testCase.wantShots)

			busy, frames := 0, 0

			for i, pts := range testCase.frames.PTS {
				for _, s := range segments {
					if pts >= s.Start && pts < s.End {
						frames++

						if i%slotFrames < 150 {
							busy++
						}
					}
				}
			}

			assert.Equal(t, testCase.count*2*frameRate, frames)
			assert.InDelta(t, testCase.wantBusy, float64(busy)/float64(frames), 0.01)
		})
	}

	t.Run("the highest scores of a ramp are its end", func(t *testing.T) {
		segments := balance.Top(title(600, ramp), nil, media.Seconds(2), 3, first)
		require.Len(t, segments, 3)

		assert.InDelta(t, 594, segments[0].Start.Seconds(), 0.05)
		assert.InDelta(t, 600, segments[2].End.Seconds(), 0.05)
	})
}

func TestTopUnusable(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		frames  balance.Frames
		segment media.Duration
		count   int
	}{
		{name: "no frame", frames: balance.Frames{}, segment: media.Seconds(2), count: 20},
		{name: "one frame", frames: balance.Frames{PTS: []media.Duration{0}, Features: [][]float64{{1}}}, segment: media.Seconds(2), count: 1},
		{name: "no feature", frames: title(600), segment: media.Seconds(2), count: 20},
		{name: "no segment", frames: title(600, bursts), segment: media.Seconds(2)},
		{name: "empty segment", frames: title(600, bursts), count: 20},
		{name: "title too short for the digest", frames: title(30, bursts), segment: media.Seconds(2), count: 20},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Nil(t, balance.Top(testCase.frames, nil, testCase.segment, testCase.count, first))
		})
	}
}
