//go:build unix

package ffexec

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errNoDescriptor = errors.New("no descriptor left")

func TestOutputPipeFallbacks(
	t *testing.T,
) {
	failSocket := func(int, int, int) ([2]int, error) { return [2]int{}, errNoDescriptor }
	failPipe := func() (*os.File, *os.File, error) { return nil, nil, errNoDescriptor }

	testCases := []struct {
		name       string
		socketpair func(int, int, int) ([2]int, error)
		pipe       func() (*os.File, *os.File, error)
		wantErr    error
	}{
		{name: "a pipe without sockets", socketpair: failSocket, pipe: os.Pipe},
		{name: "neither", socketpair: failSocket, pipe: failPipe, wantErr: errNoDescriptor},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			socketpair, pipe = testCase.socketpair, testCase.pipe
			t.Cleanup(func() { socketpair, pipe = defaultSocketpair, defaultPipe })

			out, err := Output(testContext(t), "ffmpeg", []string{"-v", "error", "-f", "lavfi", "-i", "color=size=16x16", "-frames:v", "2", "-f", "rawvideo", "-"})

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Len(t, out, 2*16*16*3/2)
		})
	}
}

func TestStreamPairOutputPipeFailure(
	t *testing.T,
) {
	calls := 0
	socketpair = func(domain, typ, proto int) ([2]int, error) {
		if calls++; calls > 1 {
			return [2]int{}, errNoDescriptor
		}

		return defaultSocketpair(domain, typ, proto)
	}
	pipe = func() (*os.File, *os.File, error) { return nil, nil, errNoDescriptor }

	t.Cleanup(func() { socketpair, pipe = defaultSocketpair, defaultPipe })

	err := StreamPair(testContext(t), "ffmpeg", []string{"-version"}, func(_, _ io.Reader) error { return nil })
	require.ErrorIs(t, err, errNoDescriptor)
}
