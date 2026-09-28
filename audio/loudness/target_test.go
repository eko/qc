package loudness

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTarget(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    Target
		wantErr bool
	}{
		{name: "default", input: "", want: Target{Name: TargetEBU, Integrated: -23, Tolerance: 0.5, MaxTruePeak: -1}},
		{name: "ebu", input: " EBU ", want: Target{Name: TargetEBU, Integrated: -23, Tolerance: 0.5, MaxTruePeak: -1}},
		{name: "ebu live", input: "ebu-live", want: Target{Name: TargetEBULive, Integrated: -23, Tolerance: 1, MaxTruePeak: -1}},
		{name: "atsc", input: "atsc", want: Target{Name: TargetATSC, Integrated: -24, Tolerance: 2, MaxTruePeak: -2}},
		{name: "streaming", input: "streaming", want: Target{Name: TargetStreaming, Integrated: -16, Tolerance: 1, MaxTruePeak: -1}},
		{name: "streaming -14", input: "streaming-14", want: Target{Name: TargetStreamingLoud, Integrated: -14, Tolerance: 1, MaxTruePeak: -1}},
		{name: "custom", input: "-18", want: Target{Name: TargetCustom, Integrated: -18, Tolerance: 1, MaxTruePeak: -1}},
		{name: "custom with unit", input: "-18.5 LUFS", want: Target{Name: TargetCustom, Integrated: -18.5, Tolerance: 1, MaxTruePeak: -1}},
		{name: "unknown", input: "loud", wantErr: true},
		{name: "positive", input: "3", wantErr: true},
		{name: "below the gate", input: "-80", wantErr: true},
		{name: "not a number", input: "NaN", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseTarget(testCase.input)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrInvalidTarget)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestTargets(
	t *testing.T,
) {
	targets := Targets()
	require.Len(t, targets, 5)
	assert.Equal(t, DefaultTarget(), targets[0])

	targets[0].Name = "changed"
	assert.Equal(t, TargetEBU, DefaultTarget().Name)
}

func TestCheck(
	t *testing.T,
) {
	ebu := DefaultTarget()

	testCases := []struct {
		name   string
		result Result
		want   Compliance
		ok     bool
	}{
		{name: "on target", result: Result{Integrated: -23.2, TruePeak: -3}, want: Compliance{Deviation: -0.2, Loudness: true, TruePeak: true}, ok: true},
		{name: "on the bounds", result: Result{Integrated: -22.5, TruePeak: -1}, want: Compliance{Deviation: 0.5, Loudness: true, TruePeak: true}, ok: true},
		{name: "too loud, peaking", result: Result{Integrated: -16, TruePeak: 0.3}, want: Compliance{Deviation: 7}},
		{name: "too quiet", result: Result{Integrated: -30, TruePeak: -10}, want: Compliance{Deviation: -7, TruePeak: true}},
		{name: "silent", result: Result{Integrated: Floor, TruePeak: Floor}, want: Compliance{Deviation: Floor + 23, TruePeak: true}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ebu.Check(testCase.result)
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.ok, got.OK())
		})
	}
}
