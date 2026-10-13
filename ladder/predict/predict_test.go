package predict

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testHeights = []int{1080, 540, 270}
	testCRFs    = []float64{20, 23.5, 27, 30.5, 34}
)

// syntheticTitle is a title whose segments follow one law: at every point
// of the grid, the logarithm of the bitrate per pixel is the segment's
// complexity plus a term of the point, and the linked VMAF the segment's
// easiness plus a term of the point. One anchor then tells everything of a
// segment, which is what a model should find.
func syntheticTitle(
	name string,
	seed uint64,
	segments int,
	complexity, easiness float64,
) Title {
	rng := rand.New(rand.NewPCG(seed, seed))
	t := Title{Name: name, Codec: "h264", Preset: "fast", SourceHeight: 1080}

	offsets := make([][2]float64, segments)
	t.Features = map[string][]float64{}

	for s := range offsets {
		offsets[s] = [2]float64{complexity + rng.NormFloat64()*0.5, easiness + rng.NormFloat64()*0.2}

		// What an analysis would see of the segment: its two offsets, and
		// something that tells nothing.
		t.Features["busy"] = append(t.Features["busy"], offsets[s][0])
		t.Features["soft"] = append(t.Features["soft"], offsets[s][1])
		t.Features["noise"] = append(t.Features["noise"], rng.NormFloat64())
	}

	for _, h := range testHeights {
		width := h * 16 / 9

		for _, crf := range testCRFs {
			tp := TitlePoint{Point: Point{Height: h, CRF: crf}, Width: width}

			for _, o := range offsets {
				rate := math.Exp(o[0]-0.12*(crf-27)+0.3*math.Log(1080/float64(h))-3) * float64(width*h)
				quality := unlink(o[1] - 0.15*(crf-27) - 1.2*math.Log(1080/float64(h)) + 2)

				tp.Bitrate = append(tp.Bitrate, rate)
				tp.VMAF = append(tp.VMAF, quality)
			}

			t.Points = append(t.Points, tp)
		}
	}

	return t
}

func syntheticCorpus() []Title {
	return []Title{
		syntheticTitle("drama", 1, 30, 0, 0),
		syntheticTitle("cartoon", 2, 40, -0.8, 0.9),
		syntheticTitle("sport", 3, 50, 0.9, -0.6),
	}
}

func allSegments(
	t Title,
) []int {
	segs := make([]int, t.segments())
	for i := range segs {
		segs[i] = i
	}

	return segs
}

func TestTrainPredictsTitlesItHasNotSeen(
	t *testing.T,
) {
	model, err := Train(syntheticCorpus(), TrainOptions{})
	require.NoError(t, err)

	assert.Equal(t, "h264", model.Codec)
	assert.Equal(t, "fast", model.Preset)
	assert.Equal(t, 1080, model.SourceHeight)
	assert.Equal(t, testHeights, model.Heights)
	assert.Equal(t, testCRFs, model.CRFs)
	assert.Equal(t, 3, model.Titles)
	assert.Equal(t, 120, model.Segments)

	full := model.Designs[0]
	require.Len(t, full.Anchors, 6)
	require.NotNil(t, full.Validation)
	assert.Equal(t, 3*41, full.Validation.Clips)
	assert.Less(t, full.Validation.MeanVMAFError, 0.5)
	assert.Less(t, full.Validation.MeanBitrateError, 0.05)

	// Each title keeps the error it was predicted with, in corpus order.
	require.Len(t, full.Validation.Titles, 3)

	for i, title := range syntheticCorpus() {
		assert.Equal(t, title.Name, full.Validation.Titles[i].Name)
		assert.Positive(t, full.Validation.Titles[i].VMAFError)
		assert.LessOrEqual(t, full.Validation.Titles[i].VMAFError, full.Validation.WorstVMAFError)
	}

	// A title unlike those of the corpus, measured at its anchors only.
	unseen := syntheticTitle("documentary", 9, 20, 0.4, 0.5)
	segs := allSegments(unseen)

	grid, err := model.Predict(full, unseen.input(full.Anchors, nil, segs))
	require.NoError(t, err)
	require.Len(t, grid, 15)

	for i, p := range model.Points() {
		truth := unseen.clip(p, segs)

		assert.Equal(t, p, grid[i].Point)
		assert.Equal(t, truth.Width, grid[i].Width)
		assert.InDelta(t, truth.VMAF, grid[i].VMAF, 1.0, "VMAF at %dp CRF %g", p.Height, p.CRF)
		assert.InEpsilon(t, truth.Bitrate, grid[i].Bitrate, 0.08, "bitrate at %dp CRF %g", p.Height, p.CRF)
	}
}

func TestTrainPredictsFromFeaturesAlone(
	t *testing.T,
) {
	// The features of the synthetic titles are their law itself: a light
	// penalty lets the models find it.
	model, err := Train(syntheticCorpus(), TrainOptions{FeaturePenalty: 0.001})
	require.NoError(t, err)

	instant, ok := model.Instant(nil)
	require.True(t, ok)
	assert.Empty(t, instant.Anchors)
	assert.Equal(t, []string{"busy", "noise", "soft"}, instant.Features)
	require.NotNil(t, instant.Validation)
	assert.Less(t, instant.Validation.MeanVMAFError, 1.0)

	// The model's last design: no anchor is the fewest.
	assert.Empty(t, model.Designs[len(model.Designs)-1].Anchors)

	unseen := syntheticTitle("documentary", 9, 20, 0.4, 0.5)
	segs := allSegments(unseen)
	in := unseen.input(nil, instant.Features, segs)
	in.Anchors = nil

	grid, err := model.Predict(instant, in)
	require.NoError(t, err)

	for i, p := range model.Points() {
		truth := unseen.clip(p, segs)

		assert.Equal(t, truth.Width, grid[i].Width)
		assert.InDelta(t, truth.VMAF, grid[i].VMAF, 1.5, "VMAF at %dp CRF %g", p.Height, p.CRF)
		assert.InEpsilon(t, truth.Bitrate, grid[i].Bitrate, 0.1, "bitrate at %dp CRF %g", p.Height, p.CRF)
		assert.Positive(t, grid[i].VMAFError)
	}

	// A feature the design reads must be given, and a width known.
	delete(in.Segments, "soft")
	_, err = model.Predict(instant, in)
	require.ErrorIs(t, err, ErrFeatures)

	in = unseen.input(nil, instant.Features, segs)
	in.Width = nil
	_, err = model.Predict(instant, in)
	require.ErrorIs(t, err, ErrAnchors)
}

func TestDesignsReadingFeaturesNeedThem(
	t *testing.T,
) {
	model, err := Train(syntheticCorpus(), TrainOptions{})
	require.NoError(t, err)

	// A tolerance any design meets: without features to give, the fewest
	// anchors of the designs reading none; with them, none at all.
	assert.Len(t, model.Design(50, nil).Anchors, 1)
	assert.Empty(t, model.Design(50, nil).Features)
	assert.Empty(t, model.Design(50, func(string) bool { return true }).Anchors)

	// A corpus without features, or told to leave them, has no such design.
	for _, opts := range []TrainOptions{{NoFeatures: true}} {
		plain, err := Train(syntheticCorpus(), opts)
		require.NoError(t, err)

		_, ok := plain.Instant(nil)
		assert.False(t, ok)
	}

	titles := syntheticCorpus()
	titles[1].Features = nil

	partial, err := Train(titles, TrainOptions{})
	require.NoError(t, err)

	_, ok := partial.Instant(nil)
	assert.False(t, ok, "a title without features leaves the designs reading them out")
}

func TestPredictKeepsMeasuredAnchors(
	t *testing.T,
) {
	model, err := Train(syntheticCorpus(), TrainOptions{})
	require.NoError(t, err)

	design := model.Designs[0]
	title := syntheticCorpus()[0]
	anchors := title.input(design.Anchors, nil, allSegments(title)).Anchors
	anchors[0].VMAF += 0.3

	grid, err := model.Predict(design, Input{Anchors: anchors})
	require.NoError(t, err)

	for _, a := range anchors {
		for _, g := range grid {
			if g.Point != a.Point {
				continue
			}

			assert.InDelta(t, a.VMAF, g.VMAF, 1e-12)
			assert.InDelta(t, a.Bitrate, g.Bitrate, 1e-9)
			assert.Zero(t, g.VMAFError)
		}
	}

	// The other points carry the error of the design there.
	assert.Positive(t, grid[0].VMAFError)
}

func TestPredictRejectsOtherMeasurements(
	t *testing.T,
) {
	model, err := Train(syntheticCorpus(), TrainOptions{})
	require.NoError(t, err)

	design := model.Designs[0]
	title := syntheticCorpus()[0]
	anchors := title.input(design.Anchors, nil, allSegments(title)).Anchors

	testCases := []struct {
		name   string
		change func([]Measurement) []Measurement
	}{
		{name: "one missing", change: func(a []Measurement) []Measurement { return a[1:] }},
		{name: "another point", change: func(a []Measurement) []Measurement { a[0].CRF = 21; return a }},
		{name: "no bitrate", change: func(a []Measurement) []Measurement { a[1].Bitrate = 0; return a }},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := model.Predict(design, Input{Anchors: testCase.change(append([]Measurement(nil), anchors...))})
			require.ErrorIs(t, err, ErrAnchors)
		})
	}
}

func TestDesignTakesTheFewestAnchorsWithinTolerance(
	t *testing.T,
) {
	model := &Model{Designs: []Design{
		{Anchors: make([]Point, 10), Validation: &Validation{MeanVMAFError: 0.45}},
		{Anchors: make([]Point, 7), Validation: &Validation{MeanVMAFError: 0.3}},
		{Anchors: make([]Point, 3), Validation: &Validation{MeanVMAFError: 0.6}},
		{Anchors: make([]Point, 1), Validation: &Validation{MeanVMAFError: 2.5}},
	}}

	testCases := []struct {
		name      string
		tolerance float64
		want      int
	}{
		{name: "no tolerance asks for the best validation", tolerance: 0, want: 7},
		{name: "tight", tolerance: 0.5, want: 7},
		{name: "looser", tolerance: 1, want: 3},
		{name: "loose", tolerance: 3, want: 1},
		{name: "none within: the best validation", tolerance: 0.1, want: 7},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Len(t, model.Design(testCase.tolerance, nil).Anchors, testCase.want)
		})
	}

	// A design that was not validated is never trusted.
	model.Designs[3].Validation = nil
	assert.Len(t, model.Design(3, nil).Anchors, 3)

	// Without any validation, the most anchors.
	for i := range model.Designs {
		model.Designs[i].Validation = nil
	}

	assert.Len(t, model.Design(1, nil).Anchors, 10)
}

func TestDefaultDesigns(
	t *testing.T,
) {
	points := func(pairs ...float64) []Point {
		out := make([]Point, 0, len(pairs)/2)
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, Point{Height: int(pairs[i]), CRF: pairs[i+1]})
		}

		return out
	}

	crfs := []float64{13, 16.5, 20, 23.5, 27, 30.5, 34, 37.5, 41, 44.5, 48}

	testCases := []struct {
		name           string
		heights        []int
		crfs           []float64
		low, high, far float64
		want           [][]Point
	}{
		{
			name: "five heights", heights: []int{1080, 720, 540, 360, 270}, crfs: crfs, low: 23.5, high: 30.5, far: 41,
			want: [][]Point{
				points(1080, 23.5, 1080, 41, 720, 30.5, 720, 41, 540, 23.5, 540, 41, 360, 30.5, 360, 23.5, 270, 23.5, 270, 30.5),
				points(1080, 23.5, 720, 30.5, 540, 23.5, 360, 30.5, 270, 23.5, 1080, 41, 540, 41),
				points(1080, 23.5, 720, 30.5, 540, 23.5, 360, 30.5, 270, 23.5, 1080, 41),
				points(1080, 23.5, 720, 30.5, 540, 23.5, 360, 30.5, 270, 23.5),
				points(1080, 23.5, 1080, 41, 540, 30.5, 270, 23.5),
				points(1080, 23.5, 540, 30.5, 270, 23.5),
				points(1080, 23.5, 270, 30.5),
				points(1080, 23.5),
			},
		},
		{
			name: "CRFs taken in the grid", heights: []int{1080, 720}, crfs: crfs,
			want: [][]Point{
				points(1080, 23.5, 1080, 41, 720, 30.5, 720, 41),
				points(1080, 23.5, 720, 30.5, 1080, 41),
				points(1080, 23.5, 720, 30.5),
				points(1080, 23.5),
			},
		},
		{
			name: "nearest CRFs of the grid", heights: []int{360}, crfs: []float64{28, 40, 52}, low: 30, high: 45, far: 70,
			want: [][]Point{points(360, 28, 360, 52, 360, 40), points(360, 28, 360, 52), points(360, 28)},
		},
		{name: "no grid"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, DefaultDesigns(testCase.heights, testCase.crfs, testCase.low, testCase.high, testCase.far))
		})
	}
}

func TestTrainRejectsBadCorpora(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		titles func() []Title
		opts   TrainOptions
	}{
		{name: "no title", titles: func() []Title { return nil }},
		{name: "another preset", titles: func() []Title {
			titles := syntheticCorpus()
			titles[1].Preset = "slow"

			return titles
		}},
		{name: "a point missing", titles: func() []Title {
			titles := syntheticCorpus()
			titles[2].Points = titles[2].Points[1:]

			return titles
		}},
		{name: "a segment missing", titles: func() []Title {
			titles := syntheticCorpus()
			titles[0].Points[3].VMAF = titles[0].Points[3].VMAF[1:]

			return titles
		}},
		{name: "an empty segment", titles: func() []Title {
			titles := syntheticCorpus()
			titles[0].Points[3].Bitrate[2] = 0

			return titles
		}},
		{name: "anchor off the grid", titles: syntheticCorpus, opts: TrainOptions{Designs: [][]Point{{{Height: 720, CRF: 27}}}}},
		{name: "design without anchor", titles: syntheticCorpus, opts: TrainOptions{Designs: [][]Point{{}}}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Train(testCase.titles(), testCase.opts)
			require.ErrorIs(t, err, ErrCorpus)
		})
	}
}

func TestTrainWithoutValidationOnOneTitle(
	t *testing.T,
) {
	model, err := Train(syntheticCorpus()[:1], TrainOptions{})
	require.NoError(t, err)

	for _, d := range model.Designs {
		assert.Nil(t, d.Validation)
	}
}

func TestSaveAndLoad(
	t *testing.T,
) {
	model, err := Train(syntheticCorpus(), TrainOptions{})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "model.json")
	require.NoError(t, model.Save(path))

	loaded, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, model, loaded)

	assert.True(t, loaded.Covers("h264", "fast", 1080))
	assert.False(t, loaded.Covers("h264", "slow", 1080))
	assert.False(t, loaded.Covers("av1", "fast", 1080))
	assert.False(t, loaded.Covers("h264", "fast", 720))
}

func TestLoadRejectsBadModels(
	t *testing.T,
) {
	testCases := []struct {
		name string
		json string
	}{
		{name: "not JSON", json: "{"},
		{name: "another schema", json: `{"schemaVersion":99,"heights":[1080],"crfs":[27],"designs":[{}]}`},
		{name: "no design", json: `{"schemaVersion":1,"heights":[1080],"crfs":[27]}`},
		{name: "models missing", json: `{"schemaVersion":1,"heights":[1080],"crfs":[27],"designs":[{"anchors":[{"height":1080,"crf":27}]}]}`},
		{
			name: "wrong number of coefficients",
			json: `{"schemaVersion":1,"heights":[1080],"crfs":[27],"designs":[{"anchors":[{"height":1080,"crf":27}],"rate":[[1,2]],"vmaf":[[1,2,3]]}]}`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "model.json")
			require.NoError(t, os.WriteFile(path, []byte(testCase.json), 0o600))

			_, err := Load(path)
			require.ErrorIs(t, err, ErrModel)
		})
	}

	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestReadCorpus(
	t *testing.T,
) {
	rows := strings.Join([]string{
		`{"kind":"grid","source":"/videos/drama.mp4","codec":"h264","preset":"fast","width":1920,"height":1080,"crf":27,"bits":[2000,4000],"vmaf":[90,80]}`,
		`{"kind":"grid","source":"/videos/drama.mp4","codec":"h264","preset":"fast","width":960,"height":540,"crf":27,"segmentSeconds":4,"bits":[1000,2000],"vmaf":[70,60]}`,
		`{"kind":"stats","source":"/videos/drama.mp4","codec":"h264","preset":"ultrafast","width":640,"height":360,"crf":29,"bits":[1,2]}`,
		`{"kind":"features","source":"/videos/drama.mp4","sourceHeight":1080,"features":{"si":[40,50],"ti":[3,4]}}`,
		`{"kind":"grid","source":"/videos/drama.mp4","codec":"av1","preset":"8","width":1920,"height":1080,"crf":40,"bits":[1,2],"vmaf":[1,2]}`,
		``,
		`{"kind":"grid","source":"/videos/cartoon.mov","codec":"h264","preset":"fast","width":1280,"height":720,"crf":27,"sourceHeight":2160,"bits":[600],"vmaf":[95]}`,
	}, "\n")

	path := filepath.Join(t.TempDir(), "corpus.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(rows), 0o600))

	titles, err := ReadCorpus("h264", "fast", path)
	require.NoError(t, err)

	assert.Equal(t, []Title{
		{
			Name: "drama", Codec: "h264", Preset: "fast", SourceHeight: 1080,
			Features: map[string][]float64{"si": {40, 50}, "ti": {3, 4}},
			Points: []TitlePoint{
				{Point: Point{Height: 1080, CRF: 27}, Width: 1920, Bitrate: []float64{1000, 2000}, VMAF: []float64{90, 80}},
				{Point: Point{Height: 540, CRF: 27}, Width: 960, Bitrate: []float64{250, 500}, VMAF: []float64{70, 60}},
			},
		},
		{
			Name: "cartoon", Codec: "h264", Preset: "fast", SourceHeight: 2160,
			Points: []TitlePoint{
				{Point: Point{Height: 720, CRF: 27}, Width: 1280, Bitrate: []float64{300}, VMAF: []float64{95}},
			},
		},
	}, titles)
}

func TestReadCorpusRejectsBadFiles(
	t *testing.T,
) {
	testCases := []struct {
		name string
		rows string
	}{
		{name: "not JSON", rows: "{"},
		{name: "scores missing", rows: `{"kind":"grid","source":"a.mp4","codec":"h264","preset":"fast","width":2,"height":2,"crf":27,"bits":[1,2],"vmaf":[1]}`},
		{name: "no segment", rows: `{"kind":"grid","source":"a.mp4","codec":"h264","preset":"fast","width":2,"height":2,"crf":27}`},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "corpus.jsonl")
			require.NoError(t, os.WriteFile(path, []byte(testCase.rows), 0o600))

			_, err := ReadCorpus("h264", "fast", path)
			require.ErrorIs(t, err, ErrCorpus)
		})
	}

	_, err := ReadCorpus("h264", "fast", filepath.Join(t.TempDir(), "missing.jsonl"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLinkIsBoundedAndInvertible(
	t *testing.T,
) {
	for _, v := range []float64{0.5, 12, 50, 88.8, 99.5} {
		assert.InDelta(t, v, unlink(link(v)), 1e-9)
	}

	assert.InDelta(t, link(0.5), link(-3), 1e-12)
	assert.InDelta(t, link(99.5), link(100), 1e-12)
}

func TestRowSame(
	t *testing.T,
) {
	grid := Row{Kind: GridKind, Source: "a.mp4", Codec: "h264", Preset: "fast", Height: 1080, CRF: 27}

	testCases := []struct {
		name  string
		a, b  Row
		wants bool
	}{
		{name: "the same encode", a: grid, b: grid, wants: true},
		{name: "another CRF", a: grid, b: Row{Kind: GridKind, Source: "a.mp4", Codec: "h264", Preset: "fast", Height: 1080, CRF: 30}},
		{name: "another title", a: grid, b: Row{Kind: GridKind, Source: "b.mp4", Codec: "h264", Preset: "fast", Height: 1080, CRF: 27}},
		{
			name: "features of a title, whatever else", wants: true,
			a: Row{Kind: FeaturesKind, Source: "a.mp4"}, b: Row{Kind: FeaturesKind, Source: "a.mp4", SourceHeight: 1080},
		},
		{name: "features of another title", a: Row{Kind: FeaturesKind, Source: "a.mp4"}, b: Row{Kind: FeaturesKind, Source: "b.mp4"}},
		{name: "features and an encode", a: Row{Kind: FeaturesKind, Source: "a.mp4"}, b: grid},
		{
			name: "the same encode of an excerpt cut another way",
			a:    Row{Kind: GridKind, Source: "a.mp4", Codec: "h264", Preset: "fast", Height: 1080, CRF: 27, Segments: 13},
			b:    Row{Kind: GridKind, Source: "a.mp4", Codec: "h264", Preset: "fast", Height: 1080, CRF: 27, Bits: make([]int64, 14)},
		},
		{
			name: "features of an excerpt cut another way",
			a:    Row{Kind: FeaturesKind, Source: "a.mp4", Segments: 13},
			b:    Row{Kind: FeaturesKind, Source: "a.mp4", Features: map[string][]float64{"si": make([]float64, 14)}},
		},
		{
			name: "the same encode, its segments counted", wants: true,
			a: Row{Kind: GridKind, Source: "a.mp4", Codec: "h264", Preset: "fast", Height: 1080, CRF: 27, Segments: 14},
			b: Row{Kind: GridKind, Source: "a.mp4", Codec: "h264", Preset: "fast", Height: 1080, CRF: 27, Bits: make([]int64, 14)},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.wants, testCase.a.Same(testCase.b))
		})
	}
}

func TestReadCorpusKeepsTheLastMeasurementOfAPoint(
	t *testing.T,
) {
	rows := strings.Join([]string{
		`{"kind":"grid","source":"a.mp4","codec":"h264","preset":"fast","width":1920,"height":1080,"crf":27,"bits":[2000,4000,6000],"vmaf":[90,80,70]}`,
		`{"kind":"features","source":"a.mp4","features":{"si":[1,2,3]}}`,
		`{"kind":"grid","source":"a.mp4","codec":"h264","preset":"fast","width":1920,"height":1080,"crf":27,"segments":2,"bits":[2000,8000],"vmaf":[91,81]}`,
		`{"kind":"features","source":"a.mp4","segments":2,"features":{"si":[5,6]}}`,
	}, "\n")

	path := filepath.Join(t.TempDir(), "corpus.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(rows), 0o600))

	titles, err := ReadCorpus("h264", "fast", path)
	require.NoError(t, err)
	require.Len(t, titles, 1)

	// The excerpt was cut again in two segments: its later measurements
	// are the title's.
	require.Len(t, titles[0].Points, 1)
	assert.Equal(t, []float64{1000, 4000}, titles[0].Points[0].Bitrate)
	assert.Equal(t, []float64{91, 81}, titles[0].Points[0].VMAF)
	assert.Equal(t, map[string][]float64{"si": {5, 6}}, titles[0].Features)
}

func TestComplete(
	t *testing.T,
) {
	titles := syntheticCorpus()
	titles[1].Points = titles[1].Points[:4]
	// As many points as the grid, but not its points.
	titles[2].Points[0].CRF = 99

	complete, partial := Complete(titles)
	require.Len(t, complete, 1)
	assert.Equal(t, "drama", complete[0].Name)
	require.Len(t, partial, 2)
	assert.Equal(t, "cartoon", partial[0].Name)
	assert.Equal(t, "sport", partial[1].Name)

	complete, partial = Complete(syntheticCorpus())
	assert.Len(t, complete, 3)
	assert.Empty(t, partial)

	complete, partial = Complete(nil)
	assert.Empty(t, complete)
	assert.Empty(t, partial)
}

func TestDigests(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		segments     int
		size         int
		wantCount    int
		wantSegments int
	}{
		{name: "segments alone", segments: 6, size: 1, wantCount: 6, wantSegments: 1},
		{name: "digests of five", segments: 15, size: 5, wantCount: 3 * fitDraws, wantSegments: 5},
		{name: "what is left of a draw is dropped", segments: 14, size: 5, wantCount: 2 * fitDraws, wantSegments: 5},
		{name: "never more than half a title", segments: 8, size: 5, wantCount: 2 * fitDraws, wantSegments: 4},
		{name: "a title too short keeps its segments", segments: 3, size: 5, wantCount: 3, wantSegments: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := digests(testCase.segments, testCase.size, rand.New(rand.NewPCG(1, 2)))

			require.Len(t, got, testCase.wantCount)

			for _, digest := range got {
				assert.Len(t, digest, testCase.wantSegments)
				assert.True(t, slices.IsSorted(digest))
				assert.Len(t, slices.Compact(slices.Clone(digest)), len(digest), "a segment once in a digest")
			}
		})
	}
}

func TestPredictAveragesSegments(
	t *testing.T,
) {
	model, err := Train(syntheticCorpus(), TrainOptions{FeaturePenalty: 0.001})
	require.NoError(t, err)

	instant, ok := model.Instant(nil)
	require.True(t, ok)

	title := syntheticTitle("documentary", 9, 20, 0.4, 0.5)
	whole := title.input(nil, instant.Features, []int{0, 1})

	grid, err := model.Predict(instant, whole)
	require.NoError(t, err)

	first, err := model.Predict(instant, title.input(nil, instant.Features, []int{0}))
	require.NoError(t, err)

	second, err := model.Predict(instant, title.input(nil, instant.Features, []int{1}))
	require.NoError(t, err)

	// Without its segments, the title is predicted from the means of
	// their features: the geometric mean of their bitrates, never above.
	whole.Segments = nil

	fromMeans, err := model.Predict(instant, whole)
	require.NoError(t, err)

	for i := range grid {
		assert.InEpsilon(t, (first[i].Bitrate+second[i].Bitrate)/2, grid[i].Bitrate, 1e-9)
		assert.InDelta(t, (first[i].VMAF+second[i].VMAF)/2, grid[i].VMAF, 1e-9)
		assert.LessOrEqual(t, fromMeans[i].Bitrate, grid[i].Bitrate*(1+1e-9))
	}
}

func TestFeatureSets(
	t *testing.T,
) {
	// A design for each thing a build can read: here everything, and the
	// features that need no encode.
	model, err := Train(syntheticCorpus(), TrainOptions{
		FeaturePenalty: 0.001,
		FeatureSets:    [][]string{{"busy", "noise", "soft"}, {"busy", "soft", "unknown"}, {"soft", "busy"}, {"unknown"}},
	})
	require.NoError(t, err)

	var sets [][]string

	for _, d := range model.Designs {
		if len(d.Anchors) == 0 && len(d.Features) > 0 {
			sets = append(sets, d.Features)
		}
	}

	assert.Equal(t, [][]string{{"busy", "noise", "soft"}, {"busy", "soft"}}, sets, "the features the corpus holds, a set once")

	full, ok := model.Instant(nil)
	require.True(t, ok)
	assert.Len(t, full.Features, 3, "all that can be read")

	light, ok := model.Instant(func(name string) bool { return name != "noise" })
	require.True(t, ok)
	assert.Equal(t, []string{"busy", "soft"}, light.Features)

	_, ok = model.Instant(func(name string) bool { return name == "busy" })
	assert.False(t, ok, "no design reads that little")

	// A design reading features is taken only when all of them can be read.
	assert.NotEmpty(t, model.Design(50, func(name string) bool { return name == "busy" }).Anchors)
	assert.Empty(t, model.Design(50, func(name string) bool { return name != "noise" }).Anchors)
}
