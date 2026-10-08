package encode

import (
	"context"
	"fmt"
	"os"
)

// RenditionSpec describes a rendition of a title: the whole title encoded
// with Params, or chunk by chunk with the CRFs of Chunks (a per-shot
// rendition).
type RenditionSpec struct {
	// Source is the title; its Rate and Origin place the chunks.
	Source      ChunkSource
	Destination string
	Params      Params
	// Chunks, when set, are the chunks of a per-shot rendition covering
	// the whole title.
	Chunks []Chunk
	// Progress, when set, is called with the number of frames written so
	// far, about twice a second.
	Progress func(frames int)
}

// EncodeRendition writes the rendition spec describes. A rendition is a
// long encode: it is watched for stalls like every encode, and reports its
// progress in frames.
func (f *FFmpeg) EncodeRendition(
	ctx context.Context,
	codec Codec,
	spec RenditionSpec,
) error {
	if len(spec.Chunks) > 0 {
		return f.encodeChunks(ctx, codec, spec.Source, spec.Destination, spec.Chunks, spec.Params, spec.Progress)
	}

	args := progressArgs(spec.Progress != nil)
	args = append(append(append(args, "-i", spec.Source.Path), codec.Args(f.capped(spec.Params))...), spec.Destination)

	if err := f.encodeWatched(ctx, args, spec.Destination, spec.Progress); err != nil {
		_ = os.Remove(spec.Destination)

		return fmt.Errorf("encode %s with %s: %w", spec.Source.Path, codec.Encoder, err)
	}

	return nil
}

// progressArgs are the leading arguments of an encode: quiet, and with the
// frames written reported on stdout (-progress) when report is set.
func progressArgs(
	report bool,
) []string {
	args := []string{"-v", "error", "-nostdin", "-y"}
	if report {
		args = append(args, "-nostats", "-progress", "pipe:1")
	}

	return args
}
