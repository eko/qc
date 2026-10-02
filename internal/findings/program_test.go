package findings

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/ladder"
)

// program is the verified ladder with every rung read on three videos:
// their VMAF on each rung, top first.
func program(
	vmaf ...[3]float64,
) *ladder.Result {
	r := verified()

	for i, row := range vmaf {
		for _, v := range row {
			r.Rungs[i].Measured.Titles = append(r.Rungs[i].Measured.Titles, ladder.TitleMeasurement{VMAF: v, ScoredFrames: 50})
		}
	}

	return r
}

func TestLadderProgram(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		result *ladder.Result
		mutate func(r *ladder.Result)
		want   []Finding
	}{
		{
			name:   "one title",
			result: verified(),
		},
		{
			name:   "videos alike",
			result: program([3]float64{94.2, 95.1, 96.5}, [3]float64{86, 88, 91.9}),
			want:   []Finding{{Level: OK, Code: ProgramEven, Value: 1.5, Limit: ProgramTolerance}},
		},
		{
			name:   "a video under-served, another over-served",
			result: program([3]float64{92.5, 95, 97.4}),
			want: []Finding{
				{Level: Warn, Code: TitleBelowProgram, Index: 0, Value: 92.5, Limit: 95},
				{Level: Info, Code: TitleAboveProgram, Index: 2, Value: 97.4, Limit: 95},
			},
		},
		{
			name:   "even at the top, apart below: the widest rung is told",
			result: program([3]float64{95, 95, 95}, [3]float64{92, 88, 85.5}, [3]float64{90, 83, 76}),
			want: []Finding{
				{Level: OK, Code: ProgramEven, Value: 0, Limit: ProgramTolerance},
				{Level: Info, Code: ProgramSpread, Index: 2, Other: 2, Value: 14, Limit: ProgramSpreadLimit},
			},
		},
		{
			name:   "a video without a scored frame is not judged",
			result: program([3]float64{80, 95, 95.5}, [3]float64{60, 88, 89}),
			mutate: func(r *ladder.Result) {
				r.Rungs[0].Measured.Titles[0].ScoredFrames = 0
				r.Rungs[1].Measured.Titles[0].ScoredFrames = 0
			},
			want: []Finding{{Level: OK, Code: ProgramEven, Value: 0.5, Limit: ProgramTolerance}},
		},
		{
			name:   "rungs not verified",
			result: program([3]float64{95, 95, 95}, [3]float64{92, 88, 70}),
			mutate: func(r *ladder.Result) { r.Rungs[1].Measured = nil },
			want:   []Finding{{Level: OK, Code: ProgramEven, Value: 0, Limit: ProgramTolerance}},
		},
		{
			name:   "top rung not verified",
			result: program([3]float64{80, 95, 99}),
			mutate: func(r *ladder.Result) { r.Rungs[0].Measured = nil },
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.mutate != nil {
				testCase.mutate(testCase.result)
			}

			got := programFindings(testCase.result)
			assert.Len(t, got, len(testCase.want))

			for i, want := range testCase.want {
				assert.Equal(t, want.Level, got[i].Level)
				assert.Equal(t, want.Code, got[i].Code)
				assert.Equal(t, want.Index, got[i].Index)
				assert.Equal(t, want.Other, got[i].Other)
				assert.InDelta(t, want.Value, got[i].Value, 1e-9)
				assert.InDelta(t, want.Limit, got[i].Limit, 1e-9)
			}

			// They are part of the findings of the ladder.
			for _, f := range got {
				assert.Contains(t, codes(Ladder(testCase.result)), [2]string{f.Level.String(), string(f.Code)})
			}
		})
	}
}

func TestSpread(
	t *testing.T,
) {
	titles := func(scored int, vmaf ...float64) *ladder.Measurement {
		m := &ladder.Measurement{}
		for _, v := range vmaf {
			m.Titles = append(m.Titles, ladder.TitleMeasurement{VMAF: v, ScoredFrames: scored})
		}

		return m
	}

	testCases := []struct {
		name     string
		in       *ladder.Measurement
		low      int
		high     int
		wantNone bool
	}{
		{name: "not measured", wantNone: true},
		{name: "one title", in: &ladder.Measurement{VMAF: 90}, wantNone: true},
		{name: "one video scored", in: func() *ladder.Measurement {
			m := titles(10, 80, 90)
			m.Titles[1].ScoredFrames = 0

			return m
		}(), wantNone: true},
		{name: "lowest and highest", in: titles(10, 88, 80, 91, 85), low: 1, high: 2},
		{name: "all equal", in: titles(10, 90, 90), low: 0, high: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			low, high, ok := Spread(testCase.in)
			assert.Equal(t, !testCase.wantNone, ok)

			if ok {
				assert.Equal(t, testCase.low, low)
				assert.Equal(t, testCase.high, high)
			}
		})
	}
}
