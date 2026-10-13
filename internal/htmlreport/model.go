package htmlreport

import (
	"cmp"
	"fmt"
	"html/template"
	"io"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/ladder/features"
	"github.com/eko/qc/ladder/predict"
)

// ModelReport is what the report of a ladder model shows: the model, the
// corpus it is trained on, or both.
type ModelReport struct {
	// Model is the trained model and ModelPath its file; nil for the
	// report of a corpus alone.
	Model     *predict.Model
	ModelPath string
	// Titles are the titles of the corpus measured on its whole grid for
	// Codec at Preset, and Partial those still missing points; both nil
	// for the report of a model alone.
	Titles, Partial []predict.Title
	// Corpus are the corpus files the titles were read from.
	Corpus        []string
	Codec, Preset string
}

// Thresholds of the findings of a model.
const (
	// fewTitles is the number of titles under which an instant ladder
	// cannot be trusted, and enoughTitles the number a corpus starts
	// being one at (docs/model.md).
	fewTitles    = 10
	enoughTitles = 30
	// measuredError, closeError and looseError are the mean VMAF errors
	// of a design predicting without a probe under which it is as good
	// as a measured ladder, within a CRF step of it, and still worth
	// exploring with.
	measuredError = 0.5
	closeError    = 1.0
	looseError    = 2.0
	// modelTolerance is the tolerance of a ladder build, which picks the
	// design with the fewest anchors within it.
	modelTolerance = 0.5
	// outlierRatio is how many times the mean error of the corpus a title
	// must be off by to be named, and outlierError the least such error.
	outlierRatio = 1.75
	outlierError = 1.0
	// maxNamed bounds the titles a finding names.
	maxNamed = 5
	// lowRungVMAF is the VMAF above which the last CRF of the grid, at
	// the resolution of the source, is not yet where the low rungs of a
	// ladder are; edgeShare is the share of the titles in that case from
	// which the grid is too short.
	lowRungVMAF = 70
	edgeShare   = 0.25
)

// Codes of the findings of a model.
const (
	codeModelTitles   findings.Code = "model-titles"
	codeModelInstant  findings.Code = "model-instant"
	codeModelNoEncode findings.Code = "model-no-encode"
	codeModelSource   findings.Code = "model-source-quantiser"
	codeModelPartial  findings.Code = "model-partial-titles"
	codeModelStale    findings.Code = "model-stale"
	codeModelGrid     findings.Code = "model-grid"
	codeModelOutliers findings.Code = "model-outliers"
	codeModelByTitle  findings.Code = "model-no-title-errors"
)

// Areas of the report of a model.
var (
	areaModel  = area{Title: "Model", Subtitle: "errors on titles left out of its training", Icon: iconLadder}
	areaCorpus = area{Title: "Corpus", Subtitle: "what the titles measure", Icon: iconVideo}
)

// Sizes of the figures of a model, in SVG units.
const (
	halfWidth     = 470
	surfaceHeight = 440
	curvesHeight  = 340
)

// RenderModel writes the HTML report of a ladder model, of its corpus, or
// of both.
func RenderModel(
	w io.Writer,
	r ModelReport,
) error {
	p := page{Kind: "Ladder model", Title: fmt.Sprintf("%s %s", r.Codec, r.Preset), Subtitle: strings.Join(modelFacts(r), " · ")}
	if r.Model == nil {
		p.Kind = "Ladder corpus"
	}

	if m := r.Model; m != nil {
		p.Sections = append(p.Sections, designsSection(m), gridErrorSection(m))

		if s, ok := titleErrorSection(m); ok {
			p.Sections = append(p.Sections, s)
		}
	}

	if len(r.Titles) > 0 {
		g := newGrid(r.Titles)
		p.Sections = append(p.Sections, surfaceSection(r.Titles, g), curvesSection(g))

		if s, ok := featuresSection(r.Titles); ok {
			p.Sections = append(p.Sections, s)
		}
	}

	if len(r.Titles)+len(r.Partial) > 0 {
		p.Sections = append(p.Sections, titlesSection(r))
	}

	p.summarize(modelCards(r), modelFindings(r))

	return render(w, p)
}

// modelFacts are the facts under the title of the report.
func modelFacts(
	r ModelReport,
) []string {
	var facts []string

	if m := r.Model; m != nil {
		facts = append(facts, fmt.Sprintf("%dp sources", m.SourceHeight), plural(m.Titles, "title"), plural(m.Segments, "segment"),
			fmt.Sprintf("%d resolutions × %d CRFs", len(m.Heights), len(m.CRFs)), filepath.Base(r.ModelPath))
	} else if len(r.Titles) > 0 {
		g := newGrid(r.Titles)
		facts = append(facts, fmt.Sprintf("%dp sources", r.Titles[0].SourceHeight), plural(len(r.Titles), "title"),
			fmt.Sprintf("%d resolutions × %d CRFs", len(g.heights), len(g.crfs)))
	}

	for _, path := range r.Corpus {
		facts = append(facts, filepath.Base(path))
	}

	return facts
}

// noEncode returns the design of the model predicting without any encode.
func noEncode(
	m *predict.Model,
) (predict.Design, bool) {
	d, ok := m.Instant(features.Usable(false, true))
	if !ok || slices.ContainsFunc(d.Features, features.NeedsEncode) {
		return predict.Design{}, false
	}

	return d, true
}

// instant returns the design of the model predicting without a probe,
// from everything a build reads of a title; not the one without encode.
func instant(
	m *predict.Model,
) (predict.Design, bool) {
	d, ok := m.Instant(nil)
	if !ok || !slices.ContainsFunc(d.Features, features.NeedsEncode) {
		return predict.Design{}, false
	}

	return d, true
}

// designName names a design by what a ladder measures to use it.
func designName(
	d predict.Design,
) string {
	switch {
	case len(d.Anchors) == 0 && !slices.ContainsFunc(d.Features, features.NeedsEncode):
		return cmp.Or(sourceSuffix("No encode", d), "No encode (--instant --no-encode)")
	case len(d.Anchors) == 0:
		return cmp.Or(sourceSuffix("Instant", d), "Instant (--instant)")
	case len(d.Features) > 0:
		return plural(len(d.Anchors), "anchor") + " and the analysis"
	}

	return plural(len(d.Anchors), "anchor")
}

// sourceSuffix names a design that does not read the quantisers of the
// source, which a build falls back on when the decoder does not tell
// them; empty for the design that reads them.
func sourceSuffix(
	name string,
	d predict.Design,
) string {
	if slices.Contains(d.Features, features.SourceQP) {
		return ""
	}

	return name + ", source quantisers unknown"
}

// designAnchors lists the points a design measures.
func designAnchors(
	d predict.Design,
) string {
	points := make([]string, len(d.Anchors))
	for i, a := range d.Anchors {
		points[i] = fmt.Sprintf("%dp@%s", a.Height, crfLabel(a.CRF))
	}

	return strings.Join(points, " ")
}

// crfLabel writes a CRF without the zeros it does not need.
func crfLabel(
	crf float64,
) string {
	return strconv.FormatFloat(crf, 'f', -1, 64)
}

// percent writes a fraction as a percentage.
func percent(
	fraction float64,
) string {
	return fmt.Sprintf("%.1f%%", fraction*100)
}

// designsSection compares the designs of the model: what each measures
// and the error it made on titles it had not seen.
func designsSection(
	m *predict.Model,
) section {
	s := section{
		Title: "Designs", Subtitle: "what a ladder measures, and the error it then makes", Area: areaModel,
		Table: &table{Head: []string{"Design", "VMAF error (mean)", "(worst)", "Bitrate error (mean)", "Features read", "Measured"}},
		Method: []string{
			"A design is what a ladder measures of a new title before the model predicts the rest of its grid: a few encodes (the anchors), " +
				"the analysis of the title, or both. A ladder build takes the design with the fewest anchors whose mean error is within its --tolerance.",
			"Each design is validated by leaving every title of the corpus out in turn: a model trained on the others predicts digests drawn " +
				"from it. The errors are those of these predictions against the measurements, averaged over the points whose VMAF a ladder uses (30 to 98).",
		},
	}

	var bars []svg.Bar

	for _, d := range m.Designs {
		row := []string{designName(d), "–", "–", "–", strconv.Itoa(len(d.Features)), cmp.Or(designAnchors(d), "nothing")}

		if v := d.Validation; v != nil {
			row[1], row[2], row[3] = fmt.Sprintf("%.2f", v.MeanVMAFError), fmt.Sprintf("%.2f", v.WorstVMAFError), percent(v.MeanBitrateError)

			color := blue
			if len(d.Anchors) == 0 {
				color = orange
			}

			bars = append(bars, svg.Bar{
				Label: designName(d), Value: v.MeanVMAFError, Text: fmt.Sprintf("%.2f", v.MeanVMAFError), Color: color,
				Detail: "bitrate " + percent(v.MeanBitrateError),
			})
		}

		s.Table.Rows = append(s.Table.Rows, row)
	}

	if len(bars) == 0 {
		s.Notes = append(s.Notes, "Not validated: leaving a title out needs at least two.")

		return s
	}

	s.Charts = append(s.Charts, svg.Bars{
		Title: "Mean VMAF error on titles the model had not seen", Width: chartWidth, Bars: bars,
		Format: func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) },
		Marks:  []svg.Mark{{Value: modelTolerance, Label: "default tolerance"}},
	}.HTML())

	return s
}

// gridErrorSection shows where on the grid the main designs are wrong:
// the error of each point, for the VMAF and for the bitrate.
func gridErrorSection(
	m *predict.Model,
) section {
	s := section{
		Title: "Error across the grid", Subtitle: "95th percentile of the error at each resolution and CRF", Area: areaModel,
		Method: []string{
			"Each cell is the error 95% of the predictions of that point stayed within, on titles left out of the training: " +
				"the half-width a ladder gives a predicted probe. The corners of the grid, where every title is near VMAF 100 or near 0, weigh little in a ladder.",
		},
	}

	shown := mainDesigns(m)
	if len(shown) == 0 {
		s.Notes = append(s.Notes, "Not validated: leaving a title out needs at least two.")

		return s
	}

	// One scale for every design: their colours compare.
	worstVMAF, worstRate := 0.0, 0.0

	for _, d := range shown {
		worstVMAF = max(worstVMAF, slices.Max(d.Validation.VMAFError))
		worstRate = max(worstRate, slices.Max(d.Validation.BitrateError))
	}

	rows, cols := make([]string, len(m.Heights)), make([]string, len(m.CRFs))
	for j, h := range m.Heights {
		rows[j] = fmt.Sprintf("%dp", h)
	}

	for i, crf := range m.CRFs {
		cols[i] = crfLabel(crf)
	}

	for _, d := range shown {
		vmaf := svg.Heatmap{
			Title: designName(d) + ": VMAF error", Width: halfWidth, Rows: rows, Cols: cols, ColTitle: "CRF", Max: worstVMAF, Color: blue,
			Values: byRow(d.Validation.VMAFError, len(cols), 1), Text: cellTexts(d.Validation.VMAFError, len(cols), "%.1f", 1),
		}
		rate := svg.Heatmap{
			Title: designName(d) + ": bitrate error (%)", Width: halfWidth, Rows: rows, Cols: cols, ColTitle: "CRF", Max: worstRate * 100, Color: blue,
			Values: byRow(d.Validation.BitrateError, len(cols), 100), Text: cellTexts(d.Validation.BitrateError, len(cols), "%.0f", 100),
		}

		s.Charts = append(s.Charts, figureRow(vmaf.HTML(), rate.HTML()))
	}

	return s
}

// mainDesigns are the validated designs a reader chooses between: the one
// predicting without a probe, the one without any encode, and the one a
// ladder build measures anchors for at its default tolerance.
func mainDesigns(
	m *predict.Model,
) []predict.Design {
	var shown []predict.Design

	if d, ok := instant(m); ok {
		shown = append(shown, d)
	}

	if d, ok := noEncode(m); ok {
		shown = append(shown, d)
	}

	if d := m.Design(modelTolerance, nil); len(d.Anchors) > 0 {
		shown = append(shown, d)
	}

	return slices.DeleteFunc(shown, func(d predict.Design) bool { return d.Validation == nil })
}

// figureRow lays figures side by side, one under the other on a narrow
// page.
func figureRow(
	figures ...template.HTML,
) template.HTML {
	var b strings.Builder

	b.WriteString(`<div class="figure-row">`)

	for _, f := range figures {
		b.WriteString(string(f))
	}

	b.WriteString(`</div>`)

	return template.HTML(b.String()) //nolint:gosec // figures already rendered
}

// byRow cuts values, a line of the grid after the other, into lines of
// cols values, each scaled.
func byRow(
	values []float64,
	cols int,
	scale float64,
) [][]float64 {
	var out [][]float64

	for start := 0; start+cols <= len(values); start += cols {
		line := make([]float64, cols)
		for i := range line {
			line[i] = values[start+i] * scale
		}

		out = append(out, line)
	}

	return out
}

// cellTexts writes the values of byRow.
func cellTexts(
	values []float64,
	cols int,
	format string,
	scale float64,
) [][]string {
	lines := byRow(values, cols, scale)
	out := make([][]string, len(lines))

	for j, line := range lines {
		out[j] = make([]string, len(line))
		for i, v := range line {
			out[j][i] = fmt.Sprintf(format, v)
		}
	}

	return out
}

// titleErrors returns the validation of a design by title, by name.
func titleErrors(
	d predict.Design,
	ok bool,
) map[string]predict.TitleValidation {
	if !ok || d.Validation == nil {
		return nil
	}

	out := make(map[string]predict.TitleValidation, len(d.Validation.Titles))
	for _, t := range d.Validation.Titles {
		out[t.Name] = t
	}

	return out
}

// titleErrorSection ranks the titles by the error of the design that
// predicts without a probe: the kinds of content the corpus lacks. It
// has nothing to show for a model that did not keep these errors.
func titleErrorSection(
	m *predict.Model,
) (section, bool) {
	lead, ok := instant(m)
	if !ok {
		lead, ok = noEncode(m)
	}

	if !ok {
		lead = m.Design(modelTolerance, nil)
	}

	if lead.Validation == nil || len(lead.Validation.Titles) == 0 {
		return section{}, false
	}

	measured := m.Design(modelTolerance, nil)
	without, hasWithout := noEncode(m)
	byMeasured, byWithout := titleErrors(measured, true), titleErrors(without, hasWithout)

	ranked := slices.Clone(lead.Validation.Titles)
	slices.SortStableFunc(ranked, func(a, b predict.TitleValidation) int { return cmp.Compare(b.VMAFError, a.VMAFError) })

	s := section{
		Title: "Error by title", Subtitle: "each title predicted by a model that had not seen it", Area: areaModel,
		Table: &table{Head: []string{"Title", designName(lead) + ": VMAF error", "Bitrate error", "No encode: VMAF error", designName(measured) + ": VMAF error"}},
		Method: []string{
			"A title far above the others is a kind of content the rest of the corpus does not teach: titles like it are what the corpus needs next. " +
				"Errors are means over the digests drawn from the title and over the points whose VMAF a ladder uses.",
		},
	}

	bars := make([]svg.Bar, len(ranked))

	for i, t := range ranked {
		bars[i] = svg.Bar{Label: t.Name, Value: t.VMAFError, Text: fmt.Sprintf("%.2f", t.VMAFError), Color: orange, Detail: "bitrate " + percent(t.BitrateError)}
		row := []string{t.Name, fmt.Sprintf("%.2f", t.VMAFError), percent(t.BitrateError), "–", "–"}

		if e, ok := byWithout[t.Name]; ok {
			row[3] = fmt.Sprintf("%.2f", e.VMAFError)
		}

		if e, ok := byMeasured[t.Name]; ok {
			row[4] = fmt.Sprintf("%.2f", e.VMAFError)
		}

		s.Table.Rows = append(s.Table.Rows, row)
	}

	s.Charts = append(s.Charts, svg.Bars{
		Title: designName(lead) + ": mean VMAF error of each title", Width: chartWidth, Bars: bars,
		Format: func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) },
		Marks:  []svg.Mark{{Value: lead.Validation.MeanVMAFError, Label: "mean"}},
	}.HTML())

	return s, true
}

// grid holds the measurements of the titles of a corpus at every point
// of their grid: the mean of the segments of each title.
type grid struct {
	heights []int
	crfs    []float64
	names   []string
	// vmaf and rate are, for each title, its values at every point, a
	// resolution after the other.
	vmaf, rate [][]float64
}

// newGrid reads the grid of titles measured on the same points.
func newGrid(
	titles []predict.Title,
) grid {
	var g grid

	for _, tp := range titles[0].Points {
		if !slices.Contains(g.heights, tp.Height) {
			g.heights = append(g.heights, tp.Height)
		}

		if !slices.Contains(g.crfs, tp.CRF) {
			g.crfs = append(g.crfs, tp.CRF)
		}
	}

	slices.SortFunc(g.heights, func(a, b int) int { return cmp.Compare(b, a) })
	slices.Sort(g.crfs)

	for _, t := range titles {
		vmaf, rate := make([]float64, 0, len(t.Points)), make([]float64, 0, len(t.Points))

		for _, h := range g.heights {
			for _, crf := range g.crfs {
				k := slices.IndexFunc(t.Points, func(tp predict.TitlePoint) bool { return tp.Height == h && tp.CRF == crf })
				if k < 0 {
					vmaf, rate = append(vmaf, 0), append(rate, 0)

					continue
				}

				vmaf, rate = append(vmaf, meanOf(t.Points[k].VMAF)), append(rate, meanOf(t.Points[k].Bitrate))
			}
		}

		g.names, g.vmaf, g.rate = append(g.names, t.Name), append(g.vmaf, vmaf), append(g.rate, rate)
	}

	return g
}

// meanOf is the mean of values; zero for none.
func meanOf(
	values []float64,
) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// across reduces the values of the titles at each point to one.
func across(
	titles [][]float64,
	reduce func(values []float64) float64,
) []float64 {
	out := make([]float64, len(titles[0]))
	column := make([]float64, len(titles))

	for i := range out {
		for t := range titles {
			column[t] = titles[t][i]
		}

		out[i] = reduce(column)
	}

	return out
}

// geometricMean is the mean of bitrates, which titles spread over
// decades: no title weighs more than another.
func geometricMean(
	values []float64,
) float64 {
	sum := 0.0
	for _, v := range values {
		sum += math.Log(max(v, 1))
	}

	return math.Exp(sum / float64(len(values)))
}

// rounded keeps what a reader can see of values: the page is lighter.
func rounded(
	values []float64,
	digits int,
) []float64 {
	scale := math.Pow(10, float64(digits))
	out := make([]float64, len(values))

	for i, v := range values {
		out[i] = math.Round(v*scale) / scale
	}

	return out
}

// surfaceSection draws the corpus as two surfaces over its grid, the VMAF
// and the bitrate: their mean over the titles, and each title for the
// page script to go through.
func surfaceSection(
	titles []predict.Title,
	g grid,
) section {
	meanVMAF, meanRate := across(g.vmaf, meanOf), across(g.rate, geometricMean)
	loRate, hiRate := across(g.rate, slices.Min[[]float64]), across(g.rate, slices.Max[[]float64])

	rows := make([]string, len(g.heights))
	for j, h := range g.heights {
		rows[j] = fmt.Sprintf("%dp", h)
	}

	shade := func(vmaf []float64) []float64 {
		out := make([]float64, len(vmaf))
		for i, v := range vmaf {
			out[i] = math.Round(v) / maxVMAF
		}

		return out
	}

	c := svg.Surface{
		Title: "VMAF and bitrate over the grid", Width: chartWidth, Height: surfaceHeight, Rows: rows, Cols: g.crfs, RowTitle: "resolution", ColTitle: "CRF",
		Panels: []svg.SurfacePanel{
			{Name: "VMAF", Unit: svg.UnitNumber, Max: maxVMAF},
			{Name: "Bitrate (log scale)", Unit: svg.UnitBitrate, Log: true, Min: max(slices.Min(loRate), 1), Max: slices.Max(hiRate)},
		},
		Shapes: []svg.SurfaceShape{{Name: fmt.Sprintf("Mean of the %d titles", len(titles)), Z: [][]float64{rounded(meanVMAF, 1), rounded(meanRate, 0)}, Shade: shade(meanVMAF)}},
		Lo:     [][]float64{rounded(across(g.vmaf, slices.Min[[]float64]), 1), rounded(loRate, 0)},
		Hi:     [][]float64{rounded(across(g.vmaf, slices.Max[[]float64]), 1), rounded(hiRate, 0)},
		Color:  orange, ShadeTitle: "VMAF 0 to 100",
	}

	for t, name := range g.names {
		c.Shapes = append(c.Shapes, svg.SurfaceShape{Name: name, Z: [][]float64{rounded(g.vmaf[t], 1), rounded(g.rate[t], 0)}, Shade: shade(g.vmaf[t])})
	}

	return section{
		Title: "Surfaces", Subtitle: "what a model learns the shape of", Area: areaCorpus, Charts: []template.HTML{c.HTML()},
		Method: []string{
			"Both surfaces stand on the grid of the corpus: a resolution per line, a CRF per column. Their faces are tinted by the VMAF, " +
				"so the bitrate surface shows what each quality costs. The bars are the room the titles take around the mean, " +
				"from the lowest to the highest of them at each point: what a model has to place a new title in.",
			"With the page script: drag to turn the surfaces, point at a corner to read it, pick a title or play them all to see each " +
				"against the outline of the mean. The mean of the bitrates is geometric.",
		},
	}
}

// curvesSection draws the mean rate-quality curve of each resolution of
// the corpus: the picture a ladder is placed on.
func curvesSection(
	g grid,
) section {
	meanVMAF, meanRate := across(g.vmaf, meanOf), across(g.rate, geometricMean)
	series := make([]svg.Series, len(g.heights))

	for j, h := range g.heights {
		samples := &svg.Samples{}

		// From the highest CRF to the lowest: by growing bitrate.
		for i := len(g.crfs) - 1; i >= 0; i-- {
			k := j*len(g.crfs) + i
			samples.X, samples.Y = append(samples.X, meanRate[k]), append(samples.Y, meanVMAF[k])
			samples.Details = append(samples.Details, svg.Detail{Title: fmt.Sprintf("%dp · CRF %s", h, crfLabel(g.crfs[i])), Fields: []svg.Field{
				{Label: "bitrate", Value: bitrateLabel(meanRate[k])}, {Label: "VMAF", Value: fmt.Sprintf("%.1f", meanVMAF[k])},
			}})
		}

		series[j] = svg.Series{Name: fmt.Sprintf("%dp", h), Color: palette[j%len(palette)], Samples: samples, Points: samples.Points(), Digits: 1}
	}

	return section{
		Title: "Rate–quality curves", Subtitle: "mean of the titles, by resolution", Area: areaCorpus,
		Charts: []template.HTML{svg.Chart{
			Title: "Mean VMAF against mean bitrate", Width: chartWidth, Height: curvesHeight, Series: series, LogX: true, YMax: maxVMAF,
			X: svg.UnitBitrate, Y: svg.UnitNumber, Nearest: true,
		}.HTML()},
		Method: []string{
			"Each curve joins the points of one resolution, from its highest CRF to its lowest. Where two curves cross, the ladder of an " +
				"average title changes resolution. The mean of the bitrates is geometric.",
		},
	}
}

// featureLabels name the features a reader meets most.
var featureLabels = map[string]string{
	"si": "Spatial information (SI)", "ti": "Temporal information (TI)", "luma": "Mean luma",
	"src_qp": "Quantiser of the source", "motion_pan": "Camera pan", "motion_tilt": "Camera tilt",
	"motion_zoom": "Camera zoom", "motion_shake": "Camera shake",
}

// featureLabel names a feature: its known name, or what the fast encode
// at a CRF reads, or the name the corpus gives it.
func featureLabel(
	name string,
) string {
	if label, ok := featureLabels[name]; ok {
		return label
	}

	if parts := strings.SplitN(name, "_", 3); len(parts) == 3 && features.NeedsEncode(name) { //nolint:mnd // encoder, CRF, measure
		return fmt.Sprintf("Fast encode CRF %s: %s", parts[1], parts[2])
	}

	return name
}

// logSuffix ends the features that repeat another on a log scale.
const logSuffix = "_log"

// featureOrder lists first the features a reader knows a title by; the
// others follow by name.
var featureOrder = []string{"si", "ti", "luma", features.SourceQP, "motion_pan", "motion_tilt", "motion_zoom", "motion_shake"}

// featureRank orders the features of a corpus for the reader.
func featureRank(
	a, b string,
) int {
	rank := func(name string) int {
		if i := slices.Index(featureOrder, name); i >= 0 {
			return i
		}

		return len(featureOrder)
	}

	return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b))
}

// featuresSection places the titles on each feature a model reads of
// them: where they gather, and what kind of title the corpus has none of.
func featuresSection(
	titles []predict.Title,
) (section, bool) {
	names := map[string]bool{}

	for _, t := range titles {
		for name := range t.Features {
			names[name] = !strings.HasSuffix(name, logSuffix)
		}
	}

	var rows []svg.Strip

	for _, name := range slices.SortedFunc(maps.Keys(names), featureRank) {
		if !names[name] {
			continue
		}

		row := svg.Strip{Label: featureLabel(name), Format: func(v float64) string { return strconv.FormatFloat(v, 'g', 3, 64) }}

		for _, t := range titles {
			if values := t.Features[name]; len(values) > 0 {
				row.Values, row.Names = append(row.Values, meanOf(values)), append(row.Names, t.Name)
			}
		}

		if len(row.Values) > 0 {
			rows = append(rows, row)
		}
	}

	if len(rows) == 0 {
		return section{}, false
	}

	return section{
		Title: "Coverage", Subtitle: "where the titles sit on what a model reads of them", Area: areaCorpus,
		Charts: []template.HTML{svg.Strips{Title: "One dot per title, each line from its lowest to its highest title", Width: chartWidth, Rows: rows}.HTML()},
		Method: []string{
			"Each line is a feature of the analysis, averaged over the segments of each title. A model is sure of itself where the dots are " +
				"many, and guesses where a line is empty: a new title falling in a gap, or beyond an end, is unlike the corpus.",
		},
	}, true
}

// titlesSection lists the titles of the corpus and what each holds.
func titlesSection(
	r ModelReport,
) section {
	s := section{
		Title: "Titles", Subtitle: plural(len(r.Titles)+len(r.Partial), "title"), Area: areaCorpus, Collapsed: true,
		Table: &table{Head: []string{"Title", "Segments", "Points measured", "Features", "State"}},
	}

	row := func(t predict.Title, state string) []string {
		segments := 0
		if len(t.Points) > 0 {
			segments = len(t.Points[0].VMAF)
		}

		described := "none"
		if len(t.Features) > 0 {
			described = strconv.Itoa(len(t.Features))
		}

		return []string{t.Name, strconv.Itoa(segments), strconv.Itoa(len(t.Points)), described, state}
	}

	for _, t := range r.Titles {
		s.Table.Rows = append(s.Table.Rows, row(t, "complete"))
	}

	for _, t := range r.Partial {
		s.Table.Rows = append(s.Table.Rows, row(t, "incomplete: left out of a training"))
	}

	return s
}

// modelCards are the key numbers of a model and of its corpus.
func modelCards(
	r ModelReport,
) []card {
	var cards []card

	switch {
	case r.Model != nil:
		m := r.Model
		cards = append(cards, card{Label: "Titles", Value: strconv.Itoa(m.Titles),
			Detail: fmt.Sprintf("%s · CRF %s to %s", plural(m.Segments, "segment"), crfLabel(m.CRFs[0]), crfLabel(m.CRFs[len(m.CRFs)-1]))})
	case len(r.Titles) > 0:
		g := newGrid(r.Titles)
		cards = append(cards,
			card{Label: "Titles", Value: strconv.Itoa(len(r.Titles)), Detail: plural(len(r.Partial), "incomplete title")},
			card{Label: "Grid", Value: fmt.Sprintf("%d × %d", len(g.heights), len(g.crfs)),
				Detail: fmt.Sprintf("CRF %s to %s", crfLabel(g.crfs[0]), crfLabel(g.crfs[len(g.crfs)-1]))})
	}

	if r.Model == nil {
		return cards
	}

	// toned colours an error a ladder is published from; the error
	// without encode, which sorts and pre-sizes, is only stated.
	errorCard := func(label string, d predict.Design, detail string, toned bool) {
		if d.Validation == nil {
			return
		}

		c := card{Label: label, Value: fmt.Sprintf("%.2f VMAF", d.Validation.MeanVMAFError), Detail: detail + " · bitrate " + percent(d.Validation.MeanBitrateError)}

		switch {
		case !toned:
		case d.Validation.MeanVMAFError <= closeError:
			c.Tone = toneGood
		case d.Validation.MeanVMAFError > looseError:
			c.Tone = toneWarn
		}

		cards = append(cards, c)
	}

	if d, ok := instant(r.Model); ok {
		errorCard("Instant error", d, "--instant", true)
	}

	if d, ok := noEncode(r.Model); ok {
		errorCard("No-encode error", d, "--no-encode", false)
	}

	if d := r.Model.Design(modelTolerance, nil); len(d.Anchors) > 0 {
		errorCard("Measured error", d, plural(len(d.Anchors), "anchor"), true)
	}

	return cards
}

// modelFindings are what a reader should know before trusting the model:
// whether it predicts without a probe and how well, and what its corpus
// lacks.
func modelFindings(
	r ModelReport,
) []finding {
	var list []finding

	titles := len(r.Titles)
	if r.Model != nil {
		titles = r.Model.Titles
	}

	switch {
	case titles < fewTitles:
		list = append(list, finding{Level: findings.Warn, Code: codeModelTitles,
			Text: fmt.Sprintf("%s: too few to trust a ladder predicted without a probe. Aim for %d or more, as different as your programmes are.", plural(titles, "title"), enoughTitles)})
	case titles < enoughTitles:
		list = append(list, finding{Level: findings.Info, Code: codeModelTitles,
			Text: fmt.Sprintf("%d titles: errors still fall as titles are added, up to %d or so.", titles, enoughTitles)})
	default:
		list = append(list, finding{Level: findings.OK, Code: codeModelTitles, Text: fmt.Sprintf("%d titles in the corpus.", titles)})
	}

	if len(r.Partial) > 0 {
		list = append(list, finding{Level: findings.Warn, Code: codeModelPartial,
			Text: fmt.Sprintf("%s not measured on the whole grid, left out of a training: %s. Run qc model corpus again to finish them.",
				plural(len(r.Partial), "title"), named(r.Partial))})
	}

	if g, ok := shortGrid(r.Titles); ok {
		list = append(list, g)
	}

	if r.Model == nil {
		return list
	}

	list = append(list, instantFindings(r.Model)...)

	if n := len(r.Titles); n > 0 && n != r.Model.Titles {
		list = append(list, finding{Level: findings.Warn, Code: codeModelStale,
			Text: fmt.Sprintf("The model was trained on %s and the corpus now holds %d complete: train it again (qc model train).", plural(r.Model.Titles, "title"), n)})
	}

	return list
}

// named lists the names of titles, the first of many.
func named(
	titles []predict.Title,
) string {
	names := make([]string, 0, maxNamed+1)

	for i, t := range titles {
		if i == maxNamed {
			names = append(names, fmt.Sprintf("and %d more", len(titles)-maxNamed))

			break
		}

		names = append(names, t.Name)
	}

	return strings.Join(names, ", ")
}

// shortGrid finds a grid ending before the low rungs of its titles: at
// the resolution of the source and the last CRF, many of them are still
// well above the quality of a low rung, which a ladder then wants at a
// CRF the model does not know.
func shortGrid(
	titles []predict.Title,
) (finding, bool) {
	if len(titles) == 0 {
		return finding{}, false
	}

	g := newGrid(titles)
	last := len(g.crfs) - 1
	above := 0

	for _, vmaf := range g.vmaf {
		if vmaf[last] > lowRungVMAF {
			above++
		}
	}

	if float64(above) < edgeShare*float64(len(titles)) {
		return finding{Level: findings.OK, Code: codeModelGrid,
			Text: fmt.Sprintf("The grid reaches the low rungs: at %dp and CRF %s, %d of %d titles are still above VMAF %d.", g.heights[0], crfLabel(g.crfs[last]), above, len(titles), lowRungVMAF)}, true
	}

	return finding{Level: findings.Warn, Code: codeModelGrid,
		Text: fmt.Sprintf("The grid stops short: at %dp and CRF %s, its last, %d of %d titles are still above VMAF %d. Their low rungs need higher CRFs, "+
			"which a ladder predicted without a probe then places at lower resolutions. Measure the corpus with higher CRFs (qc model corpus --crfs).",
			g.heights[0], crfLabel(g.crfs[last]), above, len(titles), lowRungVMAF)}, true
}

// instantFindings tell whether the model predicts without a probe, and
// without any encode, and how far it is then from a measured ladder.
func instantFindings(
	m *predict.Model,
) []finding {
	var list []finding

	lead, ok := instant(m)

	switch {
	case !ok:
		list = append(list, finding{Level: findings.Warn, Code: codeModelInstant,
			Text: "No design predicts without a probe (--instant): the corpus holds no features of some title. Run qc model corpus again, then train."})
	case lead.Validation == nil:
		list = append(list, finding{Level: findings.Info, Code: codeModelInstant, Text: "The design predicting without a probe (--instant) is not validated: a corpus of one title."})
	default:
		list = append(list, instantFinding(lead))

		if !slices.Contains(lead.Features, features.SourceQP) {
			list = append(list, finding{Level: findings.Info, Code: codeModelSource,
				Text: "No design reads the quantisers of the source, worth about a point of bitrate error on 8-bit H.264 sources: the corpus was described by an earlier version. Run qc model corpus again, then train."})
		}
	}

	if d, ok := noEncode(m); !ok {
		list = append(list, finding{Level: findings.Info, Code: codeModelNoEncode,
			Text: "No design predicts without any encode: --no-encode fails with this model. Train it again with this version of qc."})
	} else if d.Validation != nil {
		list = append(list, finding{Level: findings.OK, Code: codeModelNoEncode,
			Text: fmt.Sprintf("--no-encode is available: VMAF within %.2f on average and bitrate within %s, to sort or pre-size rather than to publish.",
				d.Validation.MeanVMAFError, percent(d.Validation.MeanBitrateError))})
	}

	if !ok || lead.Validation == nil {
		return list
	}

	if len(lead.Validation.Titles) == 0 {
		return append(list, finding{Level: findings.Info, Code: codeModelByTitle,
			Text: "The model does not hold the error of each title: it was trained by an earlier version. Train it again to see which titles it reads worst."})
	}

	var off []string

	worstFirst := slices.Clone(lead.Validation.Titles)
	slices.SortStableFunc(worstFirst, func(a, b predict.TitleValidation) int { return cmp.Compare(b.VMAFError, a.VMAFError) })

	for _, t := range worstFirst {
		if t.VMAFError > outlierError && t.VMAFError > outlierRatio*lead.Validation.MeanVMAFError {
			off = append(off, fmt.Sprintf("%s (%.1f)", t.Name, t.VMAFError))
		}
	}

	if len(off) > 0 {
		shown := off[:min(len(off), maxNamed)]
		list = append(list, finding{Level: findings.Info, Code: codeModelOutliers,
			Text: fmt.Sprintf("%s predicted far worse than the others without a probe: %s. The corpus has too few titles like them.", plural(len(off), "title"), strings.Join(shown, ", "))})
	}

	return list
}

// instantFinding words the error of the design predicting without a
// probe, on the scale of docs/model.md.
func instantFinding(
	d predict.Design,
) finding {
	v := d.Validation
	head := fmt.Sprintf("--instant: VMAF within %.2f on average of a title the model has not seen, bitrate within %s", v.MeanVMAFError, percent(v.MeanBitrateError))

	switch {
	case v.MeanVMAFError <= measuredError:
		return finding{Level: findings.OK, Code: codeModelInstant, Text: head + ": as good as a measured ladder."}
	case v.MeanVMAFError <= closeError:
		return finding{Level: findings.OK, Code: codeModelInstant, Text: head + ": most rungs within one CRF step of a verified ladder."}
	case v.MeanVMAFError <= looseError:
		return finding{Level: findings.Info, Code: codeModelInstant,
			Text: head + ": right on content close to the corpus, 2 to 3 VMAF off on content unlike it. Verify before you publish."}
	}

	return finding{Level: findings.Warn, Code: codeModelInstant, Text: head + ": add titles, and meanwhile use the model with anchors."}
}
