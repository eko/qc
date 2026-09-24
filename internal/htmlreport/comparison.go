package htmlreport

import (
	"fmt"
	"html/template"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/quality"
)

// RenderComparison writes the HTML report of a quality comparison.
func RenderComparison(
	w io.Writer,
	c *analysis.Comparison,
) error {
	p := comparisonPage(c)
	p.Kind = "Quality comparison"
	p.summarize(comparisonCards(c.VMAF), comparisonFindings(c.VMAF))

	return render(w, p)
}

func comparisonPage(
	c *analysis.Comparison,
) page {
	v := c.VMAF
	sampled := v.Mode == quality.ModeSampled

	precision := "exact"
	if sampled {
		precision = fmt.Sprintf("± %.2f (%.0f%% CI)", v.HalfWidth, v.Confidence*100)
	}

	p := page{
		Title:    fmt.Sprintf("VMAF %.2f", v.Mean),
		Subtitle: filepath.Base(c.Distorted.Info.Path) + " vs " + filepath.Base(c.Reference.Info.Path),
		Sections: []section{{
			Title:    "Quality",
			Subtitle: fmt.Sprintf("model %s at %d×%d", v.Model.Name, v.Model.Width, v.Model.Height),
			Topic:    findings.TopicQuality,
			Stats: []stat{
				{"Mean", fmt.Sprintf("%.2f", v.Mean)},
				{"Precision", precision},
				{"Frames scored", fmt.Sprintf("%d / %d", v.FramesScored, v.FramesTotal)},
				{"Worst scored", fmt.Sprintf("%.1f", v.Scored.Min)},
				{"Time", v.Elapsed.Std().Round(10 * time.Millisecond).String()},
			},
			Charts: []template.HTML{svg.Chart{
				Width: chartWidth, Height: vmafChartHeight, Series: scoreSeries(v), MaxPoints: maxScorePoints,
				YMin: floor10(v.Scored.Min), YMax: 100, X: svg.UnitTime, Y: svg.UnitNumber,
			}.HTML()},
			Notes: nonEmpty(v.Fallback),
		}},
	}

	if b := v.Sample; b != nil {
		head := &p.Sections[0]
		head.Stats = slices.Insert(head.Stats, 2, stat{"Budget", b.Summary()})
		head.Notes = append(head.Notes, nonEmpty(b.Clamped)...)
	}

	if len(v.Devices) > 0 {
		p.Sections = append(p.Sections, deviceSection(v))
	}

	if len(v.Metrics) > 0 {
		p.Sections = append(p.Sections, metricSection(v))
	}

	if v.Banding != nil {
		p.Sections = append(p.Sections, bandingSection(v))
	}

	return p
}

// maxBandingRows bounds the banded segments listed in the HTML report.
const maxBandingRows = 20

// deviceSection tables the VMAF of each viewing device.
func deviceSection(
	v *quality.Result,
) section {
	t := &table{Head: []string{"device", "model", "evaluated at", "VMAF", "interval", "worst 5%"}}

	for _, d := range v.Devices {
		t.Rows = append(t.Rows, []string{
			d.Device, d.Model.Name, fmt.Sprintf("%d×%d", d.Model.Width, d.Model.Height),
			fmt.Sprintf("%.2f", d.Mean), intervalLabel(v, d.Estimate, quality.DescribeSeries("")), fmt.Sprintf("%.2f", d.Scored.P5),
		})
	}

	return section{
		Title:    "VMAF per device",
		Subtitle: "one VMAF v1 model per viewing condition, scored on the same frames",
		Table:    t,
	}
}

// metricSection tables the metrics measured next to VMAF.
func metricSection(
	v *quality.Result,
) section {
	t := &table{Head: []string{"metric", "mean", "interval", "worst 5%", "min", "max", "unit"}}

	for _, m := range v.Metrics {
		info := quality.DescribeSeries(m.Name)
		t.Rows = append(t.Rows, []string{
			info.Label, info.Format(m.Mean), intervalLabel(v, m.Estimate, info), info.Format(info.Worst(m.Scored)),
			info.Format(m.Scored.Min), info.Format(m.Scored.Max), info.Unit,
		})
	}

	subtitle := "every frame"
	if v.Mode == quality.ModeSampled {
		subtitle = fmt.Sprintf("%.0f%% confidence intervals from the clips VMAF sampled", v.Confidence*100)
	}

	return section{
		Title:    "Metrics",
		Subtitle: subtitle,
		Table:    t,
		Notes: []string{"PSNR YUV weights the planes 14:1:1 (AV2 CTC). XPSNR pools frames like ffmpeg (square-mean-root). " +
			"CAMBI uses the VMAF v1 options; lower is better, and its worst 5% is the 95th percentile."},
	}
}

// bandingSection charts CAMBI over the scored frames and lists the segments
// above the visibility threshold.
func bandingSection(
	v *quality.Result,
) section {
	b := v.Banding

	var (
		xs, ys []float64
		frames []int
	)

	for _, f := range v.Frames {
		if cambi, ok := f.Metrics[quality.SeriesCAMBI]; ok {
			xs, ys, frames = append(xs, f.PTS.Seconds()), append(ys, cambi), append(frames, f.Index)
		}
	}

	threshold := [][2]float64{{0, b.Threshold}}
	if len(xs) > 0 {
		threshold = [][2]float64{{xs[0], b.Threshold}, {xs[len(xs)-1], b.Threshold}}
	}

	bands := make([]svg.Band, 0, len(b.Segments))
	for _, s := range b.Segments {
		bands = append(bands, svg.Band{From: s.Start.Seconds(), To: s.End.Seconds(), Color: amber, Label: "banding"})
	}

	s := section{
		Title:    "Banding",
		Subtitle: fmt.Sprintf("CAMBI per scored frame; above %.0f banding is visible", b.Threshold),
		Topic:    findings.TopicBanding,
		Stats: []stat{
			{"Banded frames", fmt.Sprintf("%d / %d scored", b.BandedFrames, len(xs))},
			{"Segments", strconv.Itoa(len(b.Segments))},
		},
		Charts: []template.HTML{svg.Chart{
			Width: chartWidth, Height: lumaChartHeight, MaxPoints: maxScorePoints,
			Series: []svg.Series{
				{
					Name: "CAMBI", Color: blue, Samples: &svg.Samples{X: xs, Y: ys, Frames: frames},
					Markers: v.Mode == quality.ModeSampled, Digits: 2,
				},
				{Name: "visible", Color: amber, Points: threshold, NoTip: true},
			},
			YMin: 0, YMax: max(2*b.Threshold, slices.Max(append(ys, 0))), X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
		}.HTML()},
	}

	if len(b.Segments) > 0 {
		s.Table = &table{Head: []string{"start", "end", "banded frames", "mean CAMBI", "peak CAMBI"}}

		for _, seg := range b.Segments[:min(len(b.Segments), maxBandingRows)] {
			s.Table.Spans = append(s.Table.Spans, span{seg.Start.Seconds(), seg.End.Seconds()})
			s.Table.Rows = append(s.Table.Rows, []string{
				clock(seg.Start), clock(seg.End), strconv.Itoa(seg.Frames), fmt.Sprintf("%.2f", seg.Mean), fmt.Sprintf("%.2f", seg.Peak),
			})
		}
	}

	return s
}

// intervalLabel is "low – high" in sampled mode, "exact" otherwise.
func intervalLabel(
	v *quality.Result,
	e quality.Estimate,
	info quality.SeriesInfo,
) string {
	if v.Mode == quality.ModeExact {
		return "exact"
	}

	return info.Format(e.Low) + " – " + info.Format(e.High)
}

// scoreSeries charts the scored frames and, when sampled, the mean of each
// stratum as steps.
func scoreSeries(
	v *quality.Result,
) []svg.Series {
	sampled := v.Mode == quality.ModeSampled

	scores := &svg.Samples{X: make([]float64, len(v.Frames)), Y: make([]float64, len(v.Frames)), Frames: make([]int, len(v.Frames))}
	for i, f := range v.Frames {
		scores.X[i], scores.Y[i], scores.Frames[i] = f.PTS.Seconds(), f.Score, f.Index
	}

	series := []svg.Series{{Name: "VMAF", Color: orange, Samples: scores, Markers: sampled, Digits: 2}}

	if sampled {
		strata := make([][2]float64, 0, 2*len(v.Strata))
		for _, s := range v.Strata {
			strata = append(strata, [2]float64{s.Start.Seconds(), s.Mean}, [2]float64{s.End.Seconds(), s.Mean})
		}

		series = append(series, svg.Series{Name: "stratum mean", Color: blue, Points: strata, Bold: true, Step: true, Digits: 2})
	}

	return append(series, metricTips(v)...)
}

// maxTipValues bounds the metric values embedded for tooltips: every metric
// of every frame of a long title scored exactly would weigh megabytes.
const maxTipValues = 600_000

// metricTips are the other metrics of the scored frames, listed in the
// VMAF tooltips without being drawn.
func metricTips(
	v *quality.Result,
) []svg.Series {
	if len(v.Metrics)*len(v.Frames) > maxTipValues {
		return nil
	}

	out := make([]svg.Series, 0, len(v.Metrics))

	for _, m := range v.Metrics {
		var sm svg.Samples

		for _, f := range v.Frames {
			if value, ok := f.Metrics[m.Name]; ok {
				sm.X, sm.Y, sm.Frames = append(sm.X, f.PTS.Seconds()), append(sm.Y, value), append(sm.Frames, f.Index)
			}
		}

		if len(sm.X) > 0 {
			info := quality.DescribeSeries(m.Name)
			out = append(out, svg.Series{Name: strings.TrimSpace(info.Label + " " + info.Unit), Samples: &sm, TipOnly: true, Digits: 2})
		}
	}

	return out
}

func comparisonCards(
	v *quality.Result,
) []card {
	vmafCard := card{Label: "VMAF", Value: fmt.Sprintf("%.2f", v.Mean), Detail: "exact · every frame", Tone: vmafTone(v.Mean)}
	if v.Mode == quality.ModeSampled {
		vmafCard.Detail = fmt.Sprintf("± %.2f · %.0f%% CI", v.HalfWidth, v.Confidence*100)
	}

	cards := []card{vmafCard}

	if worst, ok := worstFrame(v); ok {
		cards = append(cards, card{
			Label: "Worst frame", Value: fmt.Sprintf("%.1f", worst.Score),
			Detail: fmt.Sprintf("frame %d at %s", worst.Index, svg.UnitTime.Format(worst.PTS.Seconds())),
			Tone:   vmafTone(worst.Score),
		})
	}

	share := 0.0
	if v.FramesTotal > 0 {
		share = float64(v.FramesScored) / float64(v.FramesTotal) * 100
	}

	cards = append(cards,
		card{Label: "Frames scored", Value: fmt.Sprintf("%d / %d", v.FramesScored, v.FramesTotal), Detail: fmt.Sprintf("%.1f%% · %s", share, v.Mode)},
		card{Label: "Model", Value: v.Model.Name, Detail: fmt.Sprintf("at %d×%d", v.Model.Width, v.Model.Height)},
	)

	if gpu := v.GPUSummary(); gpu != "" {
		cards = append(cards, card{Label: "GPU", Value: "NVIDIA", Detail: gpu})
	}

	if b := v.Banding; b != nil {
		c := card{Label: "Banding", Value: "None visible", Tone: toneGood, Detail: fmt.Sprintf("CAMBI ≤ %.0f", b.Threshold)}
		if len(b.Segments) > 0 {
			c.Value, c.Tone = plural(len(b.Segments), "segment"), toneWarn
			c.Detail = fmt.Sprintf("%d banded frames", b.BandedFrames)
		}

		cards = append(cards, c)
	}

	return cards
}

// worstFrame is the lowest scored frame.
func worstFrame(
	v *quality.Result,
) (quality.FrameScore, bool) {
	if len(v.Frames) == 0 {
		return quality.FrameScore{}, false
	}

	worst := v.Frames[0]
	for _, f := range v.Frames[1:] {
		if f.Score < worst.Score {
			worst = f
		}
	}

	return worst, true
}
