package main

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/internal/testutil"
)

func TestPath(
	t *testing.T,
) {
	p := path{cx: 100, cy: 50, vx: 10, vy: -5, zoom: 0.1, shakeX: []sine{{amplitude: 2, frequency: 1}}}

	x, y, m := p.at(0.25)
	assert.InDelta(t, 100+2.5+2, x, 1e-9)
	assert.InDelta(t, 50-1.25, y, 1e-9)
	assert.InDelta(t, math.Exp(0.025), m, 1e-9)

	ex, ey, em := p.expressions("T")
	assert.Equal(t, "100+10*T+2*sin(2*PI*1*T+0)", ex)
	assert.Equal(t, "50+-5*T", ey)
	assert.Equal(t, "exp(0.1*T)", em)
}

func TestGraph(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		clip    clip
		want    []string
		wantNot []string
	}{
		{
			name: "plain move", clip: clip{fps: 25, path: path{cx: 10}},
			want: []string{"zoompan=z='2.8625*exp(0*(on/25))'", "[bg]format=yuv420p[out]"}, wantNot: []string{"split"},
		},
		{
			name: "cut and post filter", clip: clip{fps: 25, cut: 2, second: path{cx: 20}, post: "gblur=sigma=3"},
			want: []string{"if(lt((on/25),2),10", "gblur=sigma=3,format=yuv420p"},
		},
		{
			name: "moving object", clip: clip{fps: 25, layer: layer{sx: 1, sy: 2, w: 30, h: 40, x: 5, y: 6, vx: 7}},
			want: []string{"split=2[tex][src]", "crop=30:40:1:2", "overlay=x='5+7*t':y=6", "[comp]format=yuv420p[out]"},
		},
		{
			name: "parallax plane", clip: clip{fps: 25, layer: layer{sx: 1, sy: 2, w: 30, h: 40, vx: 7, scroll: true}},
			want: []string{"crop=30:40:x='1+7*t':y=2", "overlay=x=0:y=0"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.clip.path.cx == 0 {
				testCase.clip.path.cx = 10
			}

			g := graph(testCase.clip)
			for _, want := range testCase.want {
				assert.Contains(t, g, want)
			}

			for _, not := range testCase.wantNot {
				assert.NotContains(t, g, not)
			}
		})
	}
}

func TestRates(
	t *testing.T,
) {
	testCases := []struct {
		name                           string
		clip                           clip
		wantPan, wantTilt, wantZoomPct float64
	}{
		{name: "pan right", clip: clip{fps: 25, path: path{vx: 192}}, wantPan: 10},
		{name: "tilt up", clip: clip{fps: 25, path: path{vy: -96}}, wantTilt: 5},
		{name: "zoom in", clip: clip{fps: 25, path: path{zoom: 0.1}}, wantZoomPct: 10},
		{name: "after a cut", clip: clip{fps: 25, cut: 0.01, second: path{vx: -192}}, wantPan: -10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pan, tilt, zoom := testCase.clip.rates(5)

			assert.InDelta(t, testCase.wantPan, pan, 0.1)
			assert.InDelta(t, testCase.wantTilt, tilt, 1e-9)
			assert.InDelta(t, testCase.wantZoomPct, zoom, 1e-9)
		})
	}
}

func TestTrueShake(
	t *testing.T,
) {
	shakeX, shakeY := hand(6)

	testCases := []struct {
		name string
		clip clip
		want func(t *testing.T, got float64)
	}{
		{
			name: "steady pan", clip: clip{fps: 25, frames: 50, path: path{vx: 300}},
			want: func(t *testing.T, got float64) { assert.InDelta(t, 0, got, 1e-9) },
		},
		{
			name: "handheld up to a cut", clip: clip{fps: 25, frames: 100, cut: 3, path: path{shakeX: shakeX, shakeY: shakeY}},
			want: func(t *testing.T, got float64) { assert.Greater(t, got, 0.2) },
		},
		{
			name: "single frame", clip: clip{fps: 25, frames: 1},
			want: func(t *testing.T, got float64) { assert.Zero(t, got) },
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.want(t, trueShake(testCase.clip))
		})
	}
}

func TestLocalTrend(
	t *testing.T,
) {
	assert.InDeltaSlice(t, []float64{1, 3, 5, 7}, localTrend([]float64{1, 3, 5, 7}, 2), 1e-9)
	assert.InDeltaSlice(t, []float64{4}, localTrend([]float64{4}, 2), 1e-9)
}

func TestSummarize(
	t *testing.T,
) {
	assert.Equal(t, errorStats{}, summarize(nil))

	got := summarize([]float64{1, -1, 3})
	assert.InDelta(t, 1, got.bias, 1e-9)
	assert.InDelta(t, math.Sqrt(11.0/3), got.rmse, 1e-9)
}

func TestMatches(
	t *testing.T,
) {
	pan := expectation{class: motion.ClassPan, direction: motion.DirectionRight}

	testCases := []struct {
		name string
		want []expectation
		got  []motion.Shot
		ok   bool
	}{
		{name: "right", want: []expectation{pan}, got: []motion.Shot{{Class: motion.ClassPan, Direction: motion.DirectionRight}}, ok: true},
		{name: "wrong direction", want: []expectation{pan}, got: []motion.Shot{{Class: motion.ClassPan, Direction: motion.DirectionLeft}}},
		{name: "missing shot", want: []expectation{pan, pan}, got: []motion.Shot{{Class: motion.ClassPan}}},
		{
			name: "accepted alternative",
			want: []expectation{{class: motion.ClassPan, accept: []motion.Class{motion.ClassUnknown}}},
			got:  []motion.Shot{{Class: motion.ClassUnknown}}, ok: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.ok, matches(testCase.want, testCase.got))
		})
	}
}

func TestClips(
	t *testing.T,
) {
	seen := map[string]bool{}

	for _, c := range clips() {
		assert.False(t, seen[c.name], "unique name %s", c.name)
		assert.NotEmpty(t, c.want, c.name)
		assert.Positive(t, c.frames, c.name)
		seen[c.name] = true
	}
}

func TestReport(
	t *testing.T,
) {
	var buf bytes.Buffer

	require.NoError(t, report(&buf, []outcome{
		{clip: clip{name: "a", fps: 25}, ok: true, got: []motion.Shot{{Class: motion.ClassPan, Direction: motion.DirectionRight, Shaky: true}}},
		{clip: clip{name: "b", fps: 25}, got: []motion.Shot{{Class: motion.ClassStatic}, {Class: motion.ClassZoom, Direction: motion.DirectionIn}}},
	}))

	out := buf.String()
	assert.Contains(t, out, "pan right (shaky)")
	assert.Contains(t, out, "static | zoom in")
	assert.Contains(t, out, "classified right: 1/2")
}

func TestRunUsage(
	t *testing.T,
) {
	testCases := []struct {
		name string
		args []string
		want int
	}{
		{name: "unknown flag", args: []string{"-nope"}, want: 2},
		{name: "missing texture", args: []string{"-dir", t.TempDir()}, want: 1},
		{name: "bad filter", args: []string{"-dir", t.TempDir(), "-texture", "x.png", "-run", "("}, want: 1},
		{name: "unreadable texture", args: []string{"-dir", t.TempDir(), "-texture", "missing.png"}, want: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			assert.Equal(t, testCase.want, run(testCase.args, &stdout, &stderr))
		})
	}
}

func TestValidateStatic(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	// A detailed still: the fractal's boundary has texture at every scale.
	texture := filepath.Join(t.TempDir(), "texture.png")
	out, err := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "mandelbrot=s=1280x720:start_x=-0.7453:start_y=0.1127:start_scale=0.01:maxiter=500",
		"-frames:v", "1", texture).CombinedOutput()
	require.NoError(t, err, string(out))

	dir := t.TempDir()

	// The second run reads the texture and the clip from the cache.
	for range 2 {
		var stdout, stderr bytes.Buffer

		code := run([]string{"-texture", texture, "-dir", dir, "-run", "^static$"}, &stdout, &stderr)
		require.Zero(t, code, stderr.String())
		assert.Contains(t, stdout.String(), "classified right: 1/1")
	}
}

func TestRenderFailure(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	dir := t.TempDir()
	r := renderer{ffmpeg: "ffmpeg", texture: filepath.Join(dir, "missing.png"), dir: dir}

	_, err := r.render(t.Context(), clip{name: "static", fps: 25, frames: 2})
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(dir, "static.mp4"), "a failed clip is not cached")
}

func TestMeasureErrors(
	t *testing.T,
) {
	failing := func(context.Context, string, analysis.Options) (*analysis.Report, error) {
		return nil, errors.New("boom")
	}

	_, err := measure(t.Context(), failing, "clip.mp4", clip{name: "x"})
	require.Error(t, err)

	calls := 0
	secondFails := func(context.Context, string, analysis.Options) (*analysis.Report, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("boom")
		}

		return &analysis.Report{}, nil
	}

	_, err = measure(t.Context(), secondFails, "clip.mp4", clip{name: "x"})
	require.Error(t, err)

	empty := func(context.Context, string, analysis.Options) (*analysis.Report, error) {
		return &analysis.Report{}, nil
	}

	_, err = measure(t.Context(), empty, "clip.mp4", clip{name: "x"})
	require.ErrorIs(t, err, errNoMotion)
	assert.True(t, strings.HasPrefix(err.Error(), "x:"))
}
