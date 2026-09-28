package loudness_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/audiotest"
)

// TestConformance runs the synthetic cases of EBU Tech 3341 and 3342: every
// reading within the tolerance of the specification.
func TestConformance(
	t *testing.T,
) {
	testCases := append(audiotest.Tech3341(), audiotest.Tech3342()...)

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			meter := loudness.NewMeter(audiotest.Rate, testCase.Weights)
			require.NoError(t, meter.Add(testCase.Signal()))

			r := meter.Result()

			for _, check := range testCase.Checks {
				reading := check.Reading(r)
				t.Logf("%s %s: %.2f (want %g %s)", testCase.Name, check.Measure, reading, check.Want, check.Tolerance())
				assert.True(t, check.Pass(reading), "%s = %.3f, want %g %s", check.Measure, reading, check.Want, check.Tolerance())
			}
		})
	}
}
