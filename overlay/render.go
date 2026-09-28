package overlay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
)

// Burner burns an ASS script into a copy of a video (*encode.FFmpeg): the
// port the Renderer encodes through.
type Burner interface {
	Burn(
		ctx context.Context,
		spec encode.BurnSpec,
	) error
}

// RenderOptions configures a rendering. The zero value renders every item
// at the source's size.
type RenderOptions struct {
	Options
	// Height scales the annotated copy to that height (0 keeps the
	// source's): 720 renders about twice as fast as 1080.
	Height int
	// Encoder is the H.264 encoder of the copy (encode.BurnAuto:
	// VideoToolbox on macOS, x264 elsewhere).
	Encoder encode.BurnEncoder
	// Workers is how many segments of the video render at once (0: the
	// encoder's default, 1: a single pass). The segments start at
	// keyframes and are joined without re-encoding: the copy has the same
	// frames and timestamps either way.
	Workers int
	// CRF and Preset are the x264 settings (default 20, fast).
	CRF    float64
	Preset string
	// FontsDir, when set, is searched for the overlay's font first.
	FontsDir string
	// Progress, when set, is called as frames are written.
	Progress func(Progress)
}

// Progress reports the frames of the annotated copy written so far, out of
// the title's frame count.
type Progress struct {
	Done  int
	Total int
}

// Renderer writes annotated copies of videos.
type Renderer struct {
	burner Burner
}

// NewRenderer returns a Renderer burning through burner.
func NewRenderer(
	burner Burner,
) *Renderer {
	return &Renderer{burner: burner}
}

// Render writes to output a copy of source, the video in.Report analyses,
// with the overlay of in burnt into its frames. The ASS script lives in a
// temporary directory for the time of the encode.
func (r *Renderer) Render(
	ctx context.Context,
	source, output string,
	in Input,
	opts RenderOptions,
) (err error) {
	dir, err := os.MkdirTemp("", "qc-overlay-")
	if err != nil {
		return fmt.Errorf("overlay: %w", err)
	}

	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil && err == nil {
			err = fmt.Errorf("overlay: %w", rmErr)
		}
	}()

	spec, err := prepare(dir, source, output, in, opts)
	if err != nil {
		return err
	}

	err = r.burner.Burn(ctx, spec)
	if errors.Is(err, encode.ErrBurnSegment) {
		// A seek did not land where planned: one pass still renders
		// every frame.
		spec.Segments, spec.Workers = nil, 1
		err = r.burner.Burn(ctx, spec)
	}

	return err
}

// prepare writes into dir the script of the overlay of in, and a slice of
// it for each segment of the burn, and returns the burn.
func prepare(
	dir, source, output string,
	in Input,
	opts RenderOptions,
) (encode.BurnSpec, error) {
	script := filepath.Join(dir, "overlay.ass")
	if err := writeScript(script, in, opts.Options); err != nil {
		return encode.BurnSpec{}, err
	}

	spec := burnSpec(source, script, output, in, opts)
	if err := sliceSegments(spec, dir); err != nil {
		return encode.BurnSpec{}, err
	}

	return spec, nil
}

// sliceSegments gives each segment of spec a slice of its script, written
// into dir (see sliceScript).
func sliceSegments(
	spec encode.BurnSpec,
	dir string,
) error {
	if len(spec.Segments) < 2 {
		return nil
	}

	paths, err := sliceScript(spec.Subtitles, dir, sliceWindows(spec.Segments, spec.Start))
	if err != nil {
		return err
	}

	for k, path := range paths {
		spec.Segments[k].Subtitles = path
	}

	return nil
}

// writeScript writes the overlay's script to path.
func writeScript(
	path string,
	in Input,
	opts Options,
) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("overlay: %w", err)
	}

	if err := Write(f, in, opts); err != nil {
		_ = f.Close()

		return err
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("overlay: %w", err)
	}

	return nil
}

// burnSpec is the burn of script into source.
func burnSpec(
	source, script, output string,
	in Input,
	opts RenderOptions,
) encode.BurnSpec {
	spec := encode.BurnSpec{
		Source:    source,
		Subtitles: script,
		Output:    output,
		Height:    opts.Height,
		Encoder:   opts.Encoder,
		Workers:   opts.Workers,
		CRF:       opts.CRF,
		Preset:    opts.Preset,
		FontsDir:  opts.FontsDir,
	}

	bs := in.Report.Bitstream
	spec.Start = bs.Start

	if info := in.Report.Info; info != nil {
		// The start of the timeline the script is timed on (see
		// newTitle).
		spec.Start = min(bs.Start, info.StartTime)

		if len(info.Audio) > 0 {
			spec.AudioCodec = info.Audio[0].Codec
		}
	}

	if segs := segments(bs, opts.Workers); opts.Workers != 1 && len(segs) > 1 {
		spec.Segments = segs
	}

	if opts.Progress != nil {
		total := len(bs.PTS)
		spec.Progress = func(frames int) { opts.Progress(Progress{Done: frames, Total: total}) }
	}

	return spec
}

// Segment planning: segments of about equal length, starting at keyframes,
// more of them than renders at once so that the last ones are short and
// the renders end together.
const (
	segmentsPerWorker = 3
	// planWorkers is the concurrency segments are planned for when the
	// encoder picks it (RenderOptions.Workers 0): at least any encoder's
	// default.
	planWorkers = 8
	// minSegmentFrames bounds how short a segment gets: each one starts an
	// ffmpeg, loads the script and decodes frames it drops before its
	// first one (encode.BurnSegment).
	minSegmentFrames = 750
)

// segments splits the title of bs at keyframes for workers concurrent
// renders (0: planWorkers): a title too short to split is one segment.
func segments(
	bs *bitstream.Report,
	workers int,
) []encode.BurnSegment {
	frames := len(bs.PTS)
	count := min(segmentsPerWorker*max(workers, planWorkers), frames/minSegmentFrames)

	return segmentsAt(bs, bitstream.SplitAtKeyframes(bs.KeyFlags, count, minSegmentFrames/2))
}

// segmentsAt are the segments of the title of bs starting at the frames
// bounds, on the container's timeline.
func segmentsAt(
	bs *bitstream.Report,
	bounds []int,
) []encode.BurnSegment {
	segs := make([]encode.BurnSegment, len(bounds))

	for k, first := range bounds {
		end := len(bs.PTS)
		if k+1 < len(bounds) {
			end = bounds[k+1]
			segs[k].To = bs.Start + bs.PTS[end]
		}

		segs[k].From = bs.Start + bs.PTS[first]
		segs[k].Frames = end - first
	}

	return segs
}
