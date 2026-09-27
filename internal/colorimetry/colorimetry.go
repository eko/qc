// Package colorimetry holds the colour science of ITU-R BT.2100 HDR signals
// shared by the light level analyzer and the HDR quality metrics: the PQ
// (SMPTE ST 2084) and HLG (ARIB STD-B67) transfer functions, the BT.2020
// non-constant-luminance Y′CbCr matrix, the quantisation of code values and
// the ICtCp representation behind ΔE ITP (ITU-R BT.2124).
//
// Per-pixel work goes through lookup tables built once (Decoder, PQTable):
// hot loops index slices, they never call math.Pow.
package colorimetry

import "math"

// PQ constants of SMPTE ST 2084 (ITU-R BT.2100 Table 4).
const (
	pqM1 = 2610.0 / 16384
	pqM2 = 2523.0 / 4096 * 128
	pqC1 = 3424.0 / 4096
	pqC2 = 2413.0 / 4096 * 32
	pqC3 = 2392.0 / 4096 * 32
	// PQPeak is the luminance of the PQ signal 1.0, in cd/m².
	PQPeak = 10000.0
)

// HLG constants of ARIB STD-B67 (ITU-R BT.2100 Table 5).
const (
	hlgA = 0.17883277
	hlgB = 1 - 4*hlgA
	// hlgGamma is the system gamma of the HLG OOTF at the nominal 1000
	// cd/m² display.
	hlgGamma = 1.2
)

// Luma coefficients of ITU-R BT.2020 (non-constant luminance), shared by
// BT.2100's Y′CbCr.
const (
	KR = 0.2627
	KB = 0.0593
	KG = 1 - KR - KB
)

// PQEOTF converts a normalised PQ signal (0–1) into display light in cd/m².
func PQEOTF(
	e float64,
) float64 {
	e = min(max(e, 0), 1)
	p := math.Pow(e, 1/pqM2)

	return PQPeak * math.Pow(max(p-pqC1, 0)/(pqC2-pqC3*p), 1/pqM1)
}

// PQInverseEOTF converts display light in cd/m² into a normalised PQ
// signal.
func PQInverseEOTF(
	nits float64,
) float64 {
	y := math.Pow(min(max(nits/PQPeak, 0), 1), pqM1)

	return math.Pow((pqC1+pqC2*y)/(1+pqC3*y), pqM2)
}

// HLGInverseOETF converts a normalised HLG signal (0–1) into normalised
// scene light (0–1).
func HLGInverseOETF(
	e float64,
) float64 {
	e = min(max(e, 0), 1)
	if e <= 0.5 {
		return e * e / 3
	}

	return (math.Exp((e-(0.5-hlgA*math.Log(4*hlgA)))/hlgA) + hlgB) / 12
}

// HLGOOTFGain is the factor the HLG OOTF applies to the scene light of a
// pixel of scene luminance ys (0–1) on a display of peak cd/m²: display
// light is gain × scene light (BT.2100 Table 5, γ = 1.2 at 1000 cd/m²).
func HLGOOTFGain(
	ys, peak float64,
) float64 {
	return peak * math.Pow(max(ys, 0), hlgGamma-1)
}

// ITP converts display light in cd/m² (BT.2100 linear R, G, B) into the
// I, T, P coordinates of ITU-R BT.2124 (T = CT/2). ΔE ITP is 720 times the
// Euclidean distance between two ITP triplets.
func ITP(
	r, g, b float64,
) (i, t, p float64) {
	l, m, s := LMS(r, g, b)

	return ITPFromPQ(PQInverseEOTF(l), PQInverseEOTF(m), PQInverseEOTF(s))
}

// LMS converts BT.2100 linear R, G, B into linear L, M, S (BT.2100 Table 7).
func LMS(
	r, g, b float64,
) (l, m, s float64) {
	l = (1688*r + 2146*g + 262*b) / 4096
	m = (683*r + 2951*g + 462*b) / 4096
	s = (99*r + 309*g + 3688*b) / 4096

	return l, m, s
}

// ITPFromPQ converts PQ-encoded L′, M′, S′ into I, T, P.
func ITPFromPQ(
	l, m, s float64,
) (i, t, p float64) {
	i = (l + m) / 2
	t = (6610*l - 13613*m + 7003*s) / 4096 / 2
	p = (17933*l - 17390*m - 543*s) / 4096

	return i, t, p
}

// DeltaEITPScale is BT.2124's scale of ITP distances: ΔE ITP = 1 is a just
// noticeable difference in the most sensitive adaptation state.
const DeltaEITPScale = 720
