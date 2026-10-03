package encode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestCopyArgs(
	t *testing.T,
) {
	rate := media.Rational{Num: 25, Den: 1}
	part := CopyPart{Source: "film.mp4", Origin: media.Seconds(0.08)}
	seg := CopySegment{Start: media.Seconds(12), Frames: 50, Duration: media.Seconds(2)}

	// Seeked a quarter of a frame after the keyframe, on the timeline of
	// the container: ffmpeg starts the copy at the keyframe before.
	assert.Equal(t, []string{
		"-v", "error", "-nostdin", "-y", "-seek_timestamp", "1", "-ss", "12.090000", "-i", "film.mp4",
		"-map", "0:v:0", "-an", "-sn", "-dn", "-c", "copy", "-frames:v", "50", "-f", "mpegts", "0.ts",
	}, copySegmentArgs(part, seg, rate, "mpegts", "0.ts"))

	assert.Equal(t, []string{
		"-v", "error", "-nostdin", "-y", "-f", "concat", "-safe", "0", "-i", "list.txt", "-map", "0:v:0", "-c", "copy", "out.mkv",
	}, concatArgs("list.txt", "out.mkv"))

	assert.Equal(t, []string{
		"-v", "error", "-nostdin", "-xerror", "-err_detect", "explode", "-i", "out.mkv", "-map", "0:v:0", "-f", "null", "-",
	}, decodeArgs("out.mkv"))

	testCases := []struct {
		codec, format, ext string
	}{
		{codec: "h264", format: "mpegts", ext: ".ts"},
		{codec: "hevc", format: "mpegts", ext: ".ts"},
		{codec: "mpeg2video", format: "mpegts", ext: ".ts"},
		{codec: "prores", format: "matroska", ext: ".mkv"},
		{codec: "av1", format: "matroska", ext: ".mkv"},
	}

	for _, testCase := range testCases {
		format, ext := segmentFormat(testCase.codec)
		assert.Equal(t, testCase.format, format, testCase.codec)
		assert.Equal(t, testCase.ext, ext, testCase.codec)
	}
}

// packets reads the video packets of path.
func packets(
	t *testing.T,
	path string,
) *bitstream.Report {
	t.Helper()

	var list []media.Packet

	err := bitstream.NewFFprobeReader("ffprobe").ReadPackets(t.Context(), path, func(p media.Packet) error {
		list = append(list, p)

		return nil
	})
	require.NoError(t, err)

	report := bitstream.Analyze(list, bitstream.Options{})

	return &report
}

func TestCopy(
	t *testing.T,
) {
	// Two sources encoded with other settings: once joined, the second
	// must still decode with its own parameter sets.
	one := testutil.Generate(t, testutil.Clip{Name: "one.mp4", GOP: 25, Seconds: 4})
	two := testutil.Generate(t, testutil.Clip{Name: "two.mp4", GOP: 25, Seconds: 4, Source: "smptebars", Args: []string{"-crf", "30", "-bf", "0"}})

	testCases := []struct {
		name string
		ext  string
	}{
		{name: "matroska", ext: ".mkv"},
		{name: "mp4", ext: ".mp4"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "sample"+testCase.ext)

			var progress []int

			spec := CopySpec{
				Destination: destination, Codec: "h264", Rate: media.Rational{Num: 25, Den: 1}, Segments: 3,
				Parts: []CopyPart{
					{Source: one, Segments: []CopySegment{
						{Start: media.Seconds(1), Frames: 25, Duration: media.Seconds(1)},
						{Start: media.Seconds(3), Frames: 25, Duration: media.Seconds(1)},
					}},
					{Source: two, Segments: []CopySegment{{Start: media.Seconds(2), Frames: 50, Duration: media.Seconds(2)}}},
				},
				Progress: func(done int) { progress = append(progress, done) },
			}

			f := NewFFmpeg("ffmpeg")
			require.NoError(t, f.Copy(t.Context(), spec))
			assert.Equal(t, []int{1, 2, 3}, progress)

			// Every frame is there, at its own timestamp, and decodes.
			report := packets(t, destination)
			require.Equal(t, 100, report.PacketCount)

			for i := 1; i < len(report.PTS); i++ {
				assert.InDelta(t, 0.04, (report.PTS[i] - report.PTS[i-1]).Seconds(), 1e-3, "frame %d", i)
			}

			assert.Equal(t, 4, report.GOP.KeyframeCount, "the copies start at the keyframes asked")
			require.NoError(t, f.Decodes(t.Context(), destination))
		})
	}
}

func TestCopyErrors(
	t *testing.T,
) {
	source := testutil.Generate(t, testutil.Clip{Name: "one.mp4", GOP: 25, Seconds: 2})
	dir := t.TempDir()
	segment := []CopySegment{{Start: 0, Frames: 25, Duration: media.Seconds(1)}}
	f := NewFFmpeg("ffmpeg")

	testCases := []struct {
		name    string
		spec    CopySpec
		wantErr error
		wantMsg string
	}{
		{
			name:    "no segment",
			spec:    CopySpec{Destination: filepath.Join(dir, "out.mkv"), Codec: "h264", Parts: []CopyPart{{Source: source}}},
			wantErr: ErrNoSegments,
		},
		{
			name: "a missing source",
			spec: CopySpec{
				Destination: filepath.Join(dir, "out.mkv"), Codec: "h264", Rate: media.Rational{Num: 25, Den: 1},
				Parts: []CopyPart{{Source: filepath.Join(dir, "missing.mp4"), Segments: segment}},
			},
			wantMsg: "missing.mp4 at 0.000000",
		},
		{
			name: "a destination that cannot be written",
			spec: CopySpec{
				Destination: filepath.Join(dir, "no-such-dir", "out.mkv"), Codec: "h264", Rate: media.Rational{Num: 25, Den: 1},
				Parts: []CopyPart{{Source: source, Segments: segment}},
			},
			wantMsg: "join ",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := f.Copy(t.Context(), testCase.spec)
			require.Error(t, err)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			}

			assert.ErrorContains(t, err, testCase.wantMsg)
		})
	}

	// Without a temporary directory nothing can be copied.
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))

	err := f.Copy(t.Context(), CopySpec{Destination: filepath.Join(dir, "out.mkv"), Parts: []CopyPart{{Source: source, Segments: segment}}})
	require.ErrorContains(t, err, "copy to ")
}

func TestDecodes(
	t *testing.T,
) {
	f := NewFFmpeg("ffmpeg")
	clip := testutil.Generate(t, testutil.Clip{Name: "clip.mp4", GOP: 25, Seconds: 2})

	require.NoError(t, f.Decodes(t.Context(), clip))

	broken := filepath.Join(t.TempDir(), "broken.ts")
	ts := filepath.Join(t.TempDir(), "whole.ts")
	require.NoError(t, f.Copy(t.Context(), CopySpec{
		Destination: ts, Codec: "h264", Rate: media.Rational{Num: 25, Den: 1},
		Parts: []CopyPart{{Source: clip, Segments: []CopySegment{{Frames: 50, Duration: media.Seconds(2)}}}},
	}))

	data, err := os.ReadFile(ts)
	require.NoError(t, err)

	// Overwritten in its middle, the stream is damaged where it is read.
	for i := len(data) / 2; i < len(data)/2+4000 && i < len(data); i++ {
		data[i] = 0xFF
	}

	require.NoError(t, os.WriteFile(broken, data, 0o600))
	require.ErrorIs(t, f.Decodes(t.Context(), broken), ErrDecode)

	// A missing binary, or a cancelled run, is not a decoding error.
	err = NewFFmpeg("no-such-ffmpeg").Decodes(t.Context(), clip)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrDecode)
}
