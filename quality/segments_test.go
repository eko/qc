package quality

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/vmaf"
)

// segmentingSource is a source asking for decoders concurrent decodes, as
// VideoToolbox does, and recording the requests it decodes.
type segmentingSource struct {
	decode.Source

	decoders int

	mu       sync.Mutex
	requests []decode.Request
}

func (s *segmentingSource) SegmentDecoders(
	decode.Request,
) int {
	return s.decoders
}

func (s *segmentingSource) Decode(
	ctx context.Context,
	req decode.Request,
	fn func(*frame.Frame) error,
) error {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()

	return s.Source.Decode(ctx, req, fn)
}

// TestMeasureSegmented runs whole measurements on a fake engine scoring
// each frame with its index, from a decoder asking for concurrent decodes:
// every frame keeps its own score, and exact measurements are scored in
// segments.
func TestMeasureSegmented(
	t *testing.T,
) {
	const short, long = 600, 3000

	testCases := []struct {
		name      string
		n         int
		decoders  int
		opts      Options
		wantPlans map[string]int
		wantAll   bool
	}{
		{
			name: "exact in segments", n: long, decoders: 12, opts: Options{Exact: true},
			wantPlans: map[string]int{planSegments: 1}, wantAll: true,
		},
		{
			name: "exact in segments with a device pass", n: long, decoders: 12,
			opts:      Options{Exact: true, Devices: []string{vmaf.Device4K}, Metrics: []string{MetricCAMBI}},
			wantPlans: map[string]int{planSegments: 2}, wantAll: true,
		},
		{
			name: "exact too short to split", n: short, decoders: 12, opts: Options{Exact: true},
			wantPlans: map[string]int{planSweep: 1}, wantAll: true,
		},
		{
			name: "exact on a decoder without sessions", n: long, decoders: 1, opts: Options{Exact: true},
			wantPlans: map[string]int{planSweep: 1}, wantAll: true,
		},
		{
			name: "sweep split into runs", n: long, decoders: 12, opts: Options{Sample: Sample{PerScene: 2}},
			wantPlans: map[string]int{planSweep: 1},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			engine := &fakeEngine{}
			source := &segmentingSource{Source: selectingSource(testCase.n), decoders: testCase.decoders}
			ref, dist := fakeInput("ref.mp4", testCase.n, 25), fakeInput("dist.mp4", testCase.n, 50)
			ref.Video.PixelFormat, dist.Bitstream.Start = "yuv420p", media.Seconds(1.4)

			var (
				mu     sync.Mutex
				scored []int
			)

			opts := testCase.opts
			opts.Model = testModel
			opts.Progress = func(p Progress) {
				mu.Lock()
				scored = append(scored, p.FramesScored)
				mu.Unlock()
			}

			res, err := measureWithin(t, NewMeter(source, engine), ref, dist, opts)
			require.NoError(t, err)

			assert.Equal(t, testCase.wantPlans, res.Plans)
			assert.Zero(t, engine.open.Load(), "model sets and scorers are closed")

			for i, f := range res.Frames {
				assert.InDelta(t, float64(f.Index), f.Score, 1e-9, "frame %d keeps its own score", f.Index)

				if testCase.wantAll {
					assert.Equal(t, i, f.Index, "every frame once, in order")
				}
			}

			if testCase.wantAll {
				require.Len(t, res.Frames, testCase.n)
				assert.Equal(t, testCase.n, res.FramesScored)
				assert.InDelta(t, float64(testCase.n-1)/2, res.Mean, 1e-9)
				assert.LessOrEqual(t, maxOf(scored), testCase.n, "warm-up frames are not counted")
			}

			if testCase.decoders > 1 {
				for _, req := range source.requests {
					assert.True(t, req.Segment, "decodes are concurrent sessions")

					if req.Path == "dist.mp4" {
						assert.Equal(t, media.Seconds(1.4), req.Origin, "each side seeks on its own timeline")
					} else {
						assert.Equal(t, "yuv420p", req.PixelFormat)
					}
				}
			}
		})
	}
}

func maxOf(
	values []int,
) int {
	out := 0
	for _, v := range values {
		out = max(out, v)
	}

	return out
}

// TestSegmentsBitExact scores real clips in segments and in a single pass,
// with the default metrics: every frame of every series must be identical,
// including XPSNR's second-order temporal difference above 32 fps, which
// needs two warm-up frames. One warm-up frame, as sampled clips have, is
// not enough there.
func TestSegmentsBitExact(
	t *testing.T,
) {
	base := testutil.Clip{Seconds: 4, Filter: "noise=alls=12:allf=t"}

	testCases := []struct {
		name      string
		rate      int
		segments  bool
		wantExact bool
	}{
		{name: "25 fps", rate: 25, segments: true, wantExact: true},
		{name: "50 fps", rate: 50, segments: true, wantExact: true},
		{name: "50 fps with one warm-up frame", rate: 50},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ref := base
			ref.Name, ref.GOP, ref.Rate = "ref.mp4", 12, testCase.rate
			ref.Args = []string{"-bf", "3", "-x264-params", "open-gop=1"}

			dist := ref
			dist.Name, dist.GOP = "dist.mp4", 10
			dist.Args = []string{"-crf", "38", "-bf", "3"}

			refIn := probeInput(t, testutil.Generate(t, ref))
			distIn := probeInput(t, testutil.Generate(t, dist))
			opts := Options{Metrics: []string{MetricXPSNR, MetricPSNR}}

			exact, err := newTestRun(t, decodedMeter(), refIn, distIn, opts).exact(t.Context())
			require.NoError(t, err)

			r := newTestRun(t, decodedMeter(), refIn, distIn, opts)
			r.segments = testCase.segments
			st := newStratum(0, r.n, r.n)
			bounds := []int{0, r.n * 37 / 100, r.n * 61 / 100, r.n * 83 / 100, r.n}

			clips := make([]clip, len(bounds)-1)
			for i := range clips {
				clips[i] = clip{stratum: st, from: bounds[i], to: bounds[i+1]}
			}

			results, err := r.score(t.Context(), clips, 2, 1, 0)
			require.NoError(t, err)

			whole := mergeSegments(results)
			require.Len(t, whole.scores, r.n)

			xpsnrExact := true

			for i, f := range exact.Frames {
				assert.Equal(t, f.Score, whole.scores[i], "frame %d", i)

				for name, value := range r.frameMetrics(whole, i) {
					if strings.HasPrefix(name, "xpsnr") && value != f.Metrics[name] {
						xpsnrExact = false

						continue
					}

					assert.Equal(t, f.Metrics[name], value, "%s of frame %d", name, i)
				}
			}

			assert.Equal(t, testCase.wantExact, xpsnrExact)
		})
	}
}

func TestExactPlan(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		n, decoders  int
		wantClips    int
		wantWorkers  int
		wantSegments bool
	}{
		{name: "no hardware sessions", n: 3000, decoders: 1, wantClips: 1, wantWorkers: 1},
		{name: "too short to split", n: 900, decoders: 12, wantClips: 1, wantWorkers: 1},
		{name: "segments", n: 3000, decoders: 12, wantClips: 6, wantWorkers: min(segmentWorkers, runtime.GOMAXPROCS(0)), wantSegments: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := fakeRun(testCase.n, 25, 50)
			r.decoders = testCase.decoders

			clips, workers, threads := r.exactPlan(newStratum(0, r.n, r.n))

			assert.Len(t, clips, testCase.wantClips)
			assert.Equal(t, testCase.wantWorkers, workers)
			assert.Equal(t, max(1, runtime.GOMAXPROCS(0)/workers), threads)
			assert.Equal(t, testCase.wantSegments, r.segments)
			assert.Equal(t, 0, clips[0].from)
			assert.Equal(t, testCase.n, clips[len(clips)-1].to)
		})
	}
}

func TestSegmentClips(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		n, distGOP int
		workers    int
		want       [][2]int
	}{
		{name: "at the keyframes of the distorted video", n: 3000, distGOP: 250, workers: 2, want: [][2]int{{0, 500}, {500, 1000}, {1000, 1500}, {1500, 2000}, {2000, 2500}, {2500, 3000}}},
		{name: "bounds wait for a keyframe", n: 3000, distGOP: 700, workers: 1, want: [][2]int{{0, 1400}, {1400, 2100}, {2100, 3000}}},
		{name: "no keyframe to split at", n: 3000, distGOP: 3000, workers: 4, want: [][2]int{{0, 3000}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := fakeRun(testCase.n, 25, testCase.distGOP)
			st := newStratum(0, r.n, r.n)

			var got [][2]int
			for _, c := range r.segmentClips(st, testCase.workers) {
				assert.Same(t, st, c.stratum)

				got = append(got, [2]int{c.from, c.to})
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestMergeSegments(
	t *testing.T,
) {
	st := newStratum(0, 6, 6)
	results := []clipResult{
		{clip: clip{stratum: st, from: 0, to: 2}, scores: []float64{1, 2}, values: map[string][]float64{"psnr_y": {10, 20}}, decoded: 6},
		{clip: clip{stratum: st, from: 2, to: 6}, scores: []float64{3, 4, 5, 6}, values: map[string][]float64{"psnr_y": {30, 40, 50, 60}}, decoded: 12},
	}

	testCases := []struct {
		name    string
		results []clipResult
		want    clipResult
	}{
		{name: "a single segment is kept", results: results[:1], want: results[0]},
		{
			name: "segments joined in order", results: results,
			want: clipResult{
				clip: clip{stratum: st, from: 0, to: 6}, scores: []float64{1, 2, 3, 4, 5, 6},
				values: map[string][]float64{"psnr_y": {10, 20, 30, 40, 50, 60}}, decoded: 18,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, mergeSegments(testCase.results))
		})
	}
}

func TestSweepRuns(
	t *testing.T,
) {
	r := fakeRun(3000, 25, 50)
	r.decoders = 12
	jobs := r.newJobs(clipsAt(100, 400, 1600, 1700, 2900))
	seekFrom, _ := r.seekPoints()

	testCases := []struct {
		name  string
		count int
		want  []window
	}{
		{
			name: "one run per part of the video holding clips", count: 3,
			want: []window{{from: 99, to: 405, seek: 46}, {from: 1599, to: 1705, seek: 1546}, {from: 2899, to: 2905, seek: 2846}},
		},
		{name: "a single part", count: 1, want: []window{{from: 99, to: 2905, seek: 46}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got []window
			for _, run := range r.sweepRuns(jobs, testCase.count, seekFrom) {
				got = append(got, run.window)
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestSegmentDecoders(
	t *testing.T,
) {
	ref, dist := fakeInput("ref.mp4", 10, 5), fakeInput("dist.mp4", 10, 5)

	testCases := []struct {
		name   string
		source decode.Source
		want   int
	}{
		{name: "a decoder that cannot tell", source: decodeFunc(nil), want: 1},
		{name: "a decoder asking for sessions", source: &segmentingSource{decoders: 12}, want: 12},
		{name: "a decoder asking for one pass", source: &segmentingSource{decoders: 1}, want: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, segmentDecoders(testCase.source, ref, dist))
		})
	}
}
