package decode

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

// TestVideoToolboxFramesMatchCPU decodes a segment with VideoToolbox and
// on the CPU: H.264 and HEVC decoding is bit-exact, so the frames must be.
func TestVideoToolboxFramesMatchCPU(
	t *testing.T,
) {
	if runtime.GOOS != darwin {
		t.Skip("VideoToolbox is macOS only")
	}

	hdr := testutil.HDRClip("smpte2084")
	hdr.Seconds, hdr.GOP = 1, 10

	testCases := []struct {
		name string
		clip testutil.Clip
		pool frame.PoolOptions
	}{
		{name: "8-bit h264 luma", clip: testutil.Clip{Seconds: 1, GOP: 10}, pool: frame.PoolOptions{ThumbMaxWidth: 80}},
		{
			name: "10-bit hevc, 10-bit chroma",
			clip: testutil.Clip{Seconds: 1, GOP: 10, Codec: "libx265", PixelFormat: "yuv420p10le", Args: []string{"-x265-params", "log-level=error"}},
			pool: frame.PoolOptions{Chroma: true, HighBitDepth: true},
		},
		{name: "10-bit hevc, luma and sample grid", clip: hdr, pool: frame.PoolOptions{SampleStep: 4}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := testutil.Generate(t, testCase.clip)
			pool := frame.NewPool(clipWidth, clipHeight, testCase.pool)
			req := Request{
				Path: path, Pool: pool, SourceWidth: clipWidth, SourceHeight: clipHeight,
				FrameRate: clipRate, Start: media.Seconds(0.4), FirstIndex: 10, MaxFrames: 8, Segment: true,
				PixelFormat: testCase.clip.PixelFormat,
			}
			if req.PixelFormat == "" {
				req.PixelFormat = "yuv420p"
			}

			decodePlanes := func(d *FFmpeg) [][]byte {
				var planes [][]byte

				require.NoError(t, d.Decode(t.Context(), req, func(f *frame.Frame) error {
					defer f.Release()

					for _, p := range append(pool.Planes(f), pool.SamplePlanes(f)...) {
						planes = append(planes, append([]byte(nil), p.Pix...))
					}

					return nil
				}))

				return planes
			}

			vt := NewFFmpeg("ffmpeg", 0, WithHWAccel(HWAccelAuto))
			hardware := decodePlanes(vt)
			cpu := decodePlanes(NewFFmpeg("ffmpeg", 0))

			assert.Equal(t, HWAccelVideoToolbox, vt.modeFor(req), "no fallback to the cpu")
			require.Len(t, cpu, 8*(len(pool.Planes(pool.Get()))+3))
			assert.Equal(t, cpu, hardware)
		})
	}
}
