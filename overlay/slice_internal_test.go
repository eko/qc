package overlay

import (
	"bufio"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

func TestParseCentis(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		in     string
		want   centis
		wantOK bool
	}{
		{name: "start", in: "0:00:00.00", want: 0, wantOK: true},
		{name: "every unit", in: "1:02:03.45", want: 372_345, wantOK: true},
		{name: "as centis writes it", in: centis(987_654).String(), want: 987_654, wantOK: true},
		{name: "no centiseconds", in: "0:00:01", wantOK: false},
		{name: "not a number", in: "0:0x:01.00", wantOK: false},
		{name: "negative", in: "0:00:-1.00", wantOK: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := parseCentis([]byte(testCase.in))
			assert.Equal(t, testCase.wantOK, ok)

			if ok {
				assert.Equal(t, testCase.want, got)
			}
		})
	}
}

func TestEventTimes(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		line      string
		wantStart centis
		wantEnd   centis
		wantEvent bool
	}{
		{
			name: "event", line: "Dialogue: 1,0:00:01.20,0:00:01.24,qc,,0,0,0,,{\\pos(1,2)}a, b\n",
			wantStart: 120, wantEnd: 124, wantEvent: true,
		},
		{name: "header", line: "PlayResX: 1920\n"},
		{name: "truncated event", line: "Dialogue: 1,0:00:01.20\n"},
		{name: "event with a bad time", line: "Dialogue: 1,soon,0:00:01.24,qc,,0,0,0,,a\n"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			start, end, ok := eventTimes([]byte(testCase.line))
			assert.Equal(t, testCase.wantEvent, ok)
			assert.Equal(t, testCase.wantStart, start)
			assert.Equal(t, testCase.wantEnd, end)
		})
	}
}

func TestSliceWindows(
	t *testing.T,
) {
	segs := []encode.BurnSegment{
		{From: media.Seconds(0.14), To: media.Seconds(30.14)},
		{From: media.Seconds(30.14)},
	}

	got := sliceWindows(segs, media.Seconds(0.1))

	assert.Equal(t, []window{{from: -96, to: 3104}, {from: 2904, to: math.MaxInt64}}, got)
}

// TestSliceScript slices a script in two windows: the header in both, each
// event in the windows it overlaps, long lines whole.
func TestSliceScript(
	t *testing.T,
) {
	long := strings.Repeat("l 1 1 ", 10_000)
	script := "[Script Info]\nPlayResX: 1920\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:00.00,0:01:00.00,qc,,0,0,0,,{\\p1}m 0 0 " + long + "{\\p0}\n" +
		"Dialogue: 1,0:00:01.00,0:00:02.00,qc,,0,0,0,,early\n" +
		"Dialogue: 1,0:00:29.50,0:00:30.50,qc,,0,0,0,,seam\n" +
		"Dialogue: 1,0:00:50.00,0:00:51.00,qc,,0,0,0,,late"

	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.ass")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o600))

	paths, err := sliceScript(path, dir, []window{{from: 0, to: 3000}, {from: 3000, to: math.MaxInt64}})
	require.NoError(t, err)
	require.Len(t, paths, 2)

	events := func(k int) []string {
		b, err := os.ReadFile(paths[k])
		require.NoError(t, err)

		slice := string(b)
		assert.True(t, strings.HasPrefix(slice, "[Script Info]\nPlayResX: 1920\n\n[Events]\nFormat: Layer"))
		assert.Contains(t, slice, long, "long lines are copied whole")

		var names []string

		for line := range strings.Lines(slice) {
			if strings.HasPrefix(line, "Dialogue: 1,") {
				names = append(names, line[strings.LastIndex(line, ",")+1:])
			}
		}

		return names
	}

	assert.Equal(t, []string{"early\n", "seam\n"}, events(0))
	assert.Equal(t, []string{"seam\n", "late"}, events(1))
}

func TestSliceScriptFailures(
	t *testing.T,
) {
	dir := t.TempDir()
	script := filepath.Join(dir, "overlay.ass")
	require.NoError(t, os.WriteFile(script, []byte("[Script Info]\n"), 0o600))

	testCases := []struct {
		name    string
		path    string
		dir     string
		wantErr error
	}{
		{name: "no script", path: filepath.Join(dir, "missing.ass"), dir: dir, wantErr: os.ErrNotExist},
		{name: "no directory for the slices", path: script, dir: filepath.Join(dir, "missing"), wantErr: os.ErrNotExist},
		{name: "a directory for a script", path: dir, dir: dir},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := sliceScript(testCase.path, testCase.dir, []window{{from: 0, to: 1}})
			require.Error(t, err)

			if testCase.wantErr != nil {
				assert.ErrorIs(t, err, testCase.wantErr)
			}
		})
	}
}

// errWriter fails every write.
type errWriter struct{}

var errDiskFull = errors.New("disk full")

func (errWriter) Write([]byte) (int, error) {
	return 0, errDiskFull
}

func TestDistributeReportsWritesAtFlush(
	t *testing.T,
) {
	w := bufio.NewWriterSize(errWriter{}, 16)
	r := bufio.NewReader(strings.NewReader("[Script Info]\nPlayResX: 1920 and a long line\n"))

	require.NoError(t, distribute(r, []*bufio.Writer{w}, []window{{from: 0, to: 1}}))
	assert.ErrorIs(t, w.Flush(), errDiskFull)
}

// longTitle is the inspection of a two-minute 25 fps title with a keyframe
// every two seconds: long enough for four segments.
func longTitle() *analysis.Report {
	const frames = 3000

	bs := &bitstream.Report{Duration: media.Seconds(frames * 0.04)}
	for i := range frames {
		bs.PTS = append(bs.PTS, media.Seconds(float64(i)*0.04))
		bs.FrameSizes = append(bs.FrameSizes, 1000)
		bs.KeyFlags = append(bs.KeyFlags, i%50 == 0)
	}

	return &analysis.Report{Bitstream: bs}
}

// TestPrepare writes the script and its slices, and fails when either
// cannot be written.
func TestPrepare(
	t *testing.T,
) {
	testCases := []struct {
		name string
		// setup prepares the directory and returns it.
		setup      func(t *testing.T) string
		wantSlices int
		wantErr    bool
	}{
		{name: "a script and four slices", setup: func(t *testing.T) string { return t.TempDir() }, wantSlices: 4},
		{
			name: "no room for the script",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing")
			},
			wantErr: true,
		},
		{
			name: "no room for a slice",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(dir, "overlay-1.ass"), 0o750))

				return dir
			},
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			spec, err := prepare(testCase.setup(t), "in.mp4", "out.mp4", Input{Report: longTitle()}, RenderOptions{})
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Len(t, spec.Segments, testCase.wantSlices)

			for _, seg := range spec.Segments {
				assert.FileExists(t, seg.Subtitles)
			}
		})
	}
}
