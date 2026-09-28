package analysis

import (
	"bytes"
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
)

func TestSegmentBounds(
	t *testing.T,
) {
	keyframes := func(frames, gop int) []bool {
		k := make([]bool, frames)
		for i := 0; i < frames; i += gop {
			k[i] = true
		}

		return k
	}

	testCases := []struct {
		name     string
		keyframe []bool
		decoders int
		want     []int
	}{
		{name: "too short to split", keyframe: keyframes(900, 25), decoders: 8, want: []int{0}},
		{name: "no keyframe to split at", keyframe: keyframes(5000, 10000), decoders: 4, want: []int{0}},
		{name: "one decoder, a segment per minimum length", keyframe: keyframes(1600, 25), decoders: 1, want: []int{0, 550, 1075}},
		{name: "bounds at the next keyframe", keyframe: keyframes(6000, 100), decoders: 2, want: []int{0, 1000, 2000, 3000, 4000, 5000}},
		{
			name: "a bound too close to the previous one is dropped", keyframe: append(keyframes(1200, 1200), keyframes(300, 50)...),
			decoders: 1, want: []int{0, 1200},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, segmentBounds(testCase.keyframe, testCase.decoders))
		})
	}
}

func TestSegmentRequests(
	t *testing.T,
) {
	bs := &bitstream.Report{PTS: []media.Duration{0, media.Seconds(0.04), media.Seconds(0.08), media.Seconds(0.12)}}
	base := decode.Request{Path: "in.mp4", Codec: "h264"}

	got := segmentRequests(base, []int{0, 2}, bs, 3)

	assert.Equal(t, []decode.Request{
		{Path: "in.mp4", Codec: "h264", Threads: 3, Segment: true, MaxFrames: 3},
		{Path: "in.mp4", Codec: "h264", Threads: 3, Segment: true, FirstIndex: 2, Start: media.Seconds(0.08)},
	}, got)
}

// seekingSource is fakeSource honouring the seek (FirstIndex) and the
// frame count of a request; skew shifts where segments starting at a given
// frame land. It counts its decodes.
type seekingSource struct {
	frames  int
	skew    map[int]int
	decodes *atomic.Int32
}

func (s seekingSource) Decode(
	_ context.Context,
	req decode.Request,
	fn func(*frame.Frame) error,
) error {
	s.decodes.Add(1)

	start := req.FirstIndex + s.skew[req.FirstIndex]

	for k, i := 0, start; i < s.frames && (req.MaxFrames == 0 || k < req.MaxFrames); k, i = k+1, i+1 {
		f := req.Pool.Get()
		f.Index = req.FirstIndex + k
		f.PTS = req.PTS[f.Index]

		for y := range f.Luma.Height {
			row := f.Luma.Row(y)
			for x := range row {
				row[x] = longPixel(i, x, y)
			}
		}

		req.Pool.BuildThumb(f)

		if err := fn(f); err != nil {
			return err
		}
	}

	return nil
}

// longPixel draws frame i of a long clip: shots of 300 frames of moving
// texture, every fourth one black.
func longPixel(
	i, x, y int,
) byte {
	shot := i / 300
	if shot%4 == 3 {
		return 16
	}

	return byte(40 + ((x+i)*(shot+3)+y*11)%180)
}

func TestAnalyzeInSegments(
	t *testing.T,
) {
	const frames = 3000

	testCases := []struct {
		name        string
		skew        map[int]int
		wantDecodes int32
		wantWarning bool
	}{
		{name: "segments give the single pass's report", wantDecodes: 6},
		{name: "a misplaced segment falls back to a single pass", skew: map[int]int{1500: 1}, wantDecodes: 7, wantWarning: true},
	}

	analyze := func(t *testing.T, decoders int, source seekingSource, logger *slog.Logger) *Report {
		t.Helper()

		info := fakeVideo()
		info.Duration = media.Seconds(frames / fakeRate)

		a := New(logger, fakeProber{info: info}, fakePackets{count: frames}, source, nil)
		report, err := a.Analyze(t.Context(), "clip.mp4", Options{Video: VideoOptions{Decoders: decoders, DecoderThreads: 3, SkipMotion: true}})
		require.NoError(t, err)

		report.GeneratedAt, report.Timings = report.GeneratedAt.UTC().Truncate(0), nil

		return report
	}

	var singleDecodes atomic.Int32

	want := analyze(t, 1, seekingSource{frames: frames, decodes: &singleDecodes}, nil)
	require.Equal(t, int32(1), singleDecodes.Load())
	require.Len(t, want.Video.Shots, 8)

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				decodes atomic.Int32
				logs    bytes.Buffer
			)

			got := analyze(t, 2, seekingSource{frames: frames, skew: testCase.skew, decodes: &decodes},
				slog.New(slog.NewTextHandler(&logs, nil)))
			got.GeneratedAt = want.GeneratedAt

			assert.Equal(t, want, got)
			assert.Equal(t, testCase.wantDecodes, decodes.Load())
			assert.Equal(t, testCase.wantWarning, bytes.Contains(logs.Bytes(), []byte("analysing in one pass")))
		})
	}
}

// plannedSource is a seekingSource telling how many segments to decode.
type plannedSource struct {
	seekingSource
	decoders int
}

func (s plannedSource) SegmentDecoders(
	decode.Request,
) int {
	return s.decoders
}

func TestAnalyzeAsksTheDecoderForSegments(
	t *testing.T,
) {
	const frames = 3000

	testCases := []struct {
		name string
		// planned is what the decoder answers, 0 when it cannot tell.
		planned     int
		wantDecodes int32
	}{
		{name: "a decoder asking for segments", planned: 2, wantDecodes: 6},
		{name: "a decoder asking for one pass", planned: 1, wantDecodes: 1},
		{name: "a decoder that cannot tell", wantDecodes: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var decodes atomic.Int32

			var source decode.Source = seekingSource{frames: frames, decodes: &decodes}
			if testCase.planned > 0 {
				source = plannedSource{seekingSource: seekingSource{frames: frames, decodes: &decodes}, decoders: testCase.planned}
			}

			a := New(nil, fakeProber{info: fakeVideo()}, fakePackets{count: frames}, source, nil)
			_, err := a.Analyze(t.Context(), "clip.mp4", Options{Video: VideoOptions{SkipMotion: true}})
			require.NoError(t, err)

			assert.Equal(t, testCase.wantDecodes, decodes.Load())
		})
	}
}

func TestAnalyzeTooShortToSplit(
	t *testing.T,
) {
	report, err := world{info: fakeVideo()}.analyzer().Analyze(t.Context(), "clip.mp4", Options{
		Video: VideoOptions{Decoders: 4, SkipMotion: true},
	})
	require.NoError(t, err)

	assert.Equal(t, fakeFrames, report.Video.FramesDecoded)
}
