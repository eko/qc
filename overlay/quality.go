package overlay

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/eko/qc/quality"
)

// maxMetricRows bounds the metrics listed under VMAF: the panel stays a
// corner widget.
const maxMetricRows = 4

// VMAF bands of the score colour: green from vmafGood (transparent to most
// viewers), amber from vmafFair, red below.
const (
	vmafGood = 90
	vmafFair = 80
)

// percent converts a share to a percentage.
const percent = 100

// rightRows are the rows of the quality panel: VMAF, a few other metrics
// and how the frames were scored. They are empty without a comparison.
func (t *title) rightRows(
	opts Options,
) []row {
	if !opts.has(ItemQuality) || t.quality == nil {
		return nil
	}

	rows := []row{
		{height: rowHeight, text: func(int) string { return colour(colourMuted) + "VMAF" }},
		{height: scoreRow, text: t.scoreRow},
	}

	for _, name := range t.metricSeries() {
		rows = append(rows, row{height: rowHeight, text: t.metricRow(name)})
	}

	caption := t.scoringCaption()

	return append(rows, row{height: captionRow, text: func(int) string { return caption }})
}

// headlineSeries are the series listed first: the luma or overall value of
// each metric, the chroma components only when room is left.
var headlineSeries = []string{
	quality.SeriesXPSNRY, quality.SeriesPSNRY, quality.SeriesPSNRHVS, quality.SeriesSSIM,
	quality.SeriesMSSSIM, quality.SeriesCAMBI, quality.SeriesCIEDE2000, quality.SeriesWPSNRY,
	quality.SeriesDeltaEITP,
}

// metricSeries are the series of the metrics listed under VMAF: the
// headline ones measured, then the others in report order, up to
// maxMetricRows.
func (t *title) metricSeries() []string {
	var names, others []string

	for _, name := range headlineSeries {
		if _, ok := t.quality.Metric(name); ok {
			names = append(names, name)
		}
	}

	for _, m := range t.quality.Metrics {
		if !slices.Contains(headlineSeries, m.Name) {
			others = append(others, m.Name)
		}
	}

	names = append(names, others...)

	return names[:min(len(names), maxMetricRows)]
}

// scoreRow is the VMAF of the frame in its band's colour, or a dash for a
// frame that was not scored.
func (t *title) scoreRow(
	i int,
) string {
	s := t.score[i]
	if s < 0 {
		return fmt.Sprintf(`{\fs%d\b1}%s–`, fontScore, colour(colourMuted))
	}

	v := t.quality.Frames[s].Score

	band := colourRed
	switch {
	case v >= vmafGood:
		band = colourGreen
	case v >= vmafFair:
		band = colourAmber
	}

	return fmt.Sprintf(`{\fs%d\b1}%s%s`, fontScore, colour(band), strconv.FormatFloat(v, 'f', 1, 64))
}

// metricRow returns the row of a metric series: its value on scored frames
// at the series' precision.
func (t *title) metricRow(
	series string,
) func(i int) string {
	info := quality.DescribeSeries(series)
	name := fmt.Sprintf("%-10s", info.ShortLabel())

	return func(i int) string {
		text := colour(colourMuted) + escape(name) + colour(colourWhite)

		s := t.score[i]
		if s < 0 {
			return text + "–"
		}

		v, ok := t.quality.Frames[s].Metrics[series]
		if !ok {
			return text + "–"
		}

		text += info.Format(v)
		if info.Unit != "" {
			text += colour(colourMuted) + " " + info.Unit
		}

		return text
	}
}

// scoringCaption says which frames have a score: every frame, or the share
// of a sampled measurement (the other frames show a dash).
func (t *title) scoringCaption() string {
	q := t.quality

	text := "every frame scored"
	if q.Mode != quality.ModeExact {
		share := float64(q.FramesScored) / float64(max(q.FramesTotal, 1)) * percent
		text = "sampled · " + strconv.FormatFloat(share, 'f', 1, 64) + "% of frames scored"
	}

	return fmt.Sprintf(`{\fs%d}%s%s`, fontCaption, colour(colourMuted), text)
}
