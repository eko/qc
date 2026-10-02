package ladder

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

func TestPreparedSharedByBuilds(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		source *analysis.Report
		opts   []Options
		// wantAnalyses counts the analyses of the source over the builds:
		// its inspection by Prepare, then its frame analyses.
		wantAnalyses int
		wantDigests  int
	}{
		{
			name:         "two codecs: one frame analysis, one digest",
			source:       analysedSource(600),
			opts:         []Options{{Codec: "h264", SkipVerify: true}, {Codec: "av1", SkipVerify: true}},
			wantAnalyses: 2,
			wantDigests:  1,
		},
		{
			name:         "three codecs",
			source:       analysedSource(600),
			opts:         []Options{{Codec: "h264", SkipVerify: true}, {Codec: "hevc", SkipVerify: true}, {Codec: "av1", SkipVerify: true}},
			wantAnalyses: 2,
			wantDigests:  1,
		},
		{
			name:   "uniform digests are shared without any analysis",
			source: analysedSource(600),
			opts: []Options{
				{Codec: "h264", SkipVerify: true, DigestSampling: DigestUniform},
				{Codec: "av1", SkipVerify: true, DigestSampling: DigestUniform},
			},
			wantAnalyses: 1,
			wantDigests:  1,
		},
		{
			name:   "builds asking for other segments get their own digest, on the analysis made once",
			source: analysedSource(600),
			opts: []Options{
				{Codec: "h264", SkipVerify: true},
				{Codec: "av1", SkipVerify: true, DigestSampling: DigestTop},
			},
			wantAnalyses: 2,
			wantDigests:  2,
		},
		{
			name:   "per-shot rungs of the second build read the analysis of the first",
			source: analysedSource(600),
			opts: []Options{
				{Codec: "h264", SkipVerify: true},
				{Codec: "h264", PerShot: true, DigestSampling: DigestUniform},
			},
			wantAnalyses: 2,
			wantDigests:  2,
		},
		{
			name:         "a title used whole is extracted once",
			source:       analysedSource(30),
			opts:         []Options{{Codec: "h264", SkipVerify: true}, {Codec: "av1", SkipVerify: true}},
			wantAnalyses: 1,
			wantDigests:  1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(rateModel{}, testCase.source)
			engine := labEngine(lab)

			prepared, err := engine.Prepare(t.Context(), sourcePath)
			require.NoError(t, err)

			var results []*Result

			for _, opts := range testCase.opts {
				opts.Prepared = prepared

				res, err := engine.Build(t.Context(), sourcePath, opts)
				require.NoError(t, err)

				results = append(results, res)
			}

			assert.Len(t, lab.analyses, testCase.wantAnalyses)
			require.Len(t, lab.digests, testCase.wantDigests)

			for _, res := range results {
				assert.Same(t, results[0].Source, res.Source, "one inspection for every build")
				assert.NotEmpty(t, res.Rungs)
			}

			// The digest outlives the builds, and Close removes it.
			dir := filepath.Dir(lab.digests[0].Destination)
			assert.DirExists(t, dir)

			prepared.Close()
			assert.NoDirExists(t, dir)

			prepared.Close()
		})
	}
}

func TestPreparedSameFramesForEveryCodec(
	t *testing.T,
) {
	lab := newFakeLab(rateModel{}, analysedSource(600))
	engine := labEngine(lab)

	prepared, err := engine.Prepare(t.Context(), sourcePath)
	require.NoError(t, err)

	defer prepared.Close()

	first, err := engine.Build(t.Context(), sourcePath, Options{Codec: "h264", SkipVerify: true, Prepared: prepared})
	require.NoError(t, err)

	second, err := engine.Build(t.Context(), sourcePath, Options{Codec: "av1", SkipVerify: true, Prepared: prepared})
	require.NoError(t, err)

	assert.Equal(t, DigestBalanced, second.Digest.Sampling)
	assert.Equal(t, first.Digest, second.Digest)
	assert.Contains(t, first.Timings, StageAnalysis, "the first build analyses the source")
	assert.NotContains(t, second.Timings, StageAnalysis, "the second reads that analysis")

	// Every encode of both codecs reads the one digest file.
	require.Len(t, lab.digests, 1)

	for path := range lab.sources {
		assert.Equal(t, lab.digests[0].Destination, path)
	}

	assert.NotEmpty(t, lab.sources)
}

func TestPreparedErrors(
	t *testing.T,
) {
	t.Run("source inspection", func(t *testing.T) {
		lab := newFakeLab(rateModel{}, analysedSource(600))
		lab.failOn = failing(opAnalyze, "source.mov", 0)

		_, err := labEngine(lab).Prepare(t.Context(), sourcePath)
		require.ErrorIs(t, err, errFake)
		assert.ErrorContains(t, err, "ladder: inspect /titles/source.mov")
	})

	t.Run("prepared for another source", func(t *testing.T) {
		lab := newFakeLab(rateModel{}, analysedSource(600))
		engine := labEngine(lab)

		prepared, err := engine.Prepare(t.Context(), sourcePath)
		require.NoError(t, err)

		_, err = engine.Build(t.Context(), "/titles/other.mov", Options{Codec: "h264", Prepared: prepared})
		require.ErrorIs(t, err, ErrPreparedSource)
		assert.Empty(t, lab.digests)
	})

	t.Run("the directory of the shared digest cannot be created", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))

		_, err := labEngine(newFakeLab(rateModel{}, analysedSource(600))).Prepare(t.Context(), sourcePath)
		require.ErrorContains(t, err, "ladder: work dir")
	})

	t.Run("a failed analysis is not kept for the next build", func(t *testing.T) {
		lab := newFakeLab(rateModel{}, analysedSource(600))
		lab.failOn = failing(opAnalyze, "source.mov", 1)
		engine := labEngine(lab)

		prepared, err := engine.Prepare(t.Context(), sourcePath)
		require.NoError(t, err)

		defer prepared.Close()

		_, err = engine.Build(t.Context(), sourcePath, Options{Codec: "h264", SkipVerify: true, Prepared: prepared})
		require.ErrorIs(t, err, errFake)

		res, err := engine.Build(t.Context(), sourcePath, Options{Codec: "h264", SkipVerify: true, Prepared: prepared})
		require.NoError(t, err)
		assert.Equal(t, DigestBalanced, res.Digest.Sampling)
		assert.Equal(t, media.Seconds(40), res.Digest.Duration)
	})

	var none *Prepared

	none.Close()
}
