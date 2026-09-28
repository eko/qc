package overlay_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/overlay"
)

var errBurn = errors.New("encoder crashed")

// fakeBurner records the burns it is asked for, the script each one read
// and the scripts of its segments. Its burns fail with errs, in turn, then
// with err.
type fakeBurner struct {
	err    error
	errs   []error
	specs  []encode.BurnSpec
	script string
	slices []string
}

func (b *fakeBurner) Burn(
	_ context.Context,
	spec encode.BurnSpec,
) error {
	b.specs = append(b.specs, spec)

	script, err := os.ReadFile(spec.Subtitles)
	if err != nil {
		return err
	}

	b.script = string(script)
	b.slices = nil

	for _, seg := range spec.Segments {
		slice, err := os.ReadFile(seg.Subtitles)
		if err != nil {
			return err
		}

		b.slices = append(b.slices, string(slice))
	}

	if spec.Progress != nil {
		spec.Progress(2)
	}

	if len(b.errs) > 0 {
		err, b.errs = b.errs[0], b.errs[1:]

		return err
	}

	return b.err
}

// tinyReport is the inspection of a three-frame title with audio.
func tinyReport() *analysis.Report {
	return &analysis.Report{
		Info: &media.Info{
			Path:  "in.mov",
			Video: []media.VideoStream{{Width: 1280, Height: 720}},
			Audio: []media.AudioStream{{Codec: "pcm_s24le"}},
		},
		Bitstream: &bitstream.Report{
			Duration:   media.Seconds(0.12),
			PTS:        []media.Duration{0, media.Seconds(0.04), media.Seconds(0.08)},
			FrameSizes: []int{1000, 200, 300},
			KeyFlags:   []bool{true, false, false},
		},
	}
}

func TestRendererRender(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		input        overlay.Input
		burnErr      error
		wantErr      error
		wantBurns    int
		wantAudio    string
		wantProgress []overlay.Progress
	}{
		{
			name:         "burns the script with the render settings",
			input:        overlay.Input{Report: tinyReport()},
			wantBurns:    1,
			wantAudio:    "pcm_s24le",
			wantProgress: []overlay.Progress{{Done: 2, Total: 3}},
		},
		{
			name:      "without audio",
			input:     overlay.Input{Report: &analysis.Report{Bitstream: tinyReport().Bitstream}},
			wantBurns: 1,
			// The progress is still relayed.
			wantProgress: []overlay.Progress{{Done: 2, Total: 3}},
		},
		{
			name:    "a report without frames burns nothing",
			input:   overlay.Input{Report: &analysis.Report{}},
			wantErr: overlay.ErrNoFrames,
		},
		{
			name:         "burn failure",
			input:        overlay.Input{Report: tinyReport()},
			burnErr:      errBurn,
			wantErr:      errBurn,
			wantBurns:    1,
			wantAudio:    "pcm_s24le",
			wantProgress: []overlay.Progress{{Done: 2, Total: 3}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			burner := &fakeBurner{err: testCase.burnErr}

			var progress []overlay.Progress

			err := overlay.NewRenderer(burner).Render(t.Context(), "in.mov", "out.mp4", testCase.input, overlay.RenderOptions{
				Options:  overlay.Options{Items: []overlay.Item{overlay.ItemTime}},
				Height:   720,
				CRF:      18,
				Preset:   "veryfast",
				FontsDir: "/fonts",
				Progress: func(p overlay.Progress) { progress = append(progress, p) },
			})

			require.ErrorIs(t, err, testCase.wantErr)
			require.Len(t, burner.specs, testCase.wantBurns)

			if testCase.wantBurns == 0 {
				return
			}

			spec := burner.specs[0]
			assert.Equal(t, "in.mov", spec.Source)
			assert.Equal(t, "out.mp4", spec.Output)
			assert.Equal(t, 720, spec.Height)
			assert.InDelta(t, 18, spec.CRF, 1e-9)
			assert.Equal(t, "veryfast", spec.Preset)
			assert.Equal(t, "/fonts", spec.FontsDir)
			assert.Equal(t, testCase.wantAudio, spec.AudioCodec)
			assert.Equal(t, testCase.wantProgress, progress)
			assert.Contains(t, burner.script, "[Script Info]")
			assert.NotContains(t, burner.script, "RATE", "only the chosen items")

			_, statErr := os.Stat(spec.Subtitles)
			assert.ErrorIs(t, statErr, os.ErrNotExist, "the script is removed after the burn")
		})
	}
}

// longReport is the inspection of a two-minute 25 fps title starting
// 0.14 s into its container, 0.04 s after its audio, with a keyframe
// every two seconds.
func longReport() *analysis.Report {
	const frames = 3000

	bs := &bitstream.Report{Start: media.Seconds(0.14), Duration: media.Seconds(frames * 0.04)}
	for i := range frames {
		bs.PTS = append(bs.PTS, media.Seconds(float64(i)*0.04))
		bs.FrameSizes = append(bs.FrameSizes, 1000)
		bs.KeyFlags = append(bs.KeyFlags, i%50 == 0)
	}

	return &analysis.Report{
		Info: &media.Info{
			Path: "in.mp4", StartTime: media.Seconds(0.1),
			Video: []media.VideoStream{{Width: 1920, Height: 1080}},
			Audio: []media.AudioStream{{Codec: "aac"}},
		},
		Bitstream: bs,
	}
}

// TestRendererSegments plans the segments of a long title: at keyframes,
// on the container timeline, each with a slice of the script; one pass
// when asked, or when the segments failed.
func TestRendererSegments(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		workers      int
		errs         []error
		wantErr      error
		wantBurns    int
		wantSegments []encode.BurnSegment
	}{
		{
			name:      "four segments of the two-minute title",
			wantBurns: 1,
			wantSegments: []encode.BurnSegment{
				{From: media.Seconds(0.14), To: media.Seconds(30.14), Frames: 750},
				{From: media.Seconds(30.14), To: media.Seconds(60.14), Frames: 750},
				{From: media.Seconds(60.14), To: media.Seconds(90.14), Frames: 750},
				{From: media.Seconds(90.14), Frames: 750},
			},
		},
		{name: "a single pass asked for", workers: 1, wantBurns: 1},
		{name: "segments that failed, rendered again in one pass", errs: []error{encode.ErrBurnSegment}, wantBurns: 2},
		{
			name: "other failures are returned", errs: []error{errBurn}, wantErr: errBurn, wantBurns: 1,
			wantSegments: []encode.BurnSegment{{}, {}, {}, {}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			burner := &fakeBurner{errs: testCase.errs}

			err := overlay.NewRenderer(burner).Render(t.Context(), "in.mp4", "out.mp4", overlay.Input{Report: longReport()},
				overlay.RenderOptions{Encoder: encode.BurnVideoToolbox, Workers: testCase.workers})
			require.ErrorIs(t, err, testCase.wantErr)
			require.Len(t, burner.specs, testCase.wantBurns)

			first, last := burner.specs[0], burner.specs[len(burner.specs)-1]
			assert.Equal(t, encode.BurnVideoToolbox, first.Encoder)
			assert.Equal(t, media.Seconds(0.1), first.Start, "the timeline starts with the audio")

			if testCase.wantBurns == 2 {
				assert.Len(t, first.Segments, 4)
				assert.Empty(t, last.Segments)
				assert.Equal(t, 1, last.Workers)

				return
			}

			if testCase.wantSegments == nil {
				assert.Empty(t, first.Segments)

				return
			}

			for k, seg := range first.Segments {
				assert.NotEmpty(t, seg.Subtitles)
				first.Segments[k].Subtitles = ""
			}

			if testCase.wantErr == nil {
				assert.Equal(t, testCase.wantSegments, first.Segments)
			}

			require.Len(t, burner.slices, 4)

			for _, slice := range burner.slices {
				assert.True(t, strings.HasPrefix(slice, "[Script Info]"), "every slice has the header")
				assert.Less(t, strings.Count(slice, "Dialogue:"), strings.Count(burner.script, "Dialogue:")/2)
			}
		})
	}
}

func TestRendererWithoutProgress(
	t *testing.T,
) {
	burner := &fakeBurner{}

	err := overlay.NewRenderer(burner).Render(t.Context(), "in.mov", "out.mp4", overlay.Input{Report: tinyReport()}, overlay.RenderOptions{})
	require.NoError(t, err)
	require.Len(t, burner.specs, 1)
	assert.Nil(t, burner.specs[0].Progress)
}

func TestRendererWithoutTemporaryDirectory(
	t *testing.T,
) {
	t.Setenv("TMPDIR", "/qc-no-such-directory")

	burner := &fakeBurner{}

	err := overlay.NewRenderer(burner).Render(t.Context(), "in.mov", "out.mp4", overlay.Input{Report: tinyReport()}, overlay.RenderOptions{})
	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, burner.specs)
}
