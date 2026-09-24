package encode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// ErrNoSegments is returned when a digest is asked for no segment.
var ErrNoSegments = errors.New("digest needs at least one segment")

// ffv1Slices splits each lossless digest frame into independently coded
// slices, so FFV1 encodes and decodes on several threads.
const ffv1Slices = "16"

// FFmpeg encodes with the ffmpeg binary.
type FFmpeg struct {
	bin string
}

// NewFFmpeg returns an FFmpeg running bin. It encodes, extracts digests,
// decodes and measures grain: consumers such as the ladder engine declare
// the narrow part they use.
func NewFFmpeg(
	bin string,
) *FFmpeg {
	return &FFmpeg{bin: bin}
}

// Encode encodes src into dst with codec and the settings of p.
func (f *FFmpeg) Encode(
	ctx context.Context,
	codec Codec,
	src, dst string,
	p Params,
) error {
	args := append([]string{"-v", "error", "-nostdin", "-y", "-i", src}, codec.Args(p)...)
	args = append(args, dst)

	if err := ffexec.Stream(ctx, f.bin, args, discard); err != nil {
		return fmt.Errorf("encode %s with %s: %w", src, codec.Encoder, err)
	}

	return nil
}

// DigestSpec describes a digest: segments of a source concatenated at the
// source resolution in 4:2:0.
type DigestSpec struct {
	// Source is the file the segments are cut from; Destination receives
	// the digest.
	Source, Destination string
	Segments            []media.Interval
	// Rate is the source frame rate, restored on the concatenated timeline.
	Rate media.Rational
	// BitDepth is 8 or 10.
	BitDepth int
	// Lossless keeps the digest compact (FFV1); otherwise it is raw video in
	// a NUT container, which costs nothing to decode for the many encodes
	// and measurements that read it.
	Lossless bool
}

// Digest writes the digest spec describes.
func (f *FFmpeg) Digest(
	ctx context.Context,
	spec DigestSpec,
) error {
	if len(spec.Segments) == 0 {
		return fmt.Errorf("digest %s: %w", spec.Source, ErrNoSegments)
	}

	if err := ffexec.Stream(ctx, f.bin, digestArgs(spec), discard); err != nil {
		return fmt.Errorf("digest %s: %w", spec.Source, err)
	}

	return nil
}

// digestArgs builds the ffmpeg arguments of Digest. Each segment is a
// separate input with input seeking (fast, keyframe-accurate then decoded to
// the exact start), and the segments are joined by the concat filter.
func digestArgs(
	spec DigestSpec,
) []string {
	format := pixelFormat(spec.BitDepth)
	args := []string{"-v", "error", "-nostdin", "-y"}

	var graph strings.Builder

	for i, seg := range spec.Segments {
		args = append(args,
			"-ss", seconds(seg.Start),
			"-t", seconds(seg.Length()),
			"-i", spec.Source)
		fmt.Fprintf(&graph, "[%d:v:0]format=%s,setsar=1[v%d];", i, format, i)
	}

	for i := range spec.Segments {
		fmt.Fprintf(&graph, "[v%d]", i)
	}

	// concat loses the frame rate: restore constant timestamps at the
	// source rate so every encode and measurement sees the same timeline.
	fmt.Fprintf(&graph, "concat=n=%d:v=1:a=0,setpts=N/(%s*TB)[out]", len(spec.Segments), spec.Rate)

	args = append(args, "-filter_complex", graph.String(), "-map", "[out]", "-an", "-r", spec.Rate.String())

	if spec.Lossless {
		args = append(args, "-c:v", "ffv1", "-level", "3", "-slices", ffv1Slices)
	} else {
		args = append(args, "-c:v", "rawvideo", "-f", "nut")
	}

	return append(args, spec.Destination)
}

func seconds(
	d media.Duration,
) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 6, 64)
}

func discard(
	r io.Reader,
) error {
	if _, err := io.Copy(io.Discard, r); err != nil {
		return fmt.Errorf("read ffmpeg output: %w", err)
	}

	return nil
}
