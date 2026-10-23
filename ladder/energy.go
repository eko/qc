package ladder

import (
	"math"
	"time"
)

// Energy follows CPU time: an encode is a process whose CPU time qc
// measures (internal/cputime), and the energy of a CPU core over that time
// is what a power figure per core makes of it (Options.CoreWatts). The
// encodes of the digest give the cost of each rung on the whole title, in
// proportion of the share of the title the digest holds.

// EncodeCost is what encoding the whole title with the settings of a rung
// costs.
type EncodeCost struct {
	// CPUHours is the CPU time of the encode, in core-hours: that of the
	// verification encode of the digest, scaled to the title.
	CPUHours float64 `json:"cpuHours"`
	// Estimated is set when the rung was not verified: its cost is then
	// that of the encodes of its resolution, at other CRFs and possibly at
	// a faster preset (Options.ProbePreset).
	Estimated bool `json:"estimated,omitempty"`
}

// Energy is what encoding the ladder on the whole title costs, and what
// Constraints.EnergyTolerance saved.
type Energy struct {
	// CPUHours is the CPU time of encoding every rung, in core-hours.
	CPUHours float64 `json:"cpuHours,omitempty"`
	// DecodeCPUHours is that of decoding the source of the whole title,
	// which every encode pays unless the rungs share one decode (one
	// ffmpeg with an output per rung): what the digest's extraction took,
	// scaled to the title.
	DecodeCPUHours float64 `json:"decodeCpuHours,omitempty"`
	// CoreWatts is the power of a core the energy is computed with
	// (Options.CoreWatts), and KWh that energy, the source decoded once;
	// both zero without it.
	CoreWatts float64 `json:"coreWatts,omitempty"`
	KWh       float64 `json:"kWh,omitempty"`
	// Tolerance is the VMAF a rung could lose to be encoded at a lower
	// resolution (Constraints.EnergyTolerance), Lowered the rungs it moved,
	// and SavedCPUHours what encoding them at the resolution of the
	// envelope would have cost more, from the encodes of the digest at
	// both resolutions.
	Tolerance     float64       `json:"tolerance,omitempty"`
	Lowered       []LoweredRung `json:"lowered,omitempty"`
	SavedCPUHours float64       `json:"savedCpuHours,omitempty"`
}

// LoweredRung is a rung the energy tolerance moved to a lower resolution,
// at the same bitrate.
type LoweredRung struct {
	// Rung is its index in Result.Rungs.
	Rung int `json:"rung"`
	// From is the height of the envelope at its bitrate, To the height it
	// is encoded at, and VMAFLost what the curves predict it loses.
	From     int     `json:"from"`
	To       int     `json:"to"`
	VMAFLost float64 `json:"vmafLost"`
}

// lowerForEnergy moves each target to the lowest resolution whose curve
// reaches, at the target's bitrate, its quality minus tolerance: fewer
// pixels to encode and decode, the bitrate unchanged (delivery costs
// energy too). Resolutions still never increase down the ladder; imposed
// ones stay.
func lowerForEnergy(
	targets []Target,
	curves []Curve,
	tolerance float64,
) ([]Target, []LoweredRung) {
	if tolerance <= 0 {
		return targets, nil
	}

	out := make([]Target, len(targets))
	ceiling := math.MaxInt

	var lowered []LoweredRung

	for i, t := range targets {
		best := t

		if !t.Fixed {
			best = lowest(t, curves, min(t.Height, ceiling), tolerance)
		}

		if best.Height < t.Height {
			lowered = append(lowered, LoweredRung{Rung: i, From: t.Height, To: best.Height, VMAFLost: max(0, t.VMAF-best.VMAF)})
		}

		out[i], ceiling = best, best.Height
	}

	return out, lowered
}

// lowest is target t at the lowest resolution, up to ceiling, whose curve
// reaches t's quality minus tolerance at its bitrate; when none does, at
// the best resolution up to ceiling. Its quality is read on that curve.
func lowest(
	t Target,
	curves []Curve,
	ceiling int,
	tolerance float64,
) Target {
	var lowestFit, best *Target

	for _, c := range curves {
		v, ok := c.VMAFAt(float64(t.Bitrate))
		if !ok || c.Height > ceiling {
			continue
		}

		candidate := t
		candidate.Height, candidate.VMAF = c.Height, v

		if v >= t.VMAF-tolerance && (lowestFit == nil || c.Height < lowestFit.Height) {
			lowestFit = &candidate
		}

		if best == nil || v > best.VMAF {
			best = &candidate
		}
	}

	switch {
	case lowestFit != nil:
		return *lowestFit
	case best != nil:
		return *best
	}

	return t
}

// energyOf is what encoding the rungs of res on the whole title costs,
// nil when nothing tells it: no rung, no digest share, no encode measured
// and no rung lowered.
func energyOf(
	res *Result,
	lowered []LoweredRung,
	tolerance, coreWatts float64,
) *Energy {
	share := res.Digest.Share
	heightCost := encodeCosts(res.Probes, res.Rungs)

	e := &Energy{Tolerance: tolerance, Lowered: lowered}

	if share > 0 {
		for i := range res.Rungs {
			r := &res.Rungs[i]

			switch {
			case r.Measured != nil && r.Measured.EncodeCPU > 0:
				r.Encode = &EncodeCost{CPUHours: titleHours(r.Measured.EncodeCPU, share)}
			case heightCost[r.Height] > 0:
				r.Encode = &EncodeCost{CPUHours: titleHours(heightCost[r.Height], share), Estimated: true}
			default:
				continue
			}

			e.CPUHours += r.Encode.CPUHours
		}

		// The encodes at the higher resolution ran at other bitrates, whose
		// CPU time differs: at the same bitrate, the cost of the rung grows
		// with its pixels.
		for _, l := range lowered {
			if r := res.Rungs[l.Rung]; r.Encode != nil && l.To > 0 {
				e.SavedCPUHours += r.Encode.CPUHours * (float64(l.From*l.From)/float64(l.To*l.To) - 1)
			}
		}
	}

	if e.CPUHours > 0 && share > 0 {
		e.DecodeCPUHours = titleHours(res.Digest.DecodeCPU, share)
	}

	if e.CPUHours > 0 && coreWatts > 0 {
		e.CoreWatts, e.KWh = coreWatts, (e.CPUHours+e.DecodeCPUHours)*coreWatts/wattsPerKilowatt
	}

	if e.CPUHours == 0 && len(lowered) == 0 {
		return nil
	}

	return e
}

const wattsPerKilowatt = 1000

// titleHours scales the CPU seconds of an encode of the digest to the
// whole title, in hours.
func titleHours(
	digestSeconds, share float64,
) float64 {
	return digestSeconds / share / time.Hour.Seconds()
}

// encodeCosts is the mean CPU time of the encodes of the digest at each
// height: its probes, and the verification of its rungs (a ladder model
// encodes few probes, the verification every rung).
func encodeCosts(
	probes []Probe,
	rungs []Rung,
) map[int]float64 {
	sums, counts := map[int]float64{}, map[int]int{}

	for _, p := range probes {
		if !p.Predicted && p.EncodeCPU > 0 {
			sums[p.Height] += p.EncodeCPU
			counts[p.Height]++
		}
	}

	for _, r := range rungs {
		if r.Measured != nil && r.Measured.EncodeCPU > 0 {
			sums[r.Height] += r.Measured.EncodeCPU
			counts[r.Height]++
		}
	}

	for h, n := range counts {
		sums[h] /= float64(n)
	}

	return sums
}
