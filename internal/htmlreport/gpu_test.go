package htmlreport

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/quality"
)

func TestGPUCards(
	t *testing.T,
) {
	v := sampleVMAF(quality.ModeSampled)
	assert.NotContains(t, cardLabels(comparisonCards(v)), "Hardware")

	testCases := []struct {
		name       string
		hwaccel    string
		wantVendor string
		wantDetail string
	}{
		{name: "NVDEC", hwaccel: "cuda", wantVendor: "NVIDIA", wantDetail: "NVDEC decoding (cuda)"},
		{name: "VideoToolbox", hwaccel: "videotoolbox", wantVendor: "Apple", wantDetail: "VideoToolbox decoding"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := sampleVMAF(quality.ModeSampled)
			v.HWAccel = testCase.hwaccel

			cards := comparisonCards(v)
			i := slices.Index(cardLabels(cards), "Hardware")
			require.GreaterOrEqual(t, i, 0)
			assert.Equal(t, testCase.wantVendor, cards[i].Value)
			assert.Equal(t, testCase.wantDetail, cards[i].Detail)
		})
	}
}

func TestCodecLabel(
	t *testing.T,
) {
	cpu, err := encode.Lookup("hevc")
	require.NoError(t, err)
	nvenc, err := encode.LookupFor("hevc", encode.HardwareNVENC)
	require.NoError(t, err)

	assert.Equal(t, "hevc", codecLabel(cpu))
	assert.Equal(t, "hevc · hevc_nvenc", codecLabel(nvenc))
}

func cardLabels(
	cards []card,
) []string {
	labels := make([]string, len(cards))
	for i, c := range cards {
		labels[i] = c.Label
	}

	return labels
}
