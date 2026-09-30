package encode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// ErrNoChunks is returned when a chunked encode is asked for no chunk.
var ErrNoChunks = errors.New("chunked encode needs at least one chunk")

// Chunk is a run of consecutive frames encoded with its own CRF. Chunked
// encoding is how per-shot settings reach every encoder the same way: x264
// and x265 accept zones through their private parameters, but SVT-AV1 has
// no per-frame quantiser control reachable from ffmpeg. Each chunk is
// encoded separately (it starts with a keyframe) and the chunks are joined
// without re-encoding; decoding the joined file gives exactly the frames of
// the separate chunks for x264, x265 and SVT-AV1, whose headers do not
// depend on the CRF. Chunks starting on the fixed GOP grid keep the
// keyframes of every rung aligned, as ABR segmenting requires.
type Chunk struct {
	// Start is the index of the chunk's first frame.
	Start int `json:"start"`
	// Frames is the number of frames of the chunk.
	Frames int     `json:"frames"`
	CRF    float64 `json:"crf"`
	// Width and Height, when set, override the encode's resolution for the
	// chunk: per-shot resolution changes the resolution at chunk
	// boundaries (always keyframes).
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// chunkPreroll is how long before a chunk's first frame decoding starts, in
// seconds. Seeking straight to the chunk lands on the keyframe before it,
// which in a long-GOP source need not be a clean random access point: the
// H.264 decoder then drops the frames whose references it lacks, the chunk
// silently loses frames and takes later ones instead, and the joined file
// gets gaps in its timestamps (another average frame rate, which VMAF
// refuses). Decoding from a keyframe one preroll earlier gives the
// references back.
const chunkPreroll = 2.0

// ChunkSource is the video a chunked encode reads.
type ChunkSource struct {
	Path string
	// Rate is the frame rate of the video, which places the chunks in time.
	Rate media.Rational
	// Origin is the presentation time of the first frame of the video on
	// the container's timeline (bitstream.Report.Start), which chunks are
	// seeked on (absolute seeks): a chunk starts at Origin plus its frames'
	// time. Without it, a video starting after its audio would be seeked
	// from the container's start, and every chunk would start early by the
	// difference. A digest starts at 0.
	Origin media.Duration
}

// chunkArgs builds the ffmpeg arguments encoding chunk c of src into dst.
// The input is seeked chunkPreroll early, then decoded frames are dropped
// up to half a frame before the chunk's first frame (chunkFilter): the
// chunk starts exactly on its frame whatever the rounding of timestamps,
// with every reference decoded. Times are on the container's timeline,
// from the video's first frame (src.Origin).
func (c Codec) chunkArgs(
	src ChunkSource,
	dst string,
	chunk Chunk,
	p Params,
) []string {
	p.CRF = chunk.CRF
	if chunk.Height > 0 {
		p.Width, p.Height = chunk.Width, chunk.Height
	}

	// A video starting before the container's timeline (a negative
	// origin) may be seeked there; any other one no earlier than 0.
	floor := min(src.Origin.Seconds(), 0)
	start := max(src.Origin.Seconds()+(float64(chunk.Start)-0.5)/src.Rate.Float(), floor)
	preroll := min(chunkPreroll, start-floor)

	args := append(seekArgs(media.Seconds(start-preroll)), "-i", src.Path, "-frames:v", strconv.Itoa(chunk.Frames))
	p.preFilter = chunkFilter(preroll)

	return append(append(args, c.Args(p)...), dst)
}

// chunkFilter drops the frames decoded during the preroll (seconds), up to
// half a frame before the chunk's first frame, and restarts the chunk's
// timestamps at its first frame. An output seek (-ss after -i) would drop
// the same frames but count the chunk's time from the seek point, half a
// frame before its first frame: with timestamps rounded by the container
// (Matroska's milliseconds, 16 or 17 ms apart at 60 fps), the frames then
// sit on either side of the half ticks of the encoder's time base, and a
// frame rounded up leaves a hole of a frame in the chunk's timeline, which
// the join keeps (a variable frame rate). From its first frame, every
// frame is within a millisecond of its tick.
func chunkFilter(
	preroll float64,
) string {
	const restart = "setpts=PTS-STARTPTS,"
	if preroll <= 0 {
		return restart
	}

	return "trim=start=" + seconds(media.Seconds(preroll)) + "," + restart
}

// EncodeChunks encodes src chunk by chunk, each with its own CRF and the
// other settings of p, and joins the chunks into dst.
func (f *FFmpeg) EncodeChunks(
	ctx context.Context,
	codec Codec,
	src ChunkSource,
	dst string,
	chunks []Chunk,
	p Params,
) error {
	return f.encodeChunks(ctx, codec, src, dst, chunks, p, nil)
}

// encodeChunks is EncodeChunks reporting the frames written to progress,
// when set.
func (f *FFmpeg) encodeChunks(
	ctx context.Context,
	codec Codec,
	src ChunkSource,
	dst string,
	chunks []Chunk,
	p Params,
	progress func(frames int),
) error {
	if len(chunks) == 0 {
		return fmt.Errorf("encode %s: %w", src.Path, ErrNoChunks)
	}

	parts := chunkPaths(dst, len(chunks))
	list := dst + ".txt"

	defer func() {
		for _, part := range parts {
			_ = os.Remove(part)
		}

		_ = os.Remove(list)
	}()

	if err := f.encodeParts(ctx, codec, src, parts, chunks, p, progress); err != nil {
		return err
	}

	if codec.transportJoin(chunks) {
		return f.joinTransport(ctx, src.Path, dst, parts)
	}

	if err := os.WriteFile(list, []byte(concatList(parts)), 0o600); err != nil {
		return fmt.Errorf("encode %s: %w", src.Path, err)
	}

	args := []string{"-v", "error", "-nostdin", "-y", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", dst}
	if err := ffexec.Stream(ctx, f.bin, args, discard); err != nil {
		return fmt.Errorf("join chunks of %s: %w", src.Path, err)
	}

	return nil
}

// chunkWorkers is how many chunks of one encode are encoded at once. The
// chunks are short (a shot, or its piece of a digest), too short for an
// encoder to keep every core busy, and ffmpeg's start-up is a good part of
// each one: encoding them one after another left most of the machine idle.
const chunkWorkers = 4

// encodeParts encodes every chunk of src into its part, chunkWorkers at a
// time. progress, when set, follows the frames written by all of them.
func (f *FFmpeg) encodeParts(
	ctx context.Context,
	codec Codec,
	src ChunkSource,
	parts []string,
	chunks []Chunk,
	p Params,
	progress func(frames int),
) error {
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(chunkWorkers)

	total := &partProgress{done: make([]int, len(chunks)), report: progress}

	for i, chunk := range chunks {
		group.Go(func() error {
			var report func(int)
			if progress != nil {
				report = func(n int) { total.update(i, n) }
			}

			args := append(progressArgs(progress != nil), codec.chunkArgs(src, parts[i], chunk, p)...)
			if err := f.encodeWatched(gctx, args, parts[i], report); err != nil {
				return fmt.Errorf("encode %s chunk %d with %s: %w", src.Path, i+1, codec.Encoder, err)
			}

			return nil
		})
	}

	return group.Wait()
}

// transportJoin tells whether chunks changing resolution must be joined
// through MPEG-TS. x264 and SVT-AV1 repeat their parameter sets (SPS/PPS,
// sequence header) at every keyframe, so the MP4 concat of chunks at
// several resolutions decodes exactly; x265 in MP4 keeps them only in the
// first chunk's hvcC, and the other chunks then decode as garbage.
func (c Codec) transportJoin(
	chunks []Chunk,
) bool {
	if c.Name != "hevc" {
		return false
	}

	for _, chunk := range chunks[1:] {
		if chunk.Width != chunks[0].Width || chunk.Height != chunks[0].Height {
			return true
		}
	}

	return false
}

// transportSteps are the ffmpeg arguments joining HEVC parts through
// MPEG-TS: remuxing each part to TS writes its VPS/SPS/PPS in-band at every
// keyframe (hevc_mp4toannexb), the TS parts are concatenated, and the
// result is remuxed to MP4 as hev1 (parameter sets in-band, as a mid-stream
// resolution change requires; hvc1 forbids them). It also returns the TS
// files to remove.
func transportSteps(
	parts []string,
	dst string,
) ([][]string, []string) {
	ts := make([]string, len(parts))
	steps := make([][]string, 0, len(parts)+1)

	for i, part := range parts {
		ts[i] = part + ".ts"
		steps = append(steps, []string{"-i", part, "-c", "copy", "-f", "mpegts", ts[i]})
	}

	steps = append(steps, []string{"-i", "concat:" + strings.Join(ts, "|"), "-c", "copy", "-tag:v", "hev1", dst})

	return steps, ts
}

// joinTransport joins HEVC parts at several resolutions (see
// transportSteps).
func (f *FFmpeg) joinTransport(
	ctx context.Context,
	src, dst string,
	parts []string,
) error {
	steps, ts := transportSteps(parts, dst)

	defer func() {
		for _, t := range ts {
			_ = os.Remove(t)
		}
	}()

	for _, step := range steps {
		if err := ffexec.Stream(ctx, f.bin, append([]string{"-v", "error", "-nostdin", "-y"}, step...), discard); err != nil {
			return fmt.Errorf("join chunks of %s: %w", src, err)
		}
	}

	return nil
}

// chunkPaths names the n chunk files of dst: next to it, numbered, with its
// extension so that the muxer is the same.
func chunkPaths(
	dst string,
	n int,
) []string {
	ext := filepath.Ext(dst)
	base := strings.TrimSuffix(dst, ext)

	paths := make([]string, n)
	for i := range paths {
		paths[i] = fmt.Sprintf("%s.part%03d%s", base, i+1, ext)
	}

	return paths
}

// concatList is the concat demuxer's list of paths. Single quotes inside a
// path are escaped the way the demuxer expects.
func concatList(
	paths []string,
) string {
	var b strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(p, "'", `'\''`))
	}

	return b.String()
}

// ChunkCommandLine renders a copy-pasteable shell script encoding src chunk
// by chunk into dst (see EncodeChunks), then removing the chunk files.
func (c Codec) ChunkCommandLine(
	src ChunkSource,
	dst string,
	chunks []Chunk,
	p Params,
) string {
	parts := chunkPaths(dst, len(chunks))
	lines := make([]string, 0, len(chunks)+3)

	for i, chunk := range chunks {
		words := append([]string{"ffmpeg"}, c.InputArgs()...)
		for _, a := range c.chunkArgs(src, parts[i], chunk, p) {
			words = append(words, quote(a))
		}

		lines = append(lines, strings.Join(words, " "))
	}

	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = quote(part)
	}

	if c.transportJoin(chunks) {
		steps, ts := transportSteps(parts, dst)
		for _, step := range steps {
			words := []string{"ffmpeg"}
			for _, a := range step {
				words = append(words, quote(a))
			}

			lines = append(lines, strings.Join(words, " "))
		}

		for _, t := range ts {
			quoted = append(quoted, quote(t))
		}

		lines = append(lines, "rm "+strings.Join(quoted, " "))

		return strings.Join(lines, " && \\\n")
	}

	list := quote(dst + ".txt")

	lines = append(lines,
		fmt.Sprintf(`printf "file '%%s'\n" %s > %s`, strings.Join(quoted, " "), list),
		fmt.Sprintf("ffmpeg -f concat -safe 0 -i %s -c copy %s", list, quote(dst)),
		fmt.Sprintf("rm %s %s", strings.Join(quoted, " "), list))

	return strings.Join(lines, " && \\\n")
}
