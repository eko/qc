package ladder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/eko/qc/encode"
)

// StageLevel measures the quality level of the probes: one encode scored
// both like a probe and exactly (see Level).
const StageLevel = "level"

// Level is the correction of the probes' quality level. Every probe and
// rung is scored on the same sampled frames of the digest (common random
// numbers), which makes the differences between them precise but shares
// the error of those frames: frames harder than the digest's average put
// every measurement below its exact value by the same amount, up to about
// 1 VMAF. At the top of a rate-quality curve, which is flat, 1 VMAF is
// 25–30% of bitrate. The top rung planned on the probes is encoded once
// and scored both ways; the difference, Offset, is added to every probe
// and to every sampled rung measurement.
type Level struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	CRF    float64 `json:"crf"`
	// Sampled is the encode's VMAF on the probes' sampled frames, Exact on
	// every frame of the digest.
	Sampled float64 `json:"sampled"`
	Exact   float64 `json:"exact"`
	// Offset is Exact − Sampled.
	Offset float64 `json:"offset"`
}

// TopCalibrationTolerance is CalibrationTolerance for the top rung, which
// is verified on every frame of the digest: it is the quality the ladder
// promises, and the most expensive rung.
const TopCalibrationTolerance = 0.5

// levelProbes corrects the quality level of probes (see Level): the top
// rung planned on them, at the rungs' preset, is scored like a probe and
// exactly, and every probe moves by the difference.
func (b *build) levelProbes(
	ctx context.Context,
	probes []Probe,
) ([]Probe, error) {
	job, ok := b.levelJob(probes)
	if !ok {
		return probes, nil
	}

	b.reportProgress(Progress{Stage: StageLevel})

	sampled, exact, err := b.measureTwice(ctx, job)
	if err != nil {
		return nil, fmt.Errorf("ladder: level: %w", err)
	}

	level := &Level{
		Width: job.Width, Height: job.Height, CRF: job.CRF,
		Sampled: sampled.VMAF, Exact: exact.VMAF, Offset: exact.VMAF - sampled.VMAF,
	}
	b.level, b.probing.Level = level.Offset, level

	out := slices.Clone(probes)
	for i := range out {
		out[i].VMAF += level.Offset
	}

	return out, nil
}

// levelJob is the top rung planned on probes, or false when none is.
func (b *build) levelJob(
	probes []Probe,
) (Probe, bool) {
	curves := b.curves(probes)

	targets, err := PlanRungs(Envelope(curves, envelopePoints), curves, b.opts.Constraints)
	if err != nil || len(targets) == 0 {
		// Rung selection reports it on the probes themselves.
		return Probe{}, false
	}

	c := rungCurve(curves, targets[0])

	return Probe{Width: c.Width, Height: c.Height, CRF: b.roundCRF(c.CRFAt(float64(targets[0].Bitrate)))}, true
}

// measureTwice encodes the digest with p at the rungs' preset and scores
// it like a probe, then on every frame.
func (b *build) measureTwice(
	ctx context.Context,
	p Probe,
) (Measurement, Measurement, error) {
	path := filepath.Join(b.workDir, "level.mp4")
	defer os.Remove(path)

	if err := b.engine.encoder.Encode(ctx, b.codec, b.digest, path, b.params(p, encode.Params{})); err != nil {
		return Measurement{}, Measurement{}, fmt.Errorf("encode: %w", err)
	}

	sampled, err := b.scoreFile(ctx, path, scoreProbe)
	if err != nil {
		return Measurement{}, Measurement{}, err
	}

	exact, err := b.scoreFile(ctx, path, scoreExact)
	if err != nil {
		return Measurement{}, Measurement{}, err
	}

	return sampled, exact, nil
}

// leveled corrects a sampled measurement by the level offset; exact
// measurements need none.
func (b *build) leveled(
	m Measurement,
	mode scoring,
) Measurement {
	if mode == scoreRung || mode == scoreProbe {
		m.VMAF += b.level
	}

	return m
}

// calibrationTolerance is the gap between prediction and verification
// beyond which rung i is corrected.
func calibrationTolerance(
	i int,
) float64 {
	if i == 0 {
		return TopCalibrationTolerance
	}

	return CalibrationTolerance
}
