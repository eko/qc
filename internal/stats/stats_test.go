package stats

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSummarize(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		want   Summary
	}{
		{name: "empty", values: nil, want: Summary{}},
		{
			name:   "single value",
			values: []float64{7},
			want:   Summary{Mean: 7, Min: 7, Max: 7, P5: 7, P50: 7, P95: 7},
		},
		{
			name:   "unsorted values",
			values: []float64{5, 1, 4, 2, 3},
			want:   Summary{Mean: 3, Min: 1, Max: 5, P5: 1, P50: 3, P95: 5},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Summarize(testCase.values))
		})
	}
}

func TestSummarizeDoesNotSortInput(
	t *testing.T,
) {
	values := []float64{3, 1, 2}

	Summarize(values)

	assert.Equal(t, []float64{3, 1, 2}, values)
}

func TestMean(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		values []float64
		want   float64
	}{
		{name: "empty", values: nil, want: 0},
		{name: "values", values: []float64{1, 2, 6}, want: 3},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, Mean(testCase.values), 1e-12)
		})
	}
}

func TestPercentile(
	t *testing.T,
) {
	sorted := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	testCases := []struct {
		name   string
		sorted []float64
		p      float64
		want   float64
	}{
		{name: "empty", sorted: nil, p: 0.5, want: 0},
		{name: "zero is the minimum", sorted: sorted, p: 0, want: 10},
		{name: "nearest rank median", sorted: sorted, p: 0.5, want: 50},
		{name: "nearest rank rounds up", sorted: sorted, p: 0.55, want: 60},
		{name: "one is the maximum", sorted: sorted, p: 1, want: 100},
		{name: "above one is clamped", sorted: sorted, p: 2, want: 100},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, Percentile(testCase.sorted, testCase.p), 1e-12)
		})
	}
}

func TestPercentileIntegers(
	t *testing.T,
) {
	assert.Equal(t, 3, Percentile([]int{1, 2, 3, 4}, 0.75))
}

func TestStdDev(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		sum, sumSq, n float64
		want          float64
	}{
		{name: "no sample", want: 0},
		{name: "constant series", sum: 6, sumSq: 12, n: 3, want: 0},
		// 2, 4, 4, 4, 5, 5, 7, 9: population standard deviation 2.
		{name: "textbook series", sum: 40, sumSq: 232, n: 8, want: 2},
		{name: "negative variance is clamped", sum: 3, sumSq: 2.999, n: 3, want: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, StdDev(testCase.sum, testCase.sumSq, testCase.n), 1e-9)
		})
	}
}
