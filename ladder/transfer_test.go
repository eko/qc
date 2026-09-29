package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
)

// presetModel is rateModel where a fast preset is worse at equal CRF, as
// real encoders are (x264 veryfast scores below fast at equal CRF): an
// encode at fast behaves as the slow preset at crfShift more CRF, with
// rateGain more bitrate.
type presetModel struct {
	rateModel
	fast     string
	crfShift float64
	rateGain float64
}

func (m presetModel) slowEquivalent(
	p encode.Params,
) encode.Params {
	if p.Preset == m.fast {
		p.CRF += m.crfShift
	}

	return p
}

func (m presetModel) bitrate(
	p encode.Params,
) int64 {
	b := m.rateModel.bitrate(m.slowEquivalent(p))
	if p.Preset == m.fast {
		b = int64(float64(b) * m.rateGain)
	}

	return b
}

func (m presetModel) vmaf(
	p encode.Params,
) float64 {
	return m.rateModel.vmaf(m.slowEquivalent(p))
}

func TestBuildProbePreset(
	t *testing.T,
) {
	model := presetModel{fast: "ultrafast", crfShift: 3, rateGain: 1.3}

	testCases := []struct {
		name    string
		probing Probing
	}{
		{name: "fixed probes", probing: ProbingFixed},
		{name: "adaptive probes", probing: ProbingAdaptive},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(model, sourceReport(1920, 1080, 8, 25, 600))

			res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
				Codec: "h264", Preset: "slow", ProbePreset: "ultrafast", Probing: testCase.probing,
			})
			require.NoError(t, err)

			assertAnchored(t, res, lab, model)
		})
	}
}

// assertAnchored checks a ladder probed at the model's fast preset and
// delivered at slow.
func assertAnchored(
	t *testing.T,
	res *Result,
	lab *fakeLab,
	model presetModel,
) {
	t.Helper()

	assert.Equal(t, "ultrafast", res.Probing.ProbePreset)
	require.NotEmpty(t, res.Probing.Anchors)

	assert.LessOrEqual(t, len(res.Probing.Anchors), 2, "the top and the bottom rungs")

	for _, a := range res.Probing.Anchors {
		// Read on curves interpolated between probes, on a model far more
		// curved than real encodes: roughly the true offsets.
		assert.InDelta(t, model.crfShift, a.CRF-a.ProbeCRF, 2, "%dp: the CRF offset between the presets", a.Height)
		assert.InDelta(t, 1/model.rateGain, float64(a.Bitrate)/float64(a.ProbeBitrate), 0.15, "%dp: the bitrate ratio", a.Height)
	}

	presets := map[string]int{}
	for _, p := range lab.params {
		presets[p.Preset]++
	}

	assert.Equal(t, len(res.Probes), presets["ultrafast"], "every probe at the probe preset")

	calibrated := 0

	for _, r := range res.Rungs {
		require.NotNil(t, r.Measured)
		assert.InDelta(t, r.PredictedVMAF, r.Measured.VMAF, CalibrationTolerance, "%dp crf %.1f: verified at the rungs' preset", r.Height, r.CRF)
		assert.Contains(t, r.Command, "-preset slow")

		if r.Calibrated {
			calibrated++
		}
	}

	assert.Equal(t, len(res.Probing.Anchors)+1+len(res.Rungs)+calibrated, presets["slow"], "anchors, level, verifications and corrections at the rungs' preset")
}

func TestBuildSamePresetAnchorsNothing(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, sourceReport(1920, 1080, 8, 25, 600))

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "h264", Preset: "slow"})
	require.NoError(t, err)

	assert.Empty(t, res.Probing.ProbePreset)
	assert.Empty(t, res.Probing.Anchors)
	assert.NotContains(t, res.Timings, StageAnchor)

	for _, p := range lab.params {
		assert.Equal(t, "slow", p.Preset)
	}
}
