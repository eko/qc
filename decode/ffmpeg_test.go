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

// TestFFmpegSeekVideoStartingLate seeks in a clip whose video starts
// 0.2 s after its audio, i.e. after the container's timeline: seeking
// from the video's first frame (Origin) lands on the planned frame, while
// ffmpeg's default, counting from the container's start, lands 5 frames
// early.
func TestFFmpegSeekVideoStartingLate(
	t *testing.T,
) {
	const (
		delay = 0.2
		first = 20
		count = 3
	)

	path := testutil.Generate(t, testutil.Clip{Seconds: 2, GOP: 10, Audio: true, VideoDelay: delay})
	pool := frame.NewPool(clipWidth, clipHeight, frame.PoolOptions{})
	base := Request{Path: path, Pool: pool, SourceWidth: clipWidth, SourceHeight: clipHeight, FrameRate: clipRate}

	lumas := func(req Request) [][]byte {
		var out [][]byte

		require.NoError(t, NewFFmpeg("ffmpeg", 0).Decode(t.Context(), req, func(f *frame.Frame) error {
			defer f.Release()

			out = append(out, bytes.Clone(f.Luma.Pix))

			return nil
		}))

		return out
	}

	whole := lumas(base)
	require.Greater(t, len(whole), first+count)

	testCases := []struct {
		name      string
		origin    media.Duration
		wantFirst int
	}{
		{name: "from the video's first frame", origin: media.Seconds(delay), wantFirst: first},
		{name: "from the container's start", wantFirst: first - 5},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := base
			req.Start, req.Origin = media.Seconds(float64(first)/25), testCase.origin
			req.FirstIndex, req.MaxFrames = first, count

			assert.Equal(t, whole[testCase.wantFirst:testCase.wantFirst+count], lumas(req))
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
		{
			name: "sampling pool: 8-bit luma and 10-bit samples", path: path10, width: 320, height: 180,
			opts: frame.PoolOptions{SampleStep: 4, ThumbMaxWidth: 80}, wantLuma: black8Luma,
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

				if testCase.opts.SampleStep > 0 {
					assert.Equal(t, 80, f.Samples.Y.Width)
					assertUniform(t, &f.Samples.Y, black10Luma)
					assertUniform(t, &f.Samples.Cb, black10Chroma)
					assertUniform(t, &f.Samples.Cr, black10Chroma)
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
		pool      *frame.Pool
		input     []byte
		selection [][2]int
		wantErrIs error
		wantErr   string
		// grid is the extra output of a sampling pool.
		grid []byte
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
			name:      "truncated sample grid",
			pool:      frame.NewPool(2, 2, frame.PoolOptions{SampleStep: 2}),
			input:     make([]byte, 4),
			grid:      make([]byte, 2),
			wantErrIs: io.ErrUnexpectedEOF,
			wantErr:   "read frame 0: read plane 1",
		},
		{
			name:      "missing sample grid",
			pool:      frame.NewPool(2, 2, frame.PoolOptions{SampleStep: 2}),
			input:     make([]byte, 4),
			wantErrIs: io.ErrUnexpectedEOF,
			wantErr:   "read frame 0: sample grid",
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
			read := planeReader(bytes.NewReader(testCase.input), pool)

			if testCase.pool != nil {
				req.Pool = testCase.pool
				read = sampledReader(bytes.NewReader(testCase.input), bytes.NewReader(testCase.grid), testCase.pool)
			}

			err := readFrames(req, read, func(f *frame.Frame) error {
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

	err := readFrames(req, planeReader(bytes.NewReader(make([]byte, 2)), req.Pool), func(f *frame.Frame) error {
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

// TestFFmpegDecodeToneMap decodes a bright PQ clip with and without tone
// mapping: a PQ code value of 70% (about 1 000 cd/m²) is far above SDR
// white, and the perceptual mapping brings it into the SDR range, which a
// plain decode leaves at its PQ code value.
func TestFFmpegDecodeToneMap(
	t *testing.T,
) {
	clip := testutil.HDRClip(media.TransferPQ)
	clip.Source, clip.Seconds = "color", 0.2
	clip.Filter = "drawbox=c=0xB3B3B3:t=fill," + clip.Filter
	path := testutil.Generate(t, clip)

	testCases := []struct {
		name    string
		toneMap *ToneMap
		check   func(t *testing.T, luma float64)
	}{
		{
			name: "pq code values untouched",
			check: func(t *testing.T, luma float64) {
				assert.InDelta(t, 64+0.7*876, luma, 8)
			},
		},
		{
			name:    "tone mapped to sdr",
			toneMap: &ToneMap{Input: media.Color{Transfer: media.TransferPQ}},
			check: func(t *testing.T, luma float64) {
				// About 1 000 cd/m² maps near SDR white (852 measured):
				// far from its PQ code value, within the SDR range.
				assert.Greater(t, luma, 64+0.85*876)
				assert.LessOrEqual(t, luma, 940.0)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(clipWidth, clipHeight, frame.PoolOptions{Chroma: true, HighBitDepth: true})
			req := Request{
				Path: path, Pool: pool, SourceWidth: clipWidth, SourceHeight: clipHeight,
				FrameRate: clipRate, MaxFrames: 1, ToneMap: testCase.toneMap,
			}

			var luma float64

			decodeAll(t, req, func(f *frame.Frame) {
				luma = float64(f.Luma.Uint16()[clipWidth*clipHeight/2+clipWidth/2])
			})
			testCase.check(t, luma)
		})
	}
}

// TestReadFramesSampled reads two frames of a sampling pool: the 8-bit luma
// from one output, the 10-bit grid from the other.
func TestReadFramesSampled(
	t *testing.T,
) {
	pool := frame.NewPool(4, 2, frame.PoolOptions{SampleStep: 2})
	luma := []byte{1, 2, 3, 4, 5, 6, 7, 8, 11, 12, 13, 14, 15, 16, 17, 18}

	var grid []byte
	for v := range 12 {
		grid = append(grid, byte(v+1), 3) // 2 points × 3 planes × 2 frames, 0x03xx
	}

	var got [][2]uint16

	err := readFrames(Request{Pool: pool}, sampledReader(bytes.NewReader(luma), bytes.NewReader(grid), pool), func(f *frame.Frame) error {
		defer f.Release()

		got = append(got, [2]uint16{uint16(f.Luma.Pix[0]), f.Samples.Cr.Uint16()[1]})

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, [][2]uint16{{1, 0x0306}, {11, 0x030c}}, got)
}

// TestFFmpegDecodeSampleGridPoints decodes a frame whose every sample
// codes its position: the grid holds the luma pixel at the centre of each
// 4×4 cell and the chroma sample covering it, untouched.
func TestFFmpegDecodeSampleGridPoints(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	const width, height = 32, 16

	var raw []byte
	put := func(v int) { raw = append(raw, byte(v), byte(v>>8)) }

	for y := range height {
		for x := range width {
			put(64*y + x)
		}
	}

	for plane := range 2 {
		for y := range height / 2 {
			for x := range width / 2 {
				put(500*plane + 16*y + x)
			}
		}
	}

	dir := t.TempDir()
	rawPath, path := filepath.Join(dir, "frame.yuv"), filepath.Join(dir, "frame.nut")
	require.NoError(t, os.WriteFile(rawPath, raw, 0o600))

	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y", "-f", "rawvideo", "-pix_fmt", "yuv420p10le",
		"-s", fmt.Sprintf("%dx%d", width, height), "-i", rawPath, "-c:v", "rawvideo", path).CombinedOutput()
	require.NoError(t, err, string(out))

	pool := frame.NewPool(width, height, frame.PoolOptions{SampleStep: 4})
	req := Request{Path: path, Pool: pool, SourceWidth: width, SourceHeight: height, FrameRate: clipRate}

	decodeAll(t, req, func(f *frame.Frame) {
		ys, cbs, crs := f.Samples.Y.Uint16(), f.Samples.Cb.Uint16(), f.Samples.Cr.Uint16()

		for j := range 4 {
			for i := range 8 {
				k := j*8 + i
				assert.Equal(t, 64*(4*j+2)+4*i+2, int(ys[k]), "luma of point (%d, %d)", i, j)
				assert.Equal(t, 16*(2*j+1)+2*i+1, int(cbs[k]), "Cb of point (%d, %d)", i, j)
				assert.Equal(t, 500+16*(2*j+1)+2*i+1, int(crs[k]), "Cr of point (%d, %d)", i, j)
			}
		}
	})
}
