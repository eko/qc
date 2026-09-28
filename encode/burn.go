package encode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// ErrNoSubtitlesFilter is returned by CheckBurn when ffmpeg has no
// subtitles filter: it was built without libass.
var ErrNoSubtitlesFilter = errors.New("ffmpeg has no subtitles filter (built without libass)")

// Defaults of an x264 burn: a fast encode close to transparent, for a
// video watched to check the analysis, not delivered.
const (
	burnEncoder = "libx264"
	burnCRF     = 20
	burnPreset  = "fast"
)

// BurnSpec describes a burn: a copy of a video with an ASS subtitle script
// drawn on its frames.
type BurnSpec struct {
	// Source is the video; Subtitles the ASS script; Output the copy
	// written (H.264, 8-bit 4:2:0).
	Source, Subtitles, Output string
	// Height scales the copy to that height before the script is drawn
	// (0 keeps the source's): a smaller copy encodes faster, and libass
	// scales the script to it.
	Height int
	// Encoder is the H.264 encoder (BurnAuto: VideoToolbox on macOS, x264
	// elsewhere).
	Encoder BurnEncoder
	// CRF and Preset are the x264 settings (default 20, fast).
	CRF    float64
	Preset string
	// Segments, when there are two or more, render the copy in runs of
	// consecutive frames, Workers at once, joined without re-encoding (see
	// BurnSegment): one ffmpeg draws the overlay on a single thread, too
	// slowly to feed a hardware encoder.
	Segments []BurnSegment
	// Workers is how many segments render at once (0: the encoder's
	// default; 1 or less renders in a single pass, as x264 does by
	// default).
	Workers int
	// Start is the start of the source's timeline (its container's start
	// time), which ffmpeg subtracts from the timestamps its filters see in
	// a single pass: segments subtract it too, so that the script is the
	// same in both modes.
	Start media.Duration
	// AudioCodec is the codec of the source's first audio stream, "" when
	// it has none: its audio is copied when the output container can hold
	// it, encoded to AAC otherwise.
	AudioCodec string
	// FontsDir, when set, is searched for the script's fonts before the
	// system's.
	FontsDir string
	// Progress, when set, is called with the number of frames written so
	// far, about twice a second.
	Progress func(frames int)
}

// Burn writes the copy spec describes. The frames keep their timestamps
// (no frame is dropped or duplicated), so the script's times, taken from
// the source, land on the frames they describe.
func (f *FFmpeg) Burn(
	ctx context.Context,
	spec BurnSpec,
) error {
	encoder := f.burnEncoder(ctx, spec.Encoder)

	workers := spec.Workers
	if workers == 0 {
		workers = defaultWorkers(encoder)
	}

	if len(spec.Segments) > 1 && workers > 1 {
		return f.burnSegments(ctx, spec, encoder, workers)
	}

	var progress lastFrames

	err := ffexec.Lines(ctx, f.bin, burnArgs(spec, encoder), progress.reader(spec.Progress))
	if err != nil {
		return fmt.Errorf("burn %s into %s: %w", spec.Subtitles, spec.Source, err)
	}

	return nil
}

// CheckBurn verifies that ffmpeg can burn subtitles with encoder: that it
// has the subtitles filter (libass), failing with ErrNoSubtitlesFilter
// otherwise, and, for a hardware encoder, that it encodes a few frames on
// this machine. BurnAuto is not tested: Burn falls back to x264 when the
// hardware fails.
func (f *FFmpeg) CheckBurn(
	ctx context.Context,
	encoder BurnEncoder,
) error {
	out, err := ffexec.Output(ctx, f.bin, []string{"-hide_banner", "-filters"})
	if err != nil {
		return fmt.Errorf("list ffmpeg filters: %w", err)
	}

	if !hasSubtitlesFilter(out) {
		return ErrNoSubtitlesFilter
	}

	if encoder.Hardware() {
		return f.checkEncoder(ctx, encoder)
	}

	return nil
}

// hasSubtitlesFilter reports whether ffmpeg -filters lists the subtitles
// filter.
func hasSubtitlesFilter(
	filters []byte,
) bool {
	for line := range bytes.Lines(filters) {
		if fields := bytes.Fields(line); len(fields) >= 2 && string(fields[1]) == "subtitles" {
			return true
		}
	}

	return false
}

// burnArgs builds the ffmpeg arguments of a single-pass Burn with encoder.
// ffmpeg reports its progress on stdout (-progress pipe:1).
func burnArgs(
	spec BurnSpec,
	encoder BurnEncoder,
) []string {
	args := append([]string{"-v", "error", "-nostdin", "-y", "-nostats", "-progress", "pipe:1"}, burnInputArgs(encoder, false)...)
	args = append(args,
		"-i", spec.Source,
		"-map", "0:v:0", "-vf", burnFilter(spec),
		"-fps_mode", "passthrough",
	)
	args = append(args, burnVideoArgs(encoder, spec)...)
	args = append(args, audioArgs(spec, 0)...)

	return append(args, outputArgs(spec.Output)...)
}

// audioArgs map the first audio stream of input (the source) to the copy,
// copied when the output container holds its codec, encoded to AAC
// otherwise.
func audioArgs(
	spec BurnSpec,
	input int,
) []string {
	if spec.AudioCodec == "" {
		return nil
	}

	args := []string{"-map", strconv.Itoa(input) + ":a:0", "-c:a"}
	if audioFits(spec.AudioCodec, spec.Output) {
		return append(args, "copy")
	}

	return append(args, "aac", "-b:a", "192k")
}

// outputArgs are the muxer options of output, then output.
func outputArgs(
	output string,
) []string {
	if isMP4(output) {
		// The index first: the copy plays while it downloads or copies.
		return []string{"-movflags", "+faststart", output}
	}

	return []string{output}
}

// burnFilter scales the frames (to spec.Height, or to even dimensions, which
// 4:2:0 requires), converts them to 8-bit 4:2:0 and draws the script.
func burnFilter(
	spec BurnSpec,
) string {
	scale := "scale=trunc(iw/2)*2:trunc(ih/2)*2"
	if spec.Height > 0 {
		scale = "scale=-2:" + strconv.Itoa(spec.Height) + ":flags=bicubic"
	}

	subtitles := "subtitles=filename=" + filterValue(spec.Subtitles)
	if spec.FontsDir != "" {
		subtitles += ":fontsdir=" + filterValue(spec.FontsDir)
	}

	return scale + ",format=" + pixelFormat8 + "," + subtitles
}

// filterValue quotes a path as a filter option value inside a filter graph:
// the option parser reads '...' literally (a quote is closed, escaped and
// reopened), and the graph parser needs its own escapes for the
// backslashes and quotes that remain.
func filterValue(
	path string,
) string {
	quoted := "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"

	return strings.NewReplacer(`\`, `\\`, `'`, `\'`, "[", `\[`, "]", `\]`, ",", `\,`, ";", `\;`).Replace(quoted)
}

// mp4Audio are the audio codecs an MP4 file holds and players read.
var mp4Audio = []string{"aac", "mp3", "ac3", "eac3", "alac", "opus", "flac"}

// audioFits reports whether audio of codec can be copied into output:
// Matroska and QuickTime hold anything, MP4 a known list.
func audioFits(
	codec, output string,
) bool {
	switch strings.ToLower(filepath.Ext(output)) {
	case ".mkv", ".mov":
		return true
	}

	return slices.Contains(mp4Audio, codec)
}

// isMP4 reports whether output is an MP4 file (by its extension).
func isMP4(
	output string,
) bool {
	switch strings.ToLower(filepath.Ext(output)) {
	case ".mp4", ".m4v", ".mov":
		return true
	}

	return false
}

// progressFrames reads the frame count of a -progress line ("frame=42").
func progressFrames(
	line []byte,
) (int, bool) {
	value, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("frame="))
	if !ok {
		return 0, false
	}

	frames, err := strconv.Atoi(string(value))

	return frames, err == nil
}
