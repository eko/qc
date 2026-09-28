package findings

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJudge(
	t *testing.T,
) {
	testCases := []struct {
		name string
		list []Finding
		want Verdict
	}{
		{name: "no finding", want: Pass},
		{
			name: "notes and passed checks",
			list: []Finding{{Level: Info, Code: Fallback}, {Level: OK, Code: NoBanding}},
			want: Pass,
		},
		{
			name: "a warning",
			list: []Finding{{Level: OK, Code: NoBlackOrFrozen}, {Level: Warn, Code: PeakBitrate}},
			want: Attention,
		},
		{
			name: "a blocking warning after others",
			list: []Finding{{Level: Warn, Code: BlackSegments}, {Level: Warn, Code: TruePeakOver}},
			want: Fail,
		},
		{
			name: "loudness off target only asks for attention",
			list: []Finding{{Level: Warn, Code: LoudnessOffTarget}},
			want: Attention,
		},
		{
			name: "a blocking code only blocks as a warning",
			list: []Finding{{Level: Info, Code: SilentTrack}},
			want: Pass,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Judge(testCase.list))
		})
	}
}

func TestVerdictString(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		verdict Verdict
		want    string
	}{
		{name: "pass", verdict: Pass, want: "pass"},
		{name: "attention", verdict: Attention, want: "attention"},
		{name: "fail", verdict: Fail, want: "fail"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.verdict.String())
		})
	}
}

func TestBlocking(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		finding Finding
		want    bool
	}{
		{name: "silent track", finding: Finding{Level: Warn, Code: SilentTrack}, want: true},
		{name: "true peak over the ceiling", finding: Finding{Level: Warn, Code: TruePeakOver}, want: true},
		{name: "8-bit HDR", finding: Finding{Level: Warn, Code: HDRBitDepth}, want: true},
		{name: "no rung", finding: Finding{Level: Warn, Code: NoRungs}, want: true},
		{name: "black segments depend on intent", finding: Finding{Level: Warn, Code: BlackSegments}},
		{name: "loudness off target depends on the delivery", finding: Finding{Level: Warn, Code: LoudnessOffTarget}},
		{name: "a passed check", finding: Finding{Level: OK, Code: LoudnessOnTarget}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.finding.Blocking())
		})
	}
}
