package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

func TestCQAt(
	t *testing.T,
) {
	sweep := []sweepPoint{{cq: 20, vmaf: 95}, {cq: 30, vmaf: 75}, {cq: 40, vmaf: 55}}

	testCases := []struct {
		name   string
		points []sweepPoint
		target float64
		want   float64
		wantOK bool
	}{
		{name: "interpolated", points: sweep, target: 85, want: 25, wantOK: true},
		{name: "above the sweep", points: sweep, target: 97, want: 20},
		{name: "below the sweep", points: sweep, target: 40, want: 40},
		{name: "empty", target: 50},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := cqAt(testCase.points, testCase.target)
			assert.InDelta(t, testCase.want, got, 1e-9)
			assert.Equal(t, testCase.wantOK, ok)
		})
	}
}

func TestSuggest(
	t *testing.T,
) {
	codec, err := encode.LookupFor("h264", encode.HardwareNVENC)
	require.NoError(t, err)

	top := []sweepPoint{{cq: 15, vmaf: 99}, {cq: 25, vmaf: 89}, {cq: 35, vmaf: 79}}
	bottom := []sweepPoint{{cq: 15, vmaf: 70}, {cq: 25, vmaf: 50}, {cq: 35, vmaf: 30}}

	testCases := []struct {
		name        string
		top, bottom []sweepPoint
		want        string
	}{
		{name: "both crossed", top: top, bottom: bottom, want: "{17, 24, 30}"},
		{
			name: "targets outside the sweep", top: []sweepPoint{{cq: 15, vmaf: 90}, {cq: 45, vmaf: 60}},
			bottom: []sweepPoint{{cq: 15, vmaf: 80}, {cq: 45, vmaf: 50}},
			want:   "{15, 30, 45} (VMAF 97 not reached in the sweep; VMAF 40 not reached in the sweep)",
		},
		{
			name: "inconsistent", top: []sweepPoint{{cq: 15, vmaf: 100}, {cq: 35, vmaf: 96}},
			bottom: []sweepPoint{{cq: 15, vmaf: 45}, {cq: 25, vmaf: 35}}, want: "inconsistent sweep: keep the current CQs",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, suggest(codec, testCase.top, testCase.bottom))
		})
	}
}

func TestBitrateAt(
	t *testing.T,
) {
	hull := []ladder.HullPoint{{Bitrate: 1000, VMAF: 50}, {Bitrate: 4000, VMAF: 70}}

	testCases := []struct {
		name    string
		quality float64
		want    float64
		wantOK  bool
	}{
		{name: "first point", quality: 50, want: 1000, wantOK: true},
		{name: "below the envelope", quality: 40, want: 1000},
		{name: "log interpolation", quality: 60, want: 2000, wantOK: true},
		{name: "above the envelope", quality: 80},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := bitrateAt(hull, testCase.quality)
			assert.InDelta(t, testCase.want, got, 1e-6)
			assert.Equal(t, testCase.wantOK, ok)
		})
	}
}

func TestEqualQualityWithoutMatch(
	t *testing.T,
) {
	cpu := &ladder.Result{Rungs: []ladder.Rung{{Height: 360, Bitrate: 100_000, PredictedVMAF: 20}}}

	rows, mean := equalQuality(cpu, &ladder.Result{})
	assert.Equal(t, "n/a", mean)
	assert.Equal(t, "outside the NVENC envelope", rows[0][3])
}

func TestProbeList(
	t *testing.T,
) {
	probes := []ladder.Probe{{Height: 1080, CRF: 21, Bitrate: 5e6, VMAF: 95}, {Height: 360, CRF: 35, Bitrate: 2e5, VMAF: 41.25}}

	assert.Equal(t, "1080p CQ 21 → 5000 kb/s, 95.0, 360p CQ 35 → 200 kb/s, 41.2", probeList(probes))
}

func TestCompareFrames(
	t *testing.T,
) {
	a := &analysis.Comparison{VMAF: &quality.Result{Frames: []quality.FrameScore{{Index: 0, Score: 90}, {Index: 1, Score: 80}}}}
	b := &analysis.Comparison{VMAF: &quality.Result{Frames: []quality.FrameScore{{Index: 1, Score: 79}, {Index: 7, Score: 10}}}}

	d := compareFrames(a, b)
	assert.Equal(t, 1, d.frames)
	assert.InDelta(t, 1, d.maxAbs, 1e-12)
	assert.InDelta(t, -1, d.mean, 1e-12)

	assert.Zero(t, compareFrames(a, &analysis.Comparison{VMAF: &quality.Result{}}).frames)
}

func TestParseFrameMD5(
	t *testing.T,
) {
	out := "#format: frame checksums\n#version: 2\n0,          0,          0,        1,  3110400, 1a\n\n0, 1, 1, 1, 3110400, 2b\n"

	assert.Equal(t, []string{"1a", "2b"}, parseFrameMD5([]byte(out)))
	assert.Equal(t, 1, matching([]string{"1a", "2b", "3c"}, []string{"1a", "xx"}))
}

func TestExecRunner(
	t *testing.T,
) {
	out, err := execRunner{}.run(t.Context(), "echo", "gpu")
	require.NoError(t, err)
	assert.Equal(t, "gpu\n", string(out))
}

func TestHelpers(
	t *testing.T,
) {
	assert.Equal(t, "second", firstLine("\n  \n second \nthird"))
	assert.Empty(t, firstLine(""))
	assert.Equal(t, "a b c", oneLine("a\n b\tc "))
	assert.Equal(t, "{21, 28.5}", cqList([]float64{21, 28.5}))
	assert.Equal(t, [][]string{{"19", "1 kb/s", "90.0", "", ""}}, sweepRows([]sweepPoint{{cq: 19, bitrate: 1000, vmaf: 90}}, nil))
}
