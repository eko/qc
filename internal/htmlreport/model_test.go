package htmlreport

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
)

var (
	modelTestHeights = []int{1080, 540}
	modelTestCRFs    = []float64{20, 27, 34, 41}
)

// modelTitle is a title of 6 segments following one law over the grid:
// its complexity scales its bitrate, its easiness lifts its VMAF. Its
// features are named as those of a real corpus, so that designs with and
// without encode are fitted.
func modelTitle(
	n int,
) predict.Title {
	complexity, easiness := math.Sin(float64(n)*1.7), math.Cos(float64(n)*2.3)*0.6
	t := predict.Title{Name: fmt.Sprintf("title-%02d", n), Codec: "h264", Preset: "fast", SourceHeight: 1080, Features: map[string][]float64{}}

	for s := range 6 {
		wobble := math.Sin(float64(n*7+s)) * 0.2
		t.Features["si"] = append(t.Features["si"], 40+10*complexity+wobble)
		t.Features["si_log"] = append(t.Features["si_log"], math.Log(40+10*complexity+wobble))
		t.Features["ti"] = append(t.Features["ti"], 12+4*easiness-wobble)
		t.Features[features.SourceQP] = append(t.Features[features.SourceQP], 18+2*complexity)
		t.Features["x264_23_bits"] = append(t.Features["x264_23_bits"], complexity+wobble)
		t.Features["x264_23_vmaf"] = append(t.Features["x264_23_vmaf"], easiness-wobble)
	}

	for _, h := range modelTestHeights {
		for _, crf := range modelTestCRFs {
			tp := predict.TitlePoint{Point: predict.Point{Height: h, CRF: crf}, Width: h * 16 / 9}

			for s := range 6 {
				wobble := math.Sin(float64(n*7+s)) * 0.2
				tp.Bitrate = append(tp.Bitrate, math.Exp(complexity+wobble-0.12*(crf-27)-0.9*math.Log(1080/float64(h)))*3e6)
				tp.VMAF = append(tp.VMAF, 100/(1+math.Exp(-(easiness-wobble-0.15*(crf-27)-1.2*math.Log(1080/float64(h))+2))))
			}

			t.Points = append(t.Points, tp)
		}
	}

	return t
}

func modelTitles(
	count int,
) []predict.Title {
	titles := make([]predict.Title, count)
	for i := range titles {
		titles[i] = modelTitle(i)
	}

	return titles
}

// trained is a model of titles, with the designs qc model train fits.
func trained(
	t *testing.T,
	titles []predict.Title,
	opts predict.TrainOptions,
) *predict.Model {
	t.Helper()

	if opts.FeatureSets == nil {
		opts.FeatureSets = features.Sets([]string{"si", "si_log", features.SourceQP, "ti", "x264_23_bits", "x264_23_vmaf"})
	}

	model, err := predict.Train(titles, opts)
	require.NoError(t, err)

	return model
}

func TestRenderModel(
	t *testing.T,
) {
	titles := modelTitles(12)
	model := trained(t, titles, predict.TrainOptions{})

	testCases := []struct {
		name    string
		report  ModelReport
		want    []string
		wantNot []string
	}{
		{
			name:   "a model and its corpus",
			report: ModelReport{Model: model, ModelPath: "models/h264.json", Titles: titles, Corpus: []string{"corpus.jsonl"}, Codec: "h264", Preset: "fast"},
			want: []string{
				"Ladder model", "h264 fast", "1080p sources", "12 titles", "h264.json", "corpus.jsonl",
				"<h3>Designs</h3>", "<h3>Error across the grid</h3>", "<h3>Error by title</h3>", "<h3>Surfaces</h3>", "<h3>Rate–quality curves</h3>", "<h3>Coverage</h3>", "<h3>Titles</h3>",
				"Instant (--instant)", "No encode (--instant --no-encode)", "Instant, source quantisers unknown",
				"title-07", "Spatial information (SI)", "Fast encode CRF 23: bits", `class="surface-data"`, "Mean of the 12 titles",
			},
			wantNot: []string{"si_log"},
		},
		{
			name:    "a model alone",
			report:  ModelReport{Model: model, ModelPath: "h264.json", Codec: "h264", Preset: "fast"},
			want:    []string{"Ladder model", "<h3>Designs</h3>", "<h3>Error by title</h3>", "title-07"},
			wantNot: []string{"<h3>Surfaces</h3>", "<h3>Coverage</h3>", "<h3>Titles</h3>"},
		},
		{
			name:    "a corpus alone",
			report:  ModelReport{Titles: titles[:11], Partial: titles[11:], Corpus: []string{"corpus.jsonl"}, Codec: "h264", Preset: "fast"},
			want:    []string{"Ladder corpus", "11 titles", "2 resolutions × 4 CRFs", "<h3>Surfaces</h3>", "<h3>Coverage</h3>", "incomplete: left out of a training"},
			wantNot: []string{"<h3>Designs</h3>", "<h3>Error by title</h3>"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, RenderModel(&buf, testCase.report))

			html := buf.String()
			assertSelfContained(t, html)

			page := markup(html)
			assert.NotContains(t, page, "NaN")
			assert.NotContains(t, page, "Inf")

			for _, want := range testCase.want {
				assert.Contains(t, page, want)
			}

			for _, wantNot := range testCase.wantNot {
				assert.NotContains(t, page, wantNot)
			}
		})
	}
}

func TestModelFindings(
	t *testing.T,
) {
	titles := modelTitles(12)
	model := trained(t, titles, predict.TrainOptions{})

	// A grid whose last CRF leaves the titles near the quality of a top
	// rung: every low rung is past it.
	short := modelTitles(12)
	for i := range short {
		for k := range short[i].Points {
			for s := range short[i].Points[k].VMAF {
				short[i].Points[k].VMAF[s] = 80 + short[i].Points[k].VMAF[s]/5
			}
		}
	}

	testCases := []struct {
		name   string
		report ModelReport
		want   map[findings.Code]findings.Level
		absent []findings.Code
	}{
		{
			name:   "a model up to date with its corpus",
			report: ModelReport{Model: model, Titles: titles},
			want:   map[findings.Code]findings.Level{codeModelTitles: findings.Info, codeModelGrid: findings.OK, codeModelNoEncode: findings.OK},
			absent: []findings.Code{codeModelStale, codeModelPartial, codeModelSource, codeModelByTitle},
		},
		{
			name:   "a corpus of a few titles, one unfinished",
			report: ModelReport{Titles: titles[:4], Partial: titles[4:5]},
			want:   map[findings.Code]findings.Level{codeModelTitles: findings.Warn, codeModelPartial: findings.Warn},
			absent: []findings.Code{codeModelInstant, codeModelNoEncode},
		},
		{
			name:   "a corpus grown since the training",
			report: ModelReport{Model: model, Titles: modelTitles(14)},
			want:   map[findings.Code]findings.Level{codeModelStale: findings.Warn},
		},
		{
			name:   "a grid ending before the low rungs",
			report: ModelReport{Titles: short},
			want:   map[findings.Code]findings.Level{codeModelGrid: findings.Warn},
		},
		{
			name:   "a model without features",
			report: ModelReport{Model: trained(t, titles, predict.TrainOptions{NoFeatures: true})},
			want:   map[findings.Code]findings.Level{codeModelInstant: findings.Warn, codeModelNoEncode: findings.Info},
		},
		{
			name: "a model trained before the designs without encode",
			report: ModelReport{Model: trained(t, titles, predict.TrainOptions{
				FeatureSets: [][]string{{"si", "si_log", "ti", "x264_23_bits", "x264_23_vmaf"}},
			})},
			want: map[findings.Code]findings.Level{codeModelNoEncode: findings.Info, codeModelSource: findings.Info},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := map[findings.Code]findings.Level{}
			for _, f := range modelFindings(testCase.report) {
				got[f.Code] = f.Level
				assert.NotEmpty(t, f.Text)
			}

			for code, level := range testCase.want {
				require.Contains(t, got, code)
				assert.Equal(t, level, got[code], code)
			}

			for _, code := range testCase.absent {
				assert.NotContains(t, got, code)
			}
		})
	}
}

func TestDesignName(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		design predict.Design
		want   string
	}{
		{name: "anchors", design: predict.Design{Anchors: make([]predict.Point, 4)}, want: "4 anchors"},
		{name: "one anchor and features", design: predict.Design{Anchors: make([]predict.Point, 1), Features: []string{"si"}}, want: "1 anchor and the analysis"},
		{name: "instant", design: predict.Design{Features: []string{"si", "x264_23_bits", features.SourceQP}}, want: "Instant (--instant)"},
		{name: "instant, quantisers unknown", design: predict.Design{Features: []string{"si", "x264_23_bits"}}, want: "Instant, source quantisers unknown"},
		{name: "no encode", design: predict.Design{Features: []string{"si", features.SourceQP}}, want: "No encode (--instant --no-encode)"},
		{name: "no encode, quantisers unknown", design: predict.Design{Features: []string{"si"}}, want: "No encode, source quantisers unknown"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, designName(testCase.design))
		})
	}
}
