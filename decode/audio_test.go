package decode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/audiotest"
	"github.com/eko/qc/internal/testutil"
)

// interleaved encodes frames of channels little-endian float32 samples,
// sample value = frame + channel/10.
func interleaved(
	frames, channels int,
) []byte {
	var buf []byte
	for i := range frames {
		for c := range channels {
			buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(float32(i)+float32(c)/10))
		}
	}

	return buf
}

func TestReadAudio(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		input      []byte
		chunk      int
		wantChunks []int
		wantErr    error
	}{
		{name: "whole chunks and a shorter last one", input: interleaved(7, 2), chunk: 3, wantChunks: []int{3, 3, 1}},
		{name: "exact chunks", input: interleaved(6, 2), chunk: 3, wantChunks: []int{3, 3}},
		{name: "empty", input: nil, chunk: 3},
		{name: "default chunk of 100 ms", input: interleaved(10, 2), wantChunks: []int{4, 4, 2}},
		{name: "truncated frame", input: interleaved(2, 2)[:12], chunk: 3, wantErr: errTruncatedAudio},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := AudioRequest{SampleRate: 40, Channels: 2, ChunkFrames: testCase.chunk}

			var (
				chunks []int
				next   float32
			)

			err := readAudio(bytes.NewReader(testCase.input), req, func(samples [][]float32) error {
				require.Len(t, samples, 2)
				chunks = append(chunks, len(samples[0]))

				for i, v := range samples[0] {
					assert.InDelta(t, next+float32(i), v, 1e-6)
					assert.InDelta(t, next+float32(i)+0.1, samples[1][i], 1e-6)
				}

				next += float32(len(samples[0]))

				return nil
			})

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantChunks, chunks)
		})
	}
}

// failingReader fails after its data.
type failingReader struct {
	data []byte
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}

	n := copy(p, f.data)
	f.data = f.data[n:]

	return n, nil
}

func TestReadAudioErrors(
	t *testing.T,
) {
	errRead, errStop := errors.New("read"), errors.New("stop")
	req := AudioRequest{SampleRate: 40, Channels: 2, ChunkFrames: 2}

	err := readAudio(&failingReader{data: interleaved(1, 2), err: errRead}, req, func([][]float32) error { return nil })
	require.ErrorIs(t, err, errRead)

	err = readAudio(bytes.NewReader(interleaved(4, 2)), req, func([][]float32) error { return errStop })
	require.ErrorIs(t, err, errStop)

	err = readAudio(bytes.NewReader(interleaved(1, 2)), req, func([][]float32) error { return errStop })
	require.ErrorIs(t, err, errStop, "the last, shorter chunk too")

}

func TestAudioArgs(
	t *testing.T,
) {
	assert.Equal(t, []string{
		"-v", "error", "-nostdin", "-i", "in.mp4", "-map", "0:3", "-vn", "-sn", "-dn",
		"-ar", "48000", "-c:a", "pcm_f32le", "-f", "f32le", "-",
	}, audioArgs(AudioRequest{Path: "in.mp4", Stream: 3, SampleRate: 48000, Channels: 2}))
}

func TestDecodeAudio(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	// A float WAV decodes to its own samples, bit for bit.
	signal := audiotest.Programme(audiotest.Rate, 0.5, 1)
	path := filepath.Join(t.TempDir(), "a.wav")
	require.NoError(t, os.WriteFile(path, audiotest.WAV(audiotest.Rate, audiotest.MaskStereo, signal), 0o600))

	var got [][]float32

	req := AudioRequest{Path: path, Stream: 0, SampleRate: audiotest.Rate, Channels: 2}
	err := NewFFmpeg("ffmpeg", 0).DecodeAudio(t.Context(), req, func(samples [][]float32) error {
		for len(got) < len(samples) {
			got = append(got, nil)
		}

		for c := range samples {
			got[c] = append(got[c], samples[c]...)
		}

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, signal, got)
}

func TestDecodeAudioErrors(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	dec := NewFFmpeg("ffmpeg", 0)
	noop := func([][]float32) error { return nil }

	err := dec.DecodeAudio(t.Context(), AudioRequest{Path: "x.wav"}, noop)
	require.ErrorIs(t, err, errAudioRequest)

	err = dec.DecodeAudio(t.Context(), AudioRequest{Path: filepath.Join(t.TempDir(), "missing.wav"), SampleRate: 48000, Channels: 2}, noop)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream 0")
}

// BenchmarkDeinterleave splits 100 ms of 48 kHz stereo.
func BenchmarkDeinterleave(b *testing.B) {
	const frames = 4800

	buf := interleaved(frames, 2)
	planes := [][]float32{make([]float32, frames), make([]float32, frames)}

	b.SetBytes(int64(len(buf)))

	for b.Loop() {
		deinterleave(buf, planes, frames)
	}
}
