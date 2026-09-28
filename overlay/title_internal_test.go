package overlay

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

func TestSlidingBitrate(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		pts   []media.Duration
		sizes []int
		want  []float64
	}{
		{
			name:  "the window fills over the first second",
			pts:   ptsAt(3, 500*time.Millisecond),
			sizes: []int{1000, 2000, 3000},
			// 8000 bits, then 24 000, then the first frame leaves the
			// window: 40 000.
			want: []float64{8000, 24_000, 40_000},
		},
		{
			name:  "missing sizes count as empty frames",
			pts:   ptsAt(2, 500*time.Millisecond),
			sizes: []int{1000},
			want:  []float64{8000, 8000},
		},
		{
			name:  "a single frame",
			pts:   []media.Duration{0},
			sizes: []int{1000},
			want:  []float64{8000},
		},
		{
			name:  "duplicate timestamps",
			pts:   []media.Duration{0, 0},
			sizes: []int{1000, 1000},
			want:  []float64{8000, 16_000},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := slidingBitrate(testCase.pts, testCase.sizes)
			require.Len(t, got, len(testCase.want))

			for i := range got {
				assert.InDelta(t, testCase.want[i], got[i], 1e-6, "frame %d", i)
			}
		})
	}
}

// TestScoreIndexes checks that scores land on the frames of the same time,
// even when the compared video has more frames than the reference.
func TestScoreIndexes(
	t *testing.T,
) {
	report := inspection()

	testCases := []struct {
		name   string
		scores []media.Duration
		want   map[int]int
	}{
		{name: "same timeline", scores: []media.Duration{0, report.Bitstream.PTS[3]}, want: map[int]int{0: 0, 3: 1}},
		{name: "closest frame", scores: []media.Duration{media.Duration(38 * time.Millisecond), media.Duration(83 * time.Millisecond)}, want: map[int]int{1: 0, 2: 1}},
		{name: "beyond the last frame", scores: []media.Duration{media.Duration(time.Second)}, want: map[int]int{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := &quality.Result{}
			for _, pts := range testCase.scores {
				res.Frames = append(res.Frames, quality.FrameScore{PTS: pts})
			}

			title, err := newTitle(Input{Report: report, Quality: res})
			require.NoError(t, err)

			got := map[int]int{}

			for i, s := range title.score {
				if s >= 0 {
					got[i] = s
				}
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestFrameDuration(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		pts      []media.Duration
		duration media.Duration
		i        int
		want     media.Duration
	}{
		{name: "until the next frame", pts: ptsAt(3, frameStep), i: 0, want: media.Duration(frameStep)},
		{name: "last frame", pts: ptsAt(3, frameStep), i: 2, want: media.Duration(frameStep)},
		{name: "single frame", pts: ptsAt(1, frameStep), duration: media.Seconds(2), i: 0, want: media.Seconds(2)},
		{name: "single frame without duration", pts: ptsAt(1, frameStep), i: 0, want: bitrateWindow},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tt := &title{pts: testCase.pts, duration: testCase.duration}
			assert.Equal(t, testCase.want, tt.frameDuration(testCase.i))
		})
	}
}

// TestShortFrameAnalysis checks that frames beyond the frame analysis
// columns (a truncated analysis) show nothing rather than fail.
func TestShortFrameAnalysis(
	t *testing.T,
) {
	report := analysed()
	f := report.Frames
	f.SI, f.LumaMean, f.PeakNits, f.MotionConfidence = f.SI[:2], f.LumaMean[:2], f.PeakNits[:2], f.MotionConfidence[:2]
	report.Video.Levels.OutOfRange = nil
	report.Video.Shots[1].Camera = nil
	report.Video.Shots = report.Video.Shots[:1]
	report.Bitstream.FrameSizes = report.Bitstream.FrameSizes[:2]

	title, err := newTitle(Input{Report: report})
	require.NoError(t, err)

	last := title.n() - 1
	assert.Empty(t, title.sitiRow(last))
	assert.Empty(t, title.lumaRow(last))
	assert.Empty(t, title.lightRow(last))
	assert.Contains(t, title.moveRow(last), "–")
	assert.Contains(t, title.cameraRow(last), "–")
	assert.Contains(t, title.shotRow(last), "–")
	assert.Contains(t, title.sizeRow(last), "–")
	assert.True(t, title.outOfRange(3), "from the extremes without the share")
	assert.False(t, title.outOfRange(last), "no extremes")
}

func TestChartSeries(
	t *testing.T,
) {
	instant := inspection()
	instant.Bitstream.Duration = 0

	noScore := &quality.Result{Frames: []quality.FrameScore{{PTS: media.Seconds(9)}}}

	testCases := []struct {
		name     string
		input    Input
		bins     int
		wantName string
		wantLo   float64
		wantHi   float64
		wantNaN  int
	}{
		{name: "bitrate", input: Input{Report: inspection()}, bins: 4, wantName: "BITRATE", wantHi: 6.78e6},
		{name: "more bins than frames", input: Input{Report: inspection()}, bins: 100, wantName: "BITRATE", wantHi: 6.78e6},
		{name: "vmaf", input: Input{Report: inspection(), Quality: exactQuality()}, bins: 6, wantName: "VMAF", wantLo: 70, wantHi: 100},
		{name: "vmaf without a matching frame", input: Input{Report: inspection(), Quality: noScore}, bins: 6, wantName: "VMAF", wantLo: 80, wantHi: 100, wantNaN: 6},
		{name: "instantaneous title", input: Input{Report: instant}, bins: 3, wantName: "BITRATE", wantHi: 6.5e6},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			title, err := newTitle(testCase.input)
			require.NoError(t, err)

			s := title.chartSeries(testCase.bins)
			assert.Equal(t, testCase.wantName, s.name)
			assert.InDelta(t, testCase.wantLo, s.lo, 1e-9)
			assert.InDelta(t, testCase.wantHi, s.hi, testCase.wantHi*0.01)

			nan := 0

			for _, v := range s.values {
				if math.IsNaN(v) {
					nan++
				}
			}

			assert.Equal(t, testCase.wantNaN, nan)
		})
	}
}

func TestShotTicksWithoutDuration(
	t *testing.T,
) {
	withShots := &title{video: &analysis.VideoReport{}, duration: 0}
	assert.Empty(t, withShots.shotTicks(100))

	withoutShots := &title{}
	assert.Empty(t, withoutShots.shotTicks(100))
}

func TestBarsWithoutValues(
	t *testing.T,
) {
	s := series{values: []float64{math.NaN(), math.NaN()}, lo: 0, hi: 1}
	assert.Empty(t, s.bars(100, 10))
}

func TestVector(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		pan, tilt float64
		want      string
	}{
		{name: "still camera", pan: 0.3, tilt: 0.1, want: "m -3 -3 l 3 -3 3 3 -3 3 "},
		{name: "slow pan right", pan: 2, want: "m 0 2 l 5 2 5 7 14 0 5 -7 5 -2 0 -2 "},
		{name: "fast tilt up", tilt: 30, want: "m 2 0 l 2 -15 7 -15 0 -24 -7 -15 -2 -15 -2 0 "},
		{name: "medium pan left", pan: -6, want: "m 0 -2 l -10 -2 -10 -7 -19 0 -10 7 -10 2 0 2 "},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, vector(testCase.pan, testCase.tilt))
		})
	}
}

func TestMetricSeries(
	t *testing.T,
) {
	res := exactQuality()
	res.Metrics = []quality.MetricResult{
		{Name: quality.SeriesXPSNRU}, {Name: quality.SeriesCAMBI}, {Name: quality.SeriesPSNRCb},
		{Name: quality.SeriesXPSNRY}, {Name: quality.SeriesPSNRY}, {Name: "vmaf_phone"},
	}

	title, err := newTitle(Input{Report: inspection(), Quality: res})
	require.NoError(t, err)

	assert.Equal(t, []string{quality.SeriesXPSNRY, quality.SeriesPSNRY, quality.SeriesCAMBI, quality.SeriesXPSNRU}, title.metricSeries())
	assert.True(t, strings.HasSuffix(title.metricRow(quality.SeriesPSNRY)(0), "–"), "a scored frame without the metric")
}

// TestWriteSparseData writes the overlay of a truncated frame analysis and
// of scores matching no frame: rows and charts without values stay empty.
func TestWriteSparseData(
	t *testing.T,
) {
	report := analysed()
	f := report.Frames
	f.SI, f.LumaMean, f.PeakNits = f.SI[:2], f.LumaMean[:2], f.PeakNits[:2]

	early := &quality.Result{Frames: []quality.FrameScore{{PTS: -media.Seconds(5)}}}

	var out bytes.Buffer
	require.NoError(t, Write(&out, Input{Report: report, Quality: early}, Options{Font: testFont}))

	script := out.String()
	assert.Contains(t, script, "80–100")
	assert.NotContains(t, script, `\clip(`, "no bars to light up")
}
