package htmlreport

import (
	"fmt"
	"html/template"
	"strconv"

	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
)

// shotChartHeight is the height of the per-shot ladder chart.
const shotChartHeight = 360

// shotRung is a per-shot rung with its title-wide pooled predicted bitrate.
type shotRung struct {
	index  int
	rung   ladder.Rung
	pooled float64
}

// shotRungs are the rungs carrying a per-shot allocation of every shot.
func shotRungs(
	r *ladder.Result,
) []shotRung {
	var out []shotRung

	for i, rung := range r.Rungs {
		if rung.PerShot == nil || len(r.Shots) == 0 || len(rung.PerShot.Shots) != len(r.Shots) {
			continue
		}

		if pooled := rung.PerShot.PooledBitrate(r.Shots); pooled > 0 {
			out = append(out, shotRung{index: i, rung: rung, pooled: pooled})
		}
	}

	return out
}

// shotLadderSection is the per-shot ladder: every shot's bitrate in every
// per-shot rung along the title (a vertical slice of the chart is the
// shot's own ladder), and the table of every shot's CRF, bitrate and VMAF
// per rung.
func shotLadderSection(
	r *ladder.Result,
) (section, bool) {
	rungs := shotRungs(r)
	if len(rungs) == 0 {
		return section{}, false
	}

	measured := 0

	for _, s := range r.Shots {
		if s.Measured {
			measured++
		}
	}

	return section{
		Title: "Per-shot ladder",
		Subtitle: fmt.Sprintf("%d shots × %d rungs: each shot's predicted bitrate per rung, log scale; %d shots measured on the digest, the others predicted from similar shots",
			len(r.Shots), len(rungs), measured),
		Charts: []template.HTML{shotLadderChart(r, rungs)},
		Table:  shotLadderTable(r, rungs),
		Method: []string{"Cost is a shot's bitrate over its rung's average, averaged over the rungs. " +
			"Predicted values come from the shot models, without the rungs' rate caps; the tooltips add, for shots in the digest, " +
			"the bitrate and VMAF of their part of the verification encode."},
	}, true
}

// shotLadderChart draws every per-shot rung as steps along the title: one
// level per shot, at its predicted bitrate.
func shotLadderChart(
	r *ladder.Result,
	rungs []shotRung,
) template.HTML {
	series := make([]svg.Series, len(rungs))

	for k, sr := range rungs {
		samples := &svg.Samples{}

		for i, s := range r.Shots {
			iv := r.ShotInterval(s)
			a := sr.rung.PerShot.Shots[i]
			detail := shotDetail(i, s, iv, a)
			rate := float64(a.PredictedBitrate)

			// Steps are drawn as pairs of points, like stratum means.
			samples.X = append(samples.X, iv.Start.Seconds(), iv.End.Seconds())
			samples.Y = append(samples.Y, rate, rate)
			samples.Details = append(samples.Details, detail, detail)
		}

		series[k] = svg.Series{
			Name: rungName(sr), Color: palette[k%len(palette)], Samples: samples, Points: samples.Points(), Step: true,
		}
	}

	return svg.Chart{Title: "Predicted bitrate of every shot per rung", Width: chartWidth, Height: shotChartHeight, Series: series, X: svg.UnitTime, Y: svg.UnitBitrate, LogY: true}.HTML()
}

// rungName names a per-shot rung in legends and table heads.
func rungName(
	sr shotRung,
) string {
	return fmt.Sprintf("#%d %dp", sr.index+1, sr.rung.Height)
}

// shotDetail describes a shot in a rung for the chart tooltip: the head
// names the shot, the fields give the rung's CRF, prediction and, when
// known, measurement.
func shotDetail(
	i int,
	s ladder.Shot,
	iv media.Interval,
	a ladder.ShotAllocation,
) svg.Detail {
	origin := "predicted"
	if s.Measured {
		origin = "measured"
	}

	fields := []svg.Field{
		{Label: "CRF", Value: fmt.Sprintf("%.1f", a.CRF)},
		{Label: "VMAF", Value: fmt.Sprintf("%.1f", a.PredictedVMAF)},
	}

	if a.Height > 0 {
		fields = append([]svg.Field{{Label: "at", Value: fmt.Sprintf("%dp", a.Height)}}, fields...)
	}

	if m := a.Measured; m != nil {
		text := bitrateLabel(float64(m.Bitrate))
		if m.ScoredFrames > 0 {
			text += fmt.Sprintf(", VMAF %.1f", m.VMAF)
		}

		fields = append(fields, svg.Field{Label: "measured", Value: text})
	}

	return svg.Detail{
		Title:  fmt.Sprintf("shot %d · %s – %s · %s", i+1, clock(iv.Start), clock(iv.End), origin),
		Fields: fields,
	}
}

// shotLadderTable lists every shot with its complexity and, per rung, its
// predicted bitrate (first, so that columns sort by it), CRF and VMAF.
func shotLadderTable(
	r *ladder.Result,
	rungs []shotRung,
) *table {
	t := &table{Head: []string{"#", "start", "end", "model", "source bitrate", "TI", "cost"}, LinkCol: 1}
	for _, sr := range rungs {
		t.Head = append(t.Head, rungName(sr))
	}

	for i, s := range r.Shots {
		iv := r.ShotInterval(s)
		origin, source, ti := "predicted", "–", "–"

		if s.Measured {
			origin = "measured"
		}

		if s.SourceBitrate > 0 {
			source = bitrateLabel(float64(s.SourceBitrate))
		}

		if s.TI > 0 {
			ti = fmt.Sprintf("%.1f", s.TI)
		}

		cost := 0.0
		cells := make([]string, len(rungs))

		for k, sr := range rungs {
			a := sr.rung.PerShot.Shots[i]
			cost += float64(a.PredictedBitrate) / sr.pooled
			cells[k] = fmt.Sprintf("%s · CRF %.1f · VMAF %.1f", bitrateLabel(float64(a.PredictedBitrate)), a.CRF, a.PredictedVMAF)
			if a.Height > 0 && a.Height != sr.rung.Height {
				cells[k] += fmt.Sprintf(" · %dp", a.Height)
			}
		}

		t.Spans = append(t.Spans, span{iv.Start.Seconds(), iv.End.Seconds()})
		t.Rows = append(t.Rows, append([]string{
			strconv.Itoa(i + 1), clock(iv.Start), clock(iv.End), origin, source, ti, fmt.Sprintf("%.2f", cost/float64(len(rungs))),
		}, cells...))
	}

	return t
}
