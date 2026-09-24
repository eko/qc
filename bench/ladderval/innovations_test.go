package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

func TestCache(
	t *testing.T,
) {
	header := cacheFile{Source: "s.mp4", Codec: "h264", Preset: "fast"}
	p := encode.Params{Width: 1280, Height: 720, CRF: 23}

	calls := 0
	counting := func(p encode.Params) (ladder.Probe, error) {
		calls++

		return fakeMeasure(p)
	}

	path := filepath.Join(t.TempDir(), "cache.json")

	measure, err := cached(path, header, counting)
	require.NoError(t, err)

	first, err := measure(p)
	require.NoError(t, err)

	again, err := cached(path, header, counting)
	require.NoError(t, err)

	second, err := again(p)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, calls, "the second run reads the cache")

	testCases := []struct {
		name    string
		path    func(t *testing.T) string
		header  cacheFile
		wantErr string
	}{
		{name: "other settings", path: func(*testing.T) string { return path }, header: cacheFile{Source: "s.mp4", Codec: "av1"}, wantErr: ErrCacheMismatch.Error()},
		{
			name: "corrupt file",
			path: func(t *testing.T) string {
				bad := filepath.Join(t.TempDir(), "bad.json")
				require.NoError(t, os.WriteFile(bad, []byte("{"), 0o600))

				return bad
			},
			header:  header,
			wantErr: "decode cache",
		},
		{name: "unreadable", path: func(t *testing.T) string { return t.TempDir() }, header: header, wantErr: "read cache"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := cached(testCase.path(t), testCase.header, counting)
			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}

func TestCacheErrors(
	t *testing.T,
) {
	header := cacheFile{Source: "s.mp4"}

	failing, err := cached(filepath.Join(t.TempDir(), "c.json"), header, failAfter(0))
	require.NoError(t, err)

	_, err = failing(encode.Params{})
	require.ErrorIs(t, err, errMeasure)

	unwritable, err := cached(filepath.Join(t.TempDir(), "missing", "c.json"), header, fakeMeasure)
	require.NoError(t, err)

	_, err = unwritable(encode.Params{Width: 2, Height: 2})
	require.ErrorContains(t, err, "write cache")

	empty := filepath.Join(t.TempDir(), "empty.json")
	require.NoError(t, os.WriteFile(empty, []byte(`{"source":"s.mp4"}`), 0o600))

	fromEmpty, err := cached(empty, header, fakeMeasure)
	require.NoError(t, err)

	_, err = fromEmpty(encode.Params{Width: 2, Height: 2})
	require.NoError(t, err, "a cache without entries is extended")
}

func TestEncodeCount(
	t *testing.T,
) {
	fast := fakeLadder()
	fast.Rungs[0].Calibrated = true

	assert.Equal(t, "encodes: 3 probes + 1 verifications + 1 calibrations = 5", encodeCount(fast))
}

// fakeFrames models a title of two 10-frame shots: the second is harder
// (twice the bits) and gains more from them.
func fakeFrames(
	p encode.Params,
	chunks []encode.Chunk,
) (frames, error) {
	f := frames{}
	total, sum := 0, 0.0

	for i := range 20 {
		crf := p.CRF
		for _, c := range chunks {
			if i >= c.Start && i < c.Start+c.Frames {
				crf = c.CRF
			}
		}

		hard := 1.0
		if i >= 10 {
			hard = 2
		}

		size := int(1000 * hard * math.Exp(-(crf-20)/6))
		score := 100 - (crf-10)*hard

		f.sizes = append(f.sizes, size)
		f.scores = append(f.scores, score)
		total += size
		sum += score
	}

	f.bitrate, f.vmaf = float64(total*8*25/20), sum/20

	return f, nil
}

// failingFrames fails at its n-th call.
func failingFrames(
	n int,
) frameMeasureFunc {
	return func(p encode.Params, chunks []encode.Chunk) (frames, error) {
		if n == 0 {
			return frames{}, errMeasure
		}

		n--

		return fakeFrames(p, chunks)
	}
}

// perShotLadder is a two-shot ladder with one per-shot rung.
func perShotLadder() *ladder.Result {
	fast := fakeLadder()
	fast.Shots = []ladder.Shot{{Start: 0, Frames: 10}, {Start: 10, Frames: 10}}
	fast.Rungs[0].PerShot = &ladder.PerShot{
		Chunks: []encode.Chunk{{Start: 0, Frames: 10, CRF: 24}, {Start: 10, Frames: 10, CRF: 20}},
		Gain:   0.05,
	}

	return fast
}

func TestCheckPerShot(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		fast    *ladder.Result
		measure frameMeasureFunc
		wantOut string
		wantErr error
	}{
		{name: "per-shot against per-title", fast: perShotLadder(), measure: fakeFrames, wantOut: "mean bitrate saved by per-shot at equal VMAF (full title)"},
		{name: "no per-shot rung", fast: fakeLadder(), measure: fakeFrames, wantErr: ErrNoPerShot},
		{name: "per-title encode", fast: perShotLadder(), measure: failingFrames(0), wantErr: errMeasure},
		{name: "slope encode", fast: perShotLadder(), measure: failingFrames(1), wantErr: errMeasure},
		{name: "per-shot encode", fast: perShotLadder(), measure: failingFrames(2), wantErr: errMeasure},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer

			err := checkPerShot(&out, testCase.fast, codecOf("h264"), 0, testCase.measure)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Contains(t, out.String(), testCase.wantOut)
			assert.Contains(t, out.String(), "5.0%", "the digest's gain is shown alongside")
		})
	}

	err := checkPerShot(failingWriter{}, perShotLadder(), codecOf("h264"), 1, fakeFrames)
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestCheckShotOptimum(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		fast        *ladder.Result
		resolutions bool
		measure     frameMeasureFunc
		wantLog     []string
		wantErr     error
	}{
		{name: "optimum at least as good as per-shot", fast: perShotLadder(), measure: fakeFrames, wantLog: []string{"shots 720p crf 18"}},
		{
			name: "resolutions: the neighbouring rung's too", fast: resolutionLadder(), resolutions: true, measure: fakeFrames,
			wantLog: []string{"shots 720p crf 18", "shots 360p crf 16"},
		},
		{name: "no per-shot rung", fast: fakeLadder(), measure: fakeFrames, wantErr: ErrNoPerShot},
		{name: "grid encode", fast: perShotLadder(), measure: failingFrames(0), wantErr: errMeasure},
		{name: "per-shot encode", fast: perShotLadder(), measure: failingFrames(5), wantErr: errMeasure},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out, log bytes.Buffer

			err := checkShotOptimum(&out, &log, testCase.fast, 1, 4, 2, testCase.resolutions, testCase.measure)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Contains(t, out.String(), "per-shot gain")
			assert.Equal(t, testCase.resolutions, strings.Contains(out.String(), "res+CRF gain"))

			for _, want := range testCase.wantLog {
				assert.Contains(t, log.String(), want)
			}
		})
	}

	err := checkShotOptimum(failingWriter{}, &bytes.Buffer{}, perShotLadder(), 1, 2, 2, false, fakeFrames)
	require.ErrorIs(t, err, os.ErrClosed)
}

// resolutionLadder is perShotLadder with a probe curve at 360p reaching
// the 720p rung's quality at CRF 20.
func resolutionLadder() *ladder.Result {
	fast := perShotLadder()
	fast.Probes = []ladder.Probe{
		{Width: 640, Height: 360, CRF: 16, Bitrate: 2e6, VMAF: 98},
		{Width: 640, Height: 360, CRF: 24, Bitrate: 1e6, VMAF: 90},
	}

	return fast
}

func TestNeighbourHeights(
	t *testing.T,
) {
	rungs := []ladder.Rung{{Height: 1080}, {Height: 720}, {Height: 720}, {Height: 540}, {Height: 360}}

	assert.Equal(t, []int{1080, 720}, neighbourHeights(rungs, 1080))
	assert.Equal(t, []int{720, 540, 360}, neighbourHeights(rungs, 540))
	assert.Equal(t, []int{540, 360}, neighbourHeights(rungs, 360))
}

func TestShotTable(
	t *testing.T,
) {
	// Three CRFs at 720p, and one 360p setting the hard shot is better off
	// with.
	table := shotTable{
		settings: []setting{{1280, 720, 20}, {1280, 720, 25}, {1280, 720, 30}, {640, 360, 20}},
		weights:  []float64{0.5, 0.5},
		rates:    [][]float64{{2e6, 4e6}, {1e6, 2e6}, {0.5e6, 1e6}, {0.3e6, 1e6}},
		vmafs:    [][]float64{{98, 90}, {96, 82}, {92, 72}, {80, 84}},
	}

	title := table.perTitleAt(89, 720)
	optimum := table.optimumAt(89, 720)
	both := table.optimumAt(89, 0)

	assert.InDelta(t, 1.5e6, title, 1)
	assert.LessOrEqual(t, optimum, title, "the optimum never costs more than one CRF")
	assert.Less(t, both, optimum, "resolution is one more degree of freedom")
	assert.True(t, math.IsNaN(table.perTitleAt(99, 720)))
	assert.True(t, math.IsNaN(table.optimumAt(99, 720)))
}

func TestFrameHelpers(
	t *testing.T,
) {
	cmp := &analysis.Comparison{
		Distorted: &analysis.Report{Bitstream: &bitstream.Report{AverageBitrate: 1000, FrameSizes: []int{10, 20}}},
		VMAF:      &quality.Result{Mean: 90, Frames: []quality.FrameScore{{Index: 1, Score: 80}, {Index: 5, Score: 1}}},
	}

	got := framesOf(cmp)
	assert.Equal(t, []float64{0, 80}, got.scores, "frames beyond the sizes are ignored")

	a, b := frames{bitrate: 1000, vmaf: 90}, frames{bitrate: math.E * 1000, vmaf: 100}
	assert.InDelta(t, 10, localSlope(a, b), 1e-9)
	assert.Zero(t, localSlope(a, a))
	assert.InDelta(t, 0.1, equalQualityGain(a, frames{bitrate: 900, vmaf: 90}, 10), 1e-9)
	assert.InDelta(t, 0.1, equalQualityGain(a, frames{bitrate: 900, vmaf: 91}, 0), 1e-9)
	assert.InDelta(t, 4.0, slopeStep(codecOf("av1")), 1e-9)
}

func TestRetime(
	t *testing.T,
) {
	clip := testutil.Generate(t, testutil.Clip{Codec: "libsvtav1", Seconds: 1, Args: []string{"-preset", "12"}})
	rate := media.Rational{Num: 25, Den: 1}

	require.NoError(t, retime(t.Context(), "ffmpeg", codecOf("av1"), clip, rate))
	require.NoError(t, retime(t.Context(), "no-such-ffmpeg", codecOf("h264"), clip, rate), "only AV1 is retimed")
	require.Error(t, retime(t.Context(), "no-such-ffmpeg", codecOf("av1"), clip, rate))

	dir := filepath.Join(t.TempDir(), "gone.mp4")
	require.NoError(t, os.WriteFile(dir, nil, 0o600))
	require.ErrorContains(t, retime(t.Context(), "ffmpeg", codecOf("av1"), dir, rate), "retime")
}

func TestRunInnovations(
	t *testing.T,
) {
	perShot := realLadderWith(t, testutil.Clip{GOP: 25}, "h264", func(res *ladder.Result) {
		res.Shots = []ladder.Shot{{Start: 0, Frames: 25}, {Start: 25, Frames: 25}}
		res.Rungs[0].PerShot = &ladder.PerShot{Chunks: []encode.Chunk{{Start: 0, Frames: 25, CRF: 33}, {Start: 25, Frames: 25, CRF: 27}}}
	})
	grain := realLadderWith(t, testutil.Clip{GOP: 50, Filter: "noise=alls=10:allf=t"}, "av1", func(res *ladder.Result) {
		res.Grain = &ladder.GrainReport{Level: 20}
		res.Rungs[0].CRF = 40
	})
	clean := testutil.Generate(t, testutil.Clip{GOP: 50, Name: "clean.mp4"})
	sizeless := realLadderWith(t, testutil.Clip{GOP: 50}, "h264", func(res *ladder.Result) {
		res.Rungs[0].Width, res.Rungs[0].Height = 0, 0
	})

	cache := filepath.Join(t.TempDir(), "cache.json")
	corrupt := filepath.Join(t.TempDir(), "corrupt.json")
	require.NoError(t, os.WriteFile(corrupt, []byte("{"), 0o600))

	testCases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "rungs with a cache", args: []string{"-rungs-only", "-precision", "1", "-cache", cache, realLadder(t)}, wantStdout: "encodes: 1 probes"},
		{name: "corrupt cache", args: []string{"-rungs-only", "-cache", corrupt, realLadder(t)}, wantCode: 1, wantStderr: "decode cache"},
		{name: "per-shot and optimum", args: []string{"-per-shot", "-shot-optimum", "1", "-shot-span", "2", "-shot-step", "2", perShot}, wantStdout: "optimum gain", wantStderr: "shots 180p crf 28"},
		{name: "per-shot only", args: []string{"-per-shot", perShot}, wantStdout: "mean bitrate saved by per-shot"},
		{name: "optimum without per-shot rungs", args: []string{"-shot-optimum", "1", realLadder(t)}, wantCode: 1, wantStderr: ErrNoPerShot.Error()},
		{name: "per-shot encode error", args: []string{"-per-shot", "-ffmpeg", "no-such-ffmpeg", perShot}, wantCode: 1, wantStderr: "encode 180p"},
		{name: "per-shot measure error", args: []string{"-per-shot", "-ffprobe", "no-such-ffprobe", perShot}, wantCode: 1, wantStderr: "measure 180p"},
		{name: "film grain against the clean source", args: []string{"-grain-reference", clean, grain}, wantStdout: "film grain level 20"},
		{name: "film grain encode error", args: []string{"-grain-reference", clean, "-ffmpeg", "no-such-ffmpeg", grain}, wantCode: 1, wantStderr: "encode 180p"},
		{name: "film grain reference error", args: []string{"-grain-reference", "missing.mp4", grain}, wantCode: 1, wantStderr: "against the clean reference"},
		{name: "film grain noise error", args: []string{"-grain-reference", clean, sizeless}, wantCode: 1, wantStderr: "noise"},
		{name: "film grain measure error", args: []string{"-grain-reference", clean, "-ffprobe", "no-such-ffprobe", grain}, wantCode: 1, wantStderr: "measure 180p"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(t.Context(), testCase.args, &stdout, &stderr)

			assert.Equal(t, testCase.wantCode, code, stderr.String())
			assert.Contains(t, stdout.String(), testCase.wantStdout)
			assert.Contains(t, stderr.String(), testCase.wantStderr)
		})
	}
}
