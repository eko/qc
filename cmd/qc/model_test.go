package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
)

// TestModelCommands measures a corpus of two tiny titles, trains a model on
// it and builds a ladder with that model: the chain a user follows.
func TestModelCommands(
	t *testing.T,
) {
	t.Parallel()

	dir := t.TempDir()
	corpusPath, modelPath := filepath.Join(dir, "corpus.jsonl"), filepath.Join(dir, "model.json")

	titles := []string{
		testutil.Generate(t, testutil.Clip{GOP: 25, Seconds: 4, Name: "bars.mp4"}),
		testutil.Generate(t, testutil.Clip{GOP: 25, Seconds: 4, Name: "noise.mp4", Filter: "noise=alls=20:allf=t"}),
	}
	grid := []string{"--heights", "180,90", "--crfs", "20,26,32,38", "--segments", "2"}

	code, stdout, stderr := execute(t, testEnv, append([]string{"model", "corpus", "--to", corpusPath, titles[0], titles[1]}, grid...)...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "18 measurements added to "+corpusPath)

	rows, err := predict.ReadRows(corpusPath)
	require.NoError(t, err)
	require.Len(t, rows, 18)
	assert.Equal(t, predict.FeaturesKind, rows[0].Kind)
	assert.Len(t, rows[0].Features["si"], 2)
	assert.Len(t, rows[0].Features["x264_23_bits"], 2)
	assert.Len(t, rows[0].Features[features.SourceQP], 2, "the quantisers of an H.264 source")
	assert.NotContains(t, rows[0].Features, "scale_90", "the scale loss is no longer described")
	assert.Len(t, rows[1].Bits, 2)
	assert.Len(t, rows[1].VMAF, 2)
	assert.Equal(t, 180, rows[1].SourceHeight)

	// A second run measures nothing: the file holds every encode.
	code, stdout, stderr = execute(t, testEnv, append([]string{"model", "corpus", "--to", corpusPath, titles[0]}, grid...)...)
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "0 measurements added")

	code, stdout, stderr = execute(t, testEnv, "model", "train", corpusPath, "--to", modelPath, "--anchor-crfs", "26,32,38")
	require.Equal(t, 0, code, stderr)
	assert.Contains(t, stdout, "h264 fast, 180p sources: 2 titles, 4 segments, grid of 2 resolutions × 4 CRFs")
	assert.Contains(t, stdout, "180p@26 90p@32")
	assert.Contains(t, stdout, "the analysis alone (--instant)")
	assert.Contains(t, stdout, "the frames alone (--instant --no-encode)")
	assert.Contains(t, stdout, "From the analysis alone, without any probe")
	assert.Contains(t, stdout, "Model written to "+modelPath)

	// The report of the model and of its corpus, of either alone, and of
	// the corpus for a codec it holds nothing of.
	page := filepath.Join(dir, "model.html")

	for _, args := range [][]string{{corpusPath, "--model", modelPath}, {"--model", modelPath}, {corpusPath}} {
		code, stdout, stderr = execute(t, testEnv, append([]string{"model", "report", "--to", page}, args...)...)
		require.Equal(t, 0, code, stderr)
		assert.Contains(t, stdout, "Report written to "+page)

		html, err := os.ReadFile(page)
		require.NoError(t, err)
		assert.Contains(t, string(html), "h264 fast")
		assert.Contains(t, string(html), "180p sources")
	}

	code, _, stderr = execute(t, testEnv, "model", "report", corpusPath, "-c", "av1", "--to", page)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "no av1 8 encodes")

	report := filepath.Join(dir, "ladder.json")
	code, _, stderr = execute(t, testEnv, "ladder", titles[0], "--heights", "180,90", "--ladder-model", modelPath,
		"--no-verify", "--min-bitrate", "1000", "-f", "json", "-o", report)
	require.Equal(t, 0, code, stderr)

	data, err := os.ReadFile(report)
	require.NoError(t, err)

	var res ladder.Result
	require.NoError(t, json.Unmarshal(data, &res))

	assert.Equal(t, ladder.ProbingModel, res.Probing.Mode)
	require.NotNil(t, res.Probing.Model)
	assert.Empty(t, res.Probing.Model.Fallback)
	assert.Equal(t, 2, res.Probing.Model.Titles)
	assert.Less(t, res.ProbeEncodes(), len(res.Probes), "some probes are predicted")
	assert.NotEmpty(t, res.Rungs)

	// From the analysis alone: nothing is encoded, nothing verified.
	code, _, stderr = execute(t, testEnv, "ladder", titles[1], "--heights", "180,90", "--ladder-model", modelPath,
		"--instant", "--min-bitrate", "1000", "-f", "json", "-o", report)
	require.Equal(t, 0, code, stderr)

	data, err = os.ReadFile(report)
	require.NoError(t, err)

	res = ladder.Result{}
	require.NoError(t, json.Unmarshal(data, &res))

	assert.Equal(t, ladder.ProbingPredicted, res.Probing.Mode)
	assert.Empty(t, res.Probing.Model.Anchors)
	assert.Positive(t, res.Probing.Model.Features)
	assert.Zero(t, res.ProbeEncodes())
	require.NotEmpty(t, res.Rungs)
	assert.Nil(t, res.Rungs[0].Measured)

	withEncodes := res.Probing.Model.Features

	// Without the two fast encodes either: the frames and the source's own
	// encode, a design reading fewer features.
	code, _, stderr = execute(t, testEnv, "ladder", titles[1], "--heights", "180,90", "--ladder-model", modelPath,
		"--instant", "--no-encode", "--min-bitrate", "1000", "-f", "json", "-o", report)
	require.Equal(t, 0, code, stderr)

	data, err = os.ReadFile(report)
	require.NoError(t, err)

	res = ladder.Result{}
	require.NoError(t, json.Unmarshal(data, &res))

	assert.Equal(t, ladder.ProbingPredicted, res.Probing.Mode)
	assert.Empty(t, res.Probing.Model.Fallback)
	assert.Positive(t, res.Probing.Model.Features)
	assert.Less(t, res.Probing.Model.Features, withEncodes)
	assert.Zero(t, res.ProbeEncodes())
	require.NotEmpty(t, res.Rungs)
}

func TestModelCommandErrors(
	t *testing.T,
) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.jsonl")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))

	testCases := []struct {
		name string
		args []string
		want string
	}{
		{name: "mistyped subcommand", args: []string{"model", "coprpus", "videos/", "--to", "c.jsonl"}, want: `unknown command "coprpus" for "qc model" (did you mean "qc model corpus"?)`},
		{name: "folder without a video", args: []string{"model", "corpus", dir, "--to", filepath.Join(dir, "c.jsonl")}, want: "no video in the folder"},
		{name: "corpus without a file", args: []string{"model", "corpus", "a.mp4"}, want: "--to is required"},
		{name: "corpus of an unknown codec", args: []string{"model", "corpus", "a.mp4", "--to", filepath.Join(dir, "c.jsonl"), "-c", "vp8"}, want: "unknown codec"},
		{name: "one CRF", args: []string{"model", "corpus", "a.mp4", "--to", filepath.Join(dir, "c.jsonl"), "--crf-count", "1"}, want: "a grid needs at least two CRFs"},
		{name: "CRFs counted and listed", args: []string{"model", "corpus", "a.mp4", "--to", filepath.Join(dir, "c.jsonl"), "--crf-count", "6", "--crfs", "20,30"}, want: "--crf-count and --crfs"},
		{name: "train without a file", args: []string{"model", "train", empty}, want: "--to is required"},
		{name: "train on nothing", args: []string{"model", "train", empty, "--to", filepath.Join(dir, "m.json")}, want: "no h264 fast encodes"},
		{name: "one anchor CRF", args: []string{"model", "train", empty, "--to", filepath.Join(dir, "m.json"), "--anchor-crfs", "27"}, want: "--anchor-crfs takes three CRFs"},
		{name: "report of nothing", args: []string{"model", "report", "--to", filepath.Join(dir, "r.html")}, want: "nothing to report"},
		{name: "report without a file", args: []string{"model", "report", empty}, want: "--to is required"},
		{name: "report of a model that does not exist", args: []string{"model", "report", "--model", filepath.Join(dir, "missing.json"), "--to", filepath.Join(dir, "r.html")}, want: "missing.json"},
		{name: "probing by model without one", args: []string{"ladder", "a.mp4", "--probing", "model"}, want: "need --ladder-model"},
		{name: "instant without a model", args: []string{"ladder", "a.mp4", "--instant"}, want: "need --ladder-model"},
		{name: "no encode without a predicted ladder", args: []string{"ladder", "a.mp4", "--no-encode"}, want: "--no-encode needs --instant or --probing predicted"},
		{name: "model that does not exist", args: []string{"ladder", "a.mp4", "--ladder-model", filepath.Join(dir, "missing.json")}, want: "--ladder-model"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			code, _, stderr := execute(t, testEnv, testCase.args...)
			assert.NotEqual(t, 0, code)
			assert.Contains(t, stderr, testCase.want)
		})
	}
}

func TestCorpusSources(
	t *testing.T,
) {
	dir := t.TempDir()
	files := []string{"b.mov", "a.MP4", "poster.jpeg", "notes.vtt", ".hidden.mp4", "series/s01/e02.mkv", "series/s01/e01.mkv", ".cache/tmp.mp4"}

	for _, name := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}

	// A folder named like a video is a folder.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.mp4"), 0o750))

	got, err := corpusSources([]string{"first.mp4", dir, "missing.mov"})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"first.mp4",
		filepath.Join(dir, "a.MP4"),
		filepath.Join(dir, "b.mov"),
		filepath.Join(dir, "series", "s01", "e01.mkv"),
		filepath.Join(dir, "series", "s01", "e02.mkv"),
		"missing.mov",
	}, got)

	_, err = corpusSources([]string{t.TempDir()})
	require.ErrorIs(t, err, ErrNoVideos)
}

// TestModelCorpusGoesOnAfterAFailedTitle measures a folder holding a file
// that is no video: it is reported, and the other title is measured.
func TestModelCorpusGoesOnAfterAFailedTitle(
	t *testing.T,
) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.jsonl")

	good := testutil.Generate(t, testutil.Clip{GOP: 25, Seconds: 2, Name: "good.mp4"})
	folder := filepath.Dir(good)
	require.NoError(t, os.WriteFile(filepath.Join(folder, "broken.mp4"), []byte("not a video"), 0o600))

	code, stdout, stderr := execute(t, testEnv, "model", "corpus", folder, "--to", corpusPath, "--heights", "180", "--crfs", "30")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "titles could not be measured: 1 of 2")
	assert.Contains(t, stderr, "broken.mp4:")
	assert.Contains(t, stdout, "2 measurements added to "+corpusPath, "the features and the encode of the other title")

	rows, err := predict.ReadRows(corpusPath)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, good, rows[0].Source)
	assert.Equal(t, 1, rows[0].Segments)
}

func TestDigestDuration(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		config LadderConfig
		want   float64
	}{
		{name: "the engine's default", config: LadderConfig{}, want: 0},
		{name: "an instant ladder reads a shorter digest", config: LadderConfig{Instant: true}, want: 20},
		{name: "unless told another", config: LadderConfig{Instant: true, DigestDuration: 40}, want: 40},
		{name: "as asked", config: LadderConfig{DigestDuration: 12}, want: 12},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.want, digestDuration(testCase.config).Seconds(), 1e-9)
		})
	}
}
