package corpus

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var errLab = errors.New("lab failure")

// lab stands for ffmpeg and the analyser: a source of 1080p25, encodes
// whose frames weigh their height and score 100 minus their CRF, plus the
// index of their segment.
type lab struct {
	seconds float64
	// frameCount and average are the frame count and the average frame
	// rate the container gives (default: unknown, and 25).
	frameCount int64
	average    media.Rational
	// frames is the number of frames an encode reports (0: those of the
	// excerpt).
	frames  int
	failOn  string
	digests []encode.DigestSpec
	encodes map[string]encode.Params
	order   []encode.Params
}

func (l *lab) Analyze(
	_ context.Context,
	path string,
	_ analysis.Options,
) (*analysis.Report, error) {
	if l.failOn == "analyze" {
		return nil, errLab
	}

	rate := media.Rational{Num: 25, Den: 1}

	average := rate
	if l.average.Num > 0 {
		average = l.average
	}

	return &analysis.Report{
		Info: &media.Info{
			Path: path, Duration: media.Seconds(l.seconds),
			Video: []media.VideoStream{{Width: 1920, Height: 1080, BitDepth: 8, FrameRate: rate, AvgFrameRate: average, FrameCount: l.frameCount}},
		},
		Bitstream: &bitstream.Report{Start: media.Seconds(0.5)},
	}, nil
}

func (l *lab) Compare(
	_ context.Context,
	_, distorted string,
	opts analysis.CompareOptions,
) (*analysis.Comparison, error) {
	if l.failOn == "compare" {
		return nil, errLab
	}

	p := l.encodes[distorted]

	frames := l.frames
	if frames == 0 {
		frames = 50 * len(l.digests[0].Segments)
	}

	sizes := make([]int, frames)
	scores := make([]quality.FrameScore, frames)

	for i := range sizes {
		sizes[i] = p.Height
		scores[i] = quality.FrameScore{Index: i, Score: 100 - p.CRF + float64(i/50)}
	}

	if !opts.Quality.Exact {
		scores = scores[:1]
	}

	return &analysis.Comparison{
		Distorted: &analysis.Report{Bitstream: &bitstream.Report{FrameSizes: sizes}},
		VMAF:      &quality.Result{Frames: scores},
	}, nil
}

func (l *lab) Encode(
	_ context.Context,
	_ encode.Codec,
	_, dst string,
	p encode.Params,
) error {
	if l.failOn == "encode" {
		return errLab
	}

	if l.encodes == nil {
		l.encodes = map[string]encode.Params{}
	}

	l.encodes[dst] = p
	l.order = append(l.order, p)

	return nil
}

func (l *lab) EncodeChunks(
	context.Context,
	encode.Codec,
	encode.ChunkSource,
	string,
	[]encode.Chunk,
	encode.Params,
) error {
	return errLab
}

func (l *lab) Digest(
	_ context.Context,
	spec encode.DigestSpec,
) error {
	if l.failOn == "digest" {
		return errLab
	}

	l.digests = append(l.digests, spec)

	return nil
}

func TestMeasure(
	t *testing.T,
) {
	l := &lab{seconds: 60}
	dir := t.TempDir()

	var (
		rows     []predict.Row
		progress []Progress
	)

	err := NewBuilder(l, l, l).Measure(t.Context(), "/videos/title.mov", Options{
		Codec: "h264", Heights: []int{2160, 1080, 540, 540}, CRFs: []float64{20, 30}, Segments: 3, WorkDir: dir,
		Progress: func(p Progress) { progress = append(progress, p) },
	}, func(row predict.Row) error {
		rows = append(rows, row)

		return nil
	})
	require.NoError(t, err)

	// The excerpt: three segments of 50 frames, each centred in its third
	// of the title, seeked from the video's first frame.
	require.Len(t, l.digests, 1)
	spec := l.digests[0]
	assert.Equal(t, "/videos/title.mov", spec.Source)
	assert.Equal(t, filepath.Join(dir, "excerpt.nut"), spec.Destination)
	assert.Equal(t, media.Seconds(0.5), spec.Origin)
	assert.Equal(t, 8, spec.BitDepth)
	assert.Equal(t, 50, spec.Frames, "the frames of a segment are counted, not timed")
	assert.Equal(t, media.Rational{Num: 25, Den: 1}, spec.Rate)
	assert.False(t, spec.Lossless)
	require.Len(t, spec.Segments, 3)
	assert.InDelta(t, (225-0.25)/25, spec.Segments[0].Start.Seconds(), 1e-9)
	assert.InDelta(t, 2, spec.Segments[0].Length().Seconds(), 1e-9)
	assert.InDelta(t, (725-0.25)/25, spec.Segments[1].Start.Seconds(), 1e-9)

	// The grid: the heights not above the source, each once, at every CRF,
	// encoded as probes are (the codec's ladder preset, one GOP a segment).
	require.Len(t, rows, 4)
	assert.Equal(t, encode.Params{Width: 1920, Height: 1080, CRF: 20, Preset: "fast", GOP: 50, BitDepth: 8}, l.order[0])

	assert.Equal(t, predict.Row{
		Kind: predict.GridKind, Source: "/videos/title.mov", Codec: "h264", Preset: "fast",
		Width: 960, Height: 540, CRF: 30, SourceHeight: 1080, SegmentSeconds: 2, Segments: 3,
		Bits: []int64{50 * 540 * 8, 50 * 540 * 8, 50 * 540 * 8}, VMAF: []float64{70, 71, 72},
	}, withRounded(rows[3]))

	assert.Equal(t, Progress{Stage: StageExcerpt}, progress[0])
	assert.Equal(t, Progress{Stage: StageGrid, Done: 0, Total: 4}, progress[1])
	assert.Equal(t, Progress{Stage: StageGrid, Done: 4, Total: 4}, progress[len(progress)-1])
}

// withRounded rounds the scores of a row, sums of fiftieths.
func withRounded(
	row predict.Row,
) predict.Row {
	for i, v := range row.VMAF {
		row.VMAF[i] = float64(int(v*1e6+0.5)) / 1e6
	}

	return row
}

// describer is a Featurer giving every segment one feature.
type describer struct {
	specs []features.Spec
	// segments is the number of segments it describes (0: those asked).
	segments int
	err      error
}

func (d *describer) Extract(
	_ context.Context,
	spec features.Spec,
) (features.Set, error) {
	d.specs = append(d.specs, spec)

	n := d.segments
	if n == 0 {
		n = 3
	}

	values := make([]float64, n)
	for i := range values {
		values[i] = float64(10 * (i + 1))
	}

	return features.Set{Segments: n, Values: map[string][]float64{"si": values}}, d.err
}

func TestMeasureDescribesTheTitle(
	t *testing.T,
) {
	l, d := &lab{seconds: 60}, &describer{}
	dir := t.TempDir()

	var rows []predict.Row

	collect := func(row predict.Row) error {
		rows = append(rows, row)

		return nil
	}

	opts := Options{Codec: "h264", Heights: []int{1080, 540}, CRFs: []float64{20}, Segments: 3, WorkDir: dir}
	builder := NewBuilder(l, l, l, WithFeatures(d))
	require.NoError(t, builder.Measure(t.Context(), "/videos/title.mov", opts, collect))

	// The features come first, then the grid.
	require.Len(t, rows, 3)
	assert.Equal(t, predict.Row{
		Kind: predict.FeaturesKind, Source: "/videos/title.mov", SourceHeight: 1080, SegmentSeconds: 2, Segments: 3,
		Features: map[string][]float64{"si": {10, 20, 30}}, Version: features.Version,
	}, rows[0])

	// The excerpt is described with every frame scored, and is not
	// inspected again for it.
	require.Len(t, d.specs, 1)
	require.NotNil(t, d.specs[0].Inspection)

	described := d.specs[0]
	described.Inspection = nil
	assert.Equal(t, features.Spec{
		Path: filepath.Join(dir, "excerpt.nut"), Width: 1920, Height: 1080, Rate: 25, Frames: 50,
		Heights: []int{1080, 540}, Precise: true, WorkDir: dir,
	}, described)

	// A corpus that holds the grid and not the features gets them alone,
	// and one that holds both is left as it is.
	rows, l.order = nil, nil
	opts.Measured = func(row predict.Row) bool { return row.Kind == predict.GridKind }
	require.NoError(t, builder.Measure(t.Context(), "/videos/title.mov", opts, collect))
	require.Len(t, rows, 1)
	assert.Equal(t, predict.FeaturesKind, rows[0].Kind)
	assert.Empty(t, l.order)

	opts.Measured = func(predict.Row) bool { return true }
	require.NoError(t, builder.Measure(t.Context(), "/videos/title.mov", opts, collect))
	assert.Len(t, rows, 1)

	// Features that fail, or of other segments, stop the measurement.
	opts.Measured = nil
	require.ErrorIs(t, NewBuilder(l, l, l, WithFeatures(&describer{err: errLab})).Measure(t.Context(), "title.mov", opts, collect), errLab)
	require.ErrorIs(t, NewBuilder(l, l, l, WithFeatures(&describer{segments: 2})).Measure(t.Context(), "title.mov", opts, collect), ErrFrames)
}

func TestMeasureSkipsWhatWasMeasured(
	t *testing.T,
) {
	l := &lab{seconds: 60}
	measured := func(row predict.Row) bool { return row.Height == 1080 }

	var rows []predict.Row

	collect := func(row predict.Row) error {
		rows = append(rows, row)

		return nil
	}

	opts := Options{Codec: "h264", Heights: []int{1080, 540}, CRFs: []float64{20, 30}, WorkDir: t.TempDir(), Measured: measured}
	require.NoError(t, NewBuilder(l, l, l).Measure(t.Context(), "title.mov", opts, collect))

	require.Len(t, rows, 2)
	assert.Equal(t, 540, rows[0].Height)
	assert.Equal(t, 540, rows[1].Height)

	// Nothing left to measure: the title is not even cut.
	l = &lab{seconds: 60}
	opts.Measured = func(predict.Row) bool { return true }
	require.NoError(t, NewBuilder(l, l, l).Measure(t.Context(), "title.mov", opts, collect))
	assert.Empty(t, l.digests)
	assert.Len(t, rows, 2)
}

func TestMeasureErrors(
	t *testing.T,
) {
	testCases := []struct {
		name string
		lab  *lab
		opts Options
		want error
	}{
		{name: "unknown codec", lab: &lab{seconds: 60}, opts: Options{Codec: "vp8"}, want: encode.ErrUnknownCodec},
		{name: "inspection fails", lab: &lab{seconds: 60, failOn: "analyze"}, opts: Options{Codec: "h264"}, want: errLab},
		{name: "shorter than a segment", lab: &lab{seconds: 1.5}, opts: Options{Codec: "h264"}, want: ErrSource},
		{name: "excerpt fails", lab: &lab{seconds: 60, failOn: "digest"}, opts: Options{Codec: "h264"}, want: errLab},
		{name: "encode fails", lab: &lab{seconds: 60, failOn: "encode"}, opts: Options{Codec: "h264"}, want: errLab},
		{name: "measurement fails", lab: &lab{seconds: 60, failOn: "compare"}, opts: Options{Codec: "h264"}, want: errLab},
		{name: "frames missing", lab: &lab{seconds: 60, frames: 49}, opts: Options{Codec: "h264"}, want: ErrFrames},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			opts := testCase.opts
			opts.WorkDir = t.TempDir()

			err := NewBuilder(testCase.lab, testCase.lab, testCase.lab).Measure(t.Context(), "title.mov", opts, func(predict.Row) error { return nil })
			require.ErrorIs(t, err, testCase.want)
		})
	}

	l := &lab{seconds: 60}
	err := NewBuilder(l, l, l).Measure(t.Context(), "title.mov", Options{Codec: "h264", WorkDir: t.TempDir()}, func(predict.Row) error { return errLab })
	require.ErrorIs(t, err, errLab)
}

func TestGridCRFs(
	t *testing.T,
) {
	testCases := []struct {
		codec string
		want  []float64
	}{
		{codec: "h264", want: []float64{13, 16.5, 20, 23.5, 27, 30.5, 34, 37.5, 41, 44.5, 48}},
		{codec: "hevc", want: []float64{15, 18.5, 22, 25.5, 29, 32.5, 36, 39.5, 43, 46.5, 50}},
		{codec: "av1", want: []float64{16, 22, 28, 34, 40, 46, 52, 58}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.codec, func(t *testing.T) {
			codec, err := encode.Lookup(testCase.codec)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, GridCRFs(codec))
		})
	}
}

func TestPlace(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		total, count  int
		wantSegments  int
		wantFirstStep float64
	}{
		{name: "evenly across the title", total: 1500, count: 3, wantSegments: 3, wantFirstStep: 20},
		{name: "a short title gives what it holds", total: 120, count: 60, wantSegments: 2, wantFirstStep: 2.4},
		{name: "shorter than a segment", total: 40, count: 60, wantSegments: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			segments := Place(testCase.total, 50, testCase.count, 25)
			require.Len(t, segments, testCase.wantSegments)

			for i, s := range segments {
				assert.InDelta(t, 2, s.Length().Seconds(), 1e-9)

				if i > 0 {
					assert.InDelta(t, testCase.wantFirstStep, s.Start.Seconds()-segments[i-1].Start.Seconds(), 0.05)
				}
			}
		})
	}

	assert.Empty(t, Place(1500, 0, 3, 25))
}

// TestMeasureAVideoWithFramesMissing is a real case: 704 frames in 28.24 s,
// two of them missing, which makes an average rate of 24.93 for a nominal
// 25. Cut by time at that average, its segments held 51 frames and more.
func TestMeasureAVideoWithFramesMissing(
	t *testing.T,
) {
	l := &lab{seconds: 28.16, frameCount: 704, average: media.Rational{Num: 8800, Den: 353}}

	var rows []predict.Row

	err := NewBuilder(l, l, l).Measure(t.Context(), "title.mov", Options{Codec: "h264", Heights: []int{1080}, CRFs: []float64{27}, WorkDir: t.TempDir()},
		func(row predict.Row) error {
			rows = append(rows, row)

			return nil
		})
	require.NoError(t, err)

	// Cut and encoded at 25 frames a second, 50 frames a segment, with
	// room left at the end for the frames the gap pushed later: 13
	// segments where a steady title of that length gives 14.
	spec := l.digests[0]
	assert.Equal(t, media.Rational{Num: 25, Den: 1}, spec.Rate)
	assert.Equal(t, 50, spec.Frames)
	assert.Len(t, spec.Segments, 13)
	assert.Equal(t, 50, l.order[0].GOP)

	require.Len(t, rows, 1)
	assert.Equal(t, 13, rows[0].Segments)
	assert.Len(t, rows[0].Bits, 13)
}

func TestSteadyRate(
	t *testing.T,
) {
	rate := func(num, den int64) media.Rational { return media.Rational{Num: num, Den: den} }

	testCases := []struct {
		name             string
		nominal, average media.Rational
		want             media.Rational
	}{
		{name: "steady", nominal: rate(25, 1), average: rate(25, 1), want: rate(25, 1)},
		{name: "a few frames missing", nominal: rate(25, 1), average: rate(8800, 353), want: rate(25, 1)},
		{name: "NTSC", nominal: rate(30000, 1001), average: rate(30000, 1001), want: rate(30000, 1001)},
		{name: "a nominal rate that is not the video's", nominal: rate(50, 1), average: rate(25, 1), want: rate(25, 1)},
		{name: "no nominal rate", average: rate(24, 1), want: rate(24, 1)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, steadyRate(media.VideoStream{FrameRate: testCase.nominal, AvgFrameRate: testCase.average}))
		})
	}
}

func TestSpreadCRFs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		codec   string
		count   int
		want    []float64
		wantErr bool
	}{
		{name: "the default grid of x264", codec: "h264", count: 11, want: []float64{13, 16.5, 20, 23.5, 27, 30.5, 34, 37.5, 41, 44.5, 48}},
		{name: "half of it", codec: "h264", count: 6, want: []float64{13, 20, 27, 34, 41, 48}},
		{name: "a count between, on half CRFs", codec: "h264", count: 8, want: []float64{13, 18, 23, 28, 33, 38, 43, 48}},
		{name: "the two ends", codec: "h264", count: 2, want: []float64{13, 48}},
		{name: "twice the default keeps its CRFs", codec: "h264", count: 21, want: []float64{
			13, 15, 16.5, 18.5, 20, 22, 23.5, 25.5, 27, 29, 30.5, 32.5, 34, 36, 37.5, 39.5, 41, 43, 44.5, 46.5, 48,
		}},
		{name: "whole CRFs for SVT-AV1", codec: "av1", count: 5, want: []float64{16, 27, 37, 48, 58}},
		{name: "more than the encoder has", codec: "av1", count: 100, want: nil},
		{name: "one CRF", codec: "h264", count: 1, wantErr: true},
		{name: "none", codec: "h264", count: -3, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := encode.Lookup(testCase.codec)
			require.NoError(t, err)

			got, err := SpreadCRFs(codec, testCase.count)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrCRFCount)

				return
			}

			require.NoError(t, err)

			if testCase.want == nil {
				// Every whole CRF of the span, each once.
				assert.Len(t, got, 43)
				assert.IsIncreasing(t, got)

				return
			}

			assert.Equal(t, testCase.want, got)
		})
	}
}
