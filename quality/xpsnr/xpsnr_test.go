package xpsnr

import (
	"bufio"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
)

// Tolerances against ffmpeg: its per-frame metadata are single-precision
// floats printed with 6 decimals, its pooled values have 4 decimals.
const (
	frameTolerance  = 1e-4
	pooledTolerance = 1e-4
)

// clip describes raw test content: testsrc2 as the reference, the same
// with temporal noise as the distorted video.
type clip struct {
	width, height int
	rate          int
	frames        int
	pixFmt        string
	depth         int
}

// rawVideo writes the reference and distorted raw videos of c.
func rawVideo(
	t *testing.T,
	c clip,
) (ref, dist string) {
	t.Helper()

	dir := t.TempDir()
	source := "testsrc2=size=" + strconv.Itoa(c.width) + "x" + strconv.Itoa(c.height) + ":rate=" + strconv.Itoa(c.rate)

	for name, filter := range map[string]string{"ref.yuv": "format=" + c.pixFmt, "dist.yuv": "noise=alls=14:allf=t+u,format=" + c.pixFmt} {
		out, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi", "-i", source,
			"-frames:v", strconv.Itoa(c.frames), "-vf", filter, "-f", "rawvideo", filepath.Join(dir, name)).CombinedOutput()
		require.NoError(t, err, string(out))
	}

	return filepath.Join(dir, "ref.yuv"), filepath.Join(dir, "dist.yuv")
}

// ffmpegXPSNR runs ffmpeg's xpsnr filter with ref as its first input and
// returns the per-frame values (Y, U, V) and the pooled ones.
func ffmpegXPSNR(
	t *testing.T,
	c clip,
	ref, dist string,
) ([][3]float64, [3]float64) {
	t.Helper()

	// The logs are written in the working directory: paths in a filter
	// graph would need escaping.
	dir := t.TempDir()
	input := []string{"-f", "rawvideo", "-pix_fmt", c.pixFmt, "-s", strconv.Itoa(c.width) + "x" + strconv.Itoa(c.height), "-r", strconv.Itoa(c.rate)}

	args := append([]string{"-v", "error"}, input...)
	args = append(args, "-i", ref)
	args = append(args, input...)
	args = append(args, "-i", dist, "-lavfi", "[0:v][1:v]xpsnr=stats_file=stats.log,metadata=print:file=meta.log", "-f", "null", "-")

	cmd := exec.CommandContext(t.Context(), "ffmpeg", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	data, err := os.ReadFile(filepath.Join(dir, "meta.log"))
	require.NoError(t, err)

	var frames [][3]float64

	for _, m := range regexp.MustCompile(`xpsnr\.([yuv])=([0-9.]+|inf)`).FindAllStringSubmatch(string(data), -1) {
		plane := strings.Index("yuv", m[1])
		if plane == 0 {
			frames = append(frames, [3]float64{})
		}

		frames[len(frames)-1][plane] = parseDB(t, m[2])
	}

	data, err = os.ReadFile(filepath.Join(dir, "stats.log"))
	require.NoError(t, err)

	avg := regexp.MustCompile(`average.*y: ([0-9.]+|inf)\s+u: ([0-9.]+|inf)\s+v: ([0-9.]+|inf)`).FindStringSubmatch(string(data))
	require.Len(t, avg, 4, string(data))

	return frames, [3]float64{parseDB(t, avg[1]), parseDB(t, avg[2]), parseDB(t, avg[3])}
}

func parseDB(
	t *testing.T,
	s string,
) float64 {
	t.Helper()

	if s == "inf" {
		return math.Inf(1)
	}

	v, err := strconv.ParseFloat(s, 64)
	require.NoError(t, err)

	return v
}

// readFrames reads raw 4:2:0 frames.
func readFrames(
	t *testing.T,
	c clip,
	path string,
) []*frame.Frame {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	pool := frame.NewPool(c.width, c.height, frame.PoolOptions{Chroma: true, HighBitDepth: c.depth > 8})
	r := bufio.NewReader(f)

	var frames []*frame.Frame

	for {
		fr := pool.Get()

		for _, p := range pool.Planes(fr) {
			if _, err := io.ReadFull(r, p.Pix); err != nil {
				require.ErrorIs(t, err, io.EOF)

				return frames
			}
		}

		frames = append(frames, fr)
	}
}

// planeSize is the size of plane c of a 4:2:0 picture.
func planeSize(
	c clip,
	plane int,
) (int, int) {
	if plane == 0 {
		return c.width, c.height
	}

	return (c.width + 1) / 2, (c.height + 1) / 2
}

// TestMatchesFFmpeg checks per-frame and pooled XPSNR against ffmpeg's xpsnr
// filter on every code path: small pictures (min-smoothed weights), HD,
// high frame rates (second-order temporal activity), above-HD pictures
// (downsampled activity, with short or empty last blocks), 10 bits, and
// pictures too small to weight.
func TestMatchesFFmpeg(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	filters, err := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-filters").Output()
	require.NoError(t, err)

	if !strings.Contains(string(filters), " xpsnr ") {
		t.Skip("ffmpeg has no xpsnr filter")
	}

	testCases := []struct {
		name    string
		clip    clip
		workers int
	}{
		{name: "small, min-smoothed weights", clip: clip{width: 320, height: 180, rate: 25, frames: 6, pixFmt: "yuv420p", depth: 8}},
		{name: "small, 10 bits", clip: clip{width: 640, height: 360, rate: 25, frames: 5, pixFmt: "yuv420p10le", depth: 10}},
		{name: "HD, parallel rows", clip: clip{width: 1280, height: 720, rate: 25, frames: 5, pixFmt: "yuv420p", depth: 8}, workers: 4},
		{name: "high frame rate", clip: clip{width: 640, height: 360, rate: 60, frames: 6, pixFmt: "yuv420p", depth: 8}},
		{name: "above HD, downsampled", clip: clip{width: 2560, height: 1440, rate: 25, frames: 3, pixFmt: "yuv420p", depth: 8}, workers: 3},
		{name: "above HD, 10 bits, 50 fps, short last block", clip: clip{width: 3848, height: 1216, rate: 50, frames: 3, pixFmt: "yuv420p10le", depth: 10}},
		{name: "above HD, last block without activity", clip: clip{width: 3842, height: 1216, rate: 25, frames: 2, pixFmt: "yuv420p", depth: 8}},
		{name: "too small to weight", clip: clip{width: 40, height: 30, rate: 25, frames: 4, pixFmt: "yuv420p", depth: 8}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c := testCase.clip
			refPath, distPath := rawVideo(t, c)
			want, wantPooled := ffmpegXPSNR(t, c, refPath, distPath)

			refs, dists := readFrames(t, c, refPath), readFrames(t, c, distPath)
			require.Len(t, refs, c.frames)
			require.Len(t, want, c.frames)

			m := New(c.width, c.height, c.depth, float64(c.rate), testCase.workers)

			var sum Distortion

			for i := range refs {
				d := m.Measure(refs[i], dists[i])

				for plane := range planes {
					w, h := planeSize(c, plane)
					assert.InDelta(t, want[i][plane], Decibels(d[plane], w, h, c.depth), frameTolerance, "frame %d plane %d", i, plane)
					sum[plane] += d[plane]
				}
			}

			for plane := range planes {
				w, h := planeSize(c, plane)
				assert.InDelta(t, wantPooled[plane], Decibels(sum[plane]/float64(c.frames), w, h, c.depth), pooledTolerance, "plane %d", plane)
			}
		})
	}
}

// uniformFrame returns a frame of the pool filled with value v.
func uniformFrame(
	pool *frame.Pool,
	v uint16,
) *frame.Frame {
	f := pool.Get()

	for _, p := range pool.Planes(f) {
		for i := 0; i < len(p.Pix); i += p.BytesPerSample {
			if p.BytesPerSample == 2 {
				binary.LittleEndian.PutUint16(p.Pix[i:], v)

				continue
			}

			p.Pix[i] = byte(v)
		}
	}

	return f
}

// gradientFrame returns a frame whose samples vary with position and seed.
func gradientFrame(
	pool *frame.Pool,
	seed int,
) *frame.Frame {
	f := pool.Get()

	for _, p := range pool.Planes(f) {
		for i := range p.Pix {
			p.Pix[i] = byte((i*7 + seed*31 + (i/p.Stride)*13) % 251)
		}
	}

	return f
}

func TestHistory(
	t *testing.T,
) {
	const w, h = 320, 180

	pool := frame.NewPool(w, h, frame.PoolOptions{Chroma: true})
	a, b, c := gradientFrame(pool, 1), gradientFrame(pool, 2), gradientFrame(pool, 3)

	testCases := []struct {
		name  string
		rate  float64
		check func(t *testing.T, m *Meter)
	}{
		{
			name: "reset replays a sequence identically",
			rate: 25,
			check: func(t *testing.T, m *Meter) {
				first := []Distortion{m.Measure(a, b), m.Measure(b, c)}

				m.Reset()
				assert.Equal(t, first, []Distortion{m.Measure(a, b), m.Measure(b, c)})
			},
		},
		{
			name: "priming makes a mid-video sequence exact",
			rate: 25,
			check: func(t *testing.T, m *Meter) {
				m.Measure(a, a)
				want := m.Measure(b, c)

				m.Reset()
				m.Prime(a)
				m.Measure(a, a)
				assert.Equal(t, want, m.Measure(b, c))
			},
		},
		{
			name: "priming at high frame rates fills both history frames",
			rate: 60,
			check: func(t *testing.T, m *Meter) {
				m.Prime(a)
				assert.Equal(t, m.prev1, m.prev2)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.check(t, New(w, h, 8, testCase.rate, 1))
		})
	}
}

func TestPrimeTenBits(
	t *testing.T,
) {
	pool := frame.NewPool(64, 64, frame.PoolOptions{Chroma: true, HighBitDepth: true})
	f := uniformFrame(pool, 700)

	m := New(64, 64, 10, 25, 1)
	m.Prime(f)

	assert.Equal(t, int16(700), m.prev1[0])
	assert.Equal(t, int16(700), m.prev1[len(m.prev1)-1])
}

func TestIdenticalFrames(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		width  int
		height int
	}{
		{name: "weighted", width: 320, height: 180},
		{name: "too small to weight", width: 40, height: 30},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(testCase.width, testCase.height, frame.PoolOptions{Chroma: true})
			f := gradientFrame(pool, 4)

			d := New(testCase.width, testCase.height, 8, 25, 1).Measure(f, f)

			assert.Equal(t, Distortion{}, d)
			assert.True(t, math.IsInf(Decibels(d[0], testCase.width, testCase.height, 8), 1))
		})
	}
}

func TestDecibels(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		distortion float64
		width      int
		height     int
		depth      int
		want       float64
	}{
		// One unit of error per sample: the PSNR of a uniform ±1 error.
		{name: "8 bits", distortion: math.Sqrt(100 * 100), width: 100, height: 100, depth: 8, want: 20 * math.Log10(255)},
		{name: "10 bits", distortion: math.Sqrt(100 * 100), width: 100, height: 100, depth: 10, want: 20 * math.Log10(1023)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, Decibels(testCase.distortion, testCase.width, testCase.height, testCase.depth), 1e-9)
		})
	}
}

func BenchmarkMeasure(
	b *testing.B,
) {
	testCases := []struct {
		name          string
		width, height int
		depth         int
		workers       int
	}{
		{name: "1080p 8-bit", width: 1920, height: 1080, depth: 8, workers: 1},
		{name: "1080p 10-bit", width: 1920, height: 1080, depth: 10, workers: 1},
		{name: "1080p 8-bit, 4 workers", width: 1920, height: 1080, depth: 8, workers: 4},
		{name: "2160p 8-bit", width: 3840, height: 2160, depth: 8, workers: 1},
	}

	for _, testCase := range testCases {
		b.Run(testCase.name, func(b *testing.B) {
			pool := frame.NewPool(testCase.width, testCase.height, frame.PoolOptions{Chroma: true, HighBitDepth: testCase.depth > 8})
			ref, dist := gradientFrame(pool, 1), gradientFrame(pool, 2)

			if testCase.depth > 8 {
				// Keep 16-bit samples within 10 bits.
				for _, f := range []*frame.Frame{ref, dist} {
					for _, p := range pool.Planes(f) {
						for i := 1; i < len(p.Pix); i += 2 {
							p.Pix[i] &= 3
						}
					}
				}
			}

			m := New(testCase.width, testCase.height, testCase.depth, 25, testCase.workers)

			for b.Loop() {
				m.Measure(ref, dist)
			}
		})
	}
}
