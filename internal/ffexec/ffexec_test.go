package ffexec

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
)

// testTimeout bounds every subprocess test so a regression (a deadlock on a
// full pipe, a process that is not killed) fails instead of hanging.
const testTimeout = 10 * time.Second

// endlessVideo makes ffmpeg write raw frames to stdout until it is killed.
var endlessVideo = []string{"-v", "error", "-f", "lavfi", "-i", "color=black:size=320x180", "-f", "rawvideo", "-"}

var errConsumer = errors.New("consumer failed")

func testContext(
	t *testing.T,
) context.Context {
	t.Helper()
	testutil.RequireFFmpeg(t)

	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	t.Cleanup(cancel)

	return ctx
}

func TestOutput(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		bin          string
		args         []string
		wantContains string
		wantErrIs    error
		wantErr      string
	}{
		{
			name:         "stdout is returned",
			bin:          "ffprobe",
			args:         []string{"-version"},
			wantContains: "ffprobe version",
		},
		{
			name:      "missing binary",
			bin:       "qc-binary-that-does-not-exist",
			wantErrIs: ErrNotFound,
			wantErr:   "qc-binary-that-does-not-exist",
		},
		{
			name:    "failure carries stderr",
			bin:     "ffprobe",
			args:    []string{"-v", "error", filepath.Join(t.TempDir(), "missing.mp4")},
			wantErr: "No such file or directory",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out, err := Output(testContext(t), testCase.bin, testCase.args)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)

				if testCase.wantErrIs != nil {
					require.ErrorIs(t, err, testCase.wantErrIs)
				}

				assert.Nil(t, out)

				return
			}

			require.NoError(t, err)
			assert.Contains(t, string(out), testCase.wantContains)
		})
	}
}

func TestStreamStartFailure(
	t *testing.T,
) {
	// An executable file that is neither a binary nor a script passes the PATH
	// lookup but cannot be started.
	bin := filepath.Join(t.TempDir(), "not-a-program")
	require.NoError(t, os.WriteFile(bin, []byte("not a program"), 0o700))

	err := Stream(t.Context(), bin, nil, func(io.Reader) error { return nil })

	require.ErrorContains(t, err, "ffexec: start")
}

func TestStreamConsumerError(
	t *testing.T,
) {
	started := time.Now()

	err := Stream(testContext(t), "ffmpeg", endlessVideo, func(r io.Reader) error {
		_, _ = io.ReadFull(r, make([]byte, 1024))

		return errConsumer
	})

	require.ErrorIs(t, err, errConsumer)
	assert.Less(t, time.Since(started), testTimeout, "the process must be killed")
}

func TestStreamCancelled(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()

	err := Stream(ctx, "ffmpeg", endlessVideo, func(r io.Reader) error {
		cancel()

		_, err := io.Copy(io.Discard, r)

		return err
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorContains(t, err, "ffexec: ffmpeg")
}

func TestStreamCancelledWhileConsumerFails(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()

	err := Stream(ctx, "ffmpeg", endlessVideo, func(io.Reader) error {
		cancel()

		return io.ErrUnexpectedEOF
	})

	require.ErrorIs(t, err, context.Canceled, "cancellation is reported over its symptoms")
}

func TestStreamConsumerStoppingEarly(
	t *testing.T,
) {
	// ~4 MB of output: far more than a pipe buffer, so the process would stay
	// blocked if the unread output were not drained.
	args := []string{
		"-v", "error", "-f", "lavfi", "-i", "color=black:size=320x180:duration=2",
		"-f", "rawvideo", "-",
	}

	err := Stream(testContext(t), "ffmpeg", args, func(io.Reader) error { return nil })

	require.NoError(t, err)
}

func TestLines(
	t *testing.T,
) {
	var lines []string

	err := Lines(testContext(t), "ffprobe", []string{"-version"}, func(line []byte) error {
		lines = append(lines, string(line))

		return nil
	})

	require.NoError(t, err)
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[0], "ffprobe version")
}

func TestLinesCallbackError(
	t *testing.T,
) {
	calls := 0

	err := Lines(testContext(t), "ffprobe", []string{"-version"}, func([]byte) error {
		calls++

		return errConsumer
	})

	require.ErrorIs(t, err, errConsumer)
	assert.Equal(t, 1, calls)
}

func TestLinesTooLong(
	t *testing.T,
) {
	// One gray frame larger than maxLineSize, without any newline byte.
	args := []string{
		"-v", "error", "-f", "lavfi", "-i", "color=black:size=1280x1024",
		"-frames:v", "1", "-pix_fmt", "gray", "-f", "rawvideo", "-",
	}

	err := Lines(testContext(t), "ffmpeg", args, func([]byte) error { return nil })

	require.ErrorIs(t, err, bufio.ErrTooLong)
	assert.ErrorContains(t, err, "ffexec: scan stdout")
}

func TestBoundedBuffer(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "under the limit", writes: []string{"ab", "c"}, want: "abc"},
		{name: "truncated write", writes: []string{"abc", "defgh"}, want: "abcde"},
		{name: "writes after the limit are dropped", writes: []string{"abcde", "fg"}, want: "abcde"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			buf := &boundedBuffer{limit: 5}

			for _, w := range testCase.writes {
				n, err := buf.Write([]byte(w))

				require.NoError(t, err)
				assert.Equal(t, len(w), n, "writes always report success")
			}

			assert.Equal(t, testCase.want, buf.String())
		})
	}
}

func TestStreamPair(
	t *testing.T,
) {
	// Two outputs of one lavfi source: 10 gray 8×8 frames on stdout, the
	// same frames at 4×4 on the extra pipe.
	args := []string{
		"-v", "error", "-f", "lavfi", "-i", "color=gray:size=8x8:duration=0.4:rate=25",
		"-filter_complex", "[0:v]split=2[a][b];[b]scale=4:4[c]",
		"-map", "[a]", "-pix_fmt", "gray", "-f", "rawvideo", "-",
		"-map", "[c]", "-pix_fmt", "gray", "-f", "rawvideo", ExtraOutput,
	}

	var main, extra int

	err := StreamPair(testContext(t), "ffmpeg", args, func(stdout, pipe io.Reader) error {
		a, b := make([]byte, 64), make([]byte, 16)

		for {
			if _, err := io.ReadFull(stdout, a); err != nil {
				return nil
			}

			main++

			if _, err := io.ReadFull(pipe, b); err != nil {
				return err
			}

			extra++
		}
	})
	require.NoError(t, err)

	assert.Equal(t, 10, main)
	assert.Equal(t, 10, extra)
}

func TestStreamPairConsumerStoppingEarly(
	t *testing.T,
) {
	args := []string{
		"-v", "error", "-f", "lavfi", "-i", "color=black:size=320x180",
		"-filter_complex", "[0:v]split=2[a][b]",
		"-map", "[a]", "-f", "rawvideo", "-", "-map", "[b]", "-f", "rawvideo", ExtraOutput,
	}

	err := StreamPair(testContext(t), "ffmpeg", args, func(io.Reader, io.Reader) error {
		return errConsumer
	})

	require.ErrorIs(t, err, errConsumer)
}
