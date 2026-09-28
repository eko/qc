package overlay

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/probe"
)

// Geometry of the timing clips: a bar barWidth wide per frame, moving by
// its width from frame to frame.
const (
	timingWidth  = 320
	timingHeight = 180
	barWidth     = 4
)

// TestBurnFrameAccuracy burns, with the real ffmpeg and libass, a script
// drawing a bar at a different place on every frame, and checks that every
// frame of the copy shows its own bar: the events land on the frames they
// were timed for, at non-integer frame rates too. The copy is burnt in one
// pass, then in segments joined without re-encoding (with x264, and with
// VideoToolbox on macOS), which must give the same frames at the same
// timestamps, seams included.
func TestBurnFrameAccuracy(
	t *testing.T,
) {
	testCases := []struct {
		name string
		clip testutil.Clip
		// generate, when set, makes the clip instead of testutil.Generate.
		generate func(t *testing.T) string
	}{
		{name: "25 fps", clip: testutil.Clip{Rate: 25}},
		{name: "23.976 fps", clip: testutil.Clip{Rate: 24, Args: []string{"-r", "24000/1001"}}},
		{name: "29.97 fps", clip: testutil.Clip{Rate: 30, Args: []string{"-r", "30000/1001"}}},
		{name: "59.94 fps in MPEG-TS", clip: testutil.Clip{Rate: 60, Seconds: 1, Name: "clip.ts", Args: []string{"-r", "60000/1001"}}},
		{
			// The segments seek on the container's timeline, far from 0.
			name: "MPEG-TS starting at 10 s", clip: testutil.Clip{Rate: 25, Name: "late.ts", Args: []string{"-output_ts_offset", "10"}},
		},
		{name: "video starting 0.3 s after the audio", generate: delayedVideo},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testutil.RequireFilter(t, "subtitles")

			var source string

			if testCase.generate != nil {
				source = testCase.generate(t)
			} else {
				clip := testCase.clip
				clip.Source, clip.Width, clip.Height, clip.GOP = "color", timingWidth, timingHeight, timingGOP
				source = testutil.Generate(t, clip)
			}

			report, err := analysis.New(nil, probe.NewFFprobe("ffprobe"), bitstream.NewFFprobeReader("ffprobe"), nil, nil).
				Analyze(t.Context(), source, analysis.Options{SkipVideo: true})
			require.NoError(t, err)

			title, err := newTitle(Input{Report: report})
			require.NoError(t, err)

			dir := t.TempDir()
			script := filepath.Join(dir, "bars.ass")
			writeBars(t, script, title)

			single := filepath.Join(dir, "bars.mkv")
			require.NoError(t, encode.NewFFmpeg("ffmpeg").Burn(t.Context(), encode.BurnSpec{
				Source: source, Subtitles: script, Output: single, Encoder: encode.BurnX264, CRF: 1, Preset: "ultrafast",
			}))
			checkBars(t, single, title.n())

			for _, encoder := range segmentEncoders() {
				segmented := filepath.Join(dir, "bars-"+encoder.String()+".mkv")
				burnSegmented(t, report, script, segmented, encoder)
				assert.Equal(t, framePTS(t, single), framePTS(t, segmented), "%s: the timestamps of a single pass", encoder)
				checkBars(t, segmented, title.n())
			}
		})
	}
}

// timingGOP is the keyframe interval of the timing clips: segments start
// at keyframes, a few per clip.
const timingGOP = 12

// segmentEncoders are the encoders the segmented burns are tested with:
// x264 everywhere, VideoToolbox on macOS.
func segmentEncoders() []encode.BurnEncoder {
	if runtime.GOOS == "darwin" {
		return []encode.BurnEncoder{encode.BurnX264, encode.BurnVideoToolbox}
	}

	return []encode.BurnEncoder{encode.BurnX264}
}

// burnSegmented burns script into a copy of the video of report in three
// segments, with slices of the script, as Render does for long titles.
func burnSegmented(
	t *testing.T,
	report *analysis.Report,
	script, output string,
	encoder encode.BurnEncoder,
) {
	t.Helper()

	spec := burnSpec(report.Info.Path, script, output, Input{Report: report},
		RenderOptions{Encoder: encoder, Workers: 2, CRF: 1, Preset: "ultrafast"})
	spec.Segments = segmentsAt(report.Bitstream, bitstream.SplitAtKeyframes(report.Bitstream.KeyFlags, 3, timingGOP-2))
	require.Len(t, spec.Segments, 3)
	require.NoError(t, sliceSegments(spec, t.TempDir()))

	require.NoError(t, encode.NewFFmpeg("ffmpeg").Burn(t.Context(), spec))
}

// checkBars checks that each of the frames frames of path shows its own
// bar (see writeBars).
func checkBars(
	t *testing.T,
	path string,
	frames int,
) {
	t.Helper()

	got := grayFrames(t, path)
	require.Len(t, got, frames)

	for i, f := range got {
		lo, hi := brightColumns(f)
		want := barWidth * (i % (timingWidth / barWidth))
		assert.Equal(t, []int{want, want + barWidth - 1}, []int{lo, hi}, "%s: frame %d", filepath.Base(path), i)
	}
}

// framePTS lists the presentation times of the video frames of path.
func framePTS(
	t *testing.T,
	path string,
) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "frame=pts_time", "-of", "csv=p=0", path).Output()
	require.NoError(t, err)

	// Keyframes carry an empty side data field.
	return strings.ReplaceAll(string(out), ",", "")
}

// delayedVideo is a Matroska clip whose video starts 0.3 s after its audio:
// ffmpeg shifts both by the container's start, and the video's first frame
// reaches the filters at 0.3 s.
func delayedVideo(
	t *testing.T,
) string {
	t.Helper()
	testutil.RequireFFmpeg(t)

	path := filepath.Join(t.TempDir(), "delayed.mkv")
	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=duration=2.5",
		"-itsoffset", "0.3", "-f", "lavfi", "-i", "color=size=320x180:rate=25:duration=2",
		"-map", "1:v", "-map", "0:a", "-c:v", "libx264", "-preset", "ultrafast", "-g", strconv.Itoa(timingGOP),
		"-c:a", "aac", path).CombinedOutput()
	require.NoError(t, err, string(out))

	return path
}

// writeBars writes a script drawing, on frame i, a white bar at column
// barWidth × i.
func writeBars(
	t *testing.T,
	path string,
	title *title,
) {
	t.Helper()

	f, err := os.Create(path)
	require.NoError(t, err)

	defer f.Close()

	w := bufio.NewWriter(f)
	clk := newClock(title.pts, title.duration, title.offset)
	writeHeader(w, canvas{width: timingWidth, height: timingHeight}, testFont)
	writeTrack(w, clk, track{layer: layerContent, text: func(i int) string {
		x := barWidth * (i % (timingWidth / barWidth))

		return shape(x, 0, colourWhite, alphaOpaque, rect(0, 0, barWidth, timingHeight))
	}})
	require.NoError(t, w.Flush())
}

// grayFrames decodes the luma of every frame of path.
func grayFrames(
	t *testing.T,
	path string,
) [][]byte {
	t.Helper()

	// Every frame once: no frame duplicated to fill the time before a
	// video starting after its audio.
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-i", path, "-map", "0:v:0", "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", "gray", "-")
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	var frames [][]byte

	for {
		buf := make([]byte, timingWidth*timingHeight)
		if _, err := io.ReadFull(out, buf); err != nil {
			break
		}

		frames = append(frames, buf)
	}

	require.NoError(t, cmd.Wait())

	return frames
}

// brightColumns returns the first and last bright columns of the middle row
// of a gray frame.
func brightColumns(
	frame []byte,
) (lo, hi int) {
	const bright = 128

	lo, hi = -1, -1
	row := frame[timingWidth*timingHeight/2 : timingWidth*(timingHeight/2+1)]

	for x, v := range row {
		if v > bright {
			if lo < 0 {
				lo = x
			}

			hi = x
		}
	}

	return lo, hi
}
