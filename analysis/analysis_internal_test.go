package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analyze/freeze"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/analyze/siti"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

func TestAnalyzeErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		world   world
		wantErr error
	}{
		{name: "probe", world: world{info: fakeVideo(), probeErr: errProbe}, wantErr: errProbe},
		{name: "packets", world: world{info: fakeVideo(), packetErr: errPackets}, wantErr: errPackets},
		{name: "no stream information", world: world{}, wantErr: ErrNoVideo},
		{name: "no video stream", world: world{info: &media.Info{}}, wantErr: ErrNoVideo},
		{name: "decode", world: world{info: fakeVideo(), decodeErr: errDecode}, wantErr: errDecode},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			report, err := testCase.world.analyzer().Analyze(t.Context(), "clip.mp4", Options{})

			require.ErrorIs(t, err, testCase.wantErr)
			assert.ErrorContains(t, err, "clip.mp4")
			assert.Nil(t, report)
		})
	}
}

func TestAnalyze(
	t *testing.T,
) {
	var events []Progress

	report, err := world{info: fakeVideo()}.analyzer().Analyze(t.Context(), "clip.mp4", Options{
		Video: VideoOptions{
			Workers: 2,
			Freeze:  freeze.Options{MinDuration: media.Seconds(0.5)},
		},
		Progress: func(p Progress) { events = append(events, p) },
	})
	require.NoError(t, err)

	assert.Equal(t, SchemaVersion, report.SchemaVersion)
	assert.Contains(t, report.Timings, "probe")
	assert.Contains(t, report.Timings, "bitstream")
	assert.Contains(t, report.Timings, "video")

	require.Len(t, events, 1+fakeFrames)
	assert.Equal(t, Progress{Stage: StageProbe}, events[0])
	assert.Equal(t, Progress{Stage: StageDecode, Done: fakeFrames, Total: fakeFrames}, events[fakeFrames])

	video := report.Video
	require.NotNil(t, video)
	assert.Equal(t, fakeFrames, video.FramesDecoded)
	assert.Equal(t, []media.Interval{{Start: media.Seconds(1), End: media.Seconds(2)}}, video.Black.Segments)
	assert.Equal(t, []media.Interval{{Start: media.Seconds(1), End: media.Seconds(2)}}, video.Freeze.Segments,
		"the black half is still for 1 s")

	require.Len(t, video.Shots, 2)
	assert.Equal(t, fakeFrames/2, video.Shots[1].FirstFrame)
	assert.Positive(t, video.Shots[0].SIMean)
	assert.Zero(t, video.Shots[1].SIMean, "a flat picture has no texture")
	assert.Zero(t, video.Shots[1].TIMean, "the cut is excluded from the shot motion")
	assert.Equal(t, int64(fakeRate*1000*8), video.Shots[0].Bitrate)
	assert.Equal(t, 3, video.Crop.FramesUsed, "frames 0, 10 and 20: black frames are ignored")

	frames := report.Frames
	require.NotNil(t, frames)
	assert.Len(t, frames.PTS, fakeFrames)
	assert.Equal(t, media.Seconds(1), frames.PTS[fakeRate])
	assert.True(t, frames.Keyframe[fakeRate])
	assert.Equal(t, 1000, frames.Size[0])
	assert.InDelta(t, 16.0, frames.LumaMean[fakeFrames-1], 1e-9)
}

func TestAnalyzeSkipVideo(
	t *testing.T,
) {
	report, err := world{info: fakeVideo(), decodeErr: errDecode}.analyzer().
		Analyze(t.Context(), "clip.mp4", Options{SkipVideo: true})
	require.NoError(t, err)

	assert.Nil(t, report.Video)
	assert.Nil(t, report.Frames)
	assert.Equal(t, fakeFrames, report.Bitstream.PacketCount)
	assert.NotContains(t, report.Timings, "video")
}

func TestCompare(
	t *testing.T,
) {
	vmaf := &quality.Result{Mean: 93}
	inspected := &Report{Info: fakeVideo()}
	analysed := &Report{Info: fakeVideo(), Video: &VideoReport{}, Frames: &FrameSeries{}}

	testCases := []struct {
		name      string
		world     world
		opts      CompareOptions
		wantErr   error
		wantCalls int
	}{
		{name: "no meter", world: world{info: fakeVideo()}, wantErr: ErrNoMeter},
		{
			name:    "inspection failure",
			world:   world{info: fakeVideo(), probeErr: errProbe, meter: &fakeMeter{result: vmaf}},
			wantErr: errProbe,
		},
		{
			name:      "measure failure",
			world:     world{info: fakeVideo(), meter: &fakeMeter{err: errMeasure}},
			wantErr:   errMeasure,
			wantCalls: 1,
		},
		{
			name:      "both files inspected",
			world:     world{info: fakeVideo(), meter: &fakeMeter{result: vmaf}},
			wantCalls: 1,
		},
		{
			name:      "the inspected reference is reused",
			world:     world{info: fakeVideo(), meter: &fakeMeter{result: vmaf}},
			opts:      CompareOptions{Reference: inspected},
			wantCalls: 1,
		},
		{
			name:      "the analysed distorted video is reused, without its frame analysis",
			world:     world{info: fakeVideo(), meter: &fakeMeter{result: vmaf}},
			opts:      CompareOptions{Distorted: analysed},
			wantCalls: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cmp, err := testCase.world.analyzer().Compare(t.Context(), "ref.mp4", "dist.mp4", testCase.opts)

			if meter, ok := testCase.world.meter.(*fakeMeter); ok {
				assert.Len(t, meter.calls, testCase.wantCalls)
			}

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.Nil(t, cmp)

				return
			}

			require.NoError(t, err)
			assert.Same(t, vmaf, cmp.VMAF)
			assert.Nil(t, cmp.Distorted.Video, "a comparison never decodes")
			assert.Contains(t, cmp.Timings, "inspect")
			assert.Contains(t, cmp.Timings, "vmaf")

			if testCase.opts.Reference != nil {
				assert.Same(t, testCase.opts.Reference, cmp.Reference)
			}

			if d := testCase.opts.Distorted; d != nil {
				assert.Same(t, d.Info, cmp.Distorted.Info)
				assert.Nil(t, cmp.Distorted.Frames)
				assert.NotNil(t, d.Video, "the caller's report is left intact")
			}

			call := testCase.world.meter.(*fakeMeter).calls[0]
			assert.Equal(t, "ref.mp4", call[0].Path)
			assert.Equal(t, "dist.mp4", call[1].Path)
			assert.Equal(t, fakeWidth, call[1].Video.Width)
			assert.Same(t, cmp.Distorted.Bitstream, call[1].Bitstream)
		})
	}
}

func TestQualityOptions(
	t *testing.T,
) {
	cuts := []media.Duration{media.Seconds(2), media.Seconds(5)}
	explicit := []media.Duration{media.Seconds(9)}
	analysed := &Report{Video: &VideoReport{Shots: shotsAt(0, 2, 5)}}
	budget := quality.Sample{PerScene: 1}

	testCases := []struct {
		name string
		opts CompareOptions
		want []media.Duration
	}{
		{
			name: "a budget takes the shots of the analysed distorted video",
			opts: CompareOptions{Quality: quality.Options{Sample: budget}, Distorted: analysed},
			want: cuts,
		},
		{
			name: "cuts given by the caller are kept",
			opts: CompareOptions{Quality: quality.Options{Sample: budget, Cuts: explicit}, Distorted: analysed},
			want: explicit,
		},
		{
			name: "precision-driven sampling keeps its keyframe strata",
			opts: CompareOptions{Quality: quality.Options{Precision: 0.5}, Distorted: analysed},
		},
		{
			name: "no analysed distorted video",
			opts: CompareOptions{Quality: quality.Options{Sample: budget}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, qualityOptions(testCase.opts).Cuts)
		})
	}
}

func TestShotCuts(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		report *Report
		want   []media.Duration
	}{
		{name: "no frame analysis", report: &Report{}},
		{name: "a single shot", report: &Report{Video: &VideoReport{Shots: shotsAt(0)}}},
		{
			name:   "every shot start but the first",
			report: &Report{Video: &VideoReport{Shots: shotsAt(0, 2, 5)}},
			want:   []media.Duration{media.Seconds(2), media.Seconds(5)},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.report.ShotCuts())
		})
	}
}

// shotsAt returns shots starting at the given seconds.
func shotsAt(
	starts ...float64,
) []ShotReport {
	shots := make([]ShotReport, len(starts))
	for i, s := range starts {
		shots[i].Start = media.Seconds(s)
	}

	return shots
}

func TestNewMeter(
	t *testing.T,
) {
	var typedNil *quality.Meter

	testCases := []struct {
		name      string
		meter     Meter
		wantMeter bool
	}{
		{name: "no meter", meter: nil},
		{name: "typed nil meter", meter: typedNil},
		{name: "meter", meter: &fakeMeter{}, wantMeter: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := New(nil, nil, nil, nil, testCase.meter)

			assert.Equal(t, testCase.wantMeter, a.meter != nil, "a nil meter must not hide ErrNoMeter")
			assert.NotNil(t, a.logger, "a nil logger discards")
		})
	}
}

func TestClassify(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		si, ti float64
		want   ComplexityHint
	}{
		{name: "low", si: 29.9, ti: 7.9, want: ComplexityHint{Spatial: complexityLow, Temporal: complexityLow}},
		{name: "medium", si: 30, ti: 8, want: ComplexityHint{Spatial: complexityMedium, Temporal: complexityMedium}},
		{name: "high", si: 70, ti: 25, want: ComplexityHint{Spatial: complexityHigh, Temporal: complexityHigh}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var res siti.Result
			res.SISummary.Mean, res.TISummary.Mean = testCase.si, testCase.ti

			assert.Equal(t, testCase.want, classify(res))
		})
	}
}

func TestShotReports(
	t *testing.T,
) {
	shot := func(first, last int, start, end float64) scene.Shot {
		return scene.Shot{
			Interval:   media.Interval{Start: media.Seconds(start), End: media.Seconds(end)},
			FirstFrame: first,
			LastFrame:  last,
		}
	}

	res := siti.Result{SI: []float64{10, 20, 30}, TI: []float64{0, 4, 6}}
	bs := &bitstream.Report{FrameSizes: []int{100, 100, 100}}

	testCases := []struct {
		name string
		shot scene.Shot
		want ShotReport
	}{
		{
			name: "means and bitrate",
			shot: shot(0, 2, 0, 1),
			want: ShotReport{Frames: 3, SIMean: 20, TIMean: 5, Bitrate: 2400},
		},
		{
			name: "a single frame shot keeps its TI",
			shot: shot(2, 2, 1, 1.5),
			want: ShotReport{Frames: 1, SIMean: 30, TIMean: 6, Bitrate: 1600},
		},
		{
			name: "an empty interval has no bitrate",
			shot: shot(1, 1, 1, 1),
			want: ShotReport{Frames: 1, SIMean: 20, TIMean: 4},
		},
		{
			name: "frames beyond the series are ignored",
			shot: shot(5, 6, 2, 3),
			want: ShotReport{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			want := testCase.want
			want.Shot = testCase.shot

			assert.Equal(t, []ShotReport{want}, shotReports([]scene.Shot{testCase.shot}, res, bs))
		})
	}
}
