package ladder

import (
	"context"
	"errors"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

// The engine's ports: each is the narrow part of an adapter the engine
// uses, declared here, on the consumer side, so that an implementation
// (encode.FFmpeg satisfies Encoder, Digester and GrainLab) or a test double
// provides only what a ladder build calls.

// Inspector inspects and compares media files.
type Inspector interface {
	Analyze(
		ctx context.Context,
		path string,
		opts analysis.Options,
	) (*analysis.Report, error)
	Compare(
		ctx context.Context,
		refPath, distPath string,
		opts analysis.CompareOptions,
	) (*analysis.Comparison, error)
}

// Encoder encodes the digest: whole (probes, verifications) or chunk by
// chunk with a CRF per chunk (per-shot rungs, see encode.Chunk).
type Encoder interface {
	Encode(
		ctx context.Context,
		codec encode.Codec,
		src, dst string,
		p encode.Params,
	) error
	EncodeChunks(
		ctx context.Context,
		codec encode.Codec,
		src, dst string,
		rate media.Rational,
		chunks []encode.Chunk,
		p encode.Params,
	) error
}

// Digester extracts the digest every measurement of a build reads.
type Digester interface {
	Digest(
		ctx context.Context,
		spec encode.DigestSpec,
	) error
}

// GrainLab decodes encodes and measures their grain: AV1 film grain
// synthesis (Options.FilmGrain) needs it, other builds never call it.
type GrainLab interface {
	// DecodeRaw decodes src to raw video at dst, with or without the
	// synthesised film grain.
	DecodeRaw(
		ctx context.Context,
		src, dst string,
		withGrain bool,
	) error
	// Noise measures the grain of path at width×height, on frames taken
	// every step frames.
	Noise(
		ctx context.Context,
		path string,
		width, height, step int,
	) (grain.Stats, error)
}

// ErrNoGrainLab is returned when film grain synthesis is asked of an engine
// built without a GrainLab.
var ErrNoGrainLab = errors.New("film grain synthesis needs a grain lab (see WithGrainLab)")

// Option configures an Engine.
type Option func(*Engine)

// WithGrainLab gives the engine what AV1 film grain synthesis needs: grain
// measurement and grain-free decoding.
func WithGrainLab(
	lab GrainLab,
) Option {
	return func(e *Engine) {
		e.grainLab = lab
	}
}
