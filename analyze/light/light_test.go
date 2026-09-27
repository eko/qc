package light

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/colorimetry"
	"github.com/eko/qc/media"
)

// greyFrame returns a frame of a sampling pool whose samples are all the
// limited-range grey of luma code y, with the first bright samples set to
// luma 940 (peak white).
func greyFrame(
	t *testing.T,
	pool *frame.Pool,
	y int,
	bright int,
) *frame.Frame {
	t.Helper()

	f := pool.Get()
	fill(&f.Samples.Y, y)
	fill(&f.Samples.Cb, 512)
	fill(&f.Samples.Cr, 512)

	samples := f.Samples.Y.Uint16()
	for i := range bright {
		samples[i] = 940
	}

	return f
}

func fill(
	p *frame.Plane,
	v int,
) {
	samples := p.Uint16()
	for i := range samples {
		samples[i] = uint16(v)
	}
}

func TestAnalyzer(
	t *testing.T,
) {
	// A 1000-point grid: 80×50 pixels every 2.
	pool := frame.NewPool(80, 50, frame.PoolOptions{SampleStep: 2})
	grey := colorimetry.PQEOTF(float64(502-64) / 876)

	testCases := []struct {
		name        string
		color       media.Color
		bright      int
		wantPeak    float64
		wantRobust  float64
		wantAverage float64
		wantDisplay float64
	}{
		{
			name: "pq grey", color: media.Color{Transfer: media.TransferPQ},
			wantPeak: grey, wantRobust: grey, wantAverage: grey,
		},
		{
			name: "one peak white point is ignored by the robust peak", color: media.Color{Transfer: media.TransferPQ},
			bright: 1, wantPeak: 10000, wantRobust: grey, wantAverage: (999*grey + 10000) / 1000,
		},
		{
			name: "two thousandths of peak white are not", color: media.Color{Transfer: media.TransferPQ},
			bright: 2, wantPeak: 10000, wantRobust: 10000, wantAverage: (998*grey + 20000) / 1000,
		},
		{
			name: "hlg white on the nominal display", color: media.Color{Transfer: media.TransferHLG},
			bright: 1000, wantPeak: 1000, wantRobust: 1000, wantAverage: 1000, wantDisplay: 1000,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			a := New(testCase.color)

			for i := range 3 {
				f := greyFrame(t, pool, 502, testCase.bright)
				f.Index = i
				require.NoError(t, a.Consume(f))
				f.Release()
			}

			require.NoError(t, a.Close())
			res := a.Result()

			assert.Equal(t, testCase.color.Transfer, res.Transfer)
			assert.InDelta(t, testCase.wantDisplay, res.DisplayPeak, 1e-9)
			assert.Equal(t, 2, res.SampleStep)
			assert.Len(t, res.Peak, 3)
			assert.InEpsilon(t, testCase.wantPeak, res.MaxCLL, 2e-3)
			assert.InEpsilon(t, testCase.wantRobust, res.MaxCLLRobust, 5e-3)
			assert.InEpsilon(t, testCase.wantAverage, res.MaxFALL, 2e-3)
			assert.Zero(t, res.MaxCLLFrame)
			assert.Zero(t, res.MaxFALLFrame)
			assert.InEpsilon(t, testCase.wantAverage, res.AverageSummary.Mean, 2e-3)
		})
	}
}

func TestAnalyzerTracksBrightestFrames(
	t *testing.T,
) {
	pool := frame.NewPool(80, 50, frame.PoolOptions{SampleStep: 2})
	a := New(media.Color{Transfer: media.TransferPQ})

	for i, y := range []int{300, 700, 500} {
		f := greyFrame(t, pool, y, 0)
		f.Index = 10 + i
		require.NoError(t, a.Consume(f))
		f.Release()
	}

	res := a.Result()
	assert.Equal(t, 11, res.MaxCLLFrame)
	assert.Equal(t, 11, res.MaxFALLFrame)
}

func TestAnalyzerSkipsFramesWithoutSamples(
	t *testing.T,
) {
	a := New(media.Color{Transfer: media.TransferPQ})
	f := frame.NewPool(8, 8, frame.PoolOptions{}).Get()

	require.NoError(t, a.Consume(f))
	f.Release()

	assert.Empty(t, a.Result().Peak)
}

func TestStep(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		width, height int
		want          int
	}{
		{name: "2160p", width: 3840, height: 2160, want: 8},
		{name: "1080p", width: 1920, height: 1080, want: 4},
		{name: "720p", width: 1280, height: 720, want: 4},
		{name: "small", width: 320, height: 180, want: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Step(testCase.width, testCase.height))
		})
	}
}

func TestResultActive(
	t *testing.T,
) {
	res := Result{MaxFALL: 100, Average: []float64{50, 100}, ActiveShare: 1}

	testCases := []struct {
		name        string
		share       float64
		wantFALL    float64
		wantAverage []float64
		wantShare   float64
	}{
		{name: "letterbox", share: 0.8, wantFALL: 125, wantAverage: []float64{62.5, 125}, wantShare: 0.8},
		{name: "whole picture", share: 1, wantFALL: 100, wantAverage: []float64{50, 100}, wantShare: 1},
		{name: "unknown", share: 0, wantFALL: 100, wantAverage: []float64{50, 100}, wantShare: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := res.Active(testCase.share)

			assert.InDelta(t, testCase.wantFALL, got.MaxFALL, 1e-9)
			assert.InDeltaSlice(t, testCase.wantAverage, got.Average, 1e-9)
			assert.InDelta(t, testCase.wantShare, got.ActiveShare, 1e-9)
			assert.InDelta(t, 100.0, res.MaxFALL, 1e-9, "the receiver is unchanged")
		})
	}
}

func BenchmarkConsume1080p(
	b *testing.B,
) {
	pool := frame.NewPool(1920, 1080, frame.PoolOptions{SampleStep: Step(1920, 1080)})
	f := pool.Get()

	for i, s := 0, f.Samples.Y.Uint16(); i < len(s); i++ {
		s[i] = uint16(64 + i%876)
	}

	fill(&f.Samples.Cb, 480)
	fill(&f.Samples.Cr, 560)

	a := New(media.Color{Transfer: media.TransferPQ})

	for b.Loop() {
		_ = a.Consume(f)
	}
}

func TestAnalyzerBlackFrame(
	t *testing.T,
) {
	pool := frame.NewPool(80, 50, frame.PoolOptions{SampleStep: 2})
	a := New(media.Color{Transfer: media.TransferPQ})

	f := greyFrame(t, pool, 64, 0)
	require.NoError(t, a.Consume(f))
	f.Release()

	res := a.Result()
	assert.Zero(t, res.MaxCLL)
	assert.Zero(t, res.MaxCLLRobust)
	assert.Zero(t, res.MaxFALL)
}
