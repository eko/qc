package overlay_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/overlay"
	"github.com/eko/qc/probe"
)

// Size of the integration clip.
const (
	clipWidth  = 640
	clipHeight = 360
)

// TestRenderIntegration analyses a synthetic clip with audio, renders its
// annotated copy with the real ffmpeg and checks the copy: same frames,
// frame rate and duration as the source, audio kept, the overlay drawn in
// the top-left corner and the middle of the picture left alone.
func TestRenderIntegration(
	t *testing.T,
) {
	testutil.RequireFilter(t, "subtitles")

	source := testutil.Generate(t, testutil.Clip{
		Width: clipWidth, Height: clipHeight, Rate: 30, Seconds: 2, Audio: true, GOP: 15,
		Args: []string{"-r", "30000/1001"},
	})

	dec := decode.NewFFmpeg("ffmpeg", 0)
	report, err := analysis.New(nil, probe.NewFFprobe("ffprobe"), bitstream.NewFFprobeReader("ffprobe"), dec, nil).
		Analyze(t.Context(), source, analysis.Options{})
	require.NoError(t, err)

	output := filepath.Join(t.TempDir(), "annotated.mp4")

	var last overlay.Progress

	err = overlay.NewRenderer(encode.NewFFmpeg("ffmpeg")).Render(t.Context(), source, output, overlay.Input{Report: report},
		overlay.RenderOptions{Progress: func(p overlay.Progress) { last = p }})
	require.NoError(t, err)

	want, got := probeStreams(t, source), probeStreams(t, output)
	assert.Equal(t, want.frames, got.frames, "every frame is kept")
	assert.Equal(t, want.rate, got.rate)
	assert.InDelta(t, want.duration, got.duration, 0.05)
	assert.True(t, got.audio, "the audio is kept")
	assert.Equal(t, overlay.Progress{Done: want.frames, Total: want.frames}, last)

	const frame = 20

	src, dst := grayFrame(t, source, frame), grayFrame(t, output, frame)
	assert.Greater(t, meanDifference(src, dst, 20, 20, 200, 60), 20.0, "the frame panel is drawn")
	assert.Less(t, meanDifference(src, dst, 250, 150, 140, 60), 6.0, "the middle of the picture is left alone")
}

// streams is what the integration test compares between the source and
// its copy.
type streams struct {
	frames   int
	rate     string
	duration float64
	audio    bool
}

// probeStreams counts the frames of the video of path and reads its frame
// rate, its duration and whether it has audio.
func probeStreams(
	t *testing.T,
	path string,
) streams {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-count_frames",
		"-show_entries", "stream=codec_type,nb_read_frames,avg_frame_rate:format=duration", "-of", "json", path).Output()
	require.NoError(t, err)

	var probed struct {
		Streams []struct {
			CodecType    string `json:"codec_type"`
			Frames       int    `json:"nb_read_frames,string"`
			AvgFrameRate string `json:"avg_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration float64 `json:"duration,string"`
		} `json:"format"`
	}
	require.NoError(t, json.Unmarshal(out, &probed))

	s := streams{duration: probed.Format.Duration}

	for _, stream := range probed.Streams {
		switch stream.CodecType {
		case "video":
			s.frames, s.rate = stream.Frames, stream.AvgFrameRate
		case "audio":
			s.audio = true
		}
	}

	return s
}

// grayFrame decodes the luma of frame n of path.
func grayFrame(
	t *testing.T,
	path string,
	n int,
) []byte {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", path,
		"-vf", "select=eq(n\\,"+strconv.Itoa(n)+")", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "gray", "-").Output()
	require.NoError(t, err)
	require.Len(t, out, clipWidth*clipHeight)

	return out
}

// meanDifference is the mean absolute difference of two gray frames over a
// w×h rectangle at (x, y).
func meanDifference(
	a, b []byte,
	x, y, w, h int,
) float64 {
	var sum int

	for row := y; row < y+h; row++ {
		start := row*clipWidth + x
		for i, v := range a[start : start+w] {
			d := int(v) - int(b[start+i])
			sum += max(d, -d)
		}
	}

	return float64(sum) / float64(w*h)
}
