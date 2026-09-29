package ladder

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

func TestCRFSlope(
	t *testing.T,
) {
	probes := []Probe{
		{Height: 720, CRF: 34, VMAF: 71},
		{Height: 720, CRF: 20, VMAF: 92},
		{Height: 720, CRF: 27, VMAF: 85},
		{Height: 1080, CRF: 20, VMAF: 97},
		{Height: 540, CRF: 20, VMAF: 90},
		{Height: 540, CRF: 20, VMAF: 89},
	}

	testCases := []struct {
		name   string
		height int
		crf    float64
		want   float64
	}{
		{name: "between the first two probes", height: 720, crf: 24, want: -1},
		{name: "between the last two probes", height: 720, crf: 30, want: -2},
		{name: "below the probes uses the first pair", height: 720, crf: 10, want: -1},
		{name: "above the probes uses the last pair", height: 720, crf: 50, want: -2},
		{name: "one probe has no slope", height: 1080, crf: 20, want: 0},
		{name: "no probe has no slope", height: 360, crf: 20, want: 0},
		{name: "probes of the same CRF have no slope", height: 540, crf: 20, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, crfSlope(probes, testCase.height, testCase.crf), 1e-9)
		})
	}
}

func TestCalibrate(
	t *testing.T,
) {
	probes := []Probe{
		{Height: 720, CRF: 20, VMAF: 92},
		{Height: 720, CRF: 27, VMAF: 85},
		{Height: 360, CRF: 20, VMAF: 70},
		{Height: 360, CRF: 27, VMAF: 75},
	}
	b := &build{
		codec:  mustCodec(t, "h264"),
		source: "source.mov",
		video:  media.VideoStream{Width: 1280, Height: 720, AvgFrameRate: media.Rational{Num: 25, Den: 1}},
		opts:   Options{GOPDuration: media.Seconds(2)},
	}

	testCases := []struct {
		name        string
		rung        Rung
		selected    bool
		wantChanged bool
		wantCRF     float64
	}{
		{
			name:        "quality short of the prediction lowers the CRF",
			rung:        Rung{Height: 720, CRF: 24, PredictedVMAF: 88, Measured: &Measurement{VMAF: 85}},
			selected:    true,
			wantChanged: true,
			wantCRF:     21,
		},
		{
			name:        "quality above the prediction raises the CRF",
			rung:        Rung{Height: 720, CRF: 24, PredictedVMAF: 85, Measured: &Measurement{VMAF: 87.5}},
			selected:    true,
			wantChanged: true,
			wantCRF:     26.5,
		},
		{
			name:     "not selected",
			rung:     Rung{Height: 720, CRF: 24, PredictedVMAF: 88, Measured: &Measurement{VMAF: 85}},
			wantCRF:  24,
			selected: false,
		},
		{
			name:     "not measured",
			rung:     Rung{Height: 720, CRF: 24, PredictedVMAF: 88},
			selected: true,
			wantCRF:  24,
		},
		{
			name:     "non-decreasing probes give no direction",
			rung:     Rung{Height: 360, CRF: 24, PredictedVMAF: 72, Measured: &Measurement{VMAF: 68}},
			selected: true,
			wantCRF:  24,
		},
		{
			name:     "correction below the CRF granularity",
			rung:     Rung{Height: 720, CRF: 24, PredictedVMAF: 85.1, Measured: &Measurement{VMAF: 85}},
			selected: true,
			wantCRF:  24,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rungs := []Rung{testCase.rung}
			rungs[0].Command = "unchanged"

			changed := b.calibrate(rungs, probes, func(int) bool { return testCase.selected })

			assert.Equal(t, testCase.wantChanged, changed)
			assert.Equal(t, testCase.wantChanged, rungs[0].Calibrated)
			assert.InDelta(t, testCase.wantCRF, rungs[0].CRF, 1e-9)

			if testCase.wantChanged {
				assert.Contains(t, rungs[0].Command, "-crf "+formatCRF(testCase.wantCRF)+" ")
			} else {
				assert.Equal(t, "unchanged", rungs[0].Command)
			}
		})
	}
}

func formatCRF(
	crf float64,
) string {
	return strconv.FormatFloat(crf, 'f', -1, 64)
}

func TestVerifyWithinPrediction(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, sourceReport(1280, 720, 8, 25, 60))
	b := &build{
		engine:          labEngine(lab),
		codec:           mustCodec(t, "h264"),
		digestReport:    lab.digest,
		referenceReport: lab.digest,
		workDir:         t.TempDir(),
		video:           lab.source.Info.Video[0],
		opts:            Options{GOPDuration: media.Seconds(2), Parallel: 2},
	}

	rungs := []Rung{
		{Width: 1280, Height: 720, CRF: 23, MaxRate: 12e6, BufSize: 24e6},
		{Width: 640, Height: 360, CRF: 30, MaxRate: 2e6, BufSize: 4e6},
	}

	for i := range rungs {
		r := &rungs[i]
		r.PredictedVMAF = lab.model.vmaf(encode.Params{Height: r.Height, CRF: r.CRF, MaxRate: r.MaxRate}) + CalibrationTolerance/2
	}

	require.NoError(t, b.verifyAndCalibrate(t.Context(), rungs, nil))

	for i, r := range rungs {
		require.NotNil(t, r.Measured, "rung %d", i)
		assert.False(t, r.Calibrated, "rung %d", i)
		assert.InDelta(t, r.PredictedVMAF, r.Measured.VMAF, CalibrationTolerance)
	}

	assert.Len(t, lab.params, len(rungs), "no second pass")
}

func TestRefineTop(
	t *testing.T,
) {
	model := rateModel{}
	at := func(crf float64) float64 { return model.vmaf(encode.Params{Height: 720, CRF: crf, MaxRate: 12e6}) }

	testCases := []struct {
		name string
		// slope is the dVMAF/dCRF the probes around the rung suggest.
		slope float64
		// target is the CRF whose quality the top rung is predicted at.
		target float64
	}{
		{name: "probes too steep: the first step falls short", slope: -4, target: 18},
		{name: "probes too flat: the first step overshoots", slope: -0.4, target: 21},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(model, sourceReport(1280, 720, 8, 25, 60))
			b := &build{
				engine: labEngine(lab), codec: mustCodec(t, "h264"),
				digestReport: lab.digest, referenceReport: lab.digest, workDir: t.TempDir(),
				video: lab.source.Info.Video[0], opts: Options{GOPDuration: media.Seconds(2), Parallel: 2},
			}

			// Two probes of the rung's resolution with the misleading slope.
			probes := []Probe{
				{Width: 1280, Height: 720, CRF: 15, VMAF: 90},
				{Width: 1280, Height: 720, CRF: 35, VMAF: 90 + 20*testCase.slope},
			}
			rungs := []Rung{{Width: 1280, Height: 720, CRF: 26, MaxRate: 12e6, BufSize: 24e6, PredictedVMAF: at(testCase.target)}}

			require.NoError(t, b.verifyAndCalibrate(t.Context(), rungs, probes))

			top := rungs[0]
			require.NotNil(t, top.Measured)
			assert.True(t, top.Calibrated)
			require.Len(t, lab.params, 3, "the verification and two corrections")

			// The model saturates far more than real curves near a rung: the
			// second step does not land on the target, it gets closer.
			first := lab.params[1].CRF
			assert.Less(t, math.Abs(top.CRF-testCase.target), math.Abs(first-testCase.target), "the second step gets closer to the right CRF")
		})
	}
}

func TestTopStep(
	t *testing.T,
) {
	half := func(v float64) float64 { return math.Round(v*2) / 2 }

	testCases := []struct {
		name       string
		prevCRF    float64
		prevVMAF   float64
		crf, vmaf  float64
		target     float64
		wantCRF    float64
		wantAction stepAction
	}{
		{name: "secant step", prevCRF: 30, prevVMAF: 93, crf: 33, vmaf: 91.5, target: 92.5, wantCRF: 31, wantAction: stepEncode},
		{name: "same CRF twice", prevCRF: 30, prevVMAF: 93, crf: 30, vmaf: 93, target: 94, wantCRF: 30, wantAction: stepKeep},
		{name: "quality rising with CRF: noise", prevCRF: 30, prevVMAF: 93, crf: 32, vmaf: 93.5, target: 94, wantCRF: 32, wantAction: stepKeep},
		{name: "step rounds to the current CRF", prevCRF: 30, prevVMAF: 94, crf: 32, vmaf: 93, target: 93.1, wantCRF: 32, wantAction: stepKeep},
		{name: "back to a closer first CRF", prevCRF: 30, prevVMAF: 94.1, crf: 32, vmaf: 92, target: 94, wantCRF: 30, wantAction: stepRestore},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			crf, action := topStep(testCase.prevCRF, testCase.prevVMAF, testCase.crf, testCase.vmaf, testCase.target, half)

			assert.InDelta(t, testCase.wantCRF, crf, 1e-9)
			assert.Equal(t, testCase.wantAction, action)
		})
	}
}
