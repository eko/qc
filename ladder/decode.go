package ladder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eko/qc/internal/cputime"
	"github.com/eko/qc/media"
)

// decodeSegments bounds the segments of the digest decodeCPU decodes again:
// spread over the title, they are enough to price the decode of a second.
const decodeSegments = 8

// decodeLead is the stretch of a segment decodeCPU decodes alone, to tell
// what seeking to the segment costs from what decoding it costs.
const decodeLead = media.Duration(time.Second)

// energy sums what the ladder costs to encode on the whole title (see
// energyOf), the decode of the source included once encodes were measured.
func (b *build) energy(
	ctx context.Context,
	res *Result,
) error {
	tolerance, coreWatts := b.opts.Constraints.EnergyTolerance, b.opts.CoreWatts

	res.Energy = energyOf(res, b.lowered, tolerance, coreWatts)
	if res.Energy == nil || res.Energy.CPUHours == 0 {
		return nil
	}

	cpu, err := b.decodeCPU(ctx, res.Digest)

	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("ladder: %w", ctx.Err())
	case err != nil:
		// The ladder stands without it: its energy leaves decoding out.
		return nil
	}

	res.Digest.DecodeCPU = cpu
	res.Energy = energyOf(res, b.lowered, tolerance, coreWatts)

	return nil
}

// decodeCPU returns the CPU time decoding the segments of the digest from
// the source takes. Cutting the digest decoded them, but also the frames
// before each that a seek starts from, and wrote them out: its own CPU time
// was twice the decode of the title. So up to decodeSegments segments are
// decoded again, each from its start over decodeLead then whole, and the
// difference is what their frames past the lead cost, which is scaled to
// the length of the digest.
func (b *build) decodeCPU(
	ctx context.Context,
	digest Digest,
) (float64, error) {
	if b.engine.decoder == nil {
		return 0, nil
	}

	if cpu, ok := b.prepared.sharedDecode(digest.Segments); ok {
		return cpu, nil
	}

	var (
		spans        []decodeSpan
		total        media.Duration
		cpu, decoded float64
	)

	for _, part := range b.digestParts {
		for _, seg := range part.Segments {
			spans = append(spans, decodeSpan{source: part.Source, start: part.Origin + seg.Start, length: seg.Length()})
			total += seg.Length()
		}
	}

	for _, span := range spread(spans, decodeSegments) {
		lead := min(decodeLead, span.length/4)

		whole, err := b.decodeTime(ctx, span.source, span.start, span.length)
		if err != nil {
			return 0, err
		}

		start, err := b.decodeTime(ctx, span.source, span.start, lead)
		if err != nil {
			return 0, err
		}

		cpu += max(whole-start, 0)
		decoded += (span.length - lead).Seconds()
	}

	if decoded == 0 {
		return 0, errNoDecode
	}

	cpu *= total.Seconds() / decoded
	b.prepared.keepDecode(digest.Segments, cpu)

	return cpu, nil
}

// errNoDecode is returned when the digest has nothing to decode.
var errNoDecode = errors.New("no segment of the digest to decode")

// decodeSpan is a segment of the digest, on the timeline of its source.
type decodeSpan struct {
	source        string
	start, length media.Duration
}

// decodeTime returns the CPU time, in seconds, decoding length of source
// from start takes.
func (b *build) decodeTime(
	ctx context.Context,
	source string,
	start, length media.Duration,
) (float64, error) {
	meter := &cputime.Meter{}

	if err := b.engine.decoder.Decode(cputime.With(ctx, meter), source, start, length); err != nil {
		return 0, fmt.Errorf("ladder: %w", err)
	}

	return meter.Seconds(), nil
}

// spread returns at most n of items, evenly spaced from the first.
func spread[T any](
	items []T,
	n int,
) []T {
	if len(items) <= n {
		return items
	}

	out := make([]T, n)
	for i := range out {
		out[i] = items[i*len(items)/n]
	}

	return out
}
