package colorimetry

import "math"

// Transfer functions a Decoder knows (media.TransferPQ, media.TransferHLG;
// the names are ffmpeg's).
const (
	transferPQ  = "smpte2084"
	transferHLG = "arib-std-b67"
)

// Signal describes the Y′CbCr code values of decoded frames.
type Signal struct {
	// Transfer is "smpte2084" (PQ) or "arib-std-b67" (HLG).
	Transfer string
	// BitDepth is the depth of the samples, 8 to 16.
	BitDepth int
	// FullRange is set for full-range ("pc") samples; limited range is
	// the default of Y′CbCr video.
	FullRange bool
	// Peak is the display peak luminance HLG is rendered at, in cd/m²
	// (0: 1000, the BT.2100 reference). PQ ignores it: it is absolute.
	Peak float64
}

// lightSteps quantises non-linear R′G′B′ (0–1) for the light table: steps
// of 1/16384 are 16 times finer than 10-bit code values, well below the
// precision of the signal.
const lightSteps = 1 << 14

// defaultHLGPeak is the BT.2100 reference display of HLG, in cd/m².
const defaultHLGPeak = 1000

// Decoder turns code values into light through lookup tables built once:
// the quantisation of Y′CbCr, and the EOTF of each non-linear component.
// It is read-only after NewDecoder and safe for concurrent use.
type Decoder struct {
	luma, chroma []float32
	// light maps a quantised non-linear component to linear light: display
	// cd/m² for PQ, normalised scene light for HLG.
	light []float32
	// gain maps a quantised HLG scene luminance to its OOTF gain (nil for
	// PQ).
	gain []float32
	mask int
}

// NewDecoder builds the tables of a signal. An unknown transfer decodes as
// PQ.
func NewDecoder(
	s Signal,
) *Decoder {
	depth := min(max(s.BitDepth, 8), 16)
	codes := 1 << depth
	d := &Decoder{luma: make([]float32, codes), chroma: make([]float32, codes), mask: codes - 1}

	yOff, yScale, cOff, cScale := quantisation(depth, s.FullRange)
	for v := range codes {
		d.luma[v] = float32((float64(v) - yOff) / yScale)
		d.chroma[v] = float32((float64(v) - cOff) / cScale)
	}

	d.light = make([]float32, lightSteps+1)

	if s.Transfer == transferHLG {
		peak := s.Peak
		if peak <= 0 {
			peak = defaultHLGPeak
		}

		d.gain = make([]float32, lightSteps+1)
		for i := range d.light {
			e := float64(i) / lightSteps
			d.light[i] = float32(HLGInverseOETF(e))
			d.gain[i] = float32(HLGOOTFGain(e, peak))
		}

		return d
	}

	for i := range d.light {
		d.light[i] = float32(PQEOTF(float64(i) / lightSteps))
	}

	return d
}

// quantisation returns the offsets and scales mapping code values of depth
// bits to E′Y (0–1) and E′Cb, E′Cr (−0.5–0.5), after ITU-R BT.2100 Table 9.
func quantisation(
	depth int,
	full bool,
) (yOff, yScale, cOff, cScale float64) {
	if full {
		peak := float64(int(1)<<depth - 1)

		return 0, peak, float64(int(1) << (depth - 1)), peak
	}

	unit := float64(int(1) << (depth - 8))

	return 16 * unit, 219 * unit, 128 * unit, 224 * unit
}

// RGB converts one Y′CbCr sample into non-linear R′, G′, B′ with the
// BT.2020 non-constant luminance matrix, clamped to 0–1.
func (d *Decoder) RGB(
	y, cb, cr int,
) (r, g, b float32) {
	ey, ecb, ecr := d.luma[y&d.mask], d.chroma[cb&d.mask], d.chroma[cr&d.mask]

	r = ey + (2-2*KR)*ecr
	b = ey + (2-2*KB)*ecb
	g = (ey - KR*r - KB*b) / KG

	return clamp01(r), clamp01(g), clamp01(b)
}

// Light converts non-linear R′, G′, B′ (0–1) into display light in cd/m²:
// the PQ EOTF of each component, or for HLG the inverse OETF then the OOTF
// at the display peak.
func (d *Decoder) Light(
	r, g, b float32,
) (lr, lg, lb float32) {
	lr, lg, lb = d.light[quantise(r)], d.light[quantise(g)], d.light[quantise(b)]
	if d.gain == nil {
		return lr, lg, lb
	}

	k := d.gain[quantise(KR*lr+KG*lg+KB*lb)]

	return k * lr, k * lg, k * lb
}

// MaxLight is the display light of the brightest component of one Y′CbCr
// sample, in cd/m²: the per-pixel quantity behind CTA-861.3's MaxCLL and
// MaxFALL. PQ is monotonic, so the brightest non-linear component is the
// brightest in light and one lookup is enough.
func (d *Decoder) MaxLight(
	y, cb, cr int,
) float32 {
	r, g, b := d.RGB(y, cb, cr)
	if d.gain == nil {
		return d.light[quantise(max(r, g, b))]
	}

	lr, lg, lb := d.Light(r, g, b)

	return max(lr, lg, lb)
}

// PQBins is the resolution of the PQ code values MaxLights bins its points
// by: 10-bit steps.
const PQBins = 1024

// MaxLights computes MaxLight for every sample of ys, cbs and crs (of
// equal lengths) into nits, and the 10-bit PQ code value of each result
// into bins (0–PQBins-1). The PQ loop is written out: it runs on every
// grid point of every frame.
func (d *Decoder) MaxLights(
	ys, cbs, crs []uint16,
	nits []float32,
	bins []uint16,
	pq *PQTable,
) {
	if d.gain != nil {
		for i, y := range ys {
			nits[i] = d.MaxLight(int(y), int(cbs[i]), int(crs[i]))
			bins[i] = uint16(pq.Encode(nits[i])*(PQBins-1) + 0.5)
		}

		return
	}

	luma, chroma, light, mask := d.luma, d.chroma, d.light, d.mask
	cbs, crs, nits, bins = cbs[:len(ys)], crs[:len(ys)], nits[:len(ys)], bins[:len(ys)]

	for i, y := range ys {
		ey, ecb, ecr := luma[int(y)&mask], chroma[int(cbs[i])&mask], chroma[int(crs[i])&mask]
		r := ey + (2-2*KR)*ecr
		b := ey + (2-2*KB)*ecb
		g := (ey - KR*r - KB*b) / KG
		m := min(max(r, g, b, 0), 1)
		nits[i] = light[int(m*lightSteps+0.5)]
		bins[i] = uint16(m*(PQBins-1) + 0.5)
	}
}

// quantise maps a component in 0–1 to its table index, rounding.
func quantise(
	v float32,
) int {
	return int(v*lightSteps + 0.5)
}

func clamp01(
	v float32,
) float32 {
	return min(max(v, 0), 1)
}

// pqMantissaBits is the resolution of the inverse PQ table within each
// octave: 128 linear segments per octave keep its error below 2·10⁻⁶ in PQ
// units, 0.0015 ΔE ITP.
const pqMantissaBits = 7

// pqOctaves is the range of the inverse PQ table below 10 000 cd/m². PQ
// rises steeply from black (0.0006 cd/m² is still 0.0047), so the table
// goes down to 2⁻⁴⁴ of the peak, where the signal is within 2·10⁻⁶ of its
// value at zero.
const pqOctaves = 44

// PQTable is the inverse PQ EOTF as a table indexed by the exponent and top
// mantissa bits of a float32, interpolated linearly: the curve is close to
// a power law, so segments of constant relative width fit it evenly. It is
// read-only and safe for concurrent use.
type PQTable struct {
	values []float32
}

// NewPQTable builds the inverse PQ table.
func NewPQTable() *PQTable {
	per := 1 << pqMantissaBits
	values := make([]float32, pqOctaves*per+2)

	for i := range values {
		octave, step := i/per, i%per
		y := math.Ldexp(1+float64(step)/float64(per), octave-pqOctaves)
		values[i] = float32(PQInverseEOTF(y * PQPeak))
	}

	return &PQTable{values: values}
}

// Table layout of PQTable: values[i] is the inverse PQ of the normalised
// light whose float32 bits, shifted right by pqFracBits, are i + pqBase.
const (
	pqFracBits = 23 - pqMantissaBits
	pqFracMask = 1<<pqFracBits - 1
	pqBase     = (127 - pqOctaves) << pqMantissaBits
)

// pqFloor is the smallest normalised light of the table, 2⁻⁴⁴.
var pqFloor = float32(math.Ldexp(1, -pqOctaves))

// Encode returns the normalised PQ signal of light in cd/m². It is small
// enough to be inlined in the per-pixel loops that call it.
func (t *PQTable) Encode(
	nits float32,
) float32 {
	y := nits * (1 / PQPeak)
	if !(y >= pqFloor) { // also NaN
		y = pqFloor
	}

	bits := math.Float32bits(min(y, 1))
	idx := int(bits>>pqFracBits) - pqBase
	frac := float32(bits&pqFracMask) * (1.0 / (1 << pqFracBits))
	lo := t.values[idx]

	return lo + frac*(t.values[idx+1]-lo)
}

// ITP converts one Y′CbCr sample into the I, T, P coordinates of ITU-R
// BT.2124 (see the package function ITP), through the tables: R′G′B′,
// display light, LMS, then the inverse PQ of each LMS component.
func (d *Decoder) ITP(
	y, cb, cr int,
	pq *PQTable,
) (i, t, p float32) {
	var lr, lg, lb float32

	if d.gain == nil {
		// PQ, written out: it runs twice per point of every frame.
		ey, ecb, ecr := d.luma[y&d.mask], d.chroma[cb&d.mask], d.chroma[cr&d.mask]
		r := ey + (2-2*KR)*ecr
		b := ey + (2-2*KB)*ecb
		g := (ey - KR*r - KB*b) / KG
		lr, lg, lb = d.light[quantise(clamp01(r))], d.light[quantise(clamp01(g))], d.light[quantise(clamp01(b))]
	} else {
		lr, lg, lb = d.Light(d.RGB(y, cb, cr))
	}

	l := (1688*lr + 2146*lg + 262*lb) / 4096
	m := (683*lr + 2951*lg + 462*lb) / 4096
	s := (99*lr + 309*lg + 3688*lb) / 4096

	lp, mp, sp := pq.Encode(l), pq.Encode(m), pq.Encode(s)

	return (lp + mp) / 2, (6610*lp - 13613*mp + 7003*sp) / 8192, (17933*lp - 17390*mp - 543*sp) / 4096
}
