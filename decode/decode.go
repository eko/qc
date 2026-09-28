// Package decode turns a video file into a stream of frames.
package decode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// Source decodes the frames of a video.
type Source interface {
	// Decode calls fn for every decoded frame, in presentation order. fn owns
	// the reference it receives and must Release it.
	Decode(
		ctx context.Context,
		req Request,
		fn func(*frame.Frame) error,
	) error
}

// Request describes what to decode. The pool sets the output geometry and
// planes: frames are scaled (bicubic) to it, those already at its size pass
// through.
type Request struct {
	Path string
	Pool *frame.Pool
	// SourceWidth and SourceHeight are the coded dimensions of the video.
	SourceWidth  int
	SourceHeight int
	// Start seeks to the frame presented at Start (0 = beginning), relative
	// to the first frame of the video.
	Start media.Duration
	// Origin is the presentation time of the first frame of the video on
	// the container's timeline (bitstream.Report.Start), which ffmpeg seeks
	// on: a seek goes to Origin+Start. Without it, ffmpeg would count Start
	// from the container's start, that of its earliest stream, and land
	// one frame or more early in a video starting after its audio.
	Origin media.Duration
	// FirstIndex is the index in the video of the first decoded frame (the
	// frame presented at Start). Returned frames are numbered from it.
	FirstIndex int
	// MaxFrames stops after that many frames (0 = until the end).
	MaxFrames int
	// Select keeps only the frames in these sorted, disjoint [from, to) index
	// ranges, counted from the first decoded frame (nil = every frame).
	// Frames are still decoded but only selected ones are scaled and piped.
	Select [][2]int
	// Threads overrides the decoder thread count (0 = the Source default).
	Threads int
	// PTS gives the presentation time of every frame of the video, indexed
	// by frame index. Frames beyond it are timestamped from Start and
	// FrameRate.
	PTS []media.Duration
	// FrameRate is the nominal frame rate, used to seek and to timestamp
	// frames missing from PTS.
	FrameRate media.Rational
	// Codec is the ffprobe codec name of the video, when known: hardware
	// decoding is only tried for codecs the hardware decodes exactly.
	Codec string
	// PixelFormat is the ffprobe pixel format of the video, when known:
	// VideoToolbox only decodes the formats it outputs as they are.
	PixelFormat string
	// Segment marks the decode as one of several concurrent decodes of
	// parts of a video: HWAccelAuto then decodes with VideoToolbox, whose
	// concurrent sessions add up while a single one is slower than the
	// CPU.
	Segment bool
	// ToneMap, when set, converts HDR frames to SDR while scaling (pools
	// with chroma only, see ToneMap).
	ToneMap *ToneMap
}

// FFmpeg decodes with an ffmpeg subprocess writing raw planes to a pipe, as
// 8-bit samples or 16-bit little-endian ones for high bit depth pools.
// Luma-only pools only receive the Y plane: a third of the bytes of a 4:2:0
// frame, enough for every pixel analyzer. WithHWAccel decodes on an NVIDIA
// GPU or with VideoToolbox.
type FFmpeg struct {
	bin     string
	threads int
	hwaccel HWAccel
	logger  *slog.Logger

	// sessions holds a token per decode using VideoToolbox (see
	// WithVideoToolboxSessions), created once by acquire when not set.
	sessions chan struct{}
	once     sync.Once

	// mu guards degraded: the mode each file fell back to after a
	// hardware decoding failure.
	mu       sync.Mutex
	degraded map[string]HWAccel
}

// NewFFmpeg returns a Source backed by the ffmpeg binary. threads=0 lets ffmpeg
// decide.
func NewFFmpeg(
	bin string,
	threads int,
	opts ...Option,
) *FFmpeg {
	d := &FFmpeg{bin: bin, threads: threads}
	for _, opt := range opts {
		opt(d)
	}

	return d
}

// seekDecimals is the precision of the -ss argument: one microsecond, well
// below any frame duration.
const seekDecimals = 6

// Decode implements Source. A hardware decode failing before its first
// frame is retried with the next mode down (see WithHWAccel).
func (d *FFmpeg) Decode(
	ctx context.Context,
	req Request,
	fn func(*frame.Frame) error,
) error {
	mode, release := d.acquire(req)
	defer release()

	for {
		delivered := 0
		count := func(f *frame.Frame) error {
			delivered++

			return fn(f)
		}

		err := d.run(ctx, req, mode, count)

		switch {
		case err == nil:
			return nil
		case mode == HWAccelNone || delivered > 0 || ctx.Err() != nil:
			return fmt.Errorf("decode %s: %w", req.Path, err)
		}

		next := mode.fallback()
		d.degrade(req.Path, mode, next, err)
		mode = next
	}
}

// run runs one ffmpeg decode of req in mode. A sampling pool takes two
// outputs of one decode (see sampledArgs).
func (d *FFmpeg) run(
	ctx context.Context,
	req Request,
	mode HWAccel,
	fn func(*frame.Frame) error,
) error {
	if req.Pool.SampleStep() > 0 {
		return ffexec.StreamPair(ctx, d.bin, d.sampledArgs(req, mode), func(luma, grid io.Reader) error {
			return readFrames(req, sampledReader(luma, grid, req.Pool), fn)
		})
	}

	return ffexec.Stream(ctx, d.bin, d.args(req, mode), func(r io.Reader) error {
		return readFrames(req, planeReader(r, req.Pool), fn)
	})
}

// inputArgs are the arguments before the input file: log level, threads,
// hardware decoding and the seek.
func (d *FFmpeg) inputArgs(
	req Request,
	mode HWAccel,
) []string {
	threads := d.threads
	if req.Threads > 0 {
		threads = req.Threads
	}

	args := []string{"-v", "error", "-nostdin", "-threads", strconv.Itoa(threads)}
	args = append(args, hwInputArgs(mode)...)

	if req.Start > 0 {
		// Seek half a frame early so the frame presented at Start is the
		// first one kept by ffmpeg's accurate seeking, on the container's
		// timeline (-seek_timestamp) rather than from its start.
		origin := req.Origin.Seconds()
		seek := max(origin+req.Start.Seconds()-frameDuration(req.FrameRate)/2, origin)
		args = append(args, "-seek_timestamp", "1", "-ss", strconv.FormatFloat(seek, 'f', seekDecimals, 64))
	}

	return args
}

// args builds the ffmpeg command line decoding req in mode.
func (d *FFmpeg) args(
	req Request,
	mode HWAccel,
) []string {
	args := d.inputArgs(req, mode)

	chain := filters(req)
	if mode == HWAccelCUDAScale {
		chain = gpuFilters(req)
	} else if req.Pool.Chroma() {
		// A stream changing resolution mid-stream (per-shot resolution)
		// would otherwise rebuild the filter graph, restarting select's
		// frame counter: the selection would drift. In a kept graph, scale
		// reconfigures itself for the new input size, bit-exactly.
		// Luma pools extract the plane before scaling, which a kept graph
		// cannot follow; they rebuild it.
		args = append(args, "-reinit_filter", "0")
	}

	args = append(args,
		"-i", req.Path,
		"-map", "0:v:0",
		"-fps_mode", "passthrough",
		"-an", "-sn", "-dn",
		"-vf", chain,
	)

	if req.MaxFrames > 0 {
		args = append(args, "-frames:v", strconv.Itoa(req.MaxFrames))
	}

	return append(args, "-f", "rawvideo", "-")
}

// filters builds the video filter chain for the requested output: frame
// selection, luma extraction, scaling, then the pool's sample format.
//
// extractplanes copies Y samples untouched (no range conversion, unlike
// format=gray on a YUV input). It runs before scaling: scaling first leaves
// ffmpeg unable to negotiate the format between the two filters, and scaling
// one plane is cheaper. The final format filter only converts the sample
// depth to the pool's.
func filters(
	req Request,
) string {
	var chain []string

	if len(req.Select) > 0 {
		chain = append(chain, selectFilter(req.Select))
	}

	pool := req.Pool
	if !pool.Chroma() {
		chain = append(chain, "extractplanes=y")
	}

	// Scale even at the pool's size: a stream may change resolution at a
	// keyframe (per-shot resolution), ffmpeg then reconfigures the graph,
	// and the scale keeps every frame at the pool's size. Frames already at
	// that size and format pass through untouched (vf_scale's passthrough),
	// so measurements stay bit-exact.
	chain = append(chain, scaleFilter(req))

	switch {
	case pool.Chroma() && pool.HighBitDepth():
		chain = append(chain, "format=yuv420p10le")
	case pool.Chroma():
		chain = append(chain, "format=yuv420p")
	case pool.HighBitDepth():
		chain = append(chain, "format=gray10le")
	default:
		chain = append(chain, "format=gray")
	}

	return strings.Join(chain, ",")
}

// selectFilter keeps the frames of the [from, to) index ranges.
func selectFilter(
	ranges [][2]int,
) string {
	terms := make([]string, len(ranges))
	for i, r := range ranges {
		terms[i] = fmt.Sprintf("between(n\\,%d\\,%d)", r[0], r[1]-1)
	}

	return "select='" + sumTree(terms) + "'"
}

// maxTerms is below the 100-term limit of an ffmpeg expression sum chain.
const maxTerms = 50

// sumTree joins terms with '+' as a tree of parenthesised groups so that no
// chain exceeds maxTerms.
func sumTree(
	terms []string,
) string {
	if len(terms) <= maxTerms {
		return strings.Join(terms, "+")
	}

	size := (len(terms) + maxTerms - 1) / maxTerms
	groups := make([]string, 0, maxTerms)

	for i := 0; i < len(terms); i += size {
		groups = append(groups, "("+sumTree(terms[i:min(i+size, len(terms))])+")")
	}

	return sumTree(groups)
}

// frameDuration returns the duration of one frame in seconds, or 0 when the
// rate is unknown.
func frameDuration(
	rate media.Rational,
) float64 {
	if fps := rate.Float(); fps > 0 {
		return 1 / fps
	}

	return 0
}

// errTooManyFrames reports that ffmpeg piped more frames than selected.
var errTooManyFrames = errors.New("more frames than selected")

// readFrames reads raw frames with read until EOF, numbers and timestamps them,
// and hands them to fn. The k-th piped frame is the k-th decoded frame, or
// the k-th selected one when req.Select is set.
func readFrames(
	req Request,
	read func(*frame.Frame) error,
	fn func(*frame.Frame) error,
) error {
	step := frameDuration(req.FrameRate)
	offsets := selectedOffsets(req.Select)

	for k := 0; ; k++ {
		f := req.Pool.Get()

		if err := read(f); err != nil {
			f.Release()

			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("read frame %d: %w", k, err)
		}

		offset := k
		if offsets != nil {
			if k >= len(offsets) {
				f.Release()

				return fmt.Errorf("frame %d: %w", k, errTooManyFrames)
			}

			offset = offsets[k]
		}

		f.Index = req.FirstIndex + offset
		f.PTS = req.timestamp(f.Index, float64(offset)*step)

		req.Pool.BuildThumb(f)

		if err := fn(f); err != nil {
			return err
		}
	}
}

// planeReader reads the raw frames of r straight into the planes of pooled
// frames.
func planeReader(
	r io.Reader,
	pool *frame.Pool,
) func(*frame.Frame) error {
	return func(f *frame.Frame) error {
		return readPlanes(r, pool.Planes(f))
	}
}

// timestamp returns the presentation time of the frame at index, from PTS
// when known, otherwise elapsed seconds after Start.
func (r Request) timestamp(
	index int,
	elapsed float64,
) media.Duration {
	if index < len(r.PTS) {
		return r.PTS[index]
	}

	return r.Start + media.Seconds(elapsed)
}

// readPlanes fills planes in order. A clean end of stream before the first
// byte returns io.EOF; a truncated frame returns io.ErrUnexpectedEOF.
func readPlanes(
	r io.Reader,
	planes []*frame.Plane,
) error {
	for i, p := range planes {
		if _, err := io.ReadFull(r, p.Pix); err != nil {
			if i > 0 && errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}

			return fmt.Errorf("read plane %d: %w", i, err)
		}
	}

	return nil
}

// selectedOffsets expands [from, to) ranges into the list of frame offsets
// they select, or nil when every frame is kept.
func selectedOffsets(
	ranges [][2]int,
) []int {
	if len(ranges) == 0 {
		return nil
	}

	var out []int
	for _, r := range ranges {
		for i := r[0]; i < r[1]; i++ {
			out = append(out, i)
		}
	}

	return out
}
