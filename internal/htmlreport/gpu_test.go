package htmlreport

import (
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
	assert.NotContains(t, cardLabels(comparisonCards(v)), "GPU")

	v.HWAccel = "cuda"
	cards := comparisonCards(v)
	assert.Contains(t, cardLabels(cards), "GPU")
	assert.Equal(t, "NVDEC decoding (cuda)", cards[len(cards)-1].Detail)
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
