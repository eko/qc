package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// verified returns a rung measured at the given VMAF, with XPSNR (luma)
// when xpsnr is positive and banding on banded frames.
func verified(
	height int,
	vmafScore, xpsnr float64,
	banded int,
) Rung {
	m := &Measurement{VMAF: vmafScore, BandedFrames: banded, ScoredFrames: 100}
	if xpsnr > 0 {
		m.Metrics = map[string]float64{quality.SeriesXPSNRY: xpsnr}
	}

	return Rung{Height: height, Measured: m}
}

func TestRankConflicts(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		rungs []Rung
		want  []RankConflict
	}{
		{
			name:  "both metrics agree",
			rungs: []Rung{verified(1080, 95, 40, 0), verified(720, 88, 37, 0)},
		},
		{
			name:  "VMAF favours the lower resolution, XPSNR clearly disagrees",
			rungs: []Rung{verified(720, 80, 33, 0), verified(540, 83, 32, 0)},
			want:  []RankConflict{{Higher: 1, Lower: 0, VMAF: [2]float64{83, 80}, XPSNR: [2]float64{32, 33}}},
		},
		{
			name:  "differences within noise are not conflicts",
			rungs: []Rung{verified(720, 80, 33, 0), verified(540, 81.5, 33.3, 0)},
		},
		{
			name:  "no XPSNR, no verdict",
			rungs: []Rung{verified(720, 80, 0, 0), verified(540, 83, 0, 0)},
		},
		{
			name:  "unverified rungs are skipped",
			rungs: []Rung{{Height: 720}, verified(540, 83, 32, 0)},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, RankConflicts(testCase.rungs))
		})
	}
}

func TestBandedRungs(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		rungs []Rung
		want  []int
	}{
		{name: "clean", rungs: []Rung{verified(1080, 95, 0, 0)}},
		{name: "unverified", rungs: []Rung{{Height: 720}}},
		{
			name:  "banded on at least 5% of the scored frames",
			rungs: []Rung{verified(1080, 95, 0, 0), verified(720, 80, 0, 4), verified(540, 70, 0, 5), verified(360, 50, 0, 12)},
			want:  []int{2, 3},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, BandedRungs(testCase.rungs))
		})
	}
}

func TestMeasurementOf(
	t *testing.T,
) {
	distorted := &analysis.Report{Bitstream: &bitstream.Report{AverageBitrate: 2_000_000}}

	testCases := []struct {
		name string
		vmaf *quality.Result
		want Measurement
	}{
		{
			name: "VMAF only",
			vmaf: &quality.Result{Mean: 90, HalfWidth: 0.8},
			want: Measurement{Bitrate: 2_000_000, VMAF: 90, HalfWidth: 0.8},
		},
		{
			name: "metrics, devices and banding",
			vmaf: &quality.Result{
				Mean: 90, HalfWidth: 0.8,
				Metrics:      []quality.MetricResult{{Name: quality.SeriesXPSNRY, Estimate: quality.Estimate{Mean: 38.5}}},
				Devices:      []quality.DeviceResult{{Device: vmaf.DevicePhone, Estimate: quality.Estimate{Mean: 95}}},
				Banding:      &quality.Banding{BandedFrames: 7, SourceFrames: 4},
				FramesScored: 140,
			},
			want: Measurement{
				Bitrate: 2_000_000, VMAF: 90, HalfWidth: 0.8,
				Metrics:      map[string]float64{quality.SeriesXPSNRY: 38.5},
				Devices:      map[string]float64{vmaf.DevicePhone: 95},
				BandedFrames: 7,
				ScoredFrames: 140,

				SourceBandedFrames: 4,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := measurementOf(&analysis.Comparison{Distorted: distorted, VMAF: testCase.vmaf})
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestBandedShare(
	t *testing.T,
) {
	testCases := []struct {
		name string
		m    Measurement
		want float64
	}{
		{name: "CAMBI not measured", m: Measurement{}},
		{name: "a share of the scored frames", m: Measurement{BandedFrames: 3, ScoredFrames: 60}, want: 0.05},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, testCase.m.BandedShare(), 1e-12)
		})
	}
}

func TestInheritedBanding(
	t *testing.T,
) {
	assert.Zero(t, Measurement{}.InheritedBanding(), "no banded frame")
	assert.Zero(t, Measurement{BandedFrames: 8}.InheritedBanding(), "the source is clean")
	assert.InDelta(t, 0.75, Measurement{BandedFrames: 8, SourceBandedFrames: 6}.InheritedBanding(), 1e-12)
}
