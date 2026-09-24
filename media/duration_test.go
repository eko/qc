package media

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurationConversions(
	t *testing.T,
) {
	d := Seconds(1.5)

	assert.Equal(t, 1500*time.Millisecond, d.Std())
	assert.InDelta(t, 1.5, d.Seconds(), 1e-12)
	assert.Equal(t, "1.5s", d.String())
}

func TestDurationMarshalJSON(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		input Duration
		want  string
	}{
		{name: "zero", input: 0, want: "0"},
		{name: "whole seconds", input: Seconds(60), want: "60"},
		{name: "rounded to the microsecond", input: Duration(1234567891 * time.Nanosecond), want: "1.234568"},
		{name: "negative", input: Seconds(-0.5), want: "-0.5"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := json.Marshal(testCase.input)

			require.NoError(t, err)
			assert.JSONEq(t, testCase.want, string(got))
		})
	}
}

func TestDurationUnmarshalJSON(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    Duration
		wantErr bool
	}{
		{name: "seconds", input: "2.5", want: Seconds(2.5)},
		{name: "integer", input: "3", want: Seconds(3)},
		{name: "not a number", input: `"2s"`, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got Duration

			err := json.Unmarshal([]byte(testCase.input), &got)
			if testCase.wantErr {
				require.ErrorContains(t, err, "decode duration")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}
