package encode

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
)

// update rewrites the golden files instead of comparing with them:
// go test ./encode -run TestGoldenCommands -update.
var update = flag.Bool("update", false, "rewrite the golden files")

// goldenCommands is the golden file of every command line the codecs emit.
const goldenCommands = "testdata/commands.golden"

// TestGoldenCommands pins every ffmpeg command line emitted for every codec
// of every encoder family, over options exercising each family-specific
// branch: the argument order matters to users pasting commands and to the
// validation tools replaying them, so any change must be deliberate.
func TestGoldenCommands(
	t *testing.T,
) {
	variants := []struct {
		name   string
		params Params
	}{
		{name: "defaults", params: Params{Width: 1920, Height: 1080, CRF: 23}},
		{name: "fixed gop capped", params: Params{Width: 1280, Height: 720, CRF: 27.5, GOP: 50, MaxRate: 3_000_000, BufSize: 4_000_000}},
		{name: "preset 10-bit", params: Params{Width: 3840, Height: 2160, CRF: 30, Preset: "slow", BitDepth: 10}},
		{name: "above the encoder's max rate", params: Params{Width: 3840, Height: 2160, CRF: 18, GOP: 48, MaxRate: 150_000_000, BufSize: 300_000_000}},
		{name: "film grain", params: Params{Width: 1920, Height: 1080, CRF: 35, GOP: 48, FilmGrain: 20}},
		{name: "everything", params: Params{Width: 1280, Height: 720, CRF: 31, Preset: "4", GOP: 50, MaxRate: 2_000_000, BufSize: 4_000_000, BitDepth: 10, FilmGrain: 12}},
	}

	rate := media.Rational{Num: 25, Den: 1}
	uniform := []Chunk{{Start: 0, Frames: 50, CRF: 24}, {Start: 50, Frames: 100, CRF: 21.5}, {Start: 150, Frames: 50, CRF: 28}}
	resized := []Chunk{{Start: 0, Frames: 50, CRF: 24, Width: 1280, Height: 720}, {Start: 50, Frames: 100, CRF: 21.5, Width: 960, Height: 540}}

	var out strings.Builder

	for _, hw := range []Hardware{HardwareCPU, HardwareNVENC} {
		for _, name := range []string{"h264", "hevc", "av1"} {
			codec, err := LookupFor(name, hw)
			require.NoError(t, err)

			encoded, err := json.Marshal(codec)
			require.NoError(t, err)

			fmt.Fprintf(&out, "## %s/%s\n", hw, name)
			fmt.Fprintf(&out, "json: %s\n", encoded)
			fmt.Fprintf(&out, "settings: preset=%s crf=%v..%v probes=%v step=%v maxrate=%d option=%s input=%q\n",
				codec.DefaultPreset, codec.MinCRF, codec.MaxCRF, codec.ProbeCRFs, codec.Step(), codec.MaxRate, codec.QualityOption(), codec.InputArgs())

			for _, v := range variants {
				fmt.Fprintf(&out, "### %s\n%s\n", v.name, codec.CommandLine("in put.nut", "out.mp4", v.params))
				fmt.Fprintf(&out, "#### chunks\n%s\n", codec.ChunkCommandLine("src.mov", "rung.mp4", rate, uniform, v.params))
				fmt.Fprintf(&out, "#### resized chunks\n%s\n", codec.ChunkCommandLine("src.mov", "rung.mp4", rate, resized, v.params))
			}
		}
	}

	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenCommands), 0o750))
		require.NoError(t, os.WriteFile(goldenCommands, []byte(out.String()), 0o600))
	}

	want, err := os.ReadFile(goldenCommands)
	require.NoError(t, err)
	assert.Equal(t, string(want), out.String())
}
