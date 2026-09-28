package decode

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/eko/qc/internal/ffexec"
)

// AudioRequest describes an audio stream to decode.
type AudioRequest struct {
	Path string
	// Stream is the index of the stream in the file (ffprobe's index).
	Stream int
	// SampleRate and Channels are the stream's, as probed: the samples
	// come at that rate (ffmpeg resamples only a decoder that disagrees
	// with the probe) with that many channels, in stream order.
	SampleRate int
	Channels   int
	// ChunkFrames is the number of frames (samples per channel) handed
	// over per call; 0 means 100 ms.
	ChunkFrames int
}

// chunksPerSecond sets the default chunk: 100 ms, the step of the loudness
// meters, few enough calls and a small buffer.
const chunksPerSecond = 10

// bytesPerSample is the size of a float32 sample.
const bytesPerSample = 4

// errTruncatedAudio reports a stream ending within a frame.
var errTruncatedAudio = errors.New("truncated audio frame")

// errAudioRequest reports a request without rate or channels.
var errAudioRequest = errors.New("audio request needs a sample rate and a channel count")

// DecodeAudio decodes an audio stream to 32-bit float samples (full scale
// ±1) and calls fn with chunks of planar samples, one slice per channel,
// ChunkFrames long (the last one shorter). The slices are reused: they are
// valid only during the call.
//
// ffmpeg writes interleaved samples (its raw muxers take no planar
// format) through the same socket as frames; they are de-interleaved here,
// which costs less than a nanosecond per sample.
func (d *FFmpeg) DecodeAudio(
	ctx context.Context,
	req AudioRequest,
	fn func(samples [][]float32) error,
) error {
	if req.SampleRate <= 0 || req.Channels <= 0 {
		return fmt.Errorf("decode audio %s: %w", req.Path, errAudioRequest)
	}

	err := ffexec.Stream(ctx, d.bin, audioArgs(req), func(r io.Reader) error {
		return readAudio(r, req, fn)
	})
	if err != nil {
		return fmt.Errorf("decode audio %s stream %d: %w", req.Path, req.Stream, err)
	}

	return nil
}

// audioArgs is the ffmpeg command line decoding req.
func audioArgs(
	req AudioRequest,
) []string {
	return []string{
		"-v", "error", "-nostdin",
		"-i", req.Path,
		"-map", "0:" + strconv.Itoa(req.Stream),
		"-vn", "-sn", "-dn",
		"-ar", strconv.Itoa(req.SampleRate),
		"-c:a", "pcm_f32le",
		"-f", "f32le", "-",
	}
}

// readAudio reads interleaved samples from r until EOF and hands them to fn
// de-interleaved, chunk by chunk.
func readAudio(
	r io.Reader,
	req AudioRequest,
	fn func([][]float32) error,
) error {
	frames := req.ChunkFrames
	if frames <= 0 {
		frames = max(1, req.SampleRate/chunksPerSecond)
	}

	frameBytes := req.Channels * bytesPerSample
	buf := make([]byte, frames*frameBytes)

	planes := make([][]float32, req.Channels)
	for c := range planes {
		planes[c] = make([]float32, frames)
	}

	for {
		n, err := io.ReadFull(r, buf)

		switch {
		case errors.Is(err, io.EOF):
			return nil
		case err != nil && !errors.Is(err, io.ErrUnexpectedEOF):
			return fmt.Errorf("read audio: %w", err)
		case n%frameBytes != 0:
			return fmt.Errorf("read audio: %w", errTruncatedAudio)
		}

		got := n / frameBytes
		deinterleave(buf[:n], planes, got)

		for c := range planes {
			planes[c] = planes[c][:got]
		}

		if cbErr := fn(planes); cbErr != nil {
			return cbErr
		}

		if err != nil {
			return nil
		}
	}
}

// deinterleave splits frames interleaved little-endian float32 frames into
// the planes.
func deinterleave(
	buf []byte,
	planes [][]float32,
	frames int,
) {
	channels := len(planes)

	for c, plane := range planes {
		plane = plane[:frames]
		for i := range plane {
			at := (i*channels + c) * bytesPerSample
			plane[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[at : at+bytesPerSample]))
		}
	}
}
