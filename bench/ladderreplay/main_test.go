package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/ladder"
)

// writeGrid writes a line grid of codec: the bitrate halves every 6 CRF
// and grows with the pixel count, the quality saturates at a ceiling that
// drops with resolution.
func writeGrid(
	t *testing.T,
	codec string,
) string {
	t.Helper()

	var b strings.Builder

	for _, h := range []int{1080, 720, 540, 360} {
		scale := 6e6 * math.Pow(float64(h)/1080, 2)
		ceiling := 99 - 13*math.Log2(1080/float64(h))

		for crf := 14.0; crf <= 50; crf += 3 {
			bitrate := scale * math.Exp(-0.115*(crf-23))
			vmaf := ceiling * (1 - math.Exp(-bitrate/(0.4*scale)))
			fmt.Fprintf(&b, "%s %d %g %.0f %.4f\n", codec, h, crf, bitrate, vmaf)
		}
	}

	path := filepath.Join(t.TempDir(), "grid.txt")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))

	return path
}

func TestRun(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		args     []string
		wantCode int
		want     []string
	}{
		{
			name: "both probing modes",
			args: []string{"-codec", "h264"},
			want: []string{"fixed", "adaptive", "mean overhead", "outside the grid"},
		},
		{
			name: "sampling error and verbose rungs",
			args: []string{"-codec", "av1", "-probing", "fixed", "-bias", "-0.8", "-noise", "0.5", "-runs", "3", "-v"},
			want: []string{"bias -0.80, noise 0.50", "fixed 1080p crf"},
		},
		{
			name:     "unknown probing mode",
			args:     []string{"-codec", "h264", "-probing", "random"},
			wantCode: 2,
		},
		{
			name:     "no grid",
			args:     []string{},
			wantCode: 2,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			args := testCase.args
			if testCase.wantCode == 0 {
				codec := args[1]
				args = append(args, writeGrid(t, codec))
			}

			var stdout, stderr bytes.Buffer

			code := run(args, &stdout, &stderr)

			require.Equal(t, testCase.wantCode, code, stderr.String())

			for _, want := range testCase.want {
				assert.Contains(t, stdout.String(), want)
			}
		})
	}
}

func TestRunErrors(
	t *testing.T,
) {
	empty := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(empty, []byte("h264 not a line\n"), 0o600))

	testCases := []struct {
		name string
		path string
	}{
		{name: "missing grid", path: filepath.Join(t.TempDir(), "missing.txt")},
		{name: "grid without measurements", path: empty},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			assert.Equal(t, 1, run([]string{"-codec", "h264", testCase.path}, &stdout, &stderr))
			assert.Contains(t, stderr.String(), "error:")
		})
	}
}

func TestLoadGridCache(
	t *testing.T,
) {
	cache := `{"codec": "hevc", "entries": {
		"1280x720/crf20/max0/buf0/depth0": {"width": 1280, "height": 720, "crf": 20, "bitrate": 3000000, "vmaf": 90},
		"1280x720/crf26/max0/buf0/depth0": {"width": 1280, "height": 720, "crf": 26, "bitrate": 1500000, "vmaf": 84},
		"1280x720/crf23/max2000000/buf4000000/depth0": {"width": 1280, "height": 720, "crf": 23, "bitrate": 1900000, "vmaf": 87}
	}}`
	path := filepath.Join(t.TempDir(), "cache.json")
	require.NoError(t, os.WriteFile(path, []byte(cache), 0o600))

	g, err := loadGrid(path, "")
	require.NoError(t, err)

	assert.Equal(t, "hevc", g.codec, "named by the cache")
	assert.Len(t, g.points[720], 2, "the VBV-capped rung checks are left out")

	bitrate, vmaf, err := g.at(720, 23)
	require.NoError(t, err)
	assert.InDelta(t, math.Sqrt(3e6*1.5e6), bitrate, 1, "log-linear in CRF")
	assert.InDelta(t, 87, vmaf, 1e-9)

	_, _, err = g.at(1080, 23)
	require.ErrorIs(t, err, ErrNoGrid)
}

func TestCheck(
	t *testing.T,
) {
	g := &grid{codec: "h264", width: map[int]int{}, points: map[int][]point{
		720: {{20, 3000e3, 90}, {26, 1500e3, 84}, {32, 750e3, 76}},
		360: {{20, 1200e3, 78}, {26, 600e3, 72}, {32, 300e3, 64}},
	}}

	c := g.check([]ladder.Rung{
		{Height: 720, CRF: 20},
		{Height: 360, CRF: 20},
		{Height: 720, CRF: 40},
	})

	assert.InDelta(t, 90, c.topVMAF, 1e-9)
	assert.Equal(t, 1, c.offOptimum, "360p at 1.2 Mb/s where 720p is better")
	assert.Equal(t, 1, c.unchecked, "CRF 40 is outside the grid")
	assert.Positive(t, c.worstOverhead)
}
