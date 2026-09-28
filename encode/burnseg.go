package encode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// ErrBurnSegment is returned by Burn when a segment did not render the
// frames planned (a seek that landed after the segment's first frame): the
// segments are unusable, a single pass still is.
var ErrBurnSegment = errors.New("burn segment does not match the plan")

// BurnSegment is a run of consecutive frames of the source, rendered by its
// own ffmpeg (BurnSpec.Segments). Each ffmpeg keeps the source's
// timestamps (-copyts), seeks burnPreroll before the segment, decodes from
// the keyframe it lands on and keeps the frames presented from From to To:
// the segments hold every frame once whatever the keyframes, and the
// script draws on each frame what it draws in a single pass. The segments
// are joined without re-encoding, each one starting where the previous
// one's frames end, and the source's audio is added once.
type BurnSegment struct {
	// From is when the segment's first frame is presented, on the source's
	// container timeline; To when the frame after its last one is (0: the
	// segment runs to the end).
	From, To media.Duration
	// Frames is the number of frames of the segment: a segment rendering
	// another count fails the burn with ErrBurnSegment.
	Frames int
	// Subtitles, when set, is the script of the segment instead of
	// BurnSpec.Subtitles: the same script, with only the events shown
	// during the segment, loads and renders faster.
	Subtitles string
}

const (
	// burnPreroll is how long before a segment its ffmpeg seeks: the
	// demuxer may land after the time asked for (MPEG-TS seeks by byte
	// position), and an open-GOP keyframe needs the GOP before it. Frames
	// before the segment are decoded and dropped.
	burnPreroll = 3 * time.Second
	// burnCutMargin places the cuts of the trim filter this much before a
	// segment's first frame and the next one's: container timestamps are
	// rounded to the microsecond, and frames are more than a millisecond
	// apart below 1000 fps.
	burnCutMargin = time.Millisecond
	// partExt is the container of the segments: MP4 keeps the timestamps
	// the join reads back.
	partExt = ".mp4"
)

// burnSegments renders the segments of spec with encoder, workers at a
// time, then joins them and adds the audio.
func (f *FFmpeg) burnSegments(
	ctx context.Context,
	spec BurnSpec,
	encoder BurnEncoder,
	workers int,
) error {
	parts := partPaths(spec.Output, len(spec.Segments))
	list := spec.Output + ".parts.txt"

	defer func() {
		for _, part := range parts {
			_ = os.Remove(part)
		}

		_ = os.Remove(list)
	}()

	if err := f.renderParts(ctx, spec, encoder, workers, parts); err != nil {
		return fmt.Errorf("burn %s into %s: %w", spec.Subtitles, spec.Source, err)
	}

	if err := os.WriteFile(list, []byte(partList(parts, spec.Segments)), 0o600); err != nil {
		return fmt.Errorf("burn %s into %s: %w", spec.Subtitles, spec.Source, err)
	}

	if err := ffexec.Stream(ctx, f.bin, joinArgs(spec, list), discard); err != nil {
		return fmt.Errorf("join the segments of %s: %w", spec.Output, err)
	}

	return nil
}

// renderParts renders every segment of spec into parts, workers at once,
// in order, and checks their frame counts.
func (f *FFmpeg) renderParts(
	ctx context.Context,
	spec BurnSpec,
	encoder BurnEncoder,
	workers int,
	parts []string,
) error {
	progress := &partProgress{done: make([]int, len(parts)), report: spec.Progress}

	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(workers)

	for k, seg := range spec.Segments {
		group.Go(func() error {
			var last lastFrames

			args := partArgs(spec, encoder, seg, parts[k])
			if err := ffexec.Lines(ctx, f.bin, args, last.reader(func(n int) { progress.update(k, n) })); err != nil {
				return fmt.Errorf("segment %d: %w", k+1, err)
			}

			if last.frames != seg.Frames {
				return fmt.Errorf("%w: segment %d rendered %d frames instead of %d", ErrBurnSegment, k+1, last.frames, seg.Frames)
			}

			return nil
		})
	}

	return group.Wait()
}

// partArgs builds the ffmpeg arguments rendering seg into part. The first
// segment decodes from the start; the others seek burnPreroll early on the
// container timeline (-seek_timestamp), and every segment keeps its frames
// with the trim filter, after the decoder, where timestamps are exact; the
// timestamps are then shifted to the timeline of a single pass, which the
// script is timed on.
func partArgs(
	spec BurnSpec,
	encoder BurnEncoder,
	seg BurnSegment,
	part string,
) []string {
	args := append([]string{"-v", "error", "-nostdin", "-y", "-nostats", "-progress", "pipe:1"}, burnInputArgs(encoder, true)...)
	args = append(args, "-copyts")

	var cuts []string

	if seek := seg.From - media.Duration(burnPreroll); seek > 0 {
		// ffmpeg's own cut after a seek would add the container's start
		// time to this absolute one: the trim filter cuts instead.
		args = append(args, "-seek_timestamp", "1", "-ss", seconds(seek), "-noaccurate_seek")
	}

	if seg.From > 0 {
		cuts = append(cuts, "start="+seconds(seg.From-media.Duration(burnCutMargin)))
	}

	if seg.To > 0 {
		cuts = append(cuts, "end="+seconds(seg.To-media.Duration(burnCutMargin)))
	}

	if seg.Subtitles != "" {
		spec.Subtitles = seg.Subtitles
	}

	filter := burnFilter(spec)
	if spec.Start > 0 {
		// The timeline of a single pass, which the script is timed on.
		filter = "setpts=PTS-" + seconds(spec.Start) + "/TB," + filter
	}

	if len(cuts) > 0 {
		filter = "trim=" + strings.Join(cuts, ":") + "," + filter
	}

	args = append(args, "-i", spec.Source, "-map", "0:v:0", "-vf", filter, "-fps_mode", "passthrough")
	args = append(args, burnVideoArgs(encoder, spec)...)

	return append(args, "-an", part)
}

// joinArgs builds the ffmpeg arguments joining the segments listed in list
// (concat demuxer) and adding the source's audio: the video starts after
// the start of the timeline as the source's first frame does (a video
// starting after its audio), as in a single pass.
func joinArgs(
	spec BurnSpec,
	list string,
) []string {
	args := []string{"-v", "error", "-nostdin", "-y"}
	if delay := spec.Segments[0].From - spec.Start; delay > 0 {
		args = append(args, "-itsoffset", seconds(delay))
	}

	args = append(args, "-f", "concat", "-safe", "0", "-i", list)
	if spec.AudioCodec != "" {
		args = append(args, "-i", spec.Source)
	}

	args = append(args, "-map", "0:v:0", "-c:v", "copy")
	args = append(args, audioArgs(spec, 1)...)

	return append(args, outputArgs(spec.Output)...)
}

// partList is the concat demuxer's list of the segments: each one lasts
// until the next one's first frame, so that the joined timestamps are the
// source's, whatever the last frame's duration in the part.
func partList(
	parts []string,
	segments []BurnSegment,
) string {
	var b strings.Builder

	for k, part := range parts {
		// The parts are next to the list, which the demuxer resolves
		// relative paths against.
		b.WriteString(concatList([]string{filepath.Base(part)}))

		if seg := segments[k]; seg.To > 0 {
			fmt.Fprintf(&b, "duration %s\n", seconds(seg.To-seg.From))
		}
	}

	return b.String()
}

// partPaths names the n segment files of output: next to it, numbered.
func partPaths(
	output string,
	n int,
) []string {
	paths := make([]string, n)
	for i := range paths {
		paths[i] = output + ".part" + strconv.Itoa(i+1) + partExt
	}

	return paths
}

// lastFrames follows the -progress output of one ffmpeg: frames is the
// last frame count reported.
type lastFrames struct {
	frames int
}

// reader returns the line handler of ffexec.Lines recording the frame
// count and passing it to report, when set.
func (l *lastFrames) reader(
	report func(frames int),
) func(line []byte) error {
	return func(line []byte) error {
		if frames, ok := progressFrames(line); ok {
			l.frames = frames
			if report != nil {
				report(frames)
			}
		}

		return nil
	}
}

// partProgress adds up the frames written by concurrent segments.
type partProgress struct {
	mu     sync.Mutex
	done   []int
	report func(frames int)
}

// update records that segment k wrote frames frames and reports the total.
func (p *partProgress) update(
	k, frames int,
) {
	if p.report == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.done[k] = frames

	total := 0
	for _, n := range p.done {
		total += n
	}

	p.report(total)
}
