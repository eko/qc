package decode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

const (
	clipWidth  = 320
	clipHeight = 180
	clipFrames = 25 // 1 s at 25 fps
)

var clipRate = media.Rational{Num: 25, Den: 1}

// Nominal code values of a black limited-range picture: its samples survive
// decoding untouched, which makes range or depth conversions visible. A
// conversion to full range or a misread sample depth is far off.
const (
	black8Luma    = 16
	black8Chroma  = 128
	black10Luma   = 64
	black10Chroma = 512
	// sampleTolerance absorbs the rounding of the RGB to YUV conversion of the
	// generated clip (black encodes as 65 rather than 64 in 10 bits).
	sampleTolerance = 1
)

// decoded is what the tests keep from each frame.
type decoded struct {
	Index int
	PTS   media.Duration
}

func decodeAll(
	t *testing.T,
	req Request,
	inspect func(f *frame.Frame),
) []decoded {
	t.Helper()

	var frames []decoded

	err := NewFFmpeg("ffmpeg", 0).Decode(t.Context(), req, func(f *frame.Frame) error {
		defer f.Release()

		frames = append(frames, decoded{Index: f.Index, PTS: f.PTS})

		if inspect != nil {
			inspect(f)
		}

		return nil
	})
	require.NoError(t, err)

	return frames
}

func blackClip(
	t *testing.T,
	pixelFormat string,
) string {
	t.Helper()

	return testutil.Generate(t, testutil.Clip{Source: "color", Seconds: 1, GOP: 10, PixelFormat: pixelFormat})
}

func TestFFmpegDecodeFrames(
	t *testing.T,
) {
	path := blackClip(t, "")

	testCases := []struct {
		name  string
		req   Request
		first decoded
		last  decoded
		count int
	}{
		{
			name:  "every frame timestamped from the frame rate",
			req:   Request{},
			count: clipFrames,
			first: decoded{Index: 0, PTS: 0},
			last:  decoded{Index: 24, PTS: media.Seconds(0.96)},
		},
		{
			name:  "known timestamps are used",
			req:   Request{PTS: []media.Duration{media.Seconds(10), media.Seconds(10.04)}},
			count: clipFrames,
			first: decoded{Index: 0, PTS: media.Seconds(10)},
			last:  decoded{Index: 24, PTS: media.Seconds(0.96)},
		},
		{
			name:  "seek and frame limit",
			req:   Request{Start: media.Seconds(0.4), FirstIndex: 10, MaxFrames: 5, Threads: 1},
			count: 5,
			first: decoded{Index: 10, PTS: media.Seconds(0.4)},
			last:  decoded{Index: 14, PTS: media.Seconds(0.56)},
		},
		{
			name:  "selection",
			req:   Request{Select: [][2]int{{2, 4}, {20, 22}}},
			count: 4,
			first: decoded{Index: 2, PTS: media.Seconds(0.08)},
			last:  decoded{Index: 21, PTS: media.Seconds(0.84)},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := testCase.req
			req.Path = path
			req.Pool = frame.NewPool(clipWidth, clipHeight, frame.PoolOptions{})
			req.SourceWidth, req.SourceHeight = clipWidth, clipHeight
			req.FrameRate = clipRate

			frames := decodeAll(t, req, nil)

			require.Len(t, frames, testCase.count)
			assert.Equal(t, testCase.first, frames[0])
			assert.Equal(t, testCase.last.Index, frames[len(frames)-1].Index)
			assert.InDelta(t, testCase.last.PTS.Seconds(), frames[len(frames)-1].PTS.Seconds(), 1e-6)
		})
	}
}

func TestFFmpegDecodePixels(
	t *testing.T,
) {
	path8 := blackClip(t, "yuv420p")
	path10 := blackClip(t, "yuv420p10le")

	testCases := []struct {
		name       string
		path       string
		width      int
		height     int
		opts       frame.PoolOptions
		wantLuma   int
		wantChroma int
	}{
		{name: "8-bit luma keeps limited range", path: path8, width: 320, height: 180, wantLuma: black8Luma},
		{
			name: "scaled luma with thumbnail", path: path8, width: 160, height: 90,
			opts: frame.PoolOptions{ThumbMaxWidth: 80}, wantLuma: black8Luma,
		},
		{
			name: "scaled 10-bit luma", path: path10, width: 160, height: 90,
			opts: frame.PoolOptions{HighBitDepth: true}, wantLuma: black10Luma,
		},
		{
			name: "scaled chroma", path: path8, width: 160, height: 90,
			opts: frame.PoolOptions{Chroma: true}, wantLuma: black8Luma, wantChroma: black8Chroma,
		},
		{
			name: "8-bit chroma", path: path8, width: 320, height: 180,
			opts: frame.PoolOptions{Chroma: true}, wantLuma: black8Luma, wantChroma: black8Chroma,
		},
		{
			name: "10-bit chroma", path: path10, width: 320, height: 180,
			opts:     frame.PoolOptions{Chroma: true, HighBitDepth: true},
			wantLuma: black10Luma, wantChroma: black10Chroma,
		},
		{
			name: "10-bit luma only", path: path10, width: 320, height: 180,
			opts: frame.PoolOptions{HighBitDepth: true}, wantLuma: black10Luma,
		},
		{
			name: "8-bit source to 16-bit samples", path: path8, width: 320, height: 180,
			opts: frame.PoolOptions{HighBitDepth: true}, wantLuma: black10Luma,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(testCase.width, testCase.height, testCase.opts)
			req := Request{
				Path: testCase.path, Pool: pool, SourceWidth: clipWidth, SourceHeight: clipHeight,
				FrameRate: clipRate, MaxFrames: 2,
			}

			frames := decodeAll(t, req, func(f *frame.Frame) {
				assertUniform(t, &f.Luma, testCase.wantLuma)

				if testCase.opts.Chroma {
					assertUniform(t, &f.Cb, testCase.wantChroma)
					assertUniform(t, &f.Cr, testCase.wantChroma)
				}

				if testCase.opts.ThumbMaxWidth > 0 {
					assertUniform(t, &f.Thumb, testCase.wantLuma)
				}
			})
			assert.Len(t, frames, 2)
		})
	}
}

// assertUniform checks that every sample of p is within sampleTolerance of
// want.
func assertUniform(
	t *testing.T,
	p *frame.Plane,
	want int,
) {
	t.Helper()

	for y := range p.Height {
		row := p.Row(y)

		for x := range p.Width {
			got := int(row[x])
			if p.BytesPerSample == 2 {
				got = int(binary.LittleEndian.Uint16(row[2*x:]))
			}

			if got < want-sampleTolerance || got > want+sampleTolerance {
				require.InDelta(t, want, got, sampleTolerance, "sample (%d, %d)", x, y)
			}
		}
	}
}

func TestFFmpegDecodeErrors(
	t *testing.T,
) {
	path := blackClip(t, "")
	errStop := errors.New("stop")

	testCases := []struct {
		name      string
		path      string
		fn        func(*frame.Frame) error
		wantErrIs error
		wantErr   string
	}{
		{
			name: "callback error stops decoding",
			path: path,
			fn: func(f *frame.Frame) error {
				f.Release()

				return errStop
			},
			wantErrIs: errStop,
		},
		{
			name: "missing file",
			path: "does-not-exist.mp4",
			fn: func(f *frame.Frame) error {
				f.Release()

				return nil
			},
			wantErr: "decode does-not-exist.mp4",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := Request{
				Path: testCase.path, Pool: frame.NewPool(clipWidth, clipHeight, frame.PoolOptions{}),
				SourceWidth: clipWidth, SourceHeight: clipHeight, FrameRate: clipRate,
			}

			err := NewFFmpeg("ffmpeg", 0).Decode(t.Context(), req, testCase.fn)

			if testCase.wantErrIs != nil {
				require.ErrorIs(t, err, testCase.wantErrIs)
			}

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
			}
		})
	}
}

func TestReadFramesErrors(
	t *testing.T,
) {
	pool := frame.NewPool(2, 2, frame.PoolOptions{Chroma: true})
	frameSize := 4 + 1 + 1

	testCases := []struct {
		name      string
		input     []byte
		selection [][2]int
		wantErrIs error
		wantErr   string
	}{
		{
			name:      "truncated luma",
			input:     make([]byte, 2),
			wantErrIs: io.ErrUnexpectedEOF,
			wantErr:   "read frame 0: read plane 0",
		},
		{
			name:      "missing chroma",
			input:     make([]byte, frameSize+4),
			wantErrIs: io.ErrUnexpectedEOF,
			wantErr:   "read frame 1: read plane 1",
		},
		{
			name:      "more frames than selected",
			input:     make([]byte, 2*frameSize),
			selection: [][2]int{{0, 1}},
			wantErrIs: errTooManyFrames,
			wantErr:   "frame 1",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := Request{Pool: pool, Select: testCase.selection}

			err := readFrames(bytes.NewReader(testCase.input), req, func(f *frame.Frame) error {
				f.Release()

				return nil
			})

			require.ErrorIs(t, err, testCase.wantErrIs)
			assert.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

func TestReadFramesUnknownRate(
	t *testing.T,
) {
	req := Request{Pool: frame.NewPool(1, 1, frame.PoolOptions{}), Start: media.Seconds(3), FirstIndex: 7}

	var got []decoded

	err := readFrames(bytes.NewReader(make([]byte, 2)), req, func(f *frame.Frame) error {
		defer f.Release()

		got = append(got, decoded{Index: f.Index, PTS: f.PTS})

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, []decoded{{Index: 7, PTS: media.Seconds(3)}, {Index: 8, PTS: media.Seconds(3)}}, got)
}

// mixedClip joins two x264 chunks of a 3 s clip, frames 0–24 at 160×90 and
// 25–74 at 320×180, as per-shot resolution does; it returns the joined file
// and its two chunks.
func mixedClip(
	t *testing.T,
) (string, []string) {
	t.Helper()

	src := testutil.Generate(t, testutil.Clip{Seconds: 3, GOP: 25})
	dir := t.TempDir()
	parts := []string{filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")}

	for i, spec := range []struct {
		seek, frames, size string
	}{{"0", "25", "160:90"}, {"0.98", "50", "320:180"}} {
		out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-ss", spec.seek, "-i", src, "-frames:v", spec.frames,
			"-vf", "scale="+spec.size+",format=yuv420p", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", parts[i]).CombinedOutput()
		require.NoError(t, err, string(out))
	}

	list := filepath.Join(dir, "list.txt")
	require.NoError(t, os.WriteFile(list, []byte("file '"+parts[0]+"'\nfile '"+parts[1]+"'\n"), 0o600))

	joined := filepath.Join(dir, "joined.mp4")
	out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", list, "-c", "copy", joined).CombinedOutput()
	require.NoError(t, err, string(out))

	return joined, parts
}

func TestFFmpegDecodeResolutionChange(
	t *testing.T,
) {
	joined, parts := mixedClip(t)
	pool := frame.NewPool(clipWidth, clipHeight, frame.PoolOptions{Chroma: true})

	// alone decodes frame n of a chunk on its own, upscaled like the pool.
	alone := func(part string, n int) []byte {
		out, err := exec.Command("ffmpeg", "-v", "error", "-i", part,
			"-vf", fmt.Sprintf("select='eq(n\\,%d)',scale=320:180:flags=bicubic,format=yuv420p", n),
			"-frames:v", "1", "-f", "rawvideo", "-").Output()
		require.NoError(t, err)

		return out[:clipWidth*clipHeight]
	}

	lumas := map[int][]byte{}

	frames := decodeAll(t, Request{
		Path: joined, Pool: pool, SourceWidth: 160, SourceHeight: 90, FrameRate: clipRate,
		Select: [][2]int{{20, 30}, {40, 45}},
	}, func(f *frame.Frame) {
		lumas[f.Index] = bytes.Clone(f.Luma.Pix[:clipWidth*clipHeight])
	})

	indices := make([]int, len(frames))
	for i, f := range frames {
		indices[i] = f.Index
	}

	// The selection spans the change of resolution: a rebuilt filter graph
	// would restart select's frame counter and deliver later frames.
	assert.Equal(t, []int{20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 40, 41, 42, 43, 44}, indices)
	assert.Equal(t, alone(parts[0], 22), lumas[22], "frames scaled exactly as the chunk alone")
	assert.Equal(t, alone(parts[1], 15), lumas[40], "the frame selected after the change is the right one")
}
