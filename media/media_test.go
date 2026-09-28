package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRational(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    Rational
		wantErr bool
	}{
		{name: "empty", input: "", want: Rational{}},
		{name: "fraction", input: "30000/1001", want: Rational{Num: 30000, Den: 1001}},
		{name: "integer", input: "25", want: Rational{Num: 25, Den: 1}},
		{name: "zero denominator", input: "0/0", want: Rational{}},
		{name: "invalid numerator", input: "a/1", wantErr: true},
		{name: "invalid denominator", input: "1/b", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseRational(testCase.input)
			if testCase.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.input)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestRational(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		input      Rational
		wantFloat  float64
		wantString string
	}{
		{name: "ntsc", input: Rational{Num: 30000, Den: 1001}, wantFloat: 30000.0 / 1001, wantString: "30000/1001"},
		{name: "undefined", input: Rational{}, wantFloat: 0, wantString: "0/0"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.wantFloat, testCase.input.Float(), 1e-12)
			assert.Equal(t, testCase.wantString, testCase.input.String())
		})
	}
}

func TestInfoPrimaryVideo(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		info   Info
		want   VideoStream
		wantOK bool
	}{
		{name: "no video", info: Info{}, want: VideoStream{}, wantOK: false},
		{
			name:   "first stream",
			info:   Info{Video: []VideoStream{{Index: 1}, {Index: 2}}},
			want:   VideoStream{Index: 1},
			wantOK: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := testCase.info.PrimaryVideo()

			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestIntervalLength(
	t *testing.T,
) {
	interval := Interval{Start: Seconds(1.5), End: Seconds(4)}

	assert.Equal(t, Seconds(2.5), interval.Length())
}

func TestLevelsFor(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		colorRange string
		want       Levels
	}{
		{name: "pc", colorRange: "pc", want: Levels{Black: 0, White: 255}},
		{name: "jpeg", colorRange: "jpeg", want: Levels{Black: 0, White: 255}},
		{name: "tv", colorRange: "tv", want: Levels{Black: 16, White: 235}},
		{name: "unknown defaults to limited", colorRange: "", want: Levels{Black: 16, White: 235}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, LevelsFor(testCase.colorRange))
		})
	}
}

func TestDefaultAudio(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		audio []AudioStream
		want  int
	}{
		{name: "no audio", want: -1},
		{name: "no default flag: the first", audio: []AudioStream{{Index: 1}, {Index: 2}}, want: 0},
		{name: "flagged", audio: []AudioStream{{Index: 1}, {Index: 2, Default: true}}, want: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			info := Info{Audio: testCase.audio}
			assert.Equal(t, testCase.want, info.DefaultAudio())
		})
	}
}
