package encode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

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

	// threads caps the threads of every encode (see WithThreads).
	threads int

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

// WithThreads caps the threads of every encode at threads (0, the default,
// leaves each encoder to size itself on the cores of the machine): a
// process limited to some CPUs, by a container or by its user, would
// otherwise start encoders sized for all of them.
func WithThreads(
	threads int,
) Option {
	return func(f *FFmpeg) {
		f.threads = threads
	}
}

// capped is p with the thread cap of f, unless p sets its own.
func (f *FFmpeg) capped(
	p Params,
) Params {
	if p.Threads == 0 {
		p.Threads = f.threads
	}

	return p
}

// Encode encodes src into dst with codec and the settings of p.
func (f *FFmpeg) Encode(
	ctx context.Context,
	codec Codec,
	src, dst string,
	p Params,
) error {
	args := append([]string{"-v", "error", "-nostdin", "-y", "-i", src}, codec.Args(f.capped(p))...)
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
	// Frames, when set, gives every segment exactly that many frames: the
	// source is read a little past the end of each segment and cut by
	// count. A segment's length says how many frames it holds only when
	// the source has every frame at its time: one with frames missing
	// (a gap in its timestamps) gives fewer for the same length, and one
	// whose average frame rate is thereby under its real one gives more.
	Frames int
	// Lossless keeps the digest compact (FFV1); otherwise it is raw video in
	// a NUT container, which costs nothing to decode for the many encodes
	// and measurements that read it.
	Lossless bool
	// Fast cuts the segments at once, one process each, and starts each at
	// the keyframe of the source at or before its start instead of at its
	// start: nothing is decoded but the frames kept. A source with long
	// GOPs otherwise decodes, before each segment, what separates it from
	// its keyframe (1.65 s on average before 2 s kept, on a mezzanine with
	// GOPs of up to 7 s). The segments are then those of the digest only
	// roughly: for a digest that samples a title, not for one whose
	// segments must be where they were planned, nor for segments closer
	// to each other than a GOP, which could be cut twice from one
	// keyframe.
	Fast bool
	// Workers is the number of segments a Fast digest cuts at once (0: one
	// per processor of the Go runtime, which follows --cpus). Each is a
	// process holding a decoder: about 130 bytes per pixel of the source.
	Workers int
	// Quantisers, when set, is a file receiving the quantisers of the
	// source's own encode, frame by frame of the digest (ReadQuantisers
	// reads it): what its encoder had to do to hold its bitrate, which
	// tells how hard the video is. Only for 8-bit sources whose decoder
	// exports them (H.264).
	Quantisers string
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

	if spec.Fast {
		if err := f.digestFast(ctx, spec); err != nil {
			return fmt.Errorf("digest %s: %w", spec.name(), err)
		}

		return nil
	}

	if err := ffexec.Stream(ctx, f.bin, digestArgs(spec), discard); err != nil {
		return fmt.Errorf("digest %s: %w", spec.name(), err)
	}

	return nil
}

// Decode decodes length of the video of source from start, on as many
// threads as an encode, and drops the frames.
func (f *FFmpeg) Decode(
	ctx context.Context,
	source string,
	start, length media.Duration,
) error {
	args := []string{"-v", "error", "-nostdin"}
	if f.threads > 0 {
		args = append(args, "-threads", strconv.Itoa(f.threads))
	}

	args = append(args, seekArgs(start)...)
	args = append(args, "-t", seconds(length), "-i", source, "-map", "0:v:0", "-f", "null", "-")

	if err := ffexec.Stream(ctx, f.bin, args, discard); err != nil {
		return fmt.Errorf("decode %s: %w", source, err)
	}

	return nil
}

// quantiserChain is the filter chain turning the frames of a decoder that
// exports its quantisers into pictures holding them: codecview paints the
// quantiser of every block into the chroma, of which the U plane is kept,
// shrunk to quantiserSide×quantiserSide.
const quantiserChain = "codecview=qp=true,extractplanes=u,scale=8:8:flags=area"

// Quantiser pictures: their side, and what codecview multiplies a
// quantiser by to paint it (128/31).
const (
	quantiserSide  = 8
	quantiserScale = 128.0 / 31.0
)

// exportQuantisers are the input arguments making a decoder export the
// quantisers of its frames.
func exportQuantisers() []string {
	return []string{"-export_side_data", "venc_params"}
}

// ReadQuantisers reads the file a digest wrote the quantisers of its
// source to (DigestSpec.Quantisers): the mean quantiser of each frame of
// the digest.
func ReadQuantisers(
	path string,
) ([]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read quantisers: %w", err)
	}

	const picture = quantiserSide * quantiserSide

	out := make([]float64, len(data)/picture)

	for i := range out {
		sum := 0
		for _, v := range data[i*picture : (i+1)*picture] {
			sum += int(v)
		}

		out[i] = float64(sum) / picture / quantiserScale
	}

	return out, nil
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
			length, count := seg.Length(), ""
			if spec.Frames > 0 {
				length += overread
				count = fmt.Sprintf("trim=end_frame=%d,", spec.Frames)
			}

			if spec.Quantisers != "" {
				args = append(args, exportQuantisers()...)
			}

			args = append(args, seekArgs(part.Origin+seg.Start)...)
			args = append(args, "-t", seconds(length), "-i", part.Source)

			if spec.Quantisers != "" {
				fmt.Fprintf(&graph, "[%d:v:0]%ssplit[a%d][b%d];[a%d]format=%s,setsar=1[v%d];[b%d]%s[q%d];",
					inputs, count, inputs, inputs, inputs, format, inputs, inputs, quantiserChain, inputs)
			} else {
				fmt.Fprintf(&graph, "[%d:v:0]%sformat=%s,setsar=1[v%d];", inputs, count, format, inputs)
			}

			inputs++
		}
	}

	for i := range inputs {
		fmt.Fprintf(&graph, "[v%d]", i)
	}

	// concat loses the frame rate: restore constant timestamps at the
	// source rate so every encode and measurement sees the same timeline.
	fmt.Fprintf(&graph, "concat=n=%d:v=1:a=0,setpts=N/(%s*TB)[out]", inputs, spec.Rate)

	if spec.Quantisers != "" {
		graph.WriteString(";")

		for i := range inputs {
			fmt.Fprintf(&graph, "[q%d]", i)
		}

		fmt.Fprintf(&graph, "concat=n=%d:v=1:a=0,setpts=N/(%s*TB)[qout]", inputs, spec.Rate)
	}

	args = append(args, "-filter_complex", graph.String(), "-map", "[out]", "-an", "-r", spec.Rate.String())
	args = append(append(args, digestCodec(spec)...), spec.Destination)

	if spec.Quantisers != "" {
		args = append(args, quantiserOutput(spec)...)
	}

	return args
}

// digestCodec are the output arguments of the video of a digest.
func digestCodec(
	spec DigestSpec,
) []string {
	if spec.Lossless {
		return []string{"-c:v", "ffv1", "-level", "3", "-slices", ffv1Slices}
	}

	return []string{"-c:v", "rawvideo", "-f", "nut"}
}

// quantiserOutput are the output arguments writing the quantiser pictures
// of the filter graph (its [qout] or [q] pad) to file.
func quantiserOutput(
	spec DigestSpec,
	file ...string,
) []string {
	pad, path := "[qout]", spec.Quantisers
	if len(file) > 0 {
		pad, path = "[q]", file[0]
	}

	return []string{"-map", pad, "-r", spec.Rate.String(), "-f", "rawvideo", "-pix_fmt", "gray", path}
}

// digestFast writes the digest of spec as DigestSpec.Fast says: every
// segment cut by a process of its own, from its keyframe, then joined
// without decoding.
func (f *FFmpeg) digestFast(
	ctx context.Context,
	spec DigestSpec,
) error {
	var pieces, quantisers []string

	defer func() {
		for _, path := range slices.Concat(pieces, quantisers) {
			_ = os.Remove(path)
		}
	}()

	workers, threads := f.fastWorkers(spec)

	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(workers)

	for _, part := range spec.parts() {
		for _, seg := range part.Segments {
			piece := fmt.Sprintf("%s.%03d%s", spec.Destination, len(pieces), filepath.Ext(spec.Destination))
			qp := ""

			if spec.Quantisers != "" {
				qp = fmt.Sprintf("%s.%03d", spec.Quantisers, len(pieces))
				quantisers = append(quantisers, qp)
			}

			pieces = append(pieces, piece)

			group.Go(func() error {
				return ffexec.Stream(gctx, f.bin, fastPieceArgs(spec, part, seg, piece, qp, threads), discard)
			})
		}
	}

	if err := group.Wait(); err != nil {
		return err
	}

	list := spec.Destination + ".txt"
	defer os.Remove(list)

	if err := os.WriteFile(list, []byte(concatList(pieces)), listMode); err != nil {
		return fmt.Errorf("list the segments: %w", err)
	}

	if err := ffexec.Stream(ctx, f.bin, digestJoinArgs(spec, list), discard); err != nil {
		return err
	}

	if spec.Quantisers == "" {
		return nil
	}

	return joinFiles(spec.Quantisers, quantisers)
}

// fastWorkers returns how many processes cut the segments of a fast
// digest at once, and the threads each decodes on (0: as many as ffmpeg
// takes): under a cap of threads, each of the processes takes one.
func (f *FFmpeg) fastWorkers(
	spec DigestSpec,
) (workers, threads int) {
	workers = spec.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	if f.threads > 0 {
		return min(workers, f.threads), 1
	}

	return workers, 0
}

// listMode is the mode of the temporary list of the pieces of a digest.
const listMode = 0o600

// fastPieceArgs are the ffmpeg arguments cutting one segment of a fast
// digest into piece (and its quantisers into qp, when asked): as many
// frames as the segment holds, from the keyframe at or before its start,
// decoded on threads threads (0: as many as ffmpeg takes).
func fastPieceArgs(
	spec DigestSpec,
	part DigestPart,
	seg media.Interval,
	piece, qp string,
	threads int,
) []string {
	frames := spec.Frames
	if frames <= 0 {
		frames = int(math.Round(seg.Length().Seconds() * spec.Rate.Float()))
	}

	args := []string{"-v", "error", "-nostdin", "-y"}
	if qp != "" {
		args = append(args, exportQuantisers()...)
	}

	if threads > 0 {
		args = append(args, "-threads", strconv.Itoa(threads))
	}

	args = append(args, "-noaccurate_seek")
	args = append(args, seekArgs(part.Origin+seg.Start)...)
	args = append(args, "-t", seconds(seg.Length()+overread), "-i", part.Source, "-an")

	video := fmt.Sprintf("format=%s,setsar=1,setpts=N/(%s*TB)[v]", pixelFormat(spec.BitDepth), spec.Rate)
	graph := fmt.Sprintf("[0:v:0]trim=end_frame=%d,%s", frames, video)

	if qp != "" {
		graph = fmt.Sprintf("[0:v:0]trim=end_frame=%d,split[a][b];[a]%s;[b]%s,setpts=N/(%s*TB)[q]", frames, video, quantiserChain, spec.Rate)
	}

	args = append(args, "-filter_complex", graph, "-map", "[v]", "-r", spec.Rate.String())
	args = append(append(args, digestCodec(spec)...), piece)

	if qp != "" {
		args = append(args, quantiserOutput(spec, qp)...)
	}

	return args
}

// digestJoinArgs are the ffmpeg arguments joining the pieces of list into the
// digest of spec without decoding them, on one timeline at its rate: the
// timestamps of a piece would otherwise start again where the piece
// before it ended one frame short.
func digestJoinArgs(
	spec DigestSpec,
	list string,
) []string {
	stamp := fmt.Sprintf("N/(%s*TB)", spec.Rate)
	args := []string{
		"-v", "error", "-nostdin", "-y", "-f", "concat", "-safe", "0", "-i", list,
		"-c", "copy", "-bsf:v", "setts=pts=" + stamp + ":dts=" + stamp,
	}

	if !spec.Lossless {
		args = append(args, "-f", "nut")
	}

	return append(args, spec.Destination)
}

// joinFiles writes the files one after the other into path.
func joinFiles(
	path string,
	files []string,
) error {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("join quantisers: %w", err)
	}
	defer out.Close()

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("join quantisers: %w", err)
		}

		if _, err := out.Write(data); err != nil {
			return fmt.Errorf("join quantisers: %w", err)
		}
	}

	return nil
}

// overread is how far past the end of a segment the source is read when
// its frames are counted (DigestSpec.Frames): room for the frames a gap
// in the timestamps pushed later.
const overread = media.Duration(time.Second / 2)

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
