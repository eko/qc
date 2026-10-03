package encode

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
)

func TestEncodeStall(
	t *testing.T,
) {
	testCases := []struct {
		name string
		// freezes is how many runs freeze before one writes its output.
		freezes int
		wantErr error
		runs    int
	}{
		{name: "healthy encode", freezes: 0, runs: 1},
		{name: "one freeze: stopped and tried again", freezes: 1, runs: 2},
		{name: "freezes again: reported", freezes: 2, wantErr: ErrStalled, runs: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			runs := filepath.Join(dir, "runs")

			// Each run appends a line to runs; the first freezes runs freeze
			// (sleep in place of the shell, so that stopping it frees its
			// pipes), the others write their output, the last argument.
			bin := testutil.FakeFFmpeg(t, `echo run >> '`+runs+`'
n=$(wc -l < '`+runs+`')
if [ "$n" -le `+strconv.Itoa(testCase.freezes)+` ]; then exec sleep 30; fi
for a; do out=$a; done
echo encoded > "$out"`)

			// Long enough for the fake ffmpeg to start on a busy machine: a
			// run killed before it wrote its line was not counted.
			f := NewFFmpeg(bin, WithStallTimeout(time.Second))
			codec, err := Lookup("h264")
			require.NoError(t, err)

			err = f.Encode(t.Context(), codec, "in.nut", filepath.Join(dir, "out.mp4"), Params{Width: 64, Height: 36, CRF: 23})
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
			}

			log, err := os.ReadFile(runs)
			require.NoError(t, err)
			assert.Equal(t, testCase.runs, strings.Count(string(log), "run"))
		})
	}
}

func TestWatchOutputGrowing(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "out.mp4")
	done := make(chan struct{})
	stalled := make(chan struct{})

	go watchOutput(path, 200*time.Millisecond, done, func() { close(stalled) })

	// An output growing faster than the timeout never stalls.
	for i := range 8 {
		require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", i+1)), 0o600))
		time.Sleep(50 * time.Millisecond)
	}

	select {
	case <-stalled:
		t.Fatal("a growing output stalled")
	default:
	}

	close(done)
}
