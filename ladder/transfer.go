package ladder

import (
	"context"
	"math"
	"slices"
)

// StageAnchor encodes the anchors of probes made at a faster preset than
// the rungs' (Options.ProbePreset).
const StageAnchor = "anchor"

// Anchor is an encode of the digest at the delivery preset with the
// settings of a planned rung (the top or the bottom one), next to where the
// probes made at the probe preset reach the same quality: the two give the
// CRF offset and the bitrate ratio between the presets there, which move
// the probes onto the delivery preset (see anchorProbes).
type Anchor struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	CRF    float64 `json:"crf"`
	// VMAF, HalfWidth and Bitrate are measured at the delivery preset.
	VMAF      float64 `json:"vmaf"`
	HalfWidth float64 `json:"vmafHalfWidth"`
	Bitrate   int64   `json:"bitrate"`
	// ProbeCRF and ProbeBitrate are where the probe preset reaches the
	// same VMAF, interpolated between its probes.
	ProbeCRF     float64 `json:"probeCrf"`
	ProbeBitrate int64   `json:"probeBitrate"`
}

// transfers reports whether the probes are made at another preset than the
// rungs, and must be anchored.
func (b *build) transfers() bool {
	return b.opts.ProbePreset != b.opts.Preset
}

// anchorProbes places probes made at the probe preset on the delivery
// preset's curves: the rungs are planned on the probes once, then the top
// and the bottom rungs are encoded at the delivery preset (the anchors). A
// slower preset reaches a quality at a lower bitrate and another CRF: at
// each anchor's quality, the ratio of the bitrates and the difference of
// the CRFs between the presets move the probes (their quality kept), a
// constant bitrate saving across a curve like a BD-rate, and a CRF offset;
// resolutions between the anchors' move by the offsets interpolated in log
// height, those beyond them like the nearest anchor. The shape of the
// rate-quality envelope (which resolution when) carries over from a fast
// preset far better than the CRF scale does: at equal CRF, x264 veryfast
// scores 2–9 VMAF below fast (see docs/validation.md). Two anchors were as
// accurate as one per rung resolution in replays on two titles, for fewer
// encodes at the slow preset.
func (b *build) anchorProbes(
	ctx context.Context,
	probes []Probe,
) ([]Probe, error) {
	jobs := b.anchorJobs(probes)
	if len(jobs) == 0 {
		return probes, nil
	}

	b.resetProgress()

	measured, err := b.measureAll(ctx, jobs, StageAnchor, len(jobs), b.opts.Preset)
	if err != nil {
		return nil, err
	}

	// The probe preset's curves, as the rungs read them, give where it
	// reaches each anchor's quality.
	curves := b.curves(probes)
	anchors := make([]Anchor, len(measured))

	for i, m := range measured {
		c, _ := curveOf(curves, m.Height)
		bitrate, _ := c.BitrateFor(m.VMAF)
		anchors[i] = Anchor{
			Width: m.Width, Height: m.Height, CRF: m.CRF,
			VMAF: m.VMAF, HalfWidth: m.HalfWidth, Bitrate: m.Bitrate,
			ProbeCRF: c.CRFAt(bitrate), ProbeBitrate: int64(math.Round(bitrate)),
		}
	}

	b.probing.ProbePreset, b.probing.Anchors = b.opts.ProbePreset, anchors

	return shiftProbes(probes, anchors), nil
}

// anchorJobs are the anchor encodes: the top and the bottom rungs planned
// on probes (one when they share their resolution).
func (b *build) anchorJobs(
	probes []Probe,
) []Probe {
	curves := b.curves(probes)

	targets, err := PlanRungs(Envelope(curves, envelopePoints), curves, b.opts.Constraints)
	if err != nil || len(targets) == 0 {
		// Rung selection reports it on the probes themselves.
		return nil
	}

	jobs := make([]Probe, 0, 2)

	for _, t := range []Target{targets[0], targets[len(targets)-1]} {
		c := rungCurve(curves, t)
		if len(jobs) == 0 || jobs[0].Height != c.Height {
			jobs = append(jobs, Probe{Width: c.Width, Height: c.Height, CRF: b.roundCRF(c.CRFAt(float64(t.Bitrate)))})
		}
	}

	return jobs
}

// shiftProbes moves every probe by the CRF offset and bitrate ratio of the
// anchors (highest first): interpolated in log height between two anchors,
// those of the nearest one beyond them. Its quality is kept.
func shiftProbes(
	probes []Probe,
	anchors []Anchor,
) []Probe {
	out := slices.Clone(probes)
	top, bottom := anchors[0], anchors[len(anchors)-1]

	for i := range out {
		t := 0.0
		if top.Height != bottom.Height {
			t = math.Log(float64(top.Height)/float64(out[i].Height)) / math.Log(float64(top.Height)/float64(bottom.Height))
			t = min(max(t, 0), 1)
		}

		offset := (1-t)*(top.CRF-top.ProbeCRF) + t*(bottom.CRF-bottom.ProbeCRF)
		ratio := math.Exp((1-t)*logRatio(top) + t*logRatio(bottom))

		out[i].CRF += offset
		out[i].Bitrate = int64(math.Round(float64(out[i].Bitrate) * ratio))
	}

	return out
}

// logRatio is the log of the anchor's bitrate at the delivery preset over
// the probe preset's at the same quality.
func logRatio(
	a Anchor,
) float64 {
	return math.Log(float64(max(a.Bitrate, 1)) / float64(max(a.ProbeBitrate, 1)))
}
