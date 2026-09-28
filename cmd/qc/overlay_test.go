package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/overlay"
)

func TestOverlayCommands(
	t *testing.T,
) {
	testutil.RequireFilter(t, "subtitles")

	source, encoded := clips(t)
	dir := t.TempDir()
	out := func(name string) string { return filepath.Join(dir, name) }

	testCases := []struct {
		name       string
		args       []string
		wantOutput string
	}{
		{
			name:       "analyze",
			args:       []string{"analyze", "--overlay", out("analyze.mp4"), "--overlay-height", "90", source},
			wantOutput: out("analyze.mp4"),
		},
		{
			name:       "analyze fast: the bitstream only",
			args:       []string{"analyze", "--fast", "--overlay", out("fast.mkv"), "--overlay-items", "time,timeline", source},
			wantOutput: out("fast.mkv"),
		},
		{
			name:       "analyze with x264 in a single pass",
			args:       []string{"analyze", "--overlay", out("x264.mkv"), "--overlay-encoder", "x264", "--overlay-workers", "1", source},
			wantOutput: out("x264.mkv"),
		},
		{
			name:       "vmaf on the distorted video",
			args:       []string{"vmaf", "--exact", "--metrics=", "--overlay", out("vmaf.mp4"), source, encoded},
			wantOutput: out("vmaf.mp4"),
		},
		{
			name:       "run with a reference",
			args:       []string{"run", "-r", source, "--codecs=", "--metrics=", "--sample", "1/scene", "--overlay", out("run.mp4"), encoded},
			wantOutput: out("run.mp4"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			code, stdout, stderr := execute(t, testEnv, testCase.args...)
			require.Equal(t, 0, code, stderr)

			assert.Contains(t, stdout, "annotated video written to")
			info, err := os.Stat(testCase.wantOutput)
			require.NoError(t, err)
			assert.Positive(t, info.Size())
		})
	}
}

func TestOverlayCommandErrors(
	t *testing.T,
) {
	source, _ := clips(t)
	noLibass := testutil.FakeFFmpeg(t, `case "$*" in *-filters*) printf ' T.. scale  V->V  Scale.\n' ;; *) exec "$REAL_FFMPEG" "$@" ;; esac`)
	noNVENC := testutil.FakeFFmpeg(t, `case "$*" in *h264_nvenc*) exit 1 ;; *) exec "$REAL_FFMPEG" "$@" ;; esac`)
	output := filepath.Join(t.TempDir(), "o.mp4")

	testCases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{
			name:       "the copy would overwrite the source",
			args:       []string{"analyze", "--fast", "--overlay", source, source},
			wantStderr: ErrOverlayOverwritesSource.Error(),
		},
		{
			name:       "vmaf copy over the distorted video",
			args:       []string{"vmaf", "--overlay", source, source, source},
			wantStderr: ErrOverlayOverwritesSource.Error(),
		},
		{
			name:       "run copy over the source",
			args:       []string{"run", "--overlay", source, source},
			wantStderr: ErrOverlayOverwritesSource.Error(),
		},
		{
			name:       "unknown item",
			args:       []string{"analyze", "--overlay", output, "--overlay-items", "time,weather", source},
			wantStderr: `invalid --overlay-items: unknown overlay item "weather"`,
		},
		{
			name:       "odd height",
			args:       []string{"analyze", "--overlay", output, "--overlay-height", "719", source},
			wantStderr: "invalid --overlay-height",
		},
		{
			name:       "unknown encoder",
			args:       []string{"analyze", "--overlay", output, "--overlay-encoder", "qsv", source},
			wantStderr: "invalid --overlay-encoder",
		},
		{
			name:       "negative workers",
			args:       []string{"analyze", "--overlay", output, "--overlay-workers", "-2", source},
			wantStderr: "invalid --overlay-workers",
		},
		{
			name:       "a hardware encoder that does not encode fails before any work",
			args:       []string{"analyze", "--ffmpeg", noNVENC, "--overlay", output, "--overlay-encoder", "nvenc", source},
			wantStderr: "--overlay: nvenc encoder: ",
		},
		{
			name:       "ffmpeg without libass fails before any work",
			args:       []string{"analyze", "--ffmpeg", noLibass, "--overlay", output, source},
			wantStderr: "--overlay: " + encode.ErrNoSubtitlesFilter.Error(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			code, _, stderr := execute(t, testEnv, testCase.args...)

			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, testCase.wantStderr)
			assert.NoFileExists(t, output)
		})
	}
}

func TestValidateOverlay(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		config  OverlayConfig
		wantErr error
	}{
		{name: "defaults", config: OverlayConfig{}},
		{name: "items and height", config: OverlayConfig{Items: []string{"vmaf", "timeline"}, Height: 720}},
		{name: "unknown item", config: OverlayConfig{Items: []string{"x"}}, wantErr: overlay.ErrUnknownItem},
		{name: "negative height", config: OverlayConfig{Height: -2}, wantErr: ErrInvalidOverlayHeight},
		{name: "odd height", config: OverlayConfig{Height: 541}, wantErr: ErrInvalidOverlayHeight},
		{name: "encoder and workers", config: OverlayConfig{Encoder: "videotoolbox", Workers: 4}},
		{name: "unknown encoder", config: OverlayConfig{Encoder: "qsv"}, wantErr: encode.ErrUnknownBurnEncoder},
		{name: "negative workers", config: OverlayConfig{Workers: -1}, wantErr: ErrInvalidOverlayWorkers},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.config.validate()
			if testCase.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestOverlayOptions(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		config      Config
		wantOutput  string
		wantItems   []overlay.Item
		wantHeight  int
		wantEncoder encode.BurnEncoder
		wantWorkers int
	}{
		{name: "no copy", config: Config{Overlay: OverlayConfig{Height: 720}}},
		{
			name: "copy",
			config: Config{
				Output:  OutputConfig{Overlay: "o.mp4"},
				Overlay: OverlayConfig{Items: []string{"vmaf"}, Height: 720, Encoder: "x264", Workers: 3},
			},
			wantOutput:  "o.mp4",
			wantItems:   []overlay.Item{overlay.ItemQuality},
			wantHeight:  720,
			wantEncoder: encode.BurnX264,
			wantWorkers: 3,
		},
		{name: "automatic encoder", config: Config{Output: OutputConfig{Overlay: "o.mp4"}}, wantOutput: "o.mp4"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			opts, err := overlayOptions(testCase.config, "in.mov")
			require.NoError(t, err)

			assert.Equal(t, testCase.wantOutput, opts.Output)
			assert.Equal(t, testCase.wantItems, opts.Render.Items)
			assert.Equal(t, testCase.wantHeight, opts.Render.Height)
			assert.Equal(t, testCase.wantEncoder, opts.Render.Encoder)
			assert.Equal(t, testCase.wantWorkers, opts.Render.Workers)
		})
	}
}

func TestSameFile(
	t *testing.T,
) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.mp4")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	link := filepath.Join(dir, "link.mp4")
	require.NoError(t, os.Symlink(file, link))

	testCases := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "same path", a: file, b: file, want: true},
		{name: "relative and absolute", a: filepath.Join(dir, ".", "a.mp4"), b: file, want: true},
		{name: "through a link", a: link, b: file, want: true},
		{name: "another file", a: filepath.Join(dir, "b.mp4"), b: file},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, sameFile(testCase.a, testCase.b))
		})
	}
}

var errFilters = errors.New("cannot list filters")

// fakeChecker answers CheckBurn with err, counting its calls and recording
// the encoder checked.
type fakeChecker struct {
	err     error
	calls   int
	encoder encode.BurnEncoder
}

func (c *fakeChecker) CheckBurn(
	_ context.Context,
	encoder encode.BurnEncoder,
) error {
	c.calls++
	c.encoder = encoder

	return c.err
}

func TestCheckOverlay(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		output      OutputConfig
		config      OverlayConfig
		err         error
		wantCalls   int
		wantEncoder encode.BurnEncoder
		wantErr     error
	}{
		{name: "no copy asked: nothing checked", output: OutputConfig{}, err: errFilters},
		{name: "libass available", output: OutputConfig{Overlay: "o.mp4"}, wantCalls: 1},
		{
			name: "the encoder asked for", output: OutputConfig{Overlay: "o.mp4"}, config: OverlayConfig{Encoder: "nvenc"},
			wantCalls: 1, wantEncoder: encode.BurnNVENC,
		},
		{name: "check failure", output: OutputConfig{Overlay: "o.mp4"}, err: errFilters, wantCalls: 1, wantErr: errFilters},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			checker := &fakeChecker{err: testCase.err}

			err := checkOverlay(t.Context(), testCase.output, testCase.config, checker)

			assert.Equal(t, testCase.wantCalls, checker.calls)
			assert.Equal(t, testCase.wantEncoder, checker.encoder)

			if testCase.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, testCase.wantErr)
			assert.ErrorContains(t, err, "--overlay: ")
		})
	}
}
