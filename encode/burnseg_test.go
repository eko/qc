package encode

import (
	"cmp"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestPartArgs(
	t *testing.T,
) {
	spec := BurnSpec{Source: "in.ts", Subtitles: "o.ass", Output: "out.mp4", AudioCodec: "aac"}

	testCases := []struct {
		name    string
		spec    BurnSpec
		encoder BurnEncoder
		seg     BurnSegment
		want    []string
		notWant []string
	}{
		{
			name:    "first segment: from the start, cut at the next one",
			spec:    spec,
			encoder: BurnX264,
			seg:     BurnSegment{To: media.Seconds(10), Frames: 250},
			want: []string{
				"-progress pipe:1 -copyts -i in.ts -map 0:v:0 -vf trim=end=9.999000,scale=",
				"-fps_mode passthrough -c:v libx264 -preset fast -crf 20 -an out.mp4.part1.mp4",
			},
			notWant: []string{"-ss", "start=", "setpts", "0:a"},
		},
		{
			name:    "a segment seeks before its first frame, on the container timeline",
			spec:    BurnSpec{Source: "in.ts", Subtitles: "o.ass", Start: media.Seconds(1.4)},
			encoder: BurnVideoToolbox,
			seg:     BurnSegment{From: media.Seconds(11.4), To: media.Seconds(21.4), Frames: 250, Subtitles: "o-2.ass"},
			want: []string{
				"-hwaccel videotoolbox -copyts -seek_timestamp 1 -ss 8.400000 -noaccurate_seek -i in.ts",
				"-vf trim=start=11.399000:end=21.399000,setpts=PTS-1.400000/TB,scale=",
				"subtitles=filename=\\'o-2.ass\\'",
				"-c:v h264_videotoolbox",
			},
		},
		{
			name:    "a segment close to the start decodes from the start",
			spec:    spec,
			encoder: BurnX264,
			seg:     BurnSegment{From: media.Seconds(2), To: media.Seconds(4), Frames: 50},
			want:    []string{"-copyts -i in.ts", "trim=start=1.999000:end=3.999000,"},
			notWant: []string{"-ss"},
		},
		{
			name:    "last segment: to the end",
			spec:    spec,
			encoder: BurnX264,
			seg:     BurnSegment{From: media.Seconds(20), Frames: 250},
			want:    []string{"-ss 17.000000 -noaccurate_seek", "trim=start=19.999000,scale="},
			notWant: []string{"end="},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			args := strings.Join(partArgs(testCase.spec, testCase.encoder, testCase.seg, "out.mp4.part1.mp4"), " ")

			for _, w := range testCase.want {
				assert.Contains(t, args, w)
			}

			for _, w := range testCase.notWant {
				assert.NotContains(t, args, w)
			}
		})
	}
}

func TestJoinArgs(
	t *testing.T,
) {
	segments := []BurnSegment{{From: media.Seconds(0.3), To: media.Seconds(4)}, {From: media.Seconds(4)}}

	testCases := []struct {
		name string
		spec BurnSpec
		want string
	}{
		{
			name: "audio copied, video delayed after the start of the timeline",
			spec: BurnSpec{Source: "in.mkv", Output: "out.mp4", AudioCodec: "aac", Segments: segments},
			want: "-v error -nostdin -y -itsoffset 0.300000 -f concat -safe 0 -i list.txt -i in.mkv " +
				"-map 0:v:0 -c:v copy -map 1:a:0 -c:a copy -movflags +faststart out.mp4",
		},
		{
			name: "no audio, the video starting the timeline",
			spec: BurnSpec{Source: "in.mkv", Output: "out.mkv", Start: media.Seconds(0.3), Segments: segments},
			want: "-v error -nostdin -y -f concat -safe 0 -i list.txt -map 0:v:0 -c:v copy out.mkv",
		},
		{
			name: "audio an MP4 cannot hold",
			spec: BurnSpec{Source: "in.mov", Output: "out.mp4", AudioCodec: "pcm_s24le", Segments: segments[1:]},
			want: "-v error -nostdin -y -itsoffset 4.000000 -f concat -safe 0 -i list.txt -i in.mov " +
				"-map 0:v:0 -c:v copy -map 1:a:0 -c:a aac -b:a 192k -movflags +faststart out.mp4",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, strings.Join(joinArgs(testCase.spec, "list.txt"), " "))
		})
	}
}

func TestPartList(
	t *testing.T,
) {
	parts := partPaths(filepath.Join("dir", "it's.mp4"), 3)
	segments := []BurnSegment{
		{From: media.Seconds(0.04), To: media.Seconds(10.04)},
		{From: media.Seconds(10.04), To: media.Seconds(20.0802)},
		{From: media.Seconds(20.0802)},
	}

	want := "file 'it'\\''s.mp4.part1.mp4'\nduration 10.000000\n" +
		"file 'it'\\''s.mp4.part2.mp4'\nduration 10.040200\n" +
		"file 'it'\\''s.mp4.part3.mp4'\n"

	assert.Equal(t, filepath.Join("dir", "it's.mp4.part2.mp4"), parts[1])
	assert.Equal(t, want, partList(parts, segments))
}

func TestPartProgress(
	t *testing.T,
) {
	var reports []int

	p := &partProgress{done: make([]int, 2), report: func(n int) { reports = append(reports, n) }}
	p.update(0, 10)
	p.update(1, 5)
	p.update(0, 20)

	assert.Equal(t, []int{10, 15, 25}, reports)

	silent := &partProgress{done: make([]int, 1)}
	silent.update(0, 3)
	assert.Equal(t, []int{0}, silent.done, "nothing to report, nothing recorded")
}

// TestBurnSegments renders a clip with audio in two segments with the real
// ffmpeg and x264, and checks the copy: every frame at the source's
// timestamps, the audio kept, progress over the whole title, the segment
// files removed.
func TestBurnSegments(
	t *testing.T,
) {
	testutil.RequireFilter(t, "subtitles")

	source := testutil.Generate(t, testutil.Clip{Seconds: 4, GOP: 25, Audio: true})
	script := filepath.Join(t.TempDir(), "o.ass")
	require.NoError(t, os.WriteFile(script, []byte(testScript), 0o600))

	dir := t.TempDir()
	output := filepath.Join(dir, "out.mp4")

	var (
		mu     sync.Mutex
		frames []int
	)

	err := NewFFmpeg("ffmpeg").Burn(t.Context(), BurnSpec{
		Source: source, Subtitles: script, Output: output, Encoder: BurnX264, Preset: "ultrafast",
		AudioCodec: "aac", Workers: 2,
		Segments: []BurnSegment{
			{From: 0, To: media.Seconds(2), Frames: 50},
			{From: media.Seconds(2), Frames: 50, Subtitles: script},
		},
		Progress: func(n int) {
			mu.Lock()
			defer mu.Unlock()

			frames = append(frames, n)
		},
	})
	require.NoError(t, err)

	assert.Equal(t, probePTS(t, source), probePTS(t, output), "every frame, at the source's timestamps")
	assert.Equal(t, 100, frames[len(frames)-1])

	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_entries", "stream=codec_type",
		"-of", "csv=p=0", output).Output()
	require.NoError(t, err)
	assert.Equal(t, "video\naudio", strings.TrimSpace(string(out)))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the segments and their list are removed")
}

// TestBurnSegmentsFailures covers the failures of a segmented burn: a
// segment rendering another frame count than planned, ffmpeg failing on a
// segment or on the join, a list that cannot be written.
func TestBurnSegmentsFailures(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		body    string
		output  string
		wantErr error
		wantMsg string
	}{
		{name: "a frame missing", body: "echo frame=24", wantErr: ErrBurnSegment, wantMsg: "rendered 24 frames instead of 25"},
		{name: "a segment failing", body: "exit 3", wantMsg: "burn o.ass into in.mp4: segment "},
		{name: "the join failing", body: `case "$*" in *concat*) exit 3;; esac; echo frame=25`, wantMsg: "join the segments of"},
		{name: "no room for the list", body: "echo frame=25", output: filepath.Join("missing", "out.mp4"), wantMsg: "burn o.ass into in.mp4"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), testCase.output)
			if testCase.output == "" {
				output = filepath.Join(t.TempDir(), "out.mp4")
			}

			err := NewFFmpeg(testutil.FakeFFmpeg(t, testCase.body)).Burn(t.Context(), BurnSpec{
				Source: "in.mp4", Subtitles: "o.ass", Output: output, Encoder: BurnX264, Workers: 2,
				Segments: []BurnSegment{{To: media.Seconds(1), Frames: 25}, {From: media.Seconds(1), Frames: 25}},
			})

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			}

			require.ErrorContains(t, err, testCase.wantMsg)
		})
	}
}

// probePTS lists the presentation times of the video frames of path, in
// presentation order.
func probePTS(
	t *testing.T,
	path string,
) []string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time", "-of", "csv=p=0", path).Output()
	require.NoError(t, err)

	pts := strings.Fields(string(out))
	slices.SortFunc(pts, func(a, b string) int {
		x, _ := strconv.ParseFloat(a, 64)
		y, _ := strconv.ParseFloat(b, 64)

		return cmp.Compare(x, y)
	})

	return pts
}
