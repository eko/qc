package quality

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// testModel is libvmaf's built-in v0 model: cheaper than the v1 default and
// available in every libvmaf build.
const testModel = "vmaf_v0.6.1"

// probeInput builds the Input of a real file, as the analysis package does.
func probeInput(
	t *testing.T,
	path string,
) Input {
	t.Helper()

	info, err := probe.NewFFprobe("ffprobe").Probe(t.Context(), path)
	require.NoError(t, err)

	video, ok := info.PrimaryVideo()
	require.True(t, ok)

	var packets []media.Packet

	err = bitstream.NewFFprobeReader("ffprobe").ReadPackets(t.Context(), path, func(p media.Packet) error {
		packets = append(packets, p)

		return nil
	})
	require.NoError(t, err)

	report := bitstream.Analyze(packets, bitstream.Options{})

	return Input{Path: path, Video: video, Bitstream: &report}
}

// clips generates a reference (near lossless) and a degraded encode of the
// same synthetic content.
func clips(
	t *testing.T,
	c testutil.Clip,
) (ref, dist Input) {
	t.Helper()

	c.Name = "ref.mp4"
	c.Args = []string{"-qp", "0"}
	ref = probeInput(t, testutil.Generate(t, c))

	c.Name = "dist.mp4"
	c.Args = []string{"-crf", "45"}
	dist = probeInput(t, testutil.Generate(t, c))

	return ref, dist
}

// decodedMeter returns a Meter decoding with ffmpeg.
func decodedMeter() *Meter {
	return NewMeter(decode.NewFFmpeg("ffmpeg", 0), libvmaf.NewEngine())
}

// measureWithin runs Measure and fails instead of hanging when it does not
// return in time.
func measureWithin(
	t *testing.T,
	m *Meter,
	ref, dist Input,
	opts Options,
) (*Result, error) {
	t.Helper()

	type outcome struct {
		res *Result
		err error
	}

	done := make(chan outcome, 1)

	go func() {
		res, err := m.Measure(t.Context(), ref, dist, opts)
		done <- outcome{res: res, err: err}
	}()

	select {
	case o := <-done:
		return o.res, o.err
	case <-time.After(30 * time.Second):
		require.FailNow(t, "Measure did not return")

		return nil, nil
	}
}

func TestMeasure(
	t *testing.T,
) {
	// 2 s with a single keyframe; 4 initial clips make two strata of 6 slots.
	ref, dist := clips(t, testutil.Clip{Seconds: 2})
	n := ref.Bitstream.PacketCount

	exact, err := measureWithin(t, decodedMeter(), ref, dist, Options{Model: testModel, Exact: true})
	require.NoError(t, err)

	testCases := []struct {
		name  string
		dist  Input
		opts  Options
		check func(t *testing.T, res *Result)
	}{
		{
			name: "exact, identical",
			dist: ref,
			opts: Options{Model: testModel, Exact: true},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, ModeExact, res.Mode)
				assert.Greater(t, res.Mean, 95.0)
				assert.Greater(t, res.Mean, exact.Mean+5, "the degraded encode scores lower")
				assert.Equal(t, res.Mean, res.Low)
				assert.Equal(t, res.Mean, res.High)
				assert.Zero(t, res.HalfWidth)
				assert.InDelta(t, res.Mean, res.HarmonicMean, 1)
				assert.Equal(t, n, res.FramesScored)
				assert.Len(t, res.Frames, n)
				assert.Equal(t, 2*n, res.FramesDecoded)
				assert.Equal(t, map[string]int{planSweep: 1}, res.Plans)
				assert.Equal(t, 1, res.Rounds)
				assert.Equal(t, 1920, res.Model.Width, "evaluated at the model resolution")
				assert.Equal(t, 8, res.BitDepth)
				require.Len(t, res.Strata, 1)
				assert.Equal(t, ref.Bitstream.Duration, res.Strata[0].End)
			},
		},
		{
			name: "sampled, bit-exact with exact scores",
			dist: dist,
			opts: Options{Model: testModel, Precision: 20, Workers: 2, InitialClips: 4},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, ModeSampled, res.Mode)
				assert.Empty(t, res.Fallback)
				assert.Less(t, res.FramesScored, n)
				assert.Len(t, res.Frames, res.FramesScored)
				assert.LessOrEqual(t, res.HalfWidth, 20.0)
				assert.InDelta(t, res.Mean, res.Low+res.HalfWidth, 1e-9)
				assert.InDelta(t, exact.Mean, res.Mean, res.HalfWidth)
				assert.Zero(t, res.HarmonicMean, "only computed in exact mode")
				assert.Greater(t, res.FramesDecoded, 2*res.FramesScored, "warm-up frames are decoded too")

				for _, f := range res.Frames {
					assert.Equal(t, exact.Frames[f.Index], f, "frame %d", f.Index)
				}

				scored := 0
				for _, s := range res.Strata {
					scored += s.ClipsScored
				}

				assert.Positive(t, scored)
			},
		},
		{
			name: "fallback to exact when the precision is out of reach",
			dist: dist,
			opts: Options{Model: testModel, Precision: 1e-6, InitialClips: 4},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, ModeExact, res.Mode)
				assert.Contains(t, res.Fallback, "scored all of them")
				assert.Equal(t, n, res.FramesScored)
				assert.Equal(t, exact.Mean, res.Mean)
				assert.Greater(t, res.FramesDecoded, 2*n, "the sampled rounds were decoded too")
			},
		},
		{
			name: "budget mode never falls back",
			dist: dist,
			opts: Options{Model: testModel, Precision: 1e-6, Budget: true, MaxShare: 0.7, InitialClips: 4},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, ModeSampled, res.Mode)
				assert.Empty(t, res.Fallback)
				assert.Greater(t, res.HalfWidth, 1e-6)
				assert.Equal(t, 2, res.Rounds, "one more round spends the budget")
				assert.Less(t, res.FramesScored, n)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ref, testCase.dist, testCase.opts)
			require.NoError(t, err)

			assert.Equal(t, n, res.FramesTotal)
			assert.Equal(t, 0.95, res.Confidence)
			assert.Positive(t, res.Elapsed)
			assert.IsIncreasing(t, frameIndices(res.Frames))
			testCase.check(t, res)
		})
	}
}

func frameIndices(
	frames []FrameScore,
) []int {
	out := make([]int, len(frames))
	for i, f := range frames {
		out[i] = f.Index
	}

	return out
}

// TestMeasureTinyClip covers a video too short for two clips: the variance
// cannot be estimated, so every frame is scored.
func TestMeasureTinyClip(
	t *testing.T,
) {
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.28}))
	require.Less(t, ref.Bitstream.PacketCount, 2*defaultClipFrames)

	testCases := []struct {
		name   string
		budget bool
	}{
		{name: "default"},
		{name: "budget mode", budget: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ref, ref, Options{Model: testModel, Budget: testCase.budget})
			require.NoError(t, err)

			assert.Equal(t, ModeExact, res.Mode)
			assert.Contains(t, res.Fallback, "100%")
			assert.Equal(t, ref.Bitstream.PacketCount, res.FramesScored)
		})
	}
}

// TestMeasureDifferentLengths compares videos of different lengths: only the
// common frames are scored, and the longer decoder must not block.
func TestMeasureDifferentLengths(
	t *testing.T,
) {
	long := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 2, Name: "long.mp4"}))
	short := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.6, Name: "short.mp4"}))
	n := short.Bitstream.PacketCount

	testCases := []struct {
		name      string
		ref, dist Input
		exact     bool
	}{
		{name: "longer reference, exact", ref: long, dist: short, exact: true},
		{name: "longer distorted, exact", ref: short, dist: long, exact: true},
		{name: "longer reference, sampled", ref: long, dist: short},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), testCase.ref, testCase.dist, Options{Model: testModel, Exact: testCase.exact})
			require.NoError(t, err)

			assert.Equal(t, n, res.FramesTotal)
			assert.Equal(t, n, res.FramesScored)
			assert.Greater(t, res.Mean, 90.0)

			last := res.Strata[len(res.Strata)-1]
			assert.Equal(t, long.Bitstream.PTS[n], last.End, "strata end at the next frame")
		})
	}
}

// TestMeasureTenBits scores 10-bit videos at 10 bits unless told otherwise.
func TestMeasureTenBits(
	t *testing.T,
) {
	ten := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.4, PixelFormat: "yuv420p10le", Name: "ten.mp4"}))
	require.Equal(t, 10, ten.Video.BitDepth)

	testCases := []struct {
		name     string
		bitDepth int
		want     int
	}{
		{name: "auto", want: 10},
		{name: "forced 8 bits", bitDepth: 8, want: 8},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ten, ten, Options{Model: testModel, Exact: true, BitDepth: testCase.bitDepth})
			require.NoError(t, err)

			assert.Equal(t, testCase.want, res.BitDepth)
			assert.Greater(t, res.Mean, 95.0)
		})
	}
}

func TestMeasureProgress(
	t *testing.T,
) {
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 1, GOP: 5}))
	n := ref.Bitstream.PacketCount

	testCases := []struct {
		name  string
		opts  Options
		check func(t *testing.T, updates []Progress)
	}{
		{
			name: "exact reports every pair",
			opts: Options{Model: testModel, Exact: true},
			check: func(t *testing.T, updates []Progress) {
				for _, p := range updates {
					assert.Equal(t, ModeExact, p.Mode)
					assert.Equal(t, n, p.FramesTotal)
				}

				assert.GreaterOrEqual(t, len(updates), n)
				assert.Equal(t, n, updates[len(updates)-1].FramesScored)
			},
		},
		{
			name: "sampled reports clips and rounds",
			opts: Options{Model: testModel, Precision: 20},
			check: func(t *testing.T, updates []Progress) {
				last := updates[len(updates)-1]
				assert.Equal(t, ModeSampled, last.Mode)
				assert.True(t, last.Estimated)
				assert.Equal(t, 1, last.Round)
				assert.Positive(t, last.Mean)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				mu      sync.Mutex
				updates []Progress
			)

			testCase.opts.Progress = func(p Progress) {
				mu.Lock()
				defer mu.Unlock()

				updates = append(updates, p)
			}

			_, err := measureWithin(t, decodedMeter(), ref, ref, testCase.opts)
			require.NoError(t, err)
			require.NotEmpty(t, updates)
			testCase.check(t, updates)
		})
	}
}

// failingSource is a decoder that always fails.
type failingSource struct {
	err error
}

func (s failingSource) Decode(
	context.Context,
	decode.Request,
	func(*frame.Frame) error,
) error {
	return s.err
}

func TestMeasureErrors(
	t *testing.T,
) {
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 1, GOP: 5}))

	errDecode := errors.New("decoder broke")

	withRate := func(in Input, rate media.Rational) Input {
		in.Video.AvgFrameRate = rate

		return in
	}

	withBitstream := func(in Input, bs *bitstream.Report) Input {
		in.Bitstream = bs

		return in
	}

	testCases := []struct {
		name      string
		meter     *Meter
		ref, dist Input
		opts      Options
		wantErr   error
		wantMsg   string
	}{
		{
			name: "frame rate mismatch",
			ref:  ref, dist: withRate(ref, media.Rational{Num: 30, Den: 1}),
			wantErr: ErrFrameRateMismatch,
		},
		{
			name: "missing bitstream",
			ref:  ref, dist: withBitstream(ref, nil),
			wantErr: ErrNoFramesToCompare,
		},
		{
			name: "no packets",
			ref:  withBitstream(ref, &bitstream.Report{}), dist: ref,
			wantErr: ErrNoFramesToCompare,
		},
		{
			name: "model file not found",
			ref:  ref, dist: ref,
			opts:    Options{Model: "missing.json"},
			wantErr: vmaf.ErrModelNotFound,
		},
		{
			name: "unknown built-in model, exact",
			ref:  ref, dist: ref,
			opts:    Options{Model: "no_such_model", Exact: true},
			wantMsg: "load model",
		},
		{
			name: "unknown built-in model, sampled",
			ref:  ref, dist: ref,
			opts:    Options{Model: "no_such_model"},
			wantMsg: "load model",
		},
		{
			name:  "decoder failure, exact",
			meter: NewMeter(failingSource{err: errDecode}, libvmaf.NewEngine()),
			ref:   ref, dist: ref,
			opts:    Options{Model: testModel, Exact: true},
			wantErr: errDecode,
		},
		{
			name:  "decoder failure, sampled",
			meter: NewMeter(failingSource{err: errDecode}, libvmaf.NewEngine()),
			ref:   ref, dist: ref,
			opts:    Options{Model: testModel},
			wantErr: errDecode,
		},
		{
			name:  "nothing decoded",
			meter: NewMeter(decodeFunc(func(context.Context, decode.Request, func(*frame.Frame) error) error { return nil }), libvmaf.NewEngine()),
			ref:   ref, dist: ref,
			opts:    Options{Model: testModel},
			wantErr: errShortDecode,
		},
		{
			name: "missing file",
			ref:  ref, dist: Input{Path: "missing.mp4", Video: ref.Video, Bitstream: ref.Bitstream},
			opts:    Options{Model: testModel, Exact: true},
			wantMsg: "missing.mp4",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			meter := testCase.meter
			if meter == nil {
				meter = decodedMeter()
			}

			res, err := measureWithin(t, meter, testCase.ref, testCase.dist, testCase.opts)
			require.Error(t, err)
			assert.Nil(t, res)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			}

			assert.Contains(t, err.Error(), testCase.wantMsg)
			assert.Equal(t, 1, strings.Count(err.Error(), "quality: "), "prefixed once: %v", err)
		})
	}
}

func TestMeasureFallbackError(
	t *testing.T,
) {
	// A 7-frame video always falls back to exact scoring, which then fails.
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.28}))
	errDecode := errors.New("decoder broke")

	var calls atomic.Int32

	source := decodeFunc(func(ctx context.Context, req decode.Request, fn func(*frame.Frame) error) error {
		if calls.Add(1) > 2 {
			return errDecode
		}

		return decode.NewFFmpeg("ffmpeg", 0).Decode(ctx, req, fn)
	})

	_, err := measureWithin(t, NewMeter(source, libvmaf.NewEngine()), ref, ref, Options{Model: testModel, Workers: 1})
	require.ErrorIs(t, err, errDecode)
}

// decodeFunc adapts a function to decode.Source.
type decodeFunc func(context.Context, decode.Request, func(*frame.Frame) error) error

func (f decodeFunc) Decode(
	ctx context.Context,
	req decode.Request,
	fn func(*frame.Frame) error,
) error {
	return f(ctx, req, fn)
}

func TestWithDefaults(
	t *testing.T,
) {
	testCases := []struct {
		name string
		opts Options
		want func(o Options) bool
	}{
		{
			name: "zero value",
			opts: Options{},
			want: func(o Options) bool {
				return o.Precision == defaultPrecision && o.Confidence == defaultConfidence &&
					o.MaxShare == defaultMaxShare && o.ClipFrames == defaultClipFrames &&
					o.InitialClips == defaultInitialClips && o.Seed == defaultSeed &&
					len(o.ModelDirs) == len(vmaf.DefaultModelDirs())
			},
		},
		{
			name: "invalid confidence",
			opts: Options{Confidence: 1},
			want: func(o Options) bool { return o.Confidence == defaultConfidence },
		},
		{
			name: "kept values",
			opts: Options{Precision: 1, Confidence: 0.9, MaxShare: 0.2, ClipFrames: 6, InitialClips: 10, Seed: 3, ModelDirs: []string{"x"}},
			want: func(o Options) bool {
				return o.Precision == 1 && o.Confidence == 0.9 && o.MaxShare == 0.2 && o.ClipFrames == 6 &&
					o.InitialClips == 10 && o.Seed == 3 && o.ModelDirs[0] == "x"
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.True(t, testCase.want(testCase.opts.withDefaults()))
		})
	}
}

func TestScoringDepth(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		requested int
		ref, dist int
		want      int
	}{
		{name: "8-bit videos", ref: 8, dist: 8, want: 8},
		{name: "unknown depth", want: 8},
		{name: "10-bit reference", ref: 10, dist: 8, want: 10},
		{name: "10-bit distorted", ref: 8, dist: 10, want: 10},
		{name: "requested", requested: 8, ref: 10, dist: 10, want: 8},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := scoringDepth(testCase.requested, media.VideoStream{BitDepth: testCase.ref}, media.VideoStream{BitDepth: testCase.dist})
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestSameRate(
	t *testing.T,
) {
	testCases := []struct {
		name string
		a, b media.Rational
		want bool
	}{
		{name: "equal", a: media.Rational{Num: 25, Den: 1}, b: media.Rational{Num: 50, Den: 2}, want: true},
		{name: "ntsc", a: media.Rational{Num: 30000, Den: 1001}, b: media.Rational{Num: 2997, Den: 100}, want: true},
		{name: "different", a: media.Rational{Num: 25, Den: 1}, b: media.Rational{Num: 24, Den: 1}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, sameRate(testCase.a, testCase.b))
		})
	}
}

func TestHarmonicMean(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		scores []float64
		want   float64
	}{
		{name: "constant", scores: []float64{80, 80}, want: 80},
		{name: "zero stays finite", scores: []float64{0, 100}, want: 2/(1+1.0/101) - 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, harmonicMean(testCase.scores), 1e-9)
		})
	}
}

func TestWithPTS(
	t *testing.T,
) {
	full := &bitstream.Report{PTS: []media.Duration{0, 1, 2}}

	testCases := []struct {
		name string
		bs   *bitstream.Report
		rate media.Rational
		want []media.Duration
	}{
		{name: "kept", bs: full, rate: media.Rational{Num: 25, Den: 1}, want: full.PTS},
		{name: "synthesised", bs: &bitstream.Report{}, rate: media.Rational{Num: 2, Den: 1}, want: []media.Duration{0, media.Seconds(0.5), media.Seconds(1)}},
		{name: "unknown rate", bs: &bitstream.Report{}, want: []media.Duration{0, 0, 0}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := withPTS(testCase.bs, 3, testCase.rate)
			assert.Equal(t, testCase.want, got.PTS)
		})
	}
}

func TestParallelism(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		workers     int
		wantWorkers int
	}{
		{name: "auto", wantWorkers: max(1, runtime.NumCPU()/2)},
		{name: "set", workers: 3, wantWorkers: 3},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := &run{opts: Options{Workers: testCase.workers}}
			workers, threads := r.parallelism()

			assert.Equal(t, testCase.wantWorkers, workers)
			assert.Equal(t, max(1, runtime.NumCPU()/workers), threads)
		})
	}
}

func TestFallbackReason(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		projected float64
		want      string
	}{
		{name: "share", projected: 0.456, want: "reaching ±0.50 would need about 46% of the frames: scored all of them"},
		{name: "capped", projected: math.Inf(1), want: "reaching ±0.50 would need about 100% of the frames: scored all of them"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, fallbackReason(0.5, testCase.projected))
		})
	}
}
