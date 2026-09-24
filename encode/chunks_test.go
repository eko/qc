package encode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

var rate25 = media.Rational{Num: 25, Den: 1}

func TestChunkArgs(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		chunk     Chunk
		wantSeek  []string
		wantScale string
	}{
		{
			name: "first chunk starts at zero", chunk: Chunk{Start: 0, Frames: 50, CRF: 22},
			wantSeek: []string{"-ss", "0.000000", "-i", "in.mov", "-frames:v"}, wantScale: "scale=640:360:",
		},
		{
			name: "early chunk: preroll from the start", chunk: Chunk{Start: 25, Frames: 25, CRF: 26.5},
			wantSeek: []string{"-ss", "0.000000", "-i", "in.mov", "-ss", "0.980000", "-frames:v"}, wantScale: "scale=640:360:",
		},
		{
			name: "decode from a preroll earlier, drop to half a frame early", chunk: Chunk{Start: 150, Frames: 25, CRF: 26.5},
			wantSeek: []string{"-ss", "3.980000", "-i", "in.mov", "-ss", "2.000000", "-frames:v"}, wantScale: "scale=640:360:",
		},
		{
			name: "own resolution", chunk: Chunk{Start: 0, Frames: 50, CRF: 22, Width: 1280, Height: 720},
			wantSeek: []string{"-ss", "0.000000", "-i", "in.mov", "-frames:v"}, wantScale: "scale=1280:720:",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			args := codecs["h264"].chunkArgs("in.mov", "out.part001.mp4", rate25, testCase.chunk, Params{Width: 640, Height: 360, CRF: 99, GOP: 50})

			assert.Equal(t, testCase.wantSeek, args[:len(testCase.wantSeek)])
			assert.Equal(t, "out.part001.mp4", args[len(args)-1])

			joined := strings.Join(args, " ")
			assert.Contains(t, joined, "-crf "+strconv.FormatFloat(testCase.chunk.CRF, 'f', -1, 64)+" ")
			assert.Contains(t, joined, "-g 50")
			assert.NotContains(t, joined, "-crf 99")
			assert.Contains(t, joined, testCase.wantScale)
		})
	}
}

func TestChunkCommandLine(
	t *testing.T,
) {
	chunks := []Chunk{{Start: 0, Frames: 50, CRF: 24}, {Start: 50, Frames: 100, CRF: 27}}
	script := codecs["av1"].ChunkCommandLine("my source.mov", "01-1080p.mp4", rate25, chunks, Params{Width: 1920, Height: 1080, GOP: 50})

	lines := strings.Split(script, " && \\\n")
	require.Len(t, lines, 5)
	assert.True(t, strings.HasPrefix(lines[0], "ffmpeg -ss 0.000000 -i 'my source.mov' -frames:v 50 "), lines[0])
	assert.Contains(t, lines[0], "-crf 24")
	assert.True(t, strings.HasSuffix(lines[0], " 01-1080p.part001.mp4"))
	assert.Contains(t, lines[1], "-ss 1.980000")
	assert.Contains(t, lines[1], "-crf 27")
	assert.Equal(t, `printf "file '%s'\n" 01-1080p.part001.mp4 01-1080p.part002.mp4 > 01-1080p.mp4.txt`, lines[2])
	assert.Equal(t, "ffmpeg -f concat -safe 0 -i 01-1080p.mp4.txt -c copy 01-1080p.mp4", lines[3])
	assert.Equal(t, "rm 01-1080p.part001.mp4 01-1080p.part002.mp4 01-1080p.mp4.txt", lines[4])
}

func TestConcatList(
	t *testing.T,
) {
	assert.Equal(t, "file 'a.mp4'\nfile '/tmp/it'\\''s.mp4'\n", concatList([]string{"a.mp4", "/tmp/it's.mp4"}))
}

func TestEncodeChunksErrors(
	t *testing.T,
) {
	chunks := []Chunk{{Start: 0, Frames: 10, CRF: 30}}

	testCases := []struct {
		name    string
		bin     string
		chunks  []Chunk
		wantErr error
		wantMsg string
	}{
		{
			name:    "no chunk",
			bin:     "ffmpeg",
			wantErr: ErrNoChunks,
		},
		{
			name:    "missing binary",
			bin:     "qc-no-such-ffmpeg",
			chunks:  chunks,
			wantErr: ffexec.ErrNotFound,
			wantMsg: "chunk 1 with libx264",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out.mp4")
			err := NewFFmpeg(testCase.bin).EncodeChunks(t.Context(), codecs["h264"], "in.mov", dst, rate25, testCase.chunks, Params{Width: 64, Height: 36})
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Contains(t, err.Error(), testCase.wantMsg)
		})
	}
}

func TestEncodeChunksIntegration(
	t *testing.T,
) {
	src := testutil.Generate(t, testutil.Clip{Seconds: 4, GOP: 50})

	testCases := []struct {
		name    string
		chunks  []Chunk
		prepare func(t *testing.T, dst string)
		wantErr string
	}{
		{
			name:   "chunks are joined in order",
			chunks: []Chunk{{Start: 0, Frames: 50, CRF: 20}, {Start: 50, Frames: 25, CRF: 35}, {Start: 75, Frames: 25, CRF: 28}},
		},
		{
			name:    "the list cannot be written",
			chunks:  []Chunk{{Start: 0, Frames: 25, CRF: 30}},
			prepare: func(t *testing.T, dst string) { require.NoError(t, os.Mkdir(dst+".txt", 0o700)) },
			wantErr: "encode",
		},
		{
			name:    "a chunk past the end cannot be joined",
			chunks:  []Chunk{{Start: 500, Frames: 25, CRF: 30}},
			wantErr: "join chunks",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out.mp4")
			if testCase.prepare != nil {
				testCase.prepare(t, dst)
			}

			err := NewFFmpeg("ffmpeg").EncodeChunks(context.Background(), codecs["h264"], src, dst, rate25, testCase.chunks,
				Params{Width: 320, Height: 180, Preset: "ultrafast", GOP: 50})
			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)

			out, err := exec.Command("ffprobe", "-v", "error", "-count_frames", "-select_streams", "v:0",
				"-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", dst).Output()
			require.NoError(t, err)
			assert.Equal(t, "100", strings.TrimSpace(string(out)))

			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(dst), "out.part*"))
			require.NoError(t, err)
			assert.Empty(t, leftovers, "chunk files are removed")
			assert.NoFileExists(t, dst+".txt")
		})
	}
}

func TestEncodeChunksResolutions(
	t *testing.T,
) {
	src := testutil.Generate(t, testutil.Clip{Seconds: 3, GOP: 25})
	chunks := []Chunk{
		{Start: 0, Frames: 25, CRF: 30, Width: 320, Height: 180},
		{Start: 25, Frames: 25, CRF: 30, Width: 160, Height: 90},
		{Start: 50, Frames: 25, CRF: 30, Width: 320, Height: 180},
	}

	testCases := []struct {
		name   string
		codec  string
		preset string
	}{
		{name: "x264 repeats its SPS at every keyframe", codec: "h264", preset: "ultrafast"},
		{name: "x265 is joined through MPEG-TS", codec: "hevc", preset: "ultrafast"},
		{name: "SVT-AV1 repeats its sequence header", codec: "av1", preset: "12"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out.mp4")

			require.NoError(t, NewFFmpeg("ffmpeg").EncodeChunks(t.Context(), codecs[testCase.codec], src, dst, rate25, chunks,
				Params{Preset: testCase.preset, GOP: 25}))

			// Every frame decodes cleanly at its chunk's size.
			out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "frame=width",
				"-of", "csv=p=0", dst).CombinedOutput()
			require.NoError(t, err, string(out))

			widths := strings.Fields(strings.ReplaceAll(string(out), ",", " "))
			require.Len(t, widths, 75)
			assert.Equal(t, []string{"320", "160", "320"}, []string{widths[0], widths[25], widths[74]})

			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(dst), "out.part*"))
			require.NoError(t, err)
			assert.Empty(t, leftovers, "chunk and TS files are removed")
		})
	}
}

func TestJoinTransportError(
	t *testing.T,
) {
	src := testutil.Generate(t, testutil.Clip{Seconds: 2, GOP: 25})
	dst := filepath.Join(t.TempDir(), "out.mp4")
	require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(dst), "out.part001.mp4.ts"), 0o700))

	err := NewFFmpeg("ffmpeg").EncodeChunks(t.Context(), codecs["hevc"], src, dst, rate25,
		[]Chunk{{Start: 0, Frames: 25, CRF: 30, Width: 320, Height: 180}, {Start: 25, Frames: 25, CRF: 30, Width: 160, Height: 90}},
		Params{Preset: "ultrafast", GOP: 25})
	require.ErrorContains(t, err, "join chunks")
}

func TestChunkCommandLineTransport(
	t *testing.T,
) {
	chunks := []Chunk{{Start: 0, Frames: 50, CRF: 24, Width: 1920, Height: 1080}, {Start: 50, Frames: 50, CRF: 27, Width: 1280, Height: 720}}

	testCases := []struct {
		name   string
		codec  string
		chunks []Chunk
		want   []string
	}{
		{
			name: "HEVC changing resolution", codec: "hevc", chunks: chunks,
			want: []string{
				"ffmpeg -i p.part001.mp4 -c copy -f mpegts p.part001.mp4.ts",
				"ffmpeg -i p.part002.mp4 -c copy -f mpegts p.part002.mp4.ts",
				"ffmpeg -i 'concat:p.part001.mp4.ts|p.part002.mp4.ts' -c copy -tag:v hev1 p.mp4",
				"rm p.part001.mp4 p.part002.mp4 p.part001.mp4.ts p.part002.mp4.ts",
			},
		},
		{
			name: "HEVC at one resolution", codec: "hevc", chunks: []Chunk{chunks[0], {Start: 50, Frames: 50, CRF: 27, Width: 1920, Height: 1080}},
			want: []string{"ffmpeg -f concat -safe 0 -i p.mp4.txt -c copy p.mp4"},
		},
		{name: "H.264 changing resolution", codec: "h264", chunks: chunks, want: []string{"ffmpeg -f concat -safe 0 -i p.mp4.txt -c copy p.mp4"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lines := strings.Split(codecs[testCase.codec].ChunkCommandLine("s.mov", "p.mp4", rate25, testCase.chunks, Params{GOP: 50}), " && \\\n")
			assert.Subset(t, lines, testCase.want)
		})
	}
}
