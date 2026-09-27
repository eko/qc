package quality

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality/hdr"
	"github.com/eko/qc/vmaf/libvmaf"
)

func TestParseHDRMetric(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    HDRMetric
		wantErr bool
	}{
		{name: "default", input: "", want: HDRMetricPQ},
		{name: "pq", input: "PQ", want: HDRMetricPQ},
		{name: "tonemap", input: " tonemap ", want: HDRMetricToneMap},
		{name: "unknown", input: "hdrvmaf", want: HDRMetricPQ, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseHDRMetric(testCase.input)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrUnknownHDRMetric)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestHDRReport(
	t *testing.T,
) {
	pq := media.VideoStream{Color: media.Color{Transfer: media.TransferPQ}}
	hlg := media.VideoStream{Color: media.Color{Transfer: media.TransferHLG}}

	testCases := []struct {
		name       string
		video      media.VideoStream
		metric     HDRMetric
		wantNil    bool
		calibrated bool
		note       string
	}{
		{name: "sdr", video: media.VideoStream{}, wantNil: true},
		{name: "pq", video: pq, metric: HDRMetricPQ, note: notePQ},
		{name: "hlg", video: hlg, metric: HDRMetricPQ, note: noteHLG},
		{name: "tone mapped", video: pq, metric: HDRMetricToneMap, calibrated: true, note: noteToneMap},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := hdrReport(testCase.video, testCase.metric)
			if testCase.wantNil {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Equal(t, testCase.video.Color.Transfer, got.Transfer)
			assert.Equal(t, testCase.calibrated, got.Calibrated)
			assert.Equal(t, testCase.note, got.Note)
		})
	}
}

func TestHDRSeriesValues(
	t *testing.T,
) {
	values := map[string][]float64{}
	hdrSeriesValues([]hdr.Values{
		{WPSNR: [3]float64{40, 45, 50}, DeltaE: 2, DeltaEP99: 9},
		{WPSNR: [3]float64{posInf(), 45, 50}, DeltaE: 0, DeltaEP99: 0.02},
	}, hdrSeries, values)

	assert.Equal(t, []float64{40, maxDecibels}, values[SeriesWPSNRY], "identical planes are capped")
	assert.Equal(t, []float64{45, 45}, values[SeriesWPSNRCb])
	assert.Equal(t, []float64{50, 50}, values[SeriesWPSNRCr])
	assert.Equal(t, []float64{2, 0}, values[SeriesDeltaEITP])
	assert.Equal(t, []float64{9, 0.02}, values[SeriesDeltaEITPP99])
}

func posInf() float64 {
	return math.Inf(1)
}

// hdrClips generates a near-lossless PQ reference and a degraded encode of
// the same synthetic content, both HDR10.
func hdrClips(
	t *testing.T,
) (ref, dist Input) {
	t.Helper()

	c := testutil.HDRClip(media.TransferPQ)
	c.Seconds, c.GOP = 1.2, 10

	base := c.Args
	c.Name, c.Args = "ref.mp4", append(slices.Clone(base), "-crf", "4")
	ref = probeInput(t, testutil.Generate(t, c))

	c.Name, c.Args = "dist.mp4", append(slices.Clone(base), "-crf", "40")
	dist = probeInput(t, testutil.Generate(t, c))

	return ref, dist
}

func TestMeasureHDR(
	t *testing.T,
) {
	ref, dist := hdrClips(t)
	require.True(t, ref.Video.MeasurableHDR())

	pq, err := measureWithin(t, decodedMeter(), ref, dist, Options{Model: testModel, Exact: true})
	require.NoError(t, err)

	testCases := []struct {
		name       string
		opts       Options
		wantHDR    bool
		wantMetric HDRMetric
		check      func(t *testing.T, res *Result)
	}{
		{
			name: "pq: hdr metrics on the scored frames", opts: Options{Model: testModel, Exact: true},
			wantHDR: true, wantMetric: HDRMetricPQ,
			check: func(t *testing.T, res *Result) {
				wpsnr, _ := res.Metric(SeriesWPSNRY)
				deltaE, _ := res.Metric(SeriesDeltaEITP)
				p99, _ := res.Metric(SeriesDeltaEITPP99)

				assert.Greater(t, wpsnr.Mean, 20.0)
				assert.Less(t, wpsnr.Mean, float64(maxDecibels))
				assert.Positive(t, deltaE.Mean)
				assert.Greater(t, p99.Mean, deltaE.Mean)
			},
		},
		{
			name: "tonemap: vmaf on sdr, hdr metrics from a second pass on the pq frames", opts: Options{Model: testModel, Exact: true, HDRMetric: HDRMetricToneMap},
			wantHDR: true, wantMetric: HDRMetricToneMap,
			check: func(t *testing.T, res *Result) {
				assert.NotEqual(t, pq.Mean, res.Mean, "vmaf scores other frames")

				for _, name := range hdrSeries {
					got, _ := res.Metric(name)
					want, _ := pq.Metric(name)
					assert.InDelta(t, want.Mean, got.Mean, 1e-9, name)
				}

				assert.Greater(t, res.FramesDecoded, pq.FramesDecoded, "the hdr pass decodes again")
			},
		},
		{
			name: "tonemap sampled", opts: Options{Model: testModel, Precision: 30, InitialClips: 4, Workers: 2, HDRMetric: HDRMetricToneMap},
			wantHDR: true, wantMetric: HDRMetricToneMap,
			check: func(t *testing.T, res *Result) {
				for _, f := range res.Frames {
					assert.Equal(t, pq.Frames[f.Index].Metrics[SeriesDeltaEITP], f.Metrics[SeriesDeltaEITP], "frame %d", f.Index)
				}
			},
		},
		{
			name: "probes skip the hdr metrics", opts: Options{Model: testModel, Exact: true, SkipHDRMetrics: true},
			wantMetric: HDRMetricPQ,
			check: func(t *testing.T, res *Result) {
				assert.Equal(t, pq.Mean, res.Mean)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := measureWithin(t, decodedMeter(), ref, dist, testCase.opts)
			require.NoError(t, err)

			require.NotNil(t, res.HDR)
			assert.Equal(t, testCase.wantMetric, res.HDR.Metric)

			_, ok := res.Metric(SeriesWPSNRY)
			assert.Equal(t, testCase.wantHDR, ok)
			testCase.check(t, res)
		})
	}
}

func TestMeasureHDRUnknownMetric(
	t *testing.T,
) {
	ref, dist := metricClips(t, 1)

	_, err := measureWithin(t, decodedMeter(), ref, dist, Options{Model: testModel, HDRMetric: "hdr-vmaf"})
	require.ErrorIs(t, err, ErrUnknownHDRMetric)
}

func TestMeasureSDRHasNoHDRMetrics(
	t *testing.T,
) {
	ref, dist := metricClips(t, 1)

	res, err := measureWithin(t, decodedMeter(), ref, dist, Options{Model: testModel, Exact: true, HDRMetric: HDRMetricToneMap})
	require.NoError(t, err)

	assert.Nil(t, res.HDR)

	_, ok := res.Metric(SeriesDeltaEITP)
	assert.False(t, ok)
}

// failingHDRPass decodes like ffmpeg, but fails the decodes of the HDR
// metrics pass (the untone-mapped ones of a tone-mapped measurement).
type failingHDRPass struct {
	decode.Source
}

var errHDRPass = errors.New("hdr pass failed")

func (s failingHDRPass) Decode(
	ctx context.Context,
	req decode.Request,
	fn func(*frame.Frame) error,
) error {
	if req.ToneMap == nil {
		return errHDRPass
	}

	return s.Source.Decode(ctx, req, fn)
}

func TestMeasureHDRPassErrors(
	t *testing.T,
) {
	ref, dist := hdrClips(t)
	meter := NewMeter(failingHDRPass{decode.NewFFmpeg("ffmpeg", 0)}, libvmaf.NewEngine())

	for _, opts := range []Options{
		{Model: testModel, Exact: true, HDRMetric: HDRMetricToneMap},
		{Model: testModel, Precision: 30, InitialClips: 4, Workers: 2, HDRMetric: HDRMetricToneMap},
	} {
		_, err := measureWithin(t, meter, ref, dist, opts)
		require.ErrorIs(t, err, errHDRPass)
	}
}

func TestHDRSeriesOf(
	t *testing.T,
) {
	assert.Equal(t, hdrSeries, hdrSeriesOf(media.TransferPQ))
	assert.Equal(t, []string{SeriesDeltaEITP, SeriesDeltaEITPP99}, hdrSeriesOf(media.TransferHLG), "wPSNR is a PQ metric")

	values := map[string][]float64{}
	hdrSeriesValues([]hdr.Values{{DeltaE: 1}}, hdrSeriesOf(media.TransferHLG), values)
	assert.Len(t, values, 2)
}
