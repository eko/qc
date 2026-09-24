package ladder

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/vmaf"
)

// TestBuildNVENC builds an HEVC ladder with NVENC: the probes follow the
// NVENC CQ set, CQs stay integers, the rung commands encode (and decode)
// on the GPU, and every measurement asks for the requested VMAF backend.
func TestBuildNVENC(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{cappedBias: -4}, sourceReport(1920, 1080, 8, 25, 600))

	res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{
		Codec: "hevc", Encoder: encode.HardwareNVENC, Backend: vmaf.BackendAuto,
	})
	require.NoError(t, err)

	assert.Equal(t, "hevc_nvenc", res.Codec.Encoder)
	assert.Equal(t, encode.HardwareNVENC, res.Codec.Hardware)
	assert.Equal(t, "p5", res.Preset)

	var crfs []float64
	for _, p := range res.Probes {
		if p.Height == 1080 {
			crfs = append(crfs, p.CRF)
		}
	}

	nvenc, err := encode.LookupFor("hevc", encode.HardwareNVENC)
	require.NoError(t, err)
	assert.Subset(t, crfs, nvenc.ProbeCRFs, "NVENC's probe CQs")

	for _, r := range res.Rungs {
		assert.Zero(t, r.CRF-math.Round(r.CRF), "rung CQ %v", r.CRF)
		assert.True(t, strings.HasPrefix(r.Command, "ffmpeg -hwaccel cuda -i "), r.Command)
		assert.Contains(t, r.Command, "-c:v hevc_nvenc")
		assert.Contains(t, r.Command, "-cq ")
	}

	require.NotEmpty(t, lab.compared)

	for _, q := range lab.compared {
		assert.Equal(t, vmaf.BackendAuto, q.Backend)
	}
}

func TestEncoderOptions(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		codec   string
		hw      encode.Hardware
		opts    Options
		wantErr error
	}{
		{name: "cpu film grain", codec: "av1", opts: Options{FilmGrain: FilmGrainAuto}},
		{name: "cpu per-shot", codec: "hevc", opts: Options{PerShot: true}},
		{name: "nvenc plain", codec: "av1", hw: encode.HardwareNVENC},
		{name: "nvenc av1 film grain", codec: "av1", hw: encode.HardwareNVENC, opts: Options{FilmGrain: 10}, wantErr: ErrHardwareEncoder},
		{name: "nvenc h264 ignores film grain", codec: "h264", hw: encode.HardwareNVENC, opts: Options{FilmGrain: FilmGrainAuto}},
		{name: "nvenc per-shot", codec: "h264", hw: encode.HardwareNVENC, opts: Options{PerShot: true}, wantErr: ErrHardwareEncoder},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := encode.LookupFor(testCase.codec, testCase.hw)
			require.NoError(t, err)

			require.ErrorIs(t, encoderOptions(testCase.opts, codec), testCase.wantErr)
		})
	}
}

func TestBuildRejectsHardwareOptions(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, sourceReport(1280, 720, 8, 25, 60))

	testCases := []struct {
		name    string
		opts    Options
		wantErr error
	}{
		{name: "unknown encoder", opts: Options{Codec: "h264", Encoder: "qsv"}, wantErr: encode.ErrUnknownHardware},
		{name: "per-shot on nvenc", opts: Options{Codec: "h264", Encoder: encode.HardwareNVENC, PerShot: true}, wantErr: ErrHardwareEncoder},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := labEngine(lab).Build(t.Context(), sourcePath, testCase.opts)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Nil(t, res)
		})
	}
}
