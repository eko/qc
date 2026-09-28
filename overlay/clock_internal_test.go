package overlay

import (
	"bufio"
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

// filterMillis is the time at which ffmpeg's subtitles filter renders a
// frame: its timestamp in the stream's time base, in milliseconds,
// truncated (vf_subtitles passes pts × time_base × 1000 to libass as an
// integer).
func filterMillis(
	num, den int64,
	frame int64,
	timeBase int64,
	offset time.Duration,
) int64 {
	// The timestamp of the frame in the stream's time base.
	pts := int64(math.Round(float64(frame) * float64(den) / float64(num) * float64(timeBase)))
	ms := float64(pts)/float64(timeBase)*1000 + float64(offset.Milliseconds())

	return int64(ms)
}

// TestClockFrameBoundaries checks, for common frame rates, that every
// frame is rendered with its own events only: the time the filter renders
// frame i at falls in [start(i), start(i+1)).
func TestClockFrameBoundaries(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		num, den int64
		timeBase int64
		offset   time.Duration
	}{
		{name: "23.976 fps", num: 24000, den: 1001, timeBase: 24000},
		{name: "24 fps", num: 24, den: 1, timeBase: 12288},
		{name: "25 fps", num: 25, den: 1, timeBase: 12800},
		{name: "29.97 fps", num: 30000, den: 1001, timeBase: 30000},
		{name: "29.97 fps in a 90 kHz stream", num: 30000, den: 1001, timeBase: 90000},
		{name: "30 fps delayed by 67 ms", num: 30, den: 1, timeBase: 15360, offset: 67 * time.Millisecond},
		{name: "50 fps", num: 50, den: 1, timeBase: 12800},
		{name: "59.94 fps", num: 60000, den: 1001, timeBase: 60000},
		{name: "60 fps", num: 60, den: 1, timeBase: 15360},
	}

	// About 40 minutes at 23.976 fps: rounding drift would show.
	const frames = 60_000

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pts := make([]media.Duration, frames)
			for i := range pts {
				pts[i] = media.Duration(time.Duration(float64(i) * float64(testCase.den) / float64(testCase.num) * float64(time.Second)))
			}

			duration := pts[frames-1] + media.Duration(time.Duration(float64(testCase.den)/float64(testCase.num)*float64(time.Second)))
			clk := newClock(pts, duration, media.Duration(testCase.offset))
			require.Equal(t, frames, clk.frames())

			for i := range int64(frames) {
				at := filterMillis(testCase.num, testCase.den, i, testCase.timeBase, testCase.offset)

				require.LessOrEqual(t, clk.start(int(i)).millis(), at, "frame %d starts too late", i)
				require.Greater(t, clk.start(int(i)+1).millis(), at, "frame %d ends too early", i)
			}
		})
	}
}

func TestClockEdges(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		pts       []media.Duration
		duration  media.Duration
		wantStart []centis
	}{
		{
			name:      "single frame without duration",
			pts:       []media.Duration{0},
			wantStart: []centis{0, 1},
		},
		{
			name:      "frames closer than a centisecond share a start",
			pts:       ptsAt(4, 4*time.Millisecond),
			duration:  media.Duration(16 * time.Millisecond),
			wantStart: []centis{0, 0, 1, 1, 3},
		},
		{
			name:      "the duration rounds up",
			pts:       ptsAt(2, frameStep),
			duration:  media.Duration(81 * time.Millisecond),
			wantStart: []centis{0, 2, 9},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			clk := newClock(testCase.pts, testCase.duration, 0)
			assert.Equal(t, testCase.wantStart, clk.starts)
			assert.Equal(t, testCase.wantStart[len(testCase.wantStart)-1], clk.end())
		})
	}
}

func TestCentisString(
	t *testing.T,
) {
	testCases := []struct {
		name string
		c    centis
		want string
	}{
		{name: "zero", c: 0, want: "0:00:00.00"},
		{name: "minutes", c: 12_345, want: "0:02:03.45"},
		{name: "hours", c: 10*360_000 + 59*6000 + 5, want: "10:59:00.05"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.c.String())
		})
	}
}

// TestWriteTrack checks the coalescing: one event per run of frames showing
// the same text, none for empty text or runs too short to time.
func TestWriteTrack(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		pts   []media.Duration
		texts []string
		want  []string
	}{
		{
			name:  "runs",
			pts:   ptsAt(5, frameStep),
			texts: []string{"a", "a", "b", "", "b"},
			want: []string{
				"Dialogue: 1,0:00:00.00,0:00:00.06,qc,,0,0,0,,a",
				"Dialogue: 1,0:00:00.06,0:00:00.10,qc,,0,0,0,,b",
				"Dialogue: 1,0:00:00.14,0:00:00.20,qc,,0,0,0,,b",
			},
		},
		{
			name:  "a static text is one event",
			pts:   ptsAt(1000, frameStep),
			texts: []string{"x"},
			want:  []string{"Dialogue: 1,0:00:00.00,0:00:40.00,qc,,0,0,0,,x"},
		},
		{
			name:  "runs shorter than a centisecond are dropped",
			pts:   ptsAt(3, 4*time.Millisecond),
			texts: []string{"a", "b", "c"},
			want: []string{
				"Dialogue: 1,0:00:00.00,0:00:00.01,qc,,0,0,0,,b",
				"Dialogue: 1,0:00:00.01,0:00:00.12,qc,,0,0,0,,c",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			clk := newClock(testCase.pts, media.Duration(len(testCase.pts))*media.Duration(frameStep), 0)

			var out bytes.Buffer

			w := bufio.NewWriter(&out)
			writeTrack(w, clk, track{layer: layerContent, text: func(i int) string {
				return testCase.texts[min(i, len(testCase.texts)-1)]
			}})
			require.NoError(t, w.Flush())

			assert.Equal(t, testCase.want, strings.Split(strings.TrimSpace(out.String()), "\n"))
		})
	}
}
