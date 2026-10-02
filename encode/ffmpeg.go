package encode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// ErrNoSegments is returned when a digest is asked for no segment.
var ErrNoSegments = errors.New("digest needs at least one segment")

// ffv1Slices splits each lossless digest frame into independently coded
// slices, so FFV1 encodes and decodes on several threads.
const ffv1Slices = "16"

// darwin is the GOOS of macOS, where VideoToolbox is.
const darwin = "darwin"

// FFmpeg encodes with the ffmpeg binary.
type FFmpeg struct {
	bin    string
	logger *slog.Logger
	// goos is the operating system BurnAuto resolves for.
	goos string

	// stall is how long an encode may go without its output growing (see
	// WithStallTimeout).
	stall time.Duration

	// autoOnce resolves BurnAuto once: auto is the encoder it stands for.
	autoOnce sync.Once
	auto     BurnEncoder
}

// Option configures an FFmpeg.
type Option func(*FFmpeg)

// WithLogger logs to logger the fallbacks of automatic choices (a hardware
// encoder that does not work on this machine).
func WithLogger(
	logger *slog.Logger,
) Option {
	return func(f *FFmpeg) {
		f.logger = logger
	}
}

// NewFFmpeg returns an FFmpeg running bin. It encodes, extracts digests,
// decodes and measures grain: consumers such as the ladder engine declare
// the narrow part they use.
func NewFFmpeg(
	bin string,
	opts ...Option,
) *FFmpeg {
	f := &FFmpeg{bin: bin, logger: slog.New(slog.DiscardHandler), goos: runtime.GOOS, stall: defaultStallTimeout}
	for _, opt := range opts {
		opt(f)
	}

	return f
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

	if err := f.encodeWatched(ctx, args, dst, nil); err != nil {
		return fmt.Errorf("encode %s with %s: %w", src, codec.Encoder, err)
	}

	return nil
}

// DigestSpec describes a digest: segments of a source (or of several, see
// Parts) concatenated at the source resolution in 4:2:0.
type DigestSpec struct {
	// Source is the file the segments are cut from; Destination receives
	// the digest.
	Source, Destination string
	Segments            []media.Interval
	// Rate is the source frame rate, restored on the concatenated timeline.
	Rate media.Rational
	// Origin is the presentation time of the source's first frame on its
	// container's timeline (see ChunkSource.Origin): the segments are
	// times of the video, from its first frame, and are seeked at Origin
	// plus their start.
	Origin media.Duration
	// BitDepth is 8 or 10.
	BitDepth int
	// Lossless keeps the digest compact (FFV1); otherwise it is raw video in
	// a NUT container, which costs nothing to decode for the many encodes
	// and measurements that read it.
	Lossless bool
	// Parts, when set, cut the digest from several sources, one after the
	// other, in place of Source, Origin and Segments: the digest of a
	// program. The sources must share their geometry, frame rate and pixel
	// format, which the concatenation does not convert.
	Parts []DigestPart
}

// DigestPart is the part of a digest cut from one source: its segments,
// times of that source's video seeked at its Origin.
type DigestPart struct {
	Source   string
	Origin   media.Duration
	Segments []media.Interval
}

// parts returns the parts of the digest: Parts, or the single source.
func (s DigestSpec) parts() []DigestPart {
	if len(s.Parts) > 0 {
		return s.Parts
	}

	return []DigestPart{{Source: s.Source, Origin: s.Origin, Segments: s.Segments}}
}

// name names the digest in errors: its source, or its first source and how
// many follow.
func (s DigestSpec) name() string {
	parts := s.parts()
	if len(parts) == 1 {
		return parts[0].Source
	}

	return fmt.Sprintf("%s and %d more", parts[0].Source, len(parts)-1)
}

// segments counts the segments of the digest.
func (s DigestSpec) segments() int {
	n := 0
	for _, p := range s.parts() {
		n += len(p.Segments)
	}

	return n
}

// Digest writes the digest spec describes.
func (f *FFmpeg) Digest(
	ctx context.Context,
	spec DigestSpec,
) error {
	if spec.segments() == 0 {
		return fmt.Errorf("digest %s: %w", spec.name(), ErrNoSegments)
	}

	if err := ffexec.Stream(ctx, f.bin, digestArgs(spec), discard); err != nil {
		return fmt.Errorf("digest %s: %w", spec.name(), err)
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

	inputs := 0

	for _, part := range spec.parts() {
		for _, seg := range part.Segments {
			args = append(args, seekArgs(part.Origin+seg.Start)...)
			args = append(args, "-t", seconds(seg.Length()), "-i", part.Source)
			fmt.Fprintf(&graph, "[%d:v:0]format=%s,setsar=1[v%d];", inputs, format, inputs)

			inputs++
		}
	}

	for i := range inputs {
		fmt.Fprintf(&graph, "[v%d]", i)
	}

	// concat loses the frame rate: restore constant timestamps at the
	// source rate so every encode and measurement sees the same timeline.
	fmt.Fprintf(&graph, "concat=n=%d:v=1:a=0,setpts=N/(%s*TB)[out]", inputs, spec.Rate)

	args = append(args, "-filter_complex", graph.String(), "-map", "[out]", "-an", "-r", spec.Rate.String())

	if spec.Lossless {
		args = append(args, "-c:v", "ffv1", "-level", "3", "-slices", ffv1Slices)
	} else {
		args = append(args, "-c:v", "rawvideo", "-f", "nut")
	}

	return append(args, spec.Destination)
}

// seekArgs are the input arguments seeking to seek on the container's
// timeline: an absolute seek (-seek_timestamp), as decoding seeks. A plain
// -ss is counted from the container's start, that of its earliest stream,
// which is not the video's first frame in a video starting after its audio
// nor in a container starting before 0 (an audio track keeping its encoder
// priming at -21 ms): the seek would land early by the difference.
func seekArgs(
	seek media.Duration,
) []string {
	return []string{"-seek_timestamp", "1", "-ss", seconds(seek)}
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
