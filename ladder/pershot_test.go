package ladder

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

// shotSource is a 25 fps 1080p source of the given length whose shots last
// shotSeconds, alternating easy and hard ones.
func shotSource(
	seconds, shotSeconds float64,
) *analysis.Report {
	r := sourceReport(1920, 1080, 8, 25, seconds)
	frames := int(seconds * 25)
	step := int(shotSeconds * 25)
	video := &analysis.VideoReport{FramesDecoded: frames}

	for i, first := 0, 0; first < frames; i, first = i+1, first+step {
		last := min(first+step, frames) - 1
		video.Shots = append(video.Shots, analysis.ShotReport{
			Shot:    scene.Shot{FirstFrame: first, LastFrame: last},
			Frames:  last - first + 1,
			TIMean:  float64(5 + 10*(i%2)),
			Bitrate: int64(20e6 * (1 + float64(i%3))),
		})
	}

	r.Video = video

	return r
}

func TestBuildPerShot(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		source *analysis.Report
		opts   Options
		check  func(t *testing.T, res *Result)
	}{
		{
			name:   "long title: digest shots measured, others predicted",
			source: shotSource(600, 3.3),
			opts:   Options{Codec: "h264", PerShot: true},
			check: func(t *testing.T, res *Result) {
				measured := 0

				for _, s := range res.Shots {
					assert.Zero(t, s.Start%50, "shots start on the GOP grid")

					if s.Measured {
						measured++
					}
				}

				assert.Positive(t, measured)
				assert.Less(t, measured, len(res.Shots))

				for _, r := range res.Rungs {
					require.NotNil(t, r.PerShot)
					require.NotNil(t, r.PerShot.Measured)
					assert.Positive(t, r.PerShot.Lambda)
					// The verification's rate cap costs what the uncapped
					// shot models cannot see.
					assert.InDelta(t, r.PerShot.Measured.VMAF, r.PerShot.PredictedVMAF, 2.5)
					assert.Contains(t, r.PerShot.Command, "-f concat")
					assertChunksCover(t, r.PerShot.Chunks, 15000)
					assertShotAllocations(t, res.Shots, r.PerShot, true)
				}

				assert.Contains(t, res.Timings, StageShots)
				require.NotNil(t, res.ShotProbing)
				assert.Equal(t, ShotProbing{Probes: 2 * len(shotHeights(res.Rungs))}, *res.ShotProbing)
			},
		},
		{
			name:   "per-shot resolution",
			source: shotSource(60, 4),
			opts:   Options{Codec: "h264", PerShotResolution: true},
			check: func(t *testing.T, res *Result) {
				require.NotNil(t, res.ShotProbing)
				assert.True(t, res.ShotProbing.Resolution)
				assert.Equal(t, 2*len(shotHeights(res.Rungs))+res.ShotProbing.Extra, res.ShotProbing.Probes)

				moved := 0

				for _, r := range res.Rungs {
					require.NotNil(t, r.PerShot)
					require.NotNil(t, r.PerShot.Measured)
					assertChunksCover(t, r.PerShot.Chunks, 1500)
					w, h := declaredGeometry(r.PerShot.Chunks)
					assert.Equal(t, [2]int{w, h}, [2]int{r.PerShot.Width, r.PerShot.Height}, "the rendition declares its largest resolution")

					for i, a := range r.PerShot.Shots {
						require.Positive(t, a.Height, "shot %d", i)
						assert.Equal(t, a.Width*1080, a.Height*1920, "shot %d keeps the aspect ratio", i)

						if a.Height != r.Height {
							moved++
						}
					}

					for _, c := range r.PerShot.Chunks {
						assert.Positive(t, c.Height)
						assert.Contains(t, r.PerShot.Command, fmt.Sprintf("scale=%d:%d", c.Width, c.Height))
					}
				}

				t.Logf("%d shot allocations moved resolution", moved)
			},
		},
		{
			name:   "short title without verification: every shot measured",
			source: shotSource(30, 4),
			opts:   Options{Codec: "av1", PerShot: true, SkipVerify: true},
			check: func(t *testing.T, res *Result) {
				for _, s := range res.Shots {
					assert.True(t, s.Measured)
				}

				for _, r := range res.Rungs {
					require.NotNil(t, r.PerShot)
					assert.Nil(t, r.PerShot.Measured)
					assert.Zero(t, r.PerShot.Gain)

					for _, c := range r.PerShot.Chunks {
						assert.Equal(t, math.Round(c.CRF), c.CRF, "SVT-AV1 takes integer CRFs")
					}

					assertShotAllocations(t, res.Shots, r.PerShot, false)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, testCase.source)
			progress := &progressLog{}
			opts := testCase.opts
			opts.Progress = progress.record

			res, err := labEngine(lab).Build(t.Context(), sourcePath, opts)
			require.NoError(t, err)
			assert.False(t, progress.overlap.Load())
			assert.NotEmpty(t, progress.stage(StageShots))

			testCase.check(t, res)
		})
	}
}

// assertShotAllocations checks the per-shot ladder cells of a rung: one per
// shot, at the CRF of the chunk holding it, measured on the verification
// exactly for the shots the digest covers.
func assertShotAllocations(
	t *testing.T,
	shots []Shot,
	ps *PerShot,
	verified bool,
) {
	t.Helper()
	require.Len(t, ps.Shots, len(shots))

	for i, s := range shots {
		a := ps.Shots[i]
		assert.Positive(t, s.SourceBitrate)
		assert.Positive(t, s.TI)
		assert.Positive(t, a.PredictedBitrate)
		assert.Positive(t, a.PredictedVMAF)

		for _, c := range ps.Chunks {
			if s.Start >= c.Start && s.Start < c.Start+c.Frames {
				assert.InDelta(t, c.CRF, a.CRF, 1e-9, "shot %d", i)
			}
		}

		if !verified || !s.Measured {
			assert.Nil(t, a.Measured, "shot %d", i)

			continue
		}

		require.NotNil(t, a.Measured, "shot %d", i)
		assert.Positive(t, a.Measured.Bitrate)

		if a.Measured.ScoredFrames > 0 {
			assert.Positive(t, a.Measured.VMAF)
		}
	}

	assert.Positive(t, ps.PooledBitrate(shots))
}

// TestBuildPerShotVideoStartingLate builds per-shot rungs of a source
// whose video starts after its audio: the digest is cut and the rendered
// commands seek from the video's first frame, while the chunked encodes of
// the digest, whose timestamps start at 0, seek from 0.
func TestBuildPerShotVideoStartingLate(
	t *testing.T,
) {
	source := shotSource(30, 4)
	source.Bitstream = &bitstream.Report{Start: media.Seconds(0.2)}
	lab := newFakeLab(rateModel{}, source)

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "h264", PerShot: true})
	require.NoError(t, err)

	require.Len(t, lab.digests, 1)
	assert.Equal(t, media.Seconds(0.2), lab.digests[0].Origin)

	require.NotEmpty(t, lab.chunkSources)

	for _, src := range lab.chunkSources {
		assert.Zero(t, src.Origin, "the digest starts at 0")
	}

	for _, r := range res.Rungs {
		require.NotNil(t, r.PerShot)
		assert.Contains(t, r.PerShot.Command, "ffmpeg -seek_timestamp 1 -ss 0.000000 -i "+sourcePath+" -frames:v ")
		assert.Contains(t, r.PerShot.Command, " -vf trim=start=0.180000,setpts=PTS-STARTPTS,")
	}
}

func TestPooledBitrate(
	t *testing.T,
) {
	shots := []Shot{{Frames: 100}, {Frames: 300}}

	testCases := []struct {
		name  string
		shots []Shot
		in    []ShotAllocation
		want  float64
	}{
		{name: "frame-weighted", shots: shots, in: []ShotAllocation{{PredictedBitrate: 4e6}, {PredictedBitrate: 2e6}}, want: 2.5e6},
		{name: "extra allocations ignored", shots: shots[:1], in: []ShotAllocation{{PredictedBitrate: 4e6}, {PredictedBitrate: 2e6}}, want: 4e6},
		{name: "none", shots: shots, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, (&PerShot{Shots: testCase.in}).PooledBitrate(testCase.shots), 1e-6)
		})
	}
}

// assertChunksCover checks that chunks tile frames without gap or overlap.
func assertChunksCover(
	t *testing.T,
	chunks []encode.Chunk,
	frames int,
) {
	t.Helper()

	next := 0
	for i, c := range chunks {
		assert.Equal(t, next, c.Start, "chunk %d", i)
		next += c.Frames

		if i > 0 {
			assert.False(t, chunks[i-1].CRF == c.CRF && chunks[i-1].Height == c.Height, "adjacent chunks sharing their settings are merged")
		}
	}

	assert.Equal(t, frames, next)
}

func TestBuildPerShotErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		source  *analysis.Report
		failOn  func(op, target string, seen int) bool
		wantErr error
		wantMsg string
	}{
		{
			name:    "no shot",
			source:  sourceReport(1920, 1080, 8, 25, 600),
			wantErr: ErrNoShots,
			wantMsg: "ladder: per-shot: no shot detected",
		},
		{
			name:    "source analysis",
			failOn:  failing(opAnalyze, "source.mov", 1),
			wantErr: errFake,
			wantMsg: "per-shot: analyse",
		},
		{
			name:    "per-shot probe",
			failOn:  failing(opEncode, "shots-1.mp4", 0),
			wantErr: errFake,
			wantMsg: "per-shot: shots-1",
		},
		{
			name:    "per-shot probe measurement",
			failOn:  failing(opCompare, "shots-0.mp4", 0),
			wantErr: errFake,
			wantMsg: "shots-0: measure",
		},
		{
			name:    "per-shot verification",
			failOn:  failing(opEncode, "pershot-0.mp4", 0),
			wantErr: errFake,
			wantMsg: "per-shot: pershot-0",
		},
		{
			name:    "per-shot verification measurement",
			failOn:  failing(opCompare, "pershot-1.mp4", 0),
			wantErr: errFake,
			wantMsg: "pershot-1: measure",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			source := testCase.source
			if source == nil {
				source = shotSource(600, 4)
			}

			lab := newFakeLab(rateModel{}, source)
			lab.failOn = testCase.failOn

			_, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "h264", PerShot: true})
			require.ErrorIs(t, err, testCase.wantErr)

			if testCase.wantMsg != "" {
				assert.Contains(t, err.Error(), testCase.wantMsg)
			}
		})
	}
}

func TestShotsOf(
	t *testing.T,
) {
	testCases := []struct {
		name string
		cuts []int
		want []Shot
	}{
		{name: "no cut", want: []Shot{{Start: 0, Frames: 200}}},
		{
			name: "cuts move to the nearest GOP boundary and merge",
			cuts: []int{20, 30, 60, 70, 190},
			want: []Shot{{Start: 0, Frames: 50}, {Start: 50, Frames: 150}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, shotsOf(testCase.cuts, 200, 50))
		})
	}
}

func TestPiecesOf(
	t *testing.T,
) {
	shots := []Shot{{Start: 0, Frames: 100}, {Start: 100, Frames: 100}, {Start: 200, Frames: 100}}

	got := piecesOf(shots, []int{80, 250}, []int{50, 50})
	assert.Equal(t, []piece{
		{shot: 0, start: 0, frames: 20},
		{shot: 1, start: 20, frames: 30},
		{shot: 2, start: 50, frames: 50},
	}, got)
}

func TestEqualQualityGain(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		a, b  Measurement
		slope float64
		want  float64
	}{
		{name: "same quality", a: Measurement{Bitrate: 1000, VMAF: 90}, b: Measurement{Bitrate: 900, VMAF: 90}, slope: 10, want: 0.1},
		{name: "per-shot one point better", a: Measurement{Bitrate: 1000, VMAF: 90}, b: Measurement{Bitrate: 1000, VMAF: 91}, slope: 10, want: 1 - math.Exp(-0.1)},
		{name: "no slope", a: Measurement{Bitrate: 1000, VMAF: 90}, b: Measurement{Bitrate: 800, VMAF: 95}, want: 0.2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, equalQualityGain(testCase.a, testCase.b, testCase.slope), 1e-9)
		})
	}
}

func TestRungSlope(
	t *testing.T,
) {
	curves := []Curve{NewCurve([]Probe{
		{Height: 720, Bitrate: 1_000_000, VMAF: 80},
		{Height: 720, Bitrate: 2_718_282, VMAF: 90},
	})}

	assert.InDelta(t, 10, rungSlope(curves, Rung{Height: 720, Bitrate: 1_500_000}), 1e-3)
	assert.Zero(t, rungSlope(curves, Rung{Height: 1080, Bitrate: 1_500_000}))
}

func TestPieceStats(
	t *testing.T,
) {
	p := shotProbe{sizes: []int{1000, 2000, 3000}, scores: []float64{90, 80, 70}}

	rate, vmaf := pieceStats(p, piece{start: 1, frames: 2}, 25)
	assert.InDelta(t, 2500*8*25, rate, 1e-9)
	assert.InDelta(t, 75, vmaf, 1e-9)

	rate, vmaf = pieceStats(p, piece{start: 5, frames: 2}, 25)
	assert.InDelta(t, 1, rate, 1e-9)
	assert.Zero(t, vmaf)
}

func TestTitleFrames(
	t *testing.T,
) {
	b := &build{}
	report := &analysis.Report{Video: &analysis.VideoReport{Shots: []analysis.ShotReport{{Shot: scene.Shot{LastFrame: 99}}}}}
	assert.Equal(t, 100, b.titleFrames(report), "the last shot's end without a decoded count")

	report.Video.FramesDecoded = 120
	assert.Equal(t, 120, b.titleFrames(report))
}

func TestShotInterval(
	t *testing.T,
) {
	withRate := func(avg, nominal media.Rational) *Result {
		r := sourceReport(1920, 1080, 8, 25, 60)
		r.Info.Video[0].AvgFrameRate, r.Info.Video[0].FrameRate = avg, nominal

		return &Result{Source: r}
	}

	shot := Shot{Start: 50, Frames: 100}

	testCases := []struct {
		name string
		in   *Result
		want media.Interval
	}{
		{name: "average rate", in: withRate(media.Rational{Num: 25, Den: 1}, media.Rational{}), want: media.Interval{Start: media.Seconds(2), End: media.Seconds(6)}},
		{name: "nominal rate", in: withRate(media.Rational{}, media.Rational{Num: 50, Den: 1}), want: media.Interval{Start: media.Seconds(1), End: media.Seconds(3)}},
		{name: "no rate", in: withRate(media.Rational{}, media.Rational{})},
		{name: "no source", in: &Result{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.in.ShotInterval(shot))
		})
	}
}

func TestLevelAt(
	t *testing.T,
) {
	levels := []shotLevel{{low: 12, high: 21.5}, {low: 21.5, high: 30.5}, {low: 30.5, high: 40}}

	testCases := []struct {
		name string
		crf  float64
		want float64
	}{
		{name: "below every probe", crf: 8, want: 12},
		{name: "inside a segment", crf: 25, want: 21.5},
		{name: "on a probe", crf: 30.5, want: 21.5},
		{name: "above every probe", crf: 45, want: 30.5},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, levelAt(levels, testCase.crf).low, 1e-9)
		})
	}
}

func TestShotProbePlan(
	t *testing.T,
) {
	ranges := map[int][2]float64{1080: {22, 24}, 720: {16, 36}}

	testCases := []struct {
		name       string
		resolution bool
		want       map[int][]float64
		extra      int
	}{
		{name: "per-shot CRF: two probes per resolution", want: map[int][]float64{1080: {18, 28}, 720: {12, 40}}},
		{name: "per-shot resolution: wide ranges get probes inside", resolution: true, want: map[int][]float64{1080: {18, 28}, 720: {12, 21.5, 30.5, 40}}, extra: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := encode.Lookup("h264")
			require.NoError(t, err)

			b := &build{codec: codec, opts: Options{PerShotResolution: testCase.resolution}}
			plan, probing := b.shotProbePlan([]int{1080, 720}, ranges, 4)

			got := map[int][]float64{}
			for _, p := range plan {
				got[p.height] = append(got[p.height], p.crf)
			}

			assert.Equal(t, testCase.want, got)
			assert.Equal(t, ShotProbing{Resolution: testCase.resolution, Probes: len(plan), Extra: testCase.extra}, *probing)
		})
	}
}

func TestCandidateHeights(
	t *testing.T,
) {
	heights := []int{1080, 720, 540, 360}

	testCases := []struct {
		name       string
		height     int
		resolution bool
		want       []int
	}{
		{name: "per-shot CRF", height: 720, want: []int{720}},
		{name: "top", height: 1080, resolution: true, want: []int{1080, 720}},
		{name: "middle", height: 540, resolution: true, want: []int{720, 540, 360}},
		{name: "bottom", height: 360, resolution: true, want: []int{540, 360}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := &build{opts: Options{PerShotResolution: testCase.resolution}}
			assert.Equal(t, testCase.want, b.candidateHeights(testCase.height, heights))
		})
	}
}

func TestDeclaredGeometry(
	t *testing.T,
) {
	w, h := declaredGeometry([]encode.Chunk{{Width: 1280, Height: 720}, {Width: 1920, Height: 1080}, {Width: 960, Height: 540}})
	assert.Equal(t, [2]int{1920, 1080}, [2]int{w, h})
}

func TestShotLadder(
	t *testing.T,
) {
	shots := []Shot{{Start: 0, Frames: 50}, {Start: 50, Frames: 100}}
	res := &Result{
		Shots: shots,
		Rungs: []Rung{
			{PerShot: &PerShot{Shots: []ShotAllocation{{CRF: 20}, {CRF: 24}}}},
			{},
			{PerShot: &PerShot{Shots: []ShotAllocation{{CRF: 30}}}},
		},
	}

	testCases := []struct {
		name  string
		rung  int
		stop  int
		want  []Shot
		wantA []ShotAllocation
	}{
		{name: "every shot with its allocation", rung: 0, want: shots, wantA: []ShotAllocation{{CRF: 20}, {CRF: 24}}},
		{name: "stops when asked", rung: 0, stop: 1, want: shots[:1], wantA: []ShotAllocation{{CRF: 20}}},
		{name: "no per-shot version", rung: 1},
		{name: "incomplete allocation", rung: 2},
		{name: "no such rung", rung: 3},
		{name: "negative rung", rung: -1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got []Shot

			var gotA []ShotAllocation

			for s, a := range res.ShotLadder(testCase.rung) {
				got, gotA = append(got, s), append(gotA, a)
				if len(got) == testCase.stop {
					break
				}
			}

			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.wantA, gotA)
		})
	}
}
