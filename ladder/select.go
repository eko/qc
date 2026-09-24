package ladder

import (
	"math"
)

// Constraints shape the ladder. The zero value uses the defaults.
type Constraints struct {
	// TopVMAF is the quality of the top rung: above it viewers see no
	// difference while bitrate keeps growing. Default 95.
	TopVMAF float64 `json:"topVmaf"`
	// MinVMAF is the lowest acceptable rung quality. Default 30.
	MinVMAF float64 `json:"minVmaf"`
	// Step is the VMAF drop between rungs, about one just-noticeable
	// difference. Default 6.
	Step float64 `json:"step"`
	// MinRatio and MaxRatio bound the bitrate ratio of adjacent rungs.
	// Defaults 1.5 and 2.5.
	MinRatio float64 `json:"minRatio"`
	MaxRatio float64 `json:"maxRatio"`
	// MaxRungs caps the number of rungs. Default 8.
	MaxRungs int `json:"maxRungs"`
	// MinBitrate and MaxBitrate (bits/s) bound rung bitrates. Defaults
	// 145 kb/s (Apple's lowest rung) and unlimited.
	MinBitrate int64 `json:"minBitrate"`
	MaxBitrate int64 `json:"maxBitrate,omitempty"`
	// Rungs forces the number of rungs; their resolutions stay automatic.
	// 0 lets the ladder descend one quality step at a time (see Step).
	Rungs int `json:"rungs,omitempty"`
	// Resolutions forces the height of every rung, top first: one rung per
	// entry (a height may repeat). Only these resolutions are probed.
	Resolutions []int `json:"resolutions,omitempty"`
}

// WithDefaults fills unset fields.
func (c Constraints) WithDefaults() Constraints {
	if c.TopVMAF <= 0 {
		c.TopVMAF = 95
	}

	if c.MinVMAF <= 0 {
		c.MinVMAF = 30
	}

	if c.Step <= 0 {
		c.Step = 6
	}

	if c.MinRatio <= 1 {
		c.MinRatio = 1.5
	}

	if c.MaxRatio < c.MinRatio {
		c.MaxRatio = max(2.5, c.MinRatio)
	}

	if c.MaxRungs <= 0 {
		c.MaxRungs = 8
	}

	if c.MinBitrate <= 0 {
		c.MinBitrate = 145_000
	}

	return c
}

// Target is a planned rung: a bitrate and the resolution maximising quality
// at that bitrate.
type Target struct {
	Bitrate int64   `json:"bitrate"`
	Height  int     `json:"height"`
	VMAF    float64 `json:"vmaf"`
	// Fixed means the height was imposed: the rung must use that curve.
	Fixed bool `json:"fixed,omitempty"`
	// Extrapolated means the bitrate lies outside the probed range of the
	// rung's resolution: its prediction rests on an extrapolation.
	Extrapolated bool `json:"extrapolated,omitempty"`
}

// SelectRungs returns no rung when MaxBitrate is below the lowest probed
// bitrate: no quality can be predicted there.
//
// SelectRungs walks the envelope from the top rung down, one quality step at
// a time, within the bitrate ratio bounds. Resolutions never increase as
// bitrate decreases.
func SelectRungs(
	hull []HullPoint,
	c Constraints,
) []Target {
	c = c.WithDefaults()
	if len(hull) == 0 || (c.MaxBitrate > 0 && c.MaxBitrate < hull[0].Bitrate) {
		return nil
	}

	top := topRung(hull, c)
	rungs := []Target{top}

	for len(rungs) < c.MaxRungs {
		prev := rungs[len(rungs)-1]

		// The bitrate whose quality is one step below, kept within ratios.
		want := bitrateFor(hull, prev.VMAF-c.Step)
		lo, hi := float64(prev.Bitrate)/c.MaxRatio, float64(prev.Bitrate)/c.MinRatio
		b := math.Min(math.Max(want, lo), hi)

		if b < float64(c.MinBitrate) {
			break
		}

		p, ok := pointAt(hull, b)
		if !ok || p.VMAF < c.MinVMAF {
			break
		}

		p.Height = min(p.Height, prev.Height)
		rungs = append(rungs, Target{Bitrate: int64(math.Round(b)), Height: p.Height, VMAF: p.VMAF})
	}

	return rungs
}

// topRung is the cheapest envelope point reaching TopVMAF, or the best point
// when the title never reaches it, capped by MaxBitrate.
func topRung(
	hull []HullPoint,
	c Constraints,
) Target {
	best := hull[len(hull)-1]

	for _, p := range hull {
		if p.VMAF >= c.TopVMAF {
			best = p

			break
		}
	}

	if c.MaxBitrate > 0 && best.Bitrate > c.MaxBitrate {
		if p, ok := pointAt(hull, float64(c.MaxBitrate)); ok {
			best = p
			best.Bitrate = c.MaxBitrate
		}
	}

	return Target{Bitrate: best.Bitrate, Height: best.Height, VMAF: best.VMAF}
}

// bitrateFor returns the lowest envelope bitrate reaching vmaf (the envelope
// is not strictly monotone across resolution switches).
func bitrateFor(
	hull []HullPoint,
	vmaf float64,
) float64 {
	for i, p := range hull {
		if p.VMAF >= vmaf {
			if i == 0 {
				return float64(p.Bitrate)
			}

			q := hull[i-1]
			t := (vmaf - q.VMAF) / (p.VMAF - q.VMAF)

			return math.Exp(math.Log(float64(q.Bitrate)) + t*(math.Log(float64(p.Bitrate))-math.Log(float64(q.Bitrate))))
		}
	}

	return float64(hull[len(hull)-1].Bitrate)
}

// pointAt returns the envelope point closest to bitrate b (in log scale).
func pointAt(
	hull []HullPoint,
	b float64,
) (HullPoint, bool) {
	if b < float64(hull[0].Bitrate)*0.999 {
		return HullPoint{}, false
	}

	best, dist := hull[0], math.Inf(1)
	for _, p := range hull {
		if d := math.Abs(math.Log(float64(p.Bitrate)) - math.Log(b)); d < dist {
			best, dist = p, d
		}
	}

	return best, true
}
