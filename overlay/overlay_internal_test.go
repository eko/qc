package overlay

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/quality"
)

// update rewrites the golden files instead of comparing with them:
// go test ./overlay -run TestWriteGolden -update.
var update = flag.Bool("update", false, "rewrite the golden files")

func TestWriteGolden(
	t *testing.T,
) {
	sampled := exactQuality()
	sampled.Mode = quality.ModeSampled
	sampled.FramesScored = 3
	sampled.Frames = []quality.FrameScore{sampled.Frames[2], sampled.Frames[3], sampled.Frames[9]}

	portrait := analysed()
	portrait.Info.Video[0].Width, portrait.Info.Video[0].Height = 1080, 1920

	testCases := []struct {
		name  string
		input Input
		opts  Options
	}{
		{
			name:  "portrait comparison",
			input: Input{Report: portrait, Quality: exactQuality()},
		},
		{
			name:  "inspection",
			input: Input{Report: inspection()},
		},
		{
			name:  "analysis",
			input: Input{Report: analysed()},
		},
		{
			name:  "comparison",
			input: Input{Report: analysed(), Quality: exactQuality()},
		},
		{
			name:  "sampled comparison",
			input: Input{Report: inspection(), Quality: sampled},
		},
		{
			name:  "chosen items",
			input: Input{Report: analysed(), Quality: exactQuality()},
			opts:  Options{Items: []Item{ItemQuality, ItemMotion, ItemFlags}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			opts := testCase.opts
			opts.Font = testFont

			var out bytes.Buffer
			require.NoError(t, Write(&out, testCase.input, opts))

			golden := filepath.Join("testdata", strings.ReplaceAll(testCase.name, " ", "-")+".ass")
			if *update {
				require.NoError(t, os.WriteFile(golden, out.Bytes(), 0o600))
			}

			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			assert.Equal(t, string(want), out.String())
		})
	}
}

// errWrite fails every write.
var errWrite = errors.New("disk full")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWrite
}

func TestWriteErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   Input
		failing bool
		wantErr error
	}{
		{name: "no report", input: Input{}, wantErr: ErrNoFrames},
		{name: "no bitstream", input: Input{Report: &analysis.Report{}}, wantErr: ErrNoFrames},
		{name: "no packet", input: Input{Report: &analysis.Report{Bitstream: &bitstream.Report{}}}, wantErr: ErrNoFrames},
		{name: "failing writer", input: Input{Report: inspection()}, failing: true, wantErr: errWrite},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var err error
			if testCase.failing {
				err = Write(failingWriter{}, testCase.input, Options{})
			} else {
				err = Write(&bytes.Buffer{}, testCase.input, Options{})
			}

			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestWriteDefaultFont(
	t *testing.T,
) {
	var out bytes.Buffer
	require.NoError(t, Write(&out, Input{Report: inspection()}, Options{}))
	assert.Contains(t, out.String(), "Style: qc,"+DefaultFont+",")
}

func TestWriteWithoutInfo(
	t *testing.T,
) {
	report := inspection()
	report.Info = nil

	var out bytes.Buffer
	require.NoError(t, Write(&out, Input{Report: report}, Options{Font: "Font, with comma"}))

	script := out.String()
	assert.Contains(t, script, "PlayResX: 1920\n", "16:9 when the size is unknown")
	assert.Contains(t, script, "Style: qc,Font  with comma,", "a comma would split the style")
	assert.NotContains(t, script, "my ", "no file name")
}

func TestParseItems(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		names   []string
		want    []Item
		wantErr error
	}{
		{name: "none", names: nil, want: nil},
		{name: "blank", names: []string{" ", ""}, want: nil},
		{name: "names and aliases", names: []string{"Time", " vmaf", "luma", "light", "time", "audio"}, want: []Item{ItemTime, ItemQuality, ItemLevels, ItemHDR, ItemLoudness}},
		{name: "unknown", names: []string{"time", "weather"}, wantErr: ErrUnknownItem},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			items, err := ParseItems(testCase.names)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.Contains(t, err.Error(), "time, bitrate, shots")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, items)
		})
	}
}

func TestItems(
	t *testing.T,
) {
	items := Items()
	require.Len(t, items, 11)

	items[0] = "changed"
	assert.Equal(t, ItemTime, Items()[0], "a copy")
}

func TestWriteScriptErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		path    string
		input   Input
		wantErr error
	}{
		{name: "unwritable path", path: filepath.Join(t.TempDir(), "missing", "x.ass"), input: Input{Report: inspection()}, wantErr: os.ErrNotExist},
		{name: "no frames", path: filepath.Join(t.TempDir(), "x.ass"), input: Input{}, wantErr: ErrNoFrames},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.ErrorIs(t, writeScript(testCase.path, testCase.input, Options{}), testCase.wantErr)
		})
	}
}
