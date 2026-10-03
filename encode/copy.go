package encode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// ErrDecode is returned by Decodes for a file ffmpeg reports errors on.
var ErrDecode = errors.New("decoding errors")

// CopySpec describes a file made of segments of videos copied as they are,
// without re-encoding: whole GOPs, from a keyframe on.
type CopySpec struct {
	// Destination receives the segments, joined; its extension picks the
	// container.
	Destination string
	// Codec is the codec of the sources (ffprobe's name), which picks how
	// the segments are carried before they are joined.
	Codec string
	// Rate is the frame rate of the sources.
	Rate media.Rational
	// Parts are the sources, in the order of the file. They must share
	// their codec, geometry and frame rate.
	Parts []CopyPart
	// Segments is the number of segments over the parts, for Progress,
	// which is called with the count copied so far.
	Segments int
	Progress func(done int)
}

// CopyPart is the part of a copy cut from one source.
type CopyPart struct {
	Source string
	// Origin is the presentation time of the source's first frame on its
	// container's timeline (see ChunkSource.Origin).
	Origin   media.Duration
	Segments []CopySegment
}

// CopySegment is a run of frames of a source: Frames frames from the
// keyframe shown at Start (a time of the video, from its first frame),
// which last Duration on the timeline of the source.
type CopySegment struct {
	Start    media.Duration
	Frames   int
	Duration media.Duration
}

// Copy writes spec.Destination: every segment is copied from its source
// into a file of its own, then the files are joined, without re-encoding
// anything. H.264 and HEVC segments travel as MPEG-TS, which carries their
// parameter sets with every keyframe: sources encoded with other settings
// then decode once joined. The frames of the result are the sources' own.
func (f *FFmpeg) Copy(
	ctx context.Context,
	spec CopySpec,
) error {
	dir, err := os.MkdirTemp("", "qc-sample-")
	if err != nil {
		return fmt.Errorf("copy to %s: %w", spec.Destination, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	format, ext := segmentFormat(spec.Codec)

	var list strings.Builder

	done := 0

	for _, part := range spec.Parts {
		for _, seg := range part.Segments {
			path := filepath.Join(dir, strconv.Itoa(done)+ext)

			if err := ffexec.Stream(ctx, f.bin, copySegmentArgs(part, seg, spec.Rate, format, path), discard); err != nil {
				return fmt.Errorf("copy %s at %s: %w", part.Source, seconds(seg.Start), err)
			}

			// The concatenation trusts these durations: a segment's own
			// timestamps do not tell how long its last frame lasts.
			fmt.Fprintf(&list, "file '%s'\nduration %s\n", path, seconds(seg.Duration))

			done++
			if spec.Progress != nil {
				spec.Progress(done)
			}
		}
	}

	if done == 0 {
		return fmt.Errorf("copy to %s: %w", spec.Destination, ErrNoSegments)
	}

	listPath := filepath.Join(dir, "segments.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o600); err != nil {
		return fmt.Errorf("copy to %s: %w", spec.Destination, err)
	}

	if err := ffexec.Stream(ctx, f.bin, concatArgs(listPath, spec.Destination), discard); err != nil {
		return fmt.Errorf("join %s: %w", spec.Destination, err)
	}

	return nil
}

// segmentFormat is the container segments travel in before they are
// joined, and its extension: MPEG-TS for the codecs it carries with their
// parameter sets in band, Matroska for the others.
func segmentFormat(
	codec string,
) (format, ext string) {
	switch codec {
	case "h264", "hevc", "mpeg2video":
		return "mpegts", ".ts"
	}

	return "matroska", ".mkv"
}

// seekMargin is how far after its keyframe a segment is seeked, as a share
// of a frame: ffmpeg starts a copy at the keyframe before the time asked,
// and a time rounded under the keyframe's would start a GOP early.
const seekMargin = 4

// copySegmentArgs builds the ffmpeg arguments copying one segment: the
// video alone, from the keyframe at its start, for its number of frames.
func copySegmentArgs(
	part CopyPart,
	seg CopySegment,
	rate media.Rational,
	format, path string,
) []string {
	seek := part.Origin + seg.Start + framesDuration(1, rate)/seekMargin

	args := []string{"-v", "error", "-nostdin", "-y"}
	args = append(args, seekArgs(seek)...)

	return append(args, "-i", part.Source, "-map", "0:v:0", "-an", "-sn", "-dn", "-c", "copy",
		"-frames:v", strconv.Itoa(seg.Frames), "-f", format, path)
}

// concatArgs builds the ffmpeg arguments joining the listed segments.
func concatArgs(
	list, destination string,
) []string {
	return []string{"-v", "error", "-nostdin", "-y", "-f", "concat", "-safe", "0", "-i", list, "-map", "0:v:0", "-c", "copy", destination}
}

// framesDuration is how long n frames last at rate.
func framesDuration(
	n int,
	rate media.Rational,
) media.Duration {
	return media.Seconds(float64(n) / rate.Float())
}

// Decodes decodes every frame of the video of path and returns ErrDecode,
// with what ffmpeg reported, when a frame does not decode: ffmpeg is asked
// to stop at the first error rather than conceal it.
func (f *FFmpeg) Decodes(
	ctx context.Context,
	path string,
) error {
	if err := ffexec.Stream(ctx, f.bin, decodeArgs(path), discard); err != nil {
		if ctx.Err() != nil || errors.Is(err, ffexec.ErrNotFound) {
			return fmt.Errorf("decode %s: %w", path, err)
		}

		return fmt.Errorf("decode %s: %w: %w", path, ErrDecode, err)
	}

	return nil
}

// decodeArgs builds the ffmpeg arguments decoding the video of path to
// nothing, failing on the first error.
func decodeArgs(
	path string,
) []string {
	return []string{"-v", "error", "-nostdin", "-xerror", "-err_detect", "explode", "-i", path, "-map", "0:v:0", "-f", "null", "-"}
}
