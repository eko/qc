package ladder

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/encode"
)

// FilmGrainAuto asks the engine to detect grain on the digest and pick the
// film grain synthesis level (Options.FilmGrain).
const FilmGrainAuto = -1

// maxFilmGrain is SVT-AV1's highest film grain level.
const maxFilmGrain = 50

// ErrFilmGrain is returned for film grain settings that cannot apply.
var ErrFilmGrain = errors.New("invalid film grain setting")

// Grain synthesis settings.
const (
	// grainThreshold is the noise standard deviation (8-bit code values,
	// digest luma, flattest blocks) above which a title is grainy: clean
	// digital sources measured 0.2–0.5, light synthetic grain 2.7.
	grainThreshold = 1.0
	// referenceCRF encodes the denoised reference: near lossless, so the
	// reference is SVT-AV1's own denoised picture and not a coded one.
	referenceCRF = 4
	// grainTolerance is the relative gap between the output's noise and the
	// source's beyond which a rung's grain is flagged.
	grainTolerance = 0.3
)

// calibrationLevels are the film grain levels tried by auto mode: SVT-AV1's
// level is a denoising strength, and how much grain the decoder puts back
// depends on the content (on synthetic grain of σ 2.7 and 5.8, level 30 gave
// back 89% and 52% of it), so the level is chosen per title.
var calibrationLevels = []int{10, 25, maxFilmGrain}

// GrainReport describes the film grain handling of an AV1 ladder.
type GrainReport struct {
	// Source is the noise of the digest at the source resolution.
	Source grain.Stats `json:"source"`
	// Detected reports whether the digest is grainy (auto mode).
	Detected bool `json:"detected"`
	// Level is the film grain synthesis level used (0: off).
	Level int `json:"level"`
	// Trials are the levels tried by auto mode, with the noise the decoder
	// put back relative to the source's.
	Trials []GrainTrial `json:"trials,omitempty"`
}

// GrainTrial is one film grain level tried on the digest.
type GrainTrial struct {
	Level int     `json:"level"`
	Ratio float64 `json:"ratio"`
}

// GrainCheck compares the grain of a rung, decoded as viewers see it (grain
// synthesised), with the source's at the rung's resolution.
type GrainCheck struct {
	Source grain.Stats `json:"source"`
	Output grain.Stats `json:"output"`
	// Ratio is the output's noise standard deviation over the source's.
	Ratio float64 `json:"ratio"`
	// OK reports whether Ratio is within grainTolerance of 1.
	OK bool `json:"ok"`
}

// grainOptions checks the film grain settings and returns them for codec:
// film grain synthesis is an AV1 tool, so other codecs ignore it (one
// setting serves a multi-codec run).
func grainOptions(
	opts Options,
	codec encode.Codec,
) (Options, error) {
	switch {
	case opts.FilmGrain < FilmGrainAuto || opts.FilmGrain > maxFilmGrain:
		return opts, fmt.Errorf("%w: level %d (auto, off or 1–%d)", ErrFilmGrain, opts.FilmGrain, maxFilmGrain)
	case codec.Name != "av1":
		opts.FilmGrain = 0
	case opts.FilmGrain != 0 && opts.PerShot:
		return opts, fmt.Errorf("%w: film grain synthesis and per-shot encoding cannot be combined", ErrFilmGrain)
	}

	return opts, nil
}

// noiseStep spreads the noise measurement's frames over the digest.
func (b *build) noiseStep(
	digest Digest,
) int {
	frames := digest.Duration.Seconds() * b.video.AvgFrameRate.Float()

	return max(1, int(frames/grain.SampleFrames))
}

// prepareGrain measures the digest's noise, settles the film grain level
// (detection and calibration in auto mode) and, when grain is synthesised,
// builds the denoised reference every measurement is scored against.
func (b *build) prepareGrain(
	ctx context.Context,
	digest Digest,
) (*GrainReport, error) {
	b.noiseEvery = b.noiseStep(digest)

	source, err := b.engine.grainLab.Noise(ctx, b.digest, b.video.Width, b.video.Height, b.noiseEvery)
	if err != nil {
		return nil, fmt.Errorf("ladder: grain: %w", err)
	}

	report := &GrainReport{Source: source, Detected: source.Sigma >= grainThreshold, Level: b.opts.FilmGrain}

	if b.opts.FilmGrain == FilmGrainAuto {
		report.Level = 0

		if report.Detected {
			if report.Level, report.Trials, err = b.calibrateGrain(ctx, source); err != nil {
				return nil, err
			}
		}
	}

	if report.Level > 0 {
		if err := b.denoisedReference(ctx, report.Level); err != nil {
			return nil, err
		}
	}

	b.grain = report.Level

	return report, nil
}

// calibrateGrain encodes the digest at its resolution and the middle probe
// CRF with each calibration level (opts.Parallel at a time), and keeps the
// level whose synthesised grain comes closest to the source's.
func (b *build) calibrateGrain(
	ctx context.Context,
	source grain.Stats,
) (int, []GrainTrial, error) {
	crf := b.codec.ProbeCRFs[len(b.codec.ProbeCRFs)/2]
	trials := make([]GrainTrial, len(calibrationLevels))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(b.opts.Parallel)

	for i, level := range calibrationLevels {
		group.Go(func() error {
			path := filepath.Join(b.workDir, fmt.Sprintf("grain-%d.mp4", level))
			params := b.params(Probe{Width: b.video.Width, Height: b.video.Height, CRF: crf}, encode.Params{})
			params.FilmGrain = level

			out, err := b.encodeNoise(gctx, path, params)
			if err != nil {
				return err
			}

			trials[i] = GrainTrial{Level: level, Ratio: out.Sigma / math.Max(source.Sigma, 1e-9)}

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return 0, nil, err
	}

	best, bestGap := 0, math.Inf(1)

	for _, t := range trials {
		if gap := math.Abs(t.Ratio - 1); gap < bestGap {
			best, bestGap = t.Level, gap
		}
	}

	return best, trials, nil
}

// encodeNoise encodes the digest with params and measures the noise of the
// result as decoded (grain synthesised), at the encode's resolution.
func (b *build) encodeNoise(
	ctx context.Context,
	path string,
	params encode.Params,
) (grain.Stats, error) {
	defer os.Remove(path)

	if err := b.engine.encoder.Encode(ctx, b.codec, b.digest, path, params); err != nil {
		return grain.Stats{}, fmt.Errorf("ladder: grain: %w", err)
	}

	stats, err := b.engine.grainLab.Noise(ctx, path, params.Width, params.Height, b.noiseEvery)
	if err != nil {
		return grain.Stats{}, fmt.Errorf("ladder: grain: %w", err)
	}

	return stats, nil
}

// denoisedReference encodes the digest near losslessly with film grain
// level, decodes it without the grain and makes that the reference of every
// measurement: SVT-AV1 codes its own denoised picture, so the fidelity of
// the coded signal is what VMAF measures against it. Scored against the
// grainy source, synthesised grain would be penalised for not matching the
// source's grain sample by sample.
func (b *build) denoisedReference(
	ctx context.Context,
	level int,
) error {
	encoded := filepath.Join(b.workDir, "reference.mp4")
	defer os.Remove(encoded)

	params := b.params(Probe{Width: b.video.Width, Height: b.video.Height, CRF: referenceCRF}, encode.Params{})
	params.FilmGrain = level

	if err := b.engine.encoder.Encode(ctx, b.codec, b.digest, encoded, params); err != nil {
		return fmt.Errorf("ladder: grain reference: %w", err)
	}

	path := filepath.Join(b.workDir, "reference.nut")
	if err := b.engine.grainLab.DecodeRaw(ctx, encoded, path, false); err != nil {
		return fmt.Errorf("ladder: grain reference: %w", err)
	}

	report, err := b.engine.inspector.Analyze(ctx, path, analysis.Options{SkipVideo: true})
	if err != nil {
		return fmt.Errorf("ladder: grain reference: inspect: %w", err)
	}

	b.reference, b.referenceReport = path, report

	return nil
}

// grainCheck compares the noise of a rung's encode at path (grain
// synthesised) with the source's at the rung's resolution.
func (b *build) grainCheck(
	ctx context.Context,
	path string,
	r Rung,
) (*GrainCheck, error) {
	source, err := b.engine.grainLab.Noise(ctx, b.digest, r.Width, r.Height, b.noiseEvery)
	if err != nil {
		return nil, fmt.Errorf("grain: %w", err)
	}

	output, err := b.engine.grainLab.Noise(ctx, path, r.Width, r.Height, b.noiseEvery)
	if err != nil {
		return nil, fmt.Errorf("grain: %w", err)
	}

	ratio := output.Sigma / math.Max(source.Sigma, 1e-9)

	return &GrainCheck{Source: source, Output: output, Ratio: ratio, OK: math.Abs(ratio-1) <= grainTolerance}, nil
}

// scoreGrainless measures an encode with synthesised grain: its bitrate from
// the encoded file, its fidelity from its grain-free decode against the
// denoised reference.
func (b *build) scoreGrainless(
	ctx context.Context,
	path string,
	mode scoring,
) (Measurement, error) {
	encoded, err := b.engine.inspector.Analyze(ctx, path, analysis.Options{SkipVideo: true})
	if err != nil {
		return Measurement{}, fmt.Errorf("inspect: %w", err)
	}

	raw := path + ".nut"
	defer os.Remove(raw)

	if err := b.engine.grainLab.DecodeRaw(ctx, path, raw, false); err != nil {
		return Measurement{}, fmt.Errorf("grain-free decode: %w", err)
	}

	cmp, err := b.score(ctx, raw, mode)
	if err != nil {
		return Measurement{}, err
	}

	m := measurementOf(cmp)
	m.Bitrate = encoded.Bitstream.AverageBitrate

	return m, nil
}
