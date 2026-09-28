package analysis_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/probe"
)

// TestAnalyzeSegmentsVideoStartingLate analyses in segments a video
// starting after its audio, as some concatenations do: every segment must
// seek to its planned frame, without falling back to a single pass, and
// give the single pass's report.
func TestAnalyzeSegmentsVideoStartingLate(
	t *testing.T,
) {
	path := testutil.Generate(t, testutil.Clip{Seconds: 48, GOP: 50, Audio: true, VideoDelay: 0.04})

	analyze := func(decoders int) (*analysis.Report, string) {
		var logs bytes.Buffer

		a := analysis.New(
			slog.New(slog.NewTextHandler(&logs, nil)),
			probe.NewFFprobe("ffprobe"),
			bitstream.NewFFprobeReader("ffprobe"),
			decode.NewFFmpeg("ffmpeg", 0),
			nil,
		)

		report, err := a.Analyze(t.Context(), path, analysis.Options{
			Video: analysis.VideoOptions{Decoders: decoders, SkipMotion: true},
		})
		require.NoError(t, err)

		return report, logs.String()
	}

	want, _ := analyze(1)
	got, logs := analyze(2)

	assert.NotContains(t, logs, "analysing in one pass")
	assert.Equal(t, want.Video, got.Video)
	assert.Equal(t, want.Frames, got.Frames)
}
