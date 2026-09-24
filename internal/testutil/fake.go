package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// FakeFFmpeg writes an executable POSIX shell script standing for ffmpeg
// into t.TempDir and returns its path. body runs after "#!/bin/sh"; it can
// log its arguments ("$@"), print canned output, fail, or hand over to the
// real ffmpeg, whose path is in $REAL_FFMPEG (empty when ffmpeg is not
// installed). Fakes let tests drive GPU code paths (NVDEC, NVENC, capability
// probes) on machines without an NVIDIA GPU.
func FakeFFmpeg(
	t testing.TB,
	body string,
) string {
	t.Helper()

	realBin, _ := exec.LookPath("ffmpeg")
	path := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nREAL_FFMPEG='" + realBin + "'\n" + body + "\n"

	//nolint:gosec // the fake must be executable
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))

	return path
}
