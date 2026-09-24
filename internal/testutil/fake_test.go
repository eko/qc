package testutil

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeFFmpeg(
	t *testing.T,
) {
	bin := FakeFFmpeg(t, `echo "fake $1 $REAL_FFMPEG"`)

	out, err := exec.Command(bin, "-version").Output()
	require.NoError(t, err)

	realBin, _ := exec.LookPath("ffmpeg")
	assert.Equal(t, "fake -version "+realBin+"\n", string(out))
}
