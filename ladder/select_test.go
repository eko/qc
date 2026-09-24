package ladder

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstraintsWithDefaults(
	t *testing.T,
) {
	defaults := Constraints{
		TopVMAF: 95, MinVMAF: 30, Step: 6, MinRatio: 1.5, MaxRatio: 2.5,
		MaxRungs: 8, MinBitrate: 145_000,
	}

	testCases := []struct {
		name string
		in   Constraints
		want Constraints
	}{
		{name: "zero value", want: defaults},
		{
			name: "explicit values are kept",
			in:   Constraints{TopVMAF: 93, MinVMAF: 40, Step: 5, MinRatio: 1.6, MaxRatio: 2, MaxRungs: 5, MinBitrate: 200_000, MaxBitrate: 8e6},
			want: Constraints{TopVMAF: 93, MinVMAF: 40, Step: 5, MinRatio: 1.6, MaxRatio: 2, MaxRungs: 5, MinBitrate: 200_000, MaxBitrate: 8e6},
		},
		{
			name: "maximum ratio below a large minimum follows it",
			in:   Constraints{MinRatio: 3},
			want: func() Constraints { c := defaults; c.MinRatio, c.MaxRatio = 3, 3; return c }(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.in.WithDefaults())
		})
	}
}

func TestSelectRungsStops(
	t *testing.T,
) {
	hull := []HullPoint{
		{Bitrate: 1_000_000, VMAF: 60, Height: 360},
		{Bitrate: 2_000_000, VMAF: 80, Height: 720},
		{Bitrate: 4_000_000, VMAF: 96, Height: 1080},
	}

	testCases := []struct {
		name        string
		hull        []HullPoint
		constraints Constraints
		wantHeights []int
	}{
		{name: "empty envelope", constraints: Constraints{}},
		{name: "envelope runs out below", hull: hull, wantHeights: []int{1080, 720, 720, 360}},
		{name: "minimum quality", hull: hull, constraints: Constraints{MinVMAF: 70}, wantHeights: []int{1080, 720, 720}},
		{name: "minimum bitrate", hull: hull, constraints: Constraints{MinBitrate: 1_500_000}, wantHeights: []int{1080, 720, 720}},
		{name: "maximum rungs", hull: hull, constraints: Constraints{MaxRungs: 1}, wantHeights: []int{1080}},
		{name: "top quality never reached", hull: hull, constraints: Constraints{TopVMAF: 99, MaxRungs: 1}, wantHeights: []int{1080}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rungs := SelectRungs(testCase.hull, testCase.constraints)

			var heights []int
			for _, r := range rungs {
				heights = append(heights, r.Height)
			}

			assert.Equal(t, testCase.wantHeights, heights)
		})
	}
}

func TestBitrateFor(
	t *testing.T,
) {
	hull := []HullPoint{
		{Bitrate: 1_000_000, VMAF: 60},
		{Bitrate: 4_000_000, VMAF: 80},
	}

	testCases := []struct {
		name string
		vmaf float64
		want float64
	}{
		{name: "below the envelope: its lowest bitrate", vmaf: 50, want: 1_000_000},
		{name: "interpolated in log scale", vmaf: 70, want: 2_000_000},
		{name: "above the envelope: its highest bitrate", vmaf: 90, want: 4_000_000},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, bitrateFor(hull, testCase.vmaf), 1)
		})
	}
}

func TestPointAt(
	t *testing.T,
) {
	hull := []HullPoint{
		{Bitrate: 1_000_000, VMAF: 60},
		{Bitrate: 4_000_000, VMAF: 80},
	}

	testCases := []struct {
		name    string
		bitrate float64
		want    HullPoint
		wantOK  bool
	}{
		{name: "below the envelope", bitrate: 900_000},
		{name: "within rounding of the lowest point", bitrate: 999_500, want: hull[0], wantOK: true},
		{name: "nearest in log scale", bitrate: 2_100_000, want: hull[1], wantOK: true},
		{name: "above the envelope", bitrate: 10_000_000, want: hull[1], wantOK: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := pointAt(hull, testCase.bitrate)
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.want, got)
		})
	}
}
