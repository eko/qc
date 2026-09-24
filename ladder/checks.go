package ladder

import (
	"github.com/eko/qc/quality"
)

// bandingLimitedShare is the share of banded frames from which a rung is
// reported as banding-limited: isolated frames over the CAMBI threshold
// (a dark fade, a sky) do not make a rendition banded.
const bandingLimitedShare = 0.05

// Margins beyond which VMAF and XPSNR are considered to order two rungs
// differently: well above the sampling noise of either measurement (a rung
// is scored at about ±1 VMAF), so only real disagreements are reported.
const (
	rankVMAFMargin  = 2.0 // VMAF points
	rankXPSNRMargin = 0.5 // dB
)

// RankConflict is a pair of verified rungs that VMAF and XPSNR order
// differently: VMAF scores Higher clearly above Lower while XPSNR scores it
// clearly below. It usually marks a resolution trade-off VMAF rewards more
// than a pixel-fidelity metric (upscaled low resolutions, sharpening), and
// is worth a look before trusting the ladder's order.
type RankConflict struct {
	// Higher and Lower index Result.Rungs.
	Higher, Lower int
	// VMAF and XPSNR are the scores of Higher then Lower.
	VMAF  [2]float64
	XPSNR [2]float64
}

// RankConflicts returns the pairs of verified rungs whose VMAF and XPSNR
// (luma) orders disagree beyond noise. It needs XPSNR on the rungs
// (Options.Metrics).
func RankConflicts(
	rungs []Rung,
) []RankConflict {
	var out []RankConflict

	for i := range rungs {
		for j := range rungs {
			a, b := rungs[i].Measured, rungs[j].Measured
			if i == j || a == nil || b == nil {
				continue
			}

			xa, okA := a.Metrics[quality.SeriesXPSNRY]
			xb, okB := b.Metrics[quality.SeriesXPSNRY]

			if okA && okB && a.VMAF-b.VMAF >= rankVMAFMargin && xb-xa >= rankXPSNRMargin {
				out = append(out, RankConflict{
					Higher: i, Lower: j,
					VMAF: [2]float64{a.VMAF, b.VMAF}, XPSNR: [2]float64{xa, xb},
				})
			}
		}
	}

	return out
}

// BandedRungs returns the indices of the verified rungs with visible banding
// (CAMBI above quality.BandingThreshold) on at least 5% of their scored
// frames: their quality is limited by banding, which more bits at the same
// bit depth fix poorly and 10-bit encodes fix better.
func BandedRungs(
	rungs []Rung,
) []int {
	var out []int

	for i, r := range rungs {
		if r.Measured != nil && r.Measured.BandedShare() >= bandingLimitedShare {
			out = append(out, i)
		}
	}

	return out
}
