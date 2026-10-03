package quality

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
	"github.com/eko/qc/vmaf"
)

// fakeInput is a 25 fps video of n frames with a keyframe every gop frames,
// described without any file: selectingSource decodes it.
func fakeInput(
	path string,
	n, gop int,
) Input {
	rate := media.Rational{Num: 25, Den: 1}

	var keys []media.Duration
	for i := 0; i < n; i += gop {
		keys = append(keys, media.Seconds(float64(i)/rate.Float()))
	}

	return Input{
		Path:  path,
		Video: media.VideoStream{Width: 64, Height: 64, AvgFrameRate: rate},
		Bitstream: &bitstream.Report{
			PacketCount: n,
			Duration:    media.Seconds(float64(n) / rate.Float()),
			Keyframes:   keys,
		},
	}
}

// TestMeasureFakeEngine runs whole measurements on a fake engine scoring
// each frame with its index: every reported frame must carry its own
// score, whatever the decoding plan, and every model set and scorer must be
// closed.
func TestMeasureFakeEngine(
	t *testing.T,
) {
	const short, long = 60, 3000

	testCases := []struct {
		name      string
		n         int
		opts      Options
		choice    vmaf.BackendChoice
		wantMode  string
		wantPlan  string
		wantAll   bool
		wantCheck func(t *testing.T, res *Result)
	}{
		{
			name: "exact", n: short, opts: Options{Exact: true},
			wantMode: ModeExact, wantPlan: planSweep, wantAll: true,
			wantCheck: func(t *testing.T, res *Result) {
				assert.Equal(t, 2*short, res.FramesDecoded)
				assert.InDelta(t, float64(short-1)/2, res.Mean, 1e-9)
			},
		},
		{
			name: "sampled to a precision", n: short, opts: Options{Precision: 1000, InitialClips: 8},
			wantMode: ModeSampled, wantPlan: planSweep,
		},
		{
			name: "fixed budget on a long video: seek runs", n: long, opts: Options{Sample: Sample{Share: 0.02}},
			wantMode: ModeSampled, wantPlan: planSeek,
		},
		{
			name: "devices and metrics", n: short,
			opts:     Options{Exact: true, Metrics: []string{MetricCAMBI}, Devices: []string{vmaf.DevicePhone, vmaf.Device4K}},
			wantMode: ModeExact, wantAll: true,
			wantCheck: func(t *testing.T, res *Result) {
				for _, f := range res.Frames {
					index := float64(f.Index)
					assert.InDelta(t, index, f.Metrics[SeriesCAMBI], 1e-9)
					assert.InDelta(t, index+modelOffset, f.Metrics[seriesDevice+vmaf.DevicePhone], 1e-9, "second model of the primary scorer")
					assert.InDelta(t, index, f.Metrics[seriesDevice+vmaf.Device4K], 1e-9, "first model of the 2160p pass")
				}

				assert.Equal(t, 2, res.Plans[planSweep], "the 4K device needs a second pass")

				// The fake CAMBI of a frame is its index: frames above the
				// threshold are banded, and their reference measured too
				// in a pass over those frames alone.
				require.NotNil(t, res.Banding)
				banded := short - 1 - int(BandingThreshold)
				assert.Equal(t, banded, res.Banding.BandedFrames)
				assert.Equal(t, banded, res.Banding.SourceFrames)

				for _, f := range res.Frames {
					source, measured := f.Metrics[SeriesCAMBISource]
					assert.Equal(t, float64(f.Index) > BandingThreshold, measured, "frame %d", f.Index)

					if measured {
						assert.InDelta(t, float64(f.Index), source, 1e-9)
					}
				}

				assert.Greater(t, res.FramesDecoded, 4*short, "the banded frames are decoded once more")
			},
		},
		{
			name: "backend reported", n: short, opts: Options{Exact: true, Backend: vmaf.BackendAuto},
			choice:   vmaf.BackendChoice{Backend: vmaf.BackendCUDA},
			wantMode: ModeExact, wantAll: true,
			wantCheck: func(t *testing.T, res *Result) {
				assert.Equal(t, "cuda", res.Backend)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			engine := &fakeEngine{choice: testCase.choice}
			ref, dist := fakeInput("ref.mp4", testCase.n, 25), fakeInput("dist.mp4", testCase.n, 50)

			opts := testCase.opts
			opts.Model = testModel

			res, err := measureWithin(t, NewMeter(selectingSource(testCase.n), engine), ref, dist, opts)
			require.NoError(t, err)

			assert.Equal(t, testCase.wantMode, res.Mode)
			assert.Equal(t, opts.Backend, engine.requested)
			assert.Equal(t, vmaf.Encoded{Width: 64, Height: 64, BitDepth: 8}, engine.encoded.Load(), "the encode CAMBI reads banding at")
			assert.Zero(t, engine.open.Load(), "model sets and scorers are closed")
			require.NotEmpty(t, res.Frames)

			for _, f := range res.Frames {
				assert.InDelta(t, float64(f.Index), f.Score, 1e-9, "frame %d keeps its own score", f.Index)
			}

			if testCase.wantAll {
				assert.Len(t, res.Frames, testCase.n)
			} else {
				assert.Less(t, len(res.Frames), testCase.n)
			}

			if testCase.wantPlan != "" {
				assert.Positive(t, res.Plans[testCase.wantPlan], "plans: %v", res.Plans)
			}

			if testCase.wantCheck != nil {
				testCase.wantCheck(t, res)
			}
		})
	}
}

// TestMeasureFakeEngineErrors fails each step of the engine: the error is
// returned once prefixed, and nothing is left open.
func TestMeasureFakeEngineErrors(
	t *testing.T,
) {
	const n = 60

	errEngine := errors.New("engine broke")

	testCases := []struct {
		name   string
		engine *fakeEngine
		opts   Options
	}{
		{name: "backend refused", engine: &fakeEngine{resolveErr: errEngine}},
		{name: "models fail to load", engine: &fakeEngine{loadErr: errEngine}},
		{name: "scorer refused", engine: &fakeEngine{scorerErr: errEngine}},
		{name: "pair refused", engine: &fakeEngine{pushErr: errEngine}},
		{
			name: "banding of the reference refused, exact", engine: &fakeEngine{sourceErr: errEngine},
			opts: Options{Exact: true, Metrics: []string{MetricCAMBI}},
		},
		{
			name: "banding of the reference refused, sampled", engine: &fakeEngine{sourceErr: errEngine},
			opts: Options{Precision: 1000, InitialClips: 8, Metrics: []string{MetricCAMBI}},
		},
		{name: "collect fails", engine: &fakeEngine{collectErr: errEngine}},
		{name: "collect fails, exact", engine: &fakeEngine{collectErr: errEngine}, opts: Options{Exact: true}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			in := fakeInput("ref.mp4", n, 25)

			opts := testCase.opts
			opts.Model = testModel

			res, err := measureWithin(t, NewMeter(selectingSource(n), testCase.engine), in, in, opts)
			require.ErrorIs(t, err, errEngine)
			assert.Nil(t, res)
			assert.Equal(t, 1, strings.Count(err.Error(), "quality: "), "prefixed once: %v", err)
			assert.Zero(t, testCase.engine.open.Load(), "model sets and scorers are closed")
		})
	}
}

func TestScoreJob(
	t *testing.T,
) {
	errEngine := errors.New("engine broke")
	pool := frame.NewPool(8, 8, frame.PoolOptions{Chroma: true})

	testCases := []struct {
		name       string
		engine     *fakeEngine
		pairs      int
		wantErr    error
		wantScores []float64
	}{
		{name: "warm-up frames dropped", engine: &fakeEngine{}, pairs: 6, wantScores: []float64{1, 2, 3, 4}},
		{name: "short clip", engine: &fakeEngine{}, pairs: 3, wantScores: []float64{1, 2}},
		{name: "scorer cannot start", engine: &fakeEngine{scorerErr: errEngine}, pairs: 2, wantErr: errEngine},
		{name: "frame rejected", engine: &fakeEngine{pushErr: errEngine}, pairs: 2, wantErr: errEngine},
		{name: "collect fails", engine: &fakeEngine{collectErr: errEngine}, pairs: 2, wantErr: errEngine},
		{name: "no frame decoded", engine: &fakeEngine{}, wantErr: errShortDecode},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := &run{spec: vmaf.ModelSpec{Width: 8, Height: 8}, bitDepth: 8}
			job := &clipJob{clip: clip{from: 1, to: 5}, warmFrom: 0, warmTo: 6, pairs: make(chan pair, 8)}

			for i := range testCase.pairs {
				ref := pool.Get()
				ref.Index = i
				job.pairs <- pair{ref: ref, dist: pool.Get()}
			}

			close(job.pairs)

			models, err := testCase.engine.LoadModels([]vmaf.ModelSpec{{}})
			require.NoError(t, err)

			cr, err := r.scoreJob(models, &goMeters{}, job, 1)
			models.Close()

			assert.Empty(t, job.pairs, "pairs are drained")
			assert.Zero(t, testCase.engine.open.Load(), "the scorer is closed")

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantScores, cr.scores)
			assert.Equal(t, 2*testCase.pairs, cr.decoded)
		})
	}
}
