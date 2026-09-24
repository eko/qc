package quality

import (
	"context"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality/xpsnr"
	"github.com/eko/qc/vmaf"
)

func TestParseMetrics(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   []string
		want    []string
		wantErr error
	}{
		{name: "none", input: nil, want: nil},
		{name: "vmaf is implicit", input: []string{"vmaf"}, want: nil},
		{
			name:  "normalised, deduplicated and ordered",
			input: []string{" PSNR", "xpsnr", "", "cambi", "psnr", "CIEDE2000", "ms-ssim"},
			want:  []string{MetricXPSNR, MetricCAMBI, MetricPSNR, MetricMSSSIM, MetricCIEDE2000},
		},
		{name: "AV2 CTC set", input: AV2CTCMetrics(), want: AV2CTCMetrics()[1:]},
		{name: "unknown", input: []string{"psnr", "vif"}, wantErr: ErrUnknownMetric},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseMetrics(testCase.input)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.Contains(t, err.Error(), "supported: vmaf, xpsnr")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestParseDevices(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   []string
		want    []string
		wantErr error
	}{
		{name: "none", input: nil, want: nil},
		{name: "ordered", input: []string{"4K", "phone", "tv", "phone"}, want: []string{vmaf.DevicePhone, vmaf.DeviceTV, vmaf.Device4K}},
		{name: "unknown", input: []string{"watch"}, wantErr: ErrUnknownDevice},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseDevices(testCase.input)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestMetricSeries(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		metrics []string
		want    []string
	}{
		{name: "none", metrics: nil, want: nil},
		{
			name:    "every metric, in the given order",
			metrics: []string{MetricXPSNR, MetricCAMBI, MetricPSNR, MetricPSNRHVS, MetricSSIM, MetricMSSSIM, MetricCIEDE2000},
			want: []string{
				SeriesXPSNRY, SeriesXPSNRU, SeriesXPSNRV, SeriesCAMBI, SeriesPSNRY, SeriesPSNRCb, SeriesPSNRCr, SeriesPSNRYUV,
				SeriesPSNRHVS, SeriesSSIM, SeriesMSSSIM, SeriesCIEDE2000,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var names []string
			for _, s := range metricSeries(testCase.metrics, 1920, 1080, 8) {
				names = append(names, s.name)
			}

			assert.Equal(t, testCase.want, names)
		})
	}
}

func TestXPSNRSeries(
	t *testing.T,
) {
	s := xpsnrSeries(SeriesXPSNRU, 960, 540, 10)

	assert.True(t, s.decreasing)
	assert.InDelta(t, xpsnr.Decibels(1000, 960, 540, 10), s.report(1000), 1e-12)
	assert.Equal(t, float64(maxDecibels), s.report(0), "identical frames are capped")
}

func TestFeatureValues(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		features map[string][]float64
		want     map[string][]float64
	}{
		{
			name: "weighted PSNR-YUV",
			features: map[string][]float64{
				vmaf.FeaturePSNRY: {40, 48}, vmaf.FeaturePSNRCb: {44, 32}, vmaf.FeaturePSNRCr: {36, 32},
			},
			want: map[string][]float64{
				SeriesPSNRY: {40, 48}, SeriesPSNRCb: {44, 32}, SeriesPSNRCr: {36, 32},
				SeriesPSNRYUV: {(14*40 + 44 + 36) / 16.0, (14*48 + 32 + 32) / 16.0},
			},
		},
		{
			name: "infinite values capped",
			features: map[string][]float64{
				vmaf.FeaturePSNRHVS: {math.Inf(1), 41}, vmaf.FeatureCIEDE2000: {math.Inf(1), 38},
				vmaf.FeatureCAMBI: {3.5, 0}, vmaf.FeatureSSIM: {0.9, 1}, vmaf.FeatureMSSSIM: {0.8, 1},
			},
			want: map[string][]float64{
				SeriesPSNRHVS: {maxDecibels, 41}, SeriesCIEDE2000: {maxDecibels, 38},
				SeriesCAMBI: {3.5, 0}, SeriesSSIM: {0.9, 1}, SeriesMSSSIM: {0.8, 1},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			values := map[string][]float64{}
			featureValues(testCase.features, values)

			assert.Equal(t, testCase.want, values)
		})
	}
}

// sampledStrata runs the sampling loop on noisy scores and returns the
// strata and the clip results, each clip carrying the scores plus 10 as the
// "shifted" series and 2·score as the "scaled" one.
func sampledStrata(
	t *testing.T,
) ([]*stratum, []clipResult) {
	t.Helper()

	strata := uniformStrata(2000, 400)
	score := scoresBy(noisy(4))

	withSeries := func(ctx context.Context, clips []clip, round int) ([]clipResult, error) {
		results, err := score(ctx, clips, round)
		for i, cr := range results {
			shifted, scaled := make([]float64, len(cr.scores)), make([]float64, len(cr.scores))
			for j, s := range cr.scores {
				shifted[j], scaled[j] = s+10, 2*s
			}

			results[i].values = map[string][]float64{"same": cr.scores, "shifted": shifted, "scaled": scaled}
		}

		return results, err
	}

	out, err := sample(t.Context(), strata, 2000, Options{Precision: 0.1}.withDefaults(), withSeries, nil)
	require.NoError(t, err)

	return strata, out.results
}

// TestSeriesEstimatorIsVMAFs checks that a series gets exactly the estimate
// VMAF gets from the same clips: the generalised estimator does not change
// VMAF's.
func TestSeriesEstimatorIsVMAFs(
	t *testing.T,
) {
	strata, results := sampledStrata(t)
	vmafEst := estimateMean(strata, defaultConfidence)

	testCases := []struct {
		name  string
		shift float64
		scale float64
	}{
		{name: "same", scale: 1},
		{name: "shifted", shift: 10, scale: 1},
		{name: "scaled", scale: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			est := estimateMean(seriesStrata(strata, results, testCase.name), defaultConfidence)

			assert.InDelta(t, vmafEst.mean*testCase.scale+testCase.shift, est.mean, 1e-9)
			assert.InDelta(t, vmafEst.halfWidth*testCase.scale, est.halfWidth, 1e-9)
			assert.InDelta(t, vmafEst.df, est.df, 1e-12)
		})
	}
}

func TestPooledEstimate(
	t *testing.T,
) {
	strata, results := sampledStrata(t)
	vmafEst := estimateMean(strata, defaultConfidence)
	negate := func(v float64) float64 { return -v }

	testCases := []struct {
		name   string
		series series
		exact  bool
		check  func(t *testing.T, e Estimate)
	}{
		{
			name:   "sampled",
			series: series{name: "same"},
			check: func(t *testing.T, e Estimate) {
				assert.InDelta(t, vmafEst.mean, e.Mean, 1e-9)
				assert.InDelta(t, vmafEst.mean-vmafEst.halfWidth, e.Low, 1e-9)
				assert.InDelta(t, vmafEst.mean+vmafEst.halfWidth, e.High, 1e-9)
				assert.InDelta(t, vmafEst.halfWidth, e.HalfWidth, 1e-9)
			},
		},
		{
			name:   "decreasing transform swaps the bounds",
			series: series{name: "same", value: negate, decreasing: true},
			check: func(t *testing.T, e Estimate) {
				assert.InDelta(t, -vmafEst.mean, e.Mean, 1e-9)
				assert.InDelta(t, -vmafEst.mean-vmafEst.halfWidth, e.Low, 1e-9)
				assert.InDelta(t, -vmafEst.mean+vmafEst.halfWidth, e.High, 1e-9)
				assert.Less(t, e.Low, e.High)
			},
		},
		{
			name:   "exact: the transformed mean of every frame",
			series: series{name: "scaled", value: negate},
			exact:  true,
			check: func(t *testing.T, e Estimate) {
				var all []float64
				for _, cr := range results {
					all = append(all, cr.values["scaled"]...)
				}

				assert.InDelta(t, -stats.Mean(all), e.Mean, 1e-9)
				assert.Equal(t, e.Mean, e.Low)
				assert.Equal(t, e.Mean, e.High)
				assert.Zero(t, e.HalfWidth)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.check(t, pooledEstimate(testCase.series, strata, results, testCase.exact, estimateMean, defaultConfidence))
		})
	}
}

// cambiFrames returns frames 40 ms apart with the given CAMBI values (NaN:
// no CAMBI), starting at index first.
func cambiFrames(
	first int,
	values ...float64,
) []FrameScore {
	frames := make([]FrameScore, len(values))

	for i, v := range values {
		idx := first + i
		frames[i] = FrameScore{Index: idx, PTS: media.Seconds(float64(idx) * 0.04)}

		if !math.IsNaN(v) {
			frames[i].Metrics = map[string]float64{SeriesCAMBI: v}
		}
	}

	return frames
}

func TestBandingOf(
	t *testing.T,
) {
	frame := media.Seconds(0.04)
	nan := math.NaN()

	testCases := []struct {
		name       string
		frames     []FrameScore
		wantFrames int
		want       []BandingSegment
	}{
		{name: "clean", frames: cambiFrames(0, 1, 2, 5, nan), want: nil},
		{
			name:       "a clean frame splits segments",
			frames:     cambiFrames(0, 6, 8, 2, 7),
			wantFrames: 3,
			want: []BandingSegment{
				{Interval: media.Interval{Start: 0, End: media.Seconds(0.04) + frame}, Frames: 2, Mean: 7, Peak: 8},
				{Interval: media.Interval{Start: media.Seconds(0.12), End: media.Seconds(0.12) + frame}, Frames: 1, Mean: 7, Peak: 7},
			},
		},
		{
			name:       "nearby clips join, distant ones do not",
			frames:     append(append(cambiFrames(0, 6), cambiFrames(20, 10)...), cambiFrames(100, 6)...),
			wantFrames: 3,
			want: []BandingSegment{
				{Interval: media.Interval{Start: 0, End: media.Seconds(0.8) + frame}, Frames: 2, Mean: 8, Peak: 10},
				{Interval: media.Interval{Start: media.Seconds(4), End: media.Seconds(4) + frame}, Frames: 1, Mean: 6, Peak: 6},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := bandingOf(testCase.frames, frame)

			assert.InDelta(t, BandingThreshold, b.Threshold, 0)
			assert.Equal(t, testCase.wantFrames, b.BandedFrames)
			require.Len(t, b.Segments, len(testCase.want))

			for i, want := range testCase.want {
				got := b.Segments[i]
				assert.Equal(t, want.Frames, got.Frames)
				assert.InDelta(t, want.Mean, got.Mean, 1e-9)
				assert.InDelta(t, want.Peak, got.Peak, 1e-9)
				assert.InDelta(t, want.Start.Seconds(), got.Start.Seconds(), 1e-9)
				assert.InDelta(t, want.End.Seconds(), got.End.Seconds(), 1e-9)
			}
		})
	}
}

func TestFrameDuration(
	t *testing.T,
) {
	testCases := []struct {
		name string
		rate media.Rational
		want media.Duration
	}{
		{name: "25 fps", rate: media.Rational{Num: 25, Den: 1}, want: media.Seconds(0.04)},
		{name: "unknown", rate: media.Rational{}, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, frameDuration(testCase.rate))
		})
	}
}

func TestRawSeries(
	t *testing.T,
) {
	res := &Result{BitDepth: 8, Model: vmaf.ModelSpec{Width: 1920, Height: 1080}}

	for i, d := range []float64{5000, 20000} {
		res.Frames = append(res.Frames, FrameScore{Index: i, Metrics: map[string]float64{
			SeriesXPSNRY: xpsnr.Decibels(d, 1920, 1080, 8),
			SeriesXPSNRU: xpsnr.Decibels(d, 960, 540, 8),
			SeriesPSNRY:  40 + float64(i),
		}})
	}

	res.Frames = append(res.Frames, FrameScore{Index: 2})

	testCases := []struct {
		name   string
		series string
		want   []float64
		wantOK bool
	}{
		{name: "XPSNR luma back to its distortion", series: SeriesXPSNRY, want: []float64{5000, 20000}, wantOK: true},
		{name: "XPSNR chroma at chroma size", series: SeriesXPSNRU, want: []float64{5000, 20000}, wantOK: true},
		{name: "other series as reported", series: SeriesPSNRY, want: []float64{40, 41}, wantOK: true},
		{name: "not measured", series: SeriesCAMBI, want: []float64{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := RawSeries(res, testCase.series)

			assert.Equal(t, testCase.wantOK, ok)
			assert.InDeltaSlice(t, testCase.want, got, 1e-6)
		})
	}
}

func TestDescribeSeries(
	t *testing.T,
) {
	summary := stats.Summary{P5: 31.23456, P95: 6.5}

	testCases := []struct {
		name       string
		series     string
		wantLabel  string
		wantWorst  float64
		wantFormat string
	}{
		{name: "dB metric", series: SeriesXPSNRY, wantLabel: "XPSNR Y", wantWorst: 31.23456, wantFormat: "31.23"},
		{name: "lower is better", series: SeriesCAMBI, wantLabel: "CAMBI (banding)", wantWorst: 6.5, wantFormat: "31.23"},
		{name: "four decimals", series: SeriesMSSSIM, wantLabel: "MS-SSIM", wantWorst: 31.23456, wantFormat: "31.2346"},
		{name: "unknown", series: "vmaf_phone", wantLabel: "vmaf_phone", wantWorst: 31.23456, wantFormat: "31.23"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			info := DescribeSeries(testCase.series)

			assert.Equal(t, testCase.wantLabel, info.Label)
			assert.InDelta(t, testCase.wantWorst, info.Worst(summary), 0)
			assert.Equal(t, testCase.wantFormat, info.Format(31.23456))
		})
	}
}

// TestSimulateSeriesCoverage replays the sampling loop on shot-structured
// VMAF scores with two other series measured on the same clips: one
// correlated with VMAF, one with its own shot levels and noise. Both
// intervals must stay honest although VMAF alone sizes the sample.
func TestSimulateSeriesCoverage(
	t *testing.T,
) {
	rng := rand.New(rand.NewPCG(11, 11))

	const n = 6000

	scores := make([]float64, n)
	correlated := make([]float64, n)
	independent := make([]float64, n)
	pts := make([]media.Duration, n)

	var keys []media.Duration

	level, other := 85.0, 40.0

	for i := range scores {
		pts[i] = media.Seconds(float64(i) / 25)

		if i%(40+rng.IntN(60)) == 0 {
			keys = append(keys, pts[i])
			level, other = 70+25*rng.Float64(), 32+10*rng.Float64()
		}

		scores[i] = level + math.Sin(float64(i)/15) + 0.7*rng.NormFloat64()
		correlated[i] = 0.4*scores[i] + 0.3*rng.NormFloat64()
		independent[i] = other + 1.5*rng.NormFloat64()
	}

	series := map[string][]float64{"correlated": correlated, "independent": independent}
	sim, others := SimulateSeries(scores, series, pts, keys, Options{Precision: 0.5}, 400)

	assert.Equal(t, Simulate(scores, pts, keys, Options{Precision: 0.5}, 400), sim, "extra series do not change VMAF's replay")
	require.Len(t, others, 2)

	for name, s := range others {
		t.Logf("%s: %+v", name, s)
		assert.InDelta(t, stats.Mean(series[name]), s.True, 1e-9)
		assert.InDelta(t, 0.95, s.SampledCoverage, 0.035, name)
		assert.Equal(t, sim.Strata, s.Strata)
		assert.Equal(t, sim.Fallbacks, s.Fallbacks)
	}
}

// metricClips are a reference and a degraded encode with a smooth gradient
// that bands once quantised.
func metricClips(
	t *testing.T,
	seconds float64,
) (ref, dist Input) {
	t.Helper()

	return clips(t, testutil.Clip{Seconds: seconds, GOP: 10})
}

func TestMeasureMetrics(
	t *testing.T,
) {
	ref, dist := metricClips(t, 2)
	n := ref.Bitstream.PacketCount
	metrics := []string{MetricXPSNR, MetricCAMBI, MetricPSNR}

	exact, err := measureWithin(t, decodedMeter(), ref, dist, Options{Model: testModel, Exact: true, Metrics: metrics})
	require.NoError(t, err)

	testCases := []struct {
		name  string
		opts  Options
		check func(t *testing.T, res *Result)
	}{
		{
			name: "exact",
			opts: Options{Model: testModel, Exact: true, Metrics: metrics},
			check: func(t *testing.T, res *Result) {
				require.Len(t, res.Metrics, 8)
				assert.Len(t, res.Frames, n)

				for _, m := range res.Metrics {
					assert.Equal(t, m.Mean, m.Low, m.Name)
					assert.Zero(t, m.HalfWidth, m.Name)
					assert.Len(t, frameValues(res.Frames, m.Name), n, m.Name)
				}

				require.NotNil(t, res.Banding)
				assert.InDelta(t, BandingThreshold, res.Banding.Threshold, 0)
			},
		},
		{
			name: "sampled: frames bit-exact with the exact run",
			opts: Options{Model: testModel, Precision: 20, Workers: 2, InitialClips: 4, Metrics: metrics},
			check: func(t *testing.T, res *Result) {
				require.Equal(t, ModeSampled, res.Mode)
				require.Len(t, res.Metrics, 8)
				assert.Less(t, len(res.Frames), n)

				for _, f := range res.Frames {
					assert.Equal(t, exact.Frames[f.Index], f, "frame %d", f.Index)
				}

				for i, m := range res.Metrics {
					assert.Less(t, m.Low, m.High, m.Name)
					assert.InDelta(t, exact.Metrics[i].Mean, m.Mean, 4*m.HalfWidth+1e-6, m.Name)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ref, dist, testCase.opts)
			require.NoError(t, err)

			byName := map[string]MetricResult{}
			for _, m := range res.Metrics {
				byName[m.Name] = m
			}

			xpsnrY, ok := res.Metric(SeriesXPSNRY)
			require.True(t, ok)
			assert.Equal(t, byName[SeriesXPSNRY], xpsnrY)

			_, ok = res.Metric(SeriesSSIM)
			assert.False(t, ok, "not requested")

			assert.Greater(t, byName[SeriesXPSNRY].Mean, 20.0)
			assert.Greater(t, byName[SeriesPSNRY].Mean, 20.0)
			assert.GreaterOrEqual(t, byName[SeriesCAMBI].Mean, 0.0)
			assert.Greater(t, byName[SeriesPSNRYUV].Mean, byName[SeriesPSNRY].Mean,
				"chroma, less damaged, lifts the weighted PSNR")
			testCase.check(t, res)
		})
	}
}

// TestMeasureAV2CTC measures the AV2 CTC metric set, including the costly
// ones, on a short clip at 10 bits.
func TestMeasureAV2CTC(
	t *testing.T,
) {
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.24, PixelFormat: "yuv420p10le", Name: "ref.mp4", Args: []string{"-qp", "0"}}))
	dist := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.24, PixelFormat: "yuv420p10le", Name: "dist.mp4", Args: []string{"-crf", "40"}}))

	res, err := measureWithin(t, decodedMeter(), ref, dist, Options{Model: testModel, Exact: true, Metrics: AV2CTCMetrics()})
	require.NoError(t, err)

	assert.Equal(t, 10, res.BitDepth)

	byName := map[string]MetricResult{}
	for _, m := range res.Metrics {
		byName[m.Name] = m
	}

	assert.Len(t, byName, 9)
	assert.InDelta(t, 0.5, byName[SeriesSSIM].Mean, 0.5)
	assert.InDelta(t, 0.5, byName[SeriesMSSSIM].Mean, 0.5)
	assert.Greater(t, byName[SeriesPSNRHVS].Mean, 10.0)
	assert.Greater(t, byName[SeriesCIEDE2000].Mean, 10.0)
	assert.LessOrEqual(t, byName[SeriesCIEDE2000].Mean, float64(maxDecibels))
}

func TestMeasureDevices(
	t *testing.T,
) {
	ref, dist := metricClips(t, 0.8)
	n := ref.Bitstream.PacketCount
	devices := []string{vmaf.Device4K, vmaf.DeviceTV, vmaf.DevicePhone}

	testCases := []struct {
		name  string
		opts  Options
		check func(t *testing.T, res *Result)
	}{
		{
			name: "exact: TV is the primary model, 4K a second pass at 2160p",
			opts: Options{Exact: true, Devices: devices},
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, 4*n, res.FramesDecoded, "two passes on both sides")
				assert.Equal(t, map[string]int{planSweep: 2}, res.Plans)
			},
		},
		{
			name: "sampled: every pass on the same clips",
			opts: Options{Precision: 20, InitialClips: 4, Devices: devices},
			check: func(t *testing.T, res *Result) {
				require.Equal(t, ModeSampled, res.Mode)

				for _, d := range res.Devices {
					assert.LessOrEqual(t, d.Low, d.Mean, d.Device)
					assert.LessOrEqual(t, d.Mean, d.High, d.Device)
					assert.Len(t, frameValues(res.Frames, seriesDevice+d.Device), len(res.Frames), d.Device)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ref, dist, testCase.opts)
			require.NoError(t, err)

			require.Len(t, res.Devices, 3)
			phone, tv, uhd := res.Devices[0], res.Devices[1], res.Devices[2]

			found, ok := res.Device(vmaf.Device4K)
			require.True(t, ok)
			assert.Equal(t, uhd, found)

			_, ok = res.Device("watch")
			assert.False(t, ok)

			assert.Equal(t, []string{vmaf.DevicePhone, vmaf.DeviceTV, vmaf.Device4K}, []string{phone.Device, tv.Device, uhd.Device})
			assert.Equal(t, res.Model, tv.Model, "the automatic model is the TV one")
			assert.Equal(t, res.Mean, tv.Mean)
			assert.Equal(t, res.Low, tv.Low)
			assert.Equal(t, "vmaf_v1.0.16_5d0h", phone.Model.Name)
			assert.NotEqual(t, tv.Mean, phone.Mean, "each model is scored on its own")
			assert.Equal(t, 3840, uhd.Model.Width)
			assert.NotEqual(t, tv.Mean, uhd.Mean)
			testCase.check(t, res)
		})
	}
}

// TestPhoneDeviceMatchesItsModel checks that a device scored next to the
// primary model gives the scores of its model measured alone.
func TestPhoneDeviceMatchesItsModel(
	t *testing.T,
) {
	ref, dist := metricClips(t, 0.4)

	alone, err := measureWithin(t, decodedMeter(), ref, dist, Options{Exact: true, Model: "vmaf_v1.0.16_5d0h"})
	require.NoError(t, err)

	together, err := measureWithin(t, decodedMeter(), ref, dist, Options{Exact: true, Devices: []string{vmaf.DevicePhone}})
	require.NoError(t, err)

	require.Len(t, together.Devices, 1)
	assert.InDelta(t, alone.Mean, together.Devices[0].Mean, 1e-9)

	for i, f := range together.Frames {
		assert.InDelta(t, alone.Frames[i].Score, f.Metrics[seriesDevice+vmaf.DevicePhone], 1e-9, "frame %d", i)
	}
}

func TestMeasureMetricErrors(
	t *testing.T,
) {
	ref := probeInput(t, testutil.Generate(t, testutil.Clip{Seconds: 0.4}))

	// A broken 4K model file shadows libvmaf's built-in one.
	broken := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(broken, "vmaf_v1.0.16_3d0h_2160.json"), []byte("{"), 0o600))

	testCases := []struct {
		name    string
		opts    Options
		wantErr error
		wantMsg string
	}{
		{name: "unknown metric", opts: Options{Model: testModel, Metrics: []string{"vif"}}, wantErr: ErrUnknownMetric},
		{name: "unknown device", opts: Options{Model: testModel, Devices: []string{"watch"}}, wantErr: ErrUnknownDevice},
		{
			name:    "device model missing, exact",
			opts:    Options{Model: testModel, Exact: true, Devices: []string{vmaf.Device4K}, ModelDirs: []string{broken}},
			wantMsg: "load model",
		},
		{
			name:    "device model missing, sampled",
			opts:    Options{Model: testModel, Precision: 50, Devices: []string{vmaf.Device4K}, ModelDirs: []string{broken}},
			wantMsg: "load model",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ref, ref, testCase.opts)
			require.Error(t, err)
			assert.Nil(t, res)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			}

			assert.Contains(t, err.Error(), testCase.wantMsg)
		})
	}
}

func TestSeriesInfoShortLabel(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		series string
		want   string
	}{
		{name: "hint dropped", series: SeriesCAMBI, want: "CAMBI"},
		{name: "plain label kept", series: SeriesXPSNRY, want: "XPSNR Y"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, DescribeSeries(testCase.series).ShortLabel())
		})
	}
}

// TestNameListsAreClones checks that callers cannot alter the package's
// name lists through the slices they get.
func TestNameListsAreClones(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		list  func() []string
		first string
	}{
		{name: "metrics", list: Metrics, first: MetricVMAF},
		{name: "AV2 CTC metrics", list: AV2CTCMetrics, first: MetricVMAF},
		{name: "headline series", list: HeadlineSeries, first: SeriesXPSNRY},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.list()
			require.NotEmpty(t, got)
			assert.Equal(t, testCase.first, got[0])

			got[0] = "changed"
			assert.Equal(t, testCase.first, testCase.list()[0])
		})
	}
}
