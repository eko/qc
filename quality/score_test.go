package quality

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// newTestRun prepares a measurement scored at the native resolution of the
// clips instead of the model's, which keeps libvmaf cheap: plans and
// bit-exactness do not depend on the evaluation resolution.
func newTestRun(
	t *testing.T,
	meter *Meter,
	ref, dist Input,
	opts Options,
) *run {
	t.Helper()

	opts.Model = testModel

	r, err := meter.newRun(ref, dist, opts.withDefaults())
	require.NoError(t, err)

	r.spec.Width, r.spec.Height = ref.Video.Width, ref.Video.Height

	return r
}

// clipsAt builds clips of 4 frames starting at the given frames.
func clipsAt(
	starts ...int,
) []clip {
	st := newStratum(0, 1, 1)
	out := make([]clip, len(starts))

	for i, s := range starts {
		out[i] = clip{stratum: st, from: s, to: s + defaultClipFrames}
	}

	return out
}

// TestScorePlans scores clips with each decoding plan and checks that every
// clip frame scores exactly as in an exact run: warm-up frames and the
// open-GOP seek margin make sampled clips bit-exact.
func TestScorePlans(
	t *testing.T,
) {
	base := testutil.Clip{Seconds: 8, Filter: "noise=alls=12:allf=t"}

	ref := base
	ref.Name, ref.GOP = "ref.mp4", 12
	ref.Args = []string{"-bf", "3", "-x264-params", "open-gop=1"}

	dist := base
	dist.Name, dist.GOP = "dist.mp4", 10
	dist.Args = []string{"-crf", "38", "-bf", "3", "-x264-params", "open-gop=1"}

	ten := base
	ten.Name, ten.GOP, ten.Seconds, ten.PixelFormat = "ten.mp4", 10, 6, "yuv420p10le"

	testCases := []struct {
		name      string
		ref, dist testutil.Clip
		clips     []clip
		wantPlan  string
		wantDepth int
	}{
		{name: "dense clips: one sweep", ref: ref, dist: dist, clips: clipsAt(4, 8, 40), wantPlan: planSweep, wantDepth: 8},
		{name: "sparse clips: seek runs", ref: ref, dist: dist, clips: clipsAt(0, 50, 101, 150, 190), wantPlan: planSeek, wantDepth: 8},
		{name: "10 bits: seek runs", ref: ten, dist: ten, clips: clipsAt(3, 60, 120), wantPlan: planSeek, wantDepth: 10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			refIn := probeInput(t, testutil.Generate(t, testCase.ref))
			distIn := probeInput(t, testutil.Generate(t, testCase.dist))

			exact, err := newTestRun(t, decodedMeter(), refIn, distIn, Options{}).exact(t.Context())
			require.NoError(t, err)
			require.Len(t, exact.Frames, exact.FramesTotal)

			r := newTestRun(t, decodedMeter(), refIn, distIn, Options{})
			results, err := r.score(t.Context(), testCase.clips, 2, 1, 1)
			require.NoError(t, err)

			assert.Equal(t, map[string]int{testCase.wantPlan: 1}, r.plans)
			assert.Equal(t, testCase.wantDepth, r.bitDepth)
			require.Len(t, results, len(testCase.clips))

			for i, cr := range results {
				assert.Equal(t, testCase.clips[i], cr.clip, "results follow the order of clips")
				require.Len(t, cr.scores, defaultClipFrames)

				for j, score := range cr.scores {
					assert.Equal(t, exact.Frames[cr.clip.from+j].Score, score, "frame %d", cr.clip.from+j)
				}
			}
		})
	}
}

func TestScoreErrors(
	t *testing.T,
) {
	in := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 8, GOP: 10}))
	errDecode := errors.New("decoder broke")

	testCases := []struct {
		name    string
		meter   *Meter
		clips   []clip
		wantErr error
	}{
		{name: "no clips", meter: decodedMeter()},
		{name: "seek runs, decoder failure", meter: NewMeter(failingSource{err: errDecode}, libvmaf.NewEngine()), clips: clipsAt(0, 100, 190), wantErr: errDecode},
		{name: "sweep, decoder failure", meter: NewMeter(failingSource{err: errDecode}, libvmaf.NewEngine()), clips: clipsAt(0, 4), wantErr: errDecode},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := newTestRun(t, testCase.meter, in, in, Options{})
			results, err := r.score(t.Context(), testCase.clips, 2, 1, 1)

			if testCase.wantErr == nil {
				require.NoError(t, err)
				assert.Nil(t, results)

				return
			}

			require.ErrorIs(t, err, testCase.wantErr)
			assert.Empty(t, r.plans)
		})
	}
}

// fakeRun is a run over n frames with keyframes every gop frames at 25 fps.
func fakeRun(
	n, refGOP, distGOP int,
) *run {
	pts := make([]media.Duration, n)
	for i := range pts {
		pts[i] = media.Seconds(float64(i) / 25)
	}

	keys := func(gop int) []media.Duration {
		var out []media.Duration
		for i := 0; i < n; i += gop {
			out = append(out, pts[i])
		}

		return out
	}

	return &run{
		n:    n,
		ref:  Input{Bitstream: &bitstream.Report{PTS: pts, Keyframes: keys(refGOP)}},
		dist: Input{Bitstream: &bitstream.Report{PTS: pts, Keyframes: keys(distGOP)}},
	}
}

func TestPlanRuns(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		clips     []clip
		wantRuns  []window
		wantCost  int
		wantPlans int
	}{
		{
			name:     "overlapping warm-ups share a run",
			clips:    clipsAt(4, 8),
			wantRuns: []window{{from: 3, to: 13, seek: 0}},
			// 3 frames before the run on both sides, 2 run starts, 10 frames.
			wantCost: 3 + 3 + 2*runStartCost + 2*10,
		},
		{
			name:     "gap cheaper to decode than a seek",
			clips:    clipsAt(4, 30),
			wantRuns: []window{{from: 3, to: 35, seek: 0}},
			wantCost: 3 + 3 + 2*runStartCost + 2*32,
		},
		{
			name:  "distant clips seek from the GOP before",
			clips: clipsAt(4, 200),
			wantRuns: []window{
				{from: 3, to: 9, seek: 0},
				// Keyframes before 199: 190 (ref), 198 (dist); margin 4 → 186,
				// decoded from 180 (ref) and 180 (dist).
				{from: 199, to: 205, seek: 186},
			},
			wantCost: (3 + 3 + 2*runStartCost + 2*6) + (19 + 19 + 2*runStartCost + 2*6),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := fakeRun(300, 10, 9)
			runs, cost := r.planRuns(r.newJobs(testCase.clips))

			windows := make([]window, len(runs))
			for i, run := range runs {
				windows[i] = run.window
			}

			assert.Equal(t, testCase.wantRuns, windows)
			assert.Equal(t, testCase.wantCost, cost)
		})
	}
}

func TestKeyBefore(
	t *testing.T,
) {
	keys := []int{10, 20}

	testCases := []struct {
		name string
		i    int
		want int
	}{
		{name: "before the first keyframe", i: 5, want: 0},
		{name: "on a keyframe", i: 20, want: 20},
		{name: "between keyframes", i: 15, want: 10},
		{name: "after the last keyframe", i: 99, want: 20},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, keyBefore(keys, testCase.i))
		})
	}
}

func TestSelectRanges(
	t *testing.T,
) {
	jobs := []*clipJob{
		{warmFrom: 3, warmTo: 9},
		{warmFrom: 8, warmTo: 14},
		{warmFrom: 20, warmTo: 26},
	}

	testCases := []struct {
		name string
		jobs []*clipJob
		w    window
		want [][2]int
	}{
		{name: "overlapping ranges merge", jobs: jobs, w: window{to: 100}, want: [][2]int{{3, 14}, {20, 26}}},
		{name: "relative to the seek point", jobs: jobs, w: window{from: 3, to: 26, seek: 3}, want: [][2]int{{0, 11}, {17, 23}}},
		{name: "whole window selects nothing", jobs: []*clipJob{{warmFrom: 0, warmTo: 100}}, w: window{to: 100}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, selectRanges(testCase.jobs, testCase.w))
		})
	}
}

func TestOutputFrames(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		w         window
		selection [][2]int
		want      int
	}{
		{name: "whole video", w: window{to: 50}, want: 50},
		{name: "seek window", w: window{from: 20, to: 50, seek: 16}, want: 34},
		{name: "selection", w: window{to: 50}, selection: [][2]int{{0, 6}, {10, 16}}, want: 12},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, outputFrames(testCase.w, testCase.selection))
		})
	}
}

// framesOn returns a closed channel holding count frames numbered from 0.
func framesOn(
	pool *frame.Pool,
	count int,
) chan *frame.Frame {
	out := make(chan *frame.Frame, count)

	for i := range count {
		f := pool.Get()
		f.Index = i
		out <- f
	}

	close(out)

	return out
}

func TestDispatcher(
	t *testing.T,
) {
	pool := frame.NewPool(8, 8, frame.PoolOptions{Chroma: true})

	testCases := []struct {
		name string
		// slots and pairs are channel capacities: 0 blocks the dispatcher.
		slots, pairs int
		refFrames    int
		distFrames   int
		// cancel cancels before dispatching, or once the clip is queued.
		cancel       bool
		cancelQueued bool
		wantErr      error
		wantPairs    int
	}{
		{name: "shorter distorted stream", slots: 1, pairs: 8, refFrames: 6, distFrames: 3, wantPairs: 3},
		{name: "empty streams", slots: 1, pairs: 8, wantErr: errShortDecode},
		{name: "cancelled waiting for a slot", slots: 0, pairs: 8, refFrames: 3, distFrames: 3, cancel: true, wantErr: context.Canceled},
		{name: "cancelled waiting for a worker", slots: 1, pairs: 0, refFrames: 3, distFrames: 3, cancelQueued: true, wantErr: context.Canceled},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			job := &clipJob{warmFrom: 0, warmTo: 6, pairs: make(chan pair, testCase.pairs)}
			queue := make(chan *clipJob, 1)
			d := &dispatcher{
				jobs:  []*clipJob{job},
				queue: queue,
				slots: make(chan struct{}, testCase.slots),
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if testCase.cancel {
				cancel()
			}

			if testCase.cancelQueued {
				go func() {
					<-queue
					cancel()
				}()
			}

			err := d.run(ctx, framesOn(pool, testCase.refFrames), framesOn(pool, testCase.distFrames))
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
			}

			pairs := 0
			for p := range job.pairs {
				p.release()
				pairs++
			}

			assert.Equal(t, testCase.wantPairs, pairs, "pairs delivered, channel closed")
		})
	}
}

func TestDeliverCancelled(
	t *testing.T,
) {
	pool := frame.NewPool(8, 8, frame.PoolOptions{})
	ref, dist := pool.Get(), pool.Get()
	job := &clipJob{pairs: make(chan pair)}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, deliver(ctx, job, ref, dist), context.Canceled)
	releaseFrames(ref, dist)
}

func TestDecodeSideCancelled(
	t *testing.T,
) {
	pool := frame.NewPool(8, 8, frame.PoolOptions{})
	source := decodeFunc(func(_ context.Context, _ decode.Request, fn func(*frame.Frame) error) error {
		return fn(pool.Get())
	})

	r := fakeRun(10, 5, 5)
	r.meter = NewMeter(source, &fakeEngine{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	out := make(chan *frame.Frame)
	err := r.decodeSide(ctx, r.ref, pool, window{from: 5, to: 10, seek: 2}, nil, out)()

	require.ErrorIs(t, err, context.Canceled)

	_, open := <-out
	assert.False(t, open)
}

// TestScoreFakeDecoders scores clips from decoders emitting a given number
// of frames per file, regardless of the request.
func TestScoreFakeDecoders(
	t *testing.T,
) {
	const n = 12

	testCases := []struct {
		name    string
		ref     int
		dist    int
		exact   bool
		wantErr error
	}{
		// The extra reference frames are released and the run completes.
		{name: "longer reference", ref: n + 3*decodeBuffer, dist: n, exact: true},
		// Only the warm-up frame before the clip is decoded.
		{name: "truncated clip", ref: 1, dist: 1, wantErr: errShortDecode},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			emitted := map[string]int{"ref.mp4": testCase.ref, "dist.mp4": testCase.dist}
			source := decodeFunc(func(ctx context.Context, req decode.Request, fn func(*frame.Frame) error) error {
				for i := range emitted[req.Path] {
					f := req.Pool.Get()
					f.Index = req.FirstIndex + i

					if err := fn(f); err != nil {
						return err
					}
				}

				return ctx.Err()
			})

			r := fakeRun(n, n, n)
			r.meter = NewMeter(source, &fakeEngine{})
			r.ref.Path, r.dist.Path = "ref.mp4", "dist.mp4"
			r.spec = vmaf.ModelSpec{Source: testModel, Width: 64, Height: 64}
			r.models = []vmaf.ModelSpec{r.spec}
			r.bitDepth = 8
			r.plans = map[string]int{}

			if !testCase.exact {
				_, err := r.score(t.Context(), clipsAt(1), 1, 1, 1)
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			res, err := r.exact(t.Context())
			require.NoError(t, err)

			assert.Equal(t, n, res.FramesScored)
			assert.Equal(t, 2*n, res.FramesDecoded)
		})
	}
}
