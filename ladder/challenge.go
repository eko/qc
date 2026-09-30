package ladder

import (
	"context"
	"slices"
)

// maxChallengeRounds bounds the rounds of challenger probes.
const maxChallengeRounds = 5

// challenge probes the resolutions the rungs could not compare with: a rung
// can only take a resolution whose probes reach its bitrate, so when a
// higher resolution was never probed that low while it still beats the
// rung's at its own lowest probe, the crossover between them lies
// below that probe, unknown, and the rung falls to the lower resolution by
// default. Content that compresses well hits this: an AV1 title whose
// 1080p probes stopped at 1 Mb/s got 720p, 540p and 360p rungs at 1 Mb/s,
// 600 and 400 kb/s where 1080p and 720p were 30–78% cheaper (see
// docs/validation.md). Each such rung gets challengers: the higher
// resolutions probed at the rung's bitrate (their CRF extrapolated along
// their curve), and the rungs are planned again, for up to
// maxChallengeRounds.
func (b *build) challenge(
	ctx context.Context,
	probes []Probe,
) ([]Probe, error) {
	for range maxChallengeRounds {
		jobs := b.challengers(probes)
		if len(jobs) == 0 {
			break
		}

		b.probing.Rounds++
		b.probing.Challengers += len(jobs)
		b.resetProgress()

		measured, err := b.measureAll(ctx, jobs, StageProbe, len(jobs), b.opts.ProbePreset)
		if err != nil {
			return nil, err
		}

		probes = append(probes, measured...)
	}

	return probes, nil
}

// challengers lists the challenger probes of the rungs planned on probes
// (see challenge), once per resolution and CRF, none already probed.
func (b *build) challengers(
	probes []Probe,
) []Probe {
	curves := b.curves(probes)

	targets, err := PlanRungs(Envelope(curves, envelopePoints), curves, b.opts.Constraints)
	if err != nil {
		return nil
	}

	var jobs []Probe

	for _, t := range targets {
		for _, job := range b.challengersOf(curves, rungCurve(curves, t), float64(t.Bitrate)) {
			job, ok := unprobed(probes, job, b.codec.Step(), b.codec.MaxCRF)
			if ok && !slices.ContainsFunc(jobs, sameProbe(job)) {
				jobs = append(jobs, job)
			}
		}
	}

	return jobs
}

// challengers of a rung at bitrate on curve chosen: every higher
// resolution whose probes stop above bitrate while it beats chosen at its
// lowest probe, and whose curve, even extended, beats chosen at bitrate.
// Every such resolution is challenged at once: challenging only the
// nearest one climbs a resolution a round (270p, then 360p, then 540p)
// and ran out of rounds on a real title.
func (b *build) challengersOf(
	curves []Curve,
	chosen Curve,
	bitrate float64,
) []Probe {
	var out []Probe

	for _, higher := range curves {
		if higher.Height > chosen.Height && b.challenges(higher, chosen, bitrate) {
			out = append(out, Probe{Width: higher.Width, Height: higher.Height, CRF: b.roundCRF(higher.CRFAt(bitrate))})
		}
	}

	return out
}

// challenges reports whether curve higher, whose probes stop above bitrate,
// may beat curve chosen there.
func (b *build) challenges(
	higher, chosen Curve,
	bitrate float64,
) bool {
	lowest, _ := higher.Range()
	if bitrate >= lowest {
		// The envelope already compared the two there.
		return false
	}

	top, _ := higher.VMAFAt(lowest)
	if own, ok := chosen.VMAFAt(lowest); ok && own >= top {
		// The crossover lies within the probes: the rung's resolution wins
		// below it.
		return false
	}

	// Extended along its lowest segment, the higher curve is optimistic (a
	// rate-quality curve falls faster as the bitrate drops): when even so
	// it does not beat the rung's resolution at the rung's bitrate, the
	// crossover lies above the rung.
	own, ok := chosen.VMAFAt(bitrate)

	return !ok || higher.extendedVMAF(bitrate) > own
}

// unprobed is job, or job at the next CRFs (by step, up to maxCRF) when
// its CRF was already probed at its resolution: a rung's bitrate can round
// to the CRF of the probe just above it, and the next CRF is the one that
// reaches below. It is false when every such CRF was probed.
func unprobed(
	probes []Probe,
	job Probe,
	step, maxCRF float64,
) (Probe, bool) {
	for slices.ContainsFunc(probes, sameProbe(job)) {
		if job.CRF+step > maxCRF {
			return Probe{}, false
		}

		job.CRF += step
	}

	return job, true
}

// sameProbe matches the probes at job's resolution and CRF.
func sameProbe(
	job Probe,
) func(Probe) bool {
	return func(p Probe) bool { return p.Height == job.Height && p.CRF == job.CRF }
}
