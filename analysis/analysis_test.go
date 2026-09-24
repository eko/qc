package analysis_test

import (
	"log/slog"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf/libvmaf"
)

// encodeClip generates a short synthetic clip with a fixed GOP using ffmpeg.
func encodeClip(
	t *testing.T,
	gop int,
) string {
	t.Helper()

	testutil.RequireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.CommandContext(t.Context(), "ffmpeg",
		"-v", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x272:rate=25:duration=2",
		"-f", "lavfi", "-i", "color=black:size=640x272:rate=25:duration=1",
		"-f", "lavfi", "-i", "mandelbrot=size=640x272:rate=25,trim=duration=1",
		"-filter_complex", "[0][1][2]concat=n=3,pad=640:360:0:44:black,format=yuv420p",
		"-c:v", "libx264", "-preset", "ultrafast", "-color_range", "tv",
		"-g", strconv.Itoa(gop), "-keyint_min", strconv.Itoa(gop), "-sc_threshold", "0",
		"-pix_fmt", "yuv420p",
		path,
	)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	return path
}

func TestAnalyzerIntegration(
	t *testing.T,
) {
	path := encodeClip(t, 25)

	report, err := newAnalyzer(nil).Analyze(t.Context(), path, analysis.Options{})
	require.NoError(t, err)

	video, ok := report.Info.PrimaryVideo()
	require.True(t, ok)
	assert.Equal(t, "h264", video.Codec)
	assert.Equal(t, 640, video.Width)
	assert.Equal(t, media.Rational{Num: 25, Den: 1}, video.AvgFrameRate)

	bs := report.Bitstream
	require.NotNil(t, bs)
	assert.Equal(t, 100, bs.PacketCount)
	assert.Equal(t, media.Seconds(4), bs.Duration)
	assert.Equal(t, 4, bs.GOP.KeyframeCount)
	assert.Equal(t, media.Seconds(1), bs.GOP.MeanInterval)
	assert.True(t, bs.GOP.Fixed)
	assert.Greater(t, bs.PeakBitrate, bs.AverageBitrate)
	assert.Len(t, bs.Bitrate, 4)

	v := report.Video
	require.NotNil(t, v)
	assert.Equal(t, 100, v.FramesDecoded)
	require.Len(t, v.Shots, 3)
	assert.Equal(t, media.Seconds(2), v.Shots[1].Start)
	assert.Equal(t, media.Seconds(3), v.Shots[2].Start)
	assert.Zero(t, v.Shots[1].SIMean, "the black shot has no texture")
	assert.Equal(t, []media.Interval{{Start: media.Seconds(2), End: media.Seconds(3)}}, v.Black.Segments)
	assert.Empty(t, v.Freeze.Segments)
	assert.Equal(t, 44, v.Crop.Content.Y)
	assert.Equal(t, 272, v.Crop.Content.Height)
	assert.True(t, v.Crop.Letterbox)
	assert.Greater(t, v.SITI.SISummary.Mean, 0.0)

	require.NotNil(t, report.Frames)
	assert.Len(t, report.Frames.SI, 100)
	assert.Len(t, report.Frames.PTS, 100)
	assert.True(t, report.Frames.Keyframe[0])
}

func TestAnalyzerMissingFile(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	_, err := newAnalyzer(nil).Analyze(t.Context(), filepath.Join(t.TempDir(), "missing.mp4"), analysis.Options{})
	require.Error(t, err)
}

func TestCompareIntegration(
	t *testing.T,
) {
	clip := testutil.Clip{Width: 64, Height: 36, Seconds: 0.4, GOP: 5}
	ref := testutil.Generate(t, clip)

	clip.Name, clip.Args = "dist.mp4", []string{"-crf", "40"}
	dist := testutil.Generate(t, clip)

	dec := decode.NewFFmpeg("ffmpeg", 0)
	analyzer := newAnalyzer(quality.NewMeter(dec, libvmaf.NewEngine()))

	testCases := []struct {
		name string
		dist string
	}{
		{name: "identical", dist: ref},
		{name: "degraded", dist: dist},
	}

	means := map[string]float64{}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cmp, err := analyzer.Compare(t.Context(), ref, testCase.dist, analysis.CompareOptions{
				Quality: quality.Options{Exact: true},
			})
			require.NoError(t, err)

			assert.Equal(t, quality.ModeExact, cmp.VMAF.Mode)
			assert.Equal(t, 10, cmp.VMAF.FramesScored)
			assert.Equal(t, 10, cmp.Distorted.Bitstream.PacketCount)

			means[testCase.name] = cmp.VMAF.Mean
		})
	}

	assert.Greater(t, means["identical"], means["degraded"]+5, "a crf 40 encode is clearly worse")
}

func newAnalyzer(
	meter *quality.Meter,
) *analysis.Analyzer {
	return analysis.New(
		slog.New(slog.DiscardHandler),
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		decode.NewFFmpeg("ffmpeg", 0),
		meter,
	)
}
