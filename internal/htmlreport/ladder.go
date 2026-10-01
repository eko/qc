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
	"time"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

// RenderLadder writes the HTML report of a ladder.
func RenderLadder(
	w io.Writer,
	r *ladder.Result,
) error {
	p := ladderPage(r)
	p.Kind = "Encoding ladder"
	p.summarize(ladderCards(r), ladderFindings(r))

	return render(w, p)
}

func ladderPage(
	r *ladder.Result,
) page {
	own := ladderArea(r.Codec.Name)

	p := page{
		Title: fmt.Sprintf("%s ladder · %s", r.Codec.Name, filepath.Base(r.Source.Info.Path)),
		Subtitle: fmt.Sprintf("%d rungs · digest of %d segments (%.1f%% of the title%s) · %d probe encodes%s · %s",
			len(r.Rungs), len(r.Digest.Segments), r.Digest.Share*100, samplingNote(r.Digest.Sampling), len(r.Probes), probingNote(r.Probing), r.Elapsed.Std().Round(time.Second)),
		Sections: slices.Concat([]section{{
			Title:    "Rate / quality",
			Subtitle: "probe encodes per resolution, their upper envelope and the selected rungs",
			Charts:   []template.HTML{ladderChart(r)},
			Table:    rungTable(r.Rungs),
		}}, ladderExtras(r), []section{{
			Title:     "Encoding commands",
			Area:      areaEncoding,
			Commands:  rungCommands(r.Rungs),
			Collapsed: true,
		}}),
	}

	for i := range p.Sections {
		if p.Sections[i].Area.Title == "" {
			p.Sections[i].Area = own
		}
	}

	return p
}

// ladderExtras are the per-shot and film grain sections, when requested.
func ladderExtras(
	r *ladder.Result,
) []section {
	var out []section

	if t := perShotTable(r.Rungs); t != nil {
		out = append(out, section{
			Title:    "Per-shot",
			Subtitle: perShotSubtitle(r.ShotProbing),
			Table:    t,
		})
	}

	if s, ok := shotLadderSection(r); ok {
		out = append(out, s)
	}

	if g := r.Grain; g != nil {
		out = append(out, section{
			Title:    "Film grain",
			Subtitle: grainSummary(g),
			Table:    grainTable(r.Rungs),
		})
	}

	if len(r.Renditions) > 0 {
		out = append(out, section{
			Title:    "Renditions",
			Subtitle: "the rungs encoded on the whole title, against what the ladder predicted on the digest",
			Table:    renditionTable(r),
		})
	}

	return out
}

// renditionTable lists the renditions with their predicted and measured
// bitrate and quality.
func renditionTable(
	r *ladder.Result,
) *table {
	t := &table{Head: []string{"file", "resolution", "predicted bitrate", "bitrate", "predicted VMAF", "VMAF"}}

	for _, rd := range r.Renditions {
		vmaf, bitrate := r.Prediction(rd)

		checked := "–"
		if c := rd.Checked; c != nil {
			checked = c.VMAFLabel()
		}

		t.Rows = append(t.Rows, []string{
			filepath.Base(rd.Path), fmt.Sprintf("%d×%d", rd.Width, rd.Height),
			bitrateLabel(bitrate), bitrateLabel(float64(rd.Bitrate)), fmt.Sprintf("%.1f", vmaf), checked,
		})
	}

	return t
}

// perShotTable compares per-shot rungs with their per-title versions.
func perShotTable(
	rungs []ladder.Rung,
) *table {
	t := &table{Head: []string{"#", "resolution", "chunks", "CRF range", "per-title", "per-shot", "saved at equal VMAF"}}

	for i, rung := range rungs {
		ps := rung.PerShot
		if ps == nil {
			continue
		}

		low, high := ps.Chunks[0].CRF, ps.Chunks[0].CRF
		for _, c := range ps.Chunks {
			low, high = math.Min(low, c.CRF), math.Max(high, c.CRF)
		}

		title, shots, saved := "–", measuredLabel(nil), "–"
		if m := rung.Measured; m != nil {
			title = measuredLabel(m)
		}

		if ps.Measured != nil {
			shots = measuredLabel(ps.Measured)
		}

		if rung.Measured != nil && ps.Measured != nil {
			saved = fmt.Sprintf("%.1f%%", ps.Gain*100)
		}

		resolution := fmt.Sprintf("%d×%d", rung.Width, rung.Height)
		if ps.Height > 0 && ps.Height != rung.Height {
			resolution += fmt.Sprintf(" (declares %d×%d)", ps.Width, ps.Height)
		}

		t.Rows = append(t.Rows, []string{
			strconv.Itoa(i + 1), resolution, strconv.Itoa(len(ps.Chunks)),
			fmt.Sprintf("%.1f–%.1f", low, high), title, shots, saved,
		})
	}

	if len(t.Rows) == 0 {
		return nil
	}

	return t
}

// perShotSubtitle describes the per-shot rungs and what their probes cost.
func perShotSubtitle(
	probing *ladder.ShotProbing,
) string {
	text := "one CRF per shot at equal rate-quality slope, same pooled quality as the rung; both measured on the digest"
	if probing == nil {
		return text
	}

	if probing.Resolution {
		text = "one resolution and CRF per shot at equal rate-quality slope, same pooled quality as the rung; both measured on the digest. " +
			"Renditions change resolution at shot boundaries and declare their largest one: players must accept resolution changes within a rendition"
	}

	text += fmt.Sprintf(" · %d exact probes of the digest", probing.Probes)
	if probing.Extra > 0 {
		text += fmt.Sprintf(" (%d to reach the neighbouring resolutions)", probing.Extra)
	}

	return text
}

// measuredLabel is a measured bitrate and VMAF.
func measuredLabel(
	m *ladder.Measurement,
) string {
	if m == nil {
		return "–"
	}

	return fmt.Sprintf("%s · VMAF %.1f", bitrateLabel(float64(m.Bitrate)), m.VMAF)
}

// grainSummary describes the film grain decision.
func grainSummary(
	g *ladder.GrainReport,
) string {
	text := fmt.Sprintf("source noise σ %.2f (8-bit luma, flattest blocks)", g.Source.Sigma)

	switch {
	case g.Level > 0:
		text += fmt.Sprintf(" · synthesis level %d; fidelity scored against SVT-AV1's denoised reference", g.Level)
	case g.Detected:
		text += " · grain detected, synthesis off"
	default:
		text += " · no grain detected, synthesis off"
	}

	var trials strings.Builder
	for _, tr := range g.Trials {
		fmt.Fprintf(&trials, " · level %d gives back %.0f%%", tr.Level, tr.Ratio*100)
	}

	return text + trials.String()
}

// grainTable compares every rung's synthesised grain with the source's.
func grainTable(
	rungs []ladder.Rung,
) *table {
	t := &table{Head: []string{"#", "resolution", "source σ", "output σ", "ratio", "grain"}}

	for i, rung := range rungs {
		g := rung.Grain
		if g == nil {
			continue
		}

		verdict := "matches"
		if !g.OK {
			verdict = "off"
		}

		t.Rows = append(t.Rows, []string{
			strconv.Itoa(i + 1), fmt.Sprintf("%d×%d", rung.Width, rung.Height),
			fmt.Sprintf("%.2f", g.Source.Sigma), fmt.Sprintf("%.2f", g.Output.Sigma), fmt.Sprintf("%.0f%%", g.Ratio*100), verdict,
		})
	}

	if len(t.Rows) == 0 {
		return nil
	}

	return t
}

// ladderChart plots probe curves per resolution, the envelope and the rungs.
func ladderChart(
	r *ladder.Result,
) template.HTML {
	byHeight := map[int][]ladder.Probe{}
	lowest := 100.0

	for _, pr := range r.Probes {
		byHeight[pr.Height] = append(byHeight[pr.Height], pr)
		lowest = min(lowest, pr.VMAF)
	}

	heights := slices.Sorted(maps.Keys(byHeight))
	slices.Reverse(heights)

	var curves, markers []svg.Series

	for i, h := range heights {
		probes := byHeight[h]
		slices.SortFunc(probes, func(a, b ladder.Probe) int { return cmp.Compare(a.Bitrate, b.Bitrate) })

		name, color := fmt.Sprintf("%dp", h), palette[i%len(palette)]
		samples := &svg.Samples{}

		for _, pr := range probes {
			samples.X, samples.Y = append(samples.X, float64(pr.Bitrate)), append(samples.Y, pr.VMAF)
			samples.Details = append(samples.Details, probeDetail(pr))
		}

		curves = append(curves, svg.Series{Name: name, Color: color, Points: samples.Points(), NoTip: true})
		markers = append(markers, svg.Series{Name: name + " probe", Key: name, Color: color, Samples: samples, Markers: true, NoLegend: true, Digits: 2})
	}

	hull := make([][2]float64, len(r.Hull))
	for i, h := range r.Hull {
		hull[i] = [2]float64{float64(h.Bitrate), h.VMAF}
	}

	rungs, measured := rungSeries(r.Rungs)

	// Probe markers go over the envelope so every measurement stays visible.
	series := slices.Concat(curves, []svg.Series{{Name: "envelope", Color: foreground, Points: hull, Bold: true, NoTip: true}}, markers, []svg.Series{rungs})
	if measured.Samples != nil {
		series = append(series, measured)
	}

	return svg.Chart{
		Title: "Rate–quality: probes, envelope and rungs", Width: chartWidth, Height: ladderChartHeight, Series: series, LogX: true, YMin: floor10(lowest), YMax: 100,
		X: svg.UnitBitrate, Y: svg.UnitNumber, Nearest: true,
	}.HTML()
}

// probeDetail describes a probe encode in the chart tooltip.
func probeDetail(
	pr ladder.Probe,
) svg.Detail {
	vmafValue := fmt.Sprintf("%.2f", pr.VMAF)
	if pr.HalfWidth > 0 {
		vmafValue += fmt.Sprintf(" ± %.2f", pr.HalfWidth)
	}

	title := fmt.Sprintf("Probe · %d×%d", pr.Width, pr.Height)
	if pr.Extra {
		title += " (extra)"
	}

	return svg.Detail{Title: title, Fields: []svg.Field{
		{Label: "bitrate", Value: bitrateLabel(float64(pr.Bitrate))}, {Label: "VMAF", Value: vmafValue}, {Label: "CRF", Value: fmt.Sprintf("%.1f", pr.CRF)},
	}}
}

// rungSeries are the rungs at their predicted quality and, when verified,
// at their measured quality.
func rungSeries(
	rungs []ladder.Rung,
) (predicted, measured svg.Series) {
	predicted = svg.Series{Name: "rung", Color: foreground, Markers: true, Samples: &svg.Samples{}, Digits: 2}
	measured = svg.Series{Name: "measured", Color: palette[7], Markers: true, Digits: 2}

	for i, rung := range rungs {
		title := fmt.Sprintf("Rung %d · %d×%d", i+1, rung.Width, rung.Height)
		fields := []svg.Field{
			{Label: "bitrate", Value: bitrateLabel(float64(rung.Bitrate))}, {Label: "predicted VMAF", Value: predictedLabel(rung)},
			{Label: "CRF", Value: fmt.Sprintf("%.1f", rung.CRF)}, {Label: "maxrate", Value: bitrateLabel(float64(rung.MaxRate))},
		}

		if m := rung.Measured; m != nil {
			fields = append(fields, svg.Field{Label: "measured", Value: fmt.Sprintf("%s · VMAF %.2f ± %.2f", bitrateLabel(float64(m.Bitrate)), m.VMAF, m.HalfWidth)})

			if measured.Samples == nil {
				measured.Samples = &svg.Samples{}
			}

			ms := measured.Samples
			ms.X, ms.Y = append(ms.X, float64(m.Bitrate)), append(ms.Y, m.VMAF)
			ms.Details = append(ms.Details, svg.Detail{Title: title + " · measured", Fields: fields})
		}

		p := predicted.Samples
		p.X, p.Y = append(p.X, float64(rung.Bitrate)), append(p.Y, rung.PredictedVMAF)
		p.Details = append(p.Details, svg.Detail{Title: title, Fields: fields})
	}

	return predicted, measured
}

func rungTable(
	rungs []ladder.Rung,
) *table {
	series, devices := rungExtras(rungs)

	t := &table{Head: []string{"#", "resolution", "bitrate", "CRF", "maxrate", "predicted VMAF", "measured VMAF", "measured bitrate"}}
	for _, name := range series {
		t.Head = append(t.Head, quality.DescribeSeries(name).ShortLabel())
	}

	for _, d := range devices {
		t.Head = append(t.Head, d+" VMAF")
	}

	for i, rung := range rungs {
		measured, measuredRate := "–", "–"
		if m := rung.Measured; m != nil {
			measured = fmt.Sprintf("%.1f ± %.1f", m.VMAF, m.HalfWidth)
			measuredRate = bitrateLabel(float64(m.Bitrate))
		}

		row := []string{
			strconv.Itoa(i + 1), fmt.Sprintf("%d×%d", rung.Width, rung.Height), bitrateLabel(float64(rung.Bitrate)),
			fmt.Sprintf("%.1f", rung.CRF), bitrateLabel(float64(rung.MaxRate)), predictedLabel(rung),
			measured, measuredRate,
		}

		t.Rows = append(t.Rows, append(row, rungExtraCells(rung.Measured, series, devices)...))
	}

	return t
}

// rungExtras returns the headline series (in report order) and the devices
// (sorted) measured on the verified rungs.
func rungExtras(
	rungs []ladder.Rung,
) (series, devices []string) {
	measured, seenDevice := map[string]bool{}, map[string]bool{}

	for _, r := range rungs {
		if r.Measured == nil {
			continue
		}

		for name := range r.Measured.Metrics {
			measured[name] = true
		}

		for d := range r.Measured.Devices {
			if !seenDevice[d] {
				seenDevice[d] = true
				devices = append(devices, d)
			}
		}
	}

	for _, name := range quality.HeadlineSeries() {
		if measured[name] {
			series = append(series, name)
		}
	}

	slices.Sort(devices)

	return series, devices
}

// rungExtraCells are the metric and device cells of a rung: "–" when it
// was not measured.
func rungExtraCells(
	m *ladder.Measurement,
	series, devices []string,
) []string {
	cells := make([]string, 0, len(series)+len(devices))

	for _, name := range series {
		v, ok := 0.0, false
		if m != nil {
			v, ok = m.Metrics[name]
		}

		cells = append(cells, cellOr(ok, quality.DescribeSeries(name).Format(v)))
	}

	for _, d := range devices {
		v, ok := 0.0, false
		if m != nil {
			v, ok = m.Devices[d]
		}

		cells = append(cells, cellOr(ok, fmt.Sprintf("%.1f", v)))
	}

	return cells
}

// cellOr is text when ok, a dash otherwise.
func cellOr(
	ok bool,
	text string,
) string {
	if !ok {
		return "–"
	}

	return text
}

// predictedLabel is a rung's predicted VMAF, with its half-width when the
// curve model gives one (adaptive probing).
func predictedLabel(
	rung ladder.Rung,
) string {
	if rung.PredictionError > 0 {
		return fmt.Sprintf("%.1f ± %.1f", rung.PredictedVMAF, rung.PredictionError)
	}

	return fmt.Sprintf("%.1f", rung.PredictedVMAF)
}

// samplingNote describes how the segments of the digest were placed in the
// page subtitle; a title used whole has no sampling.
func samplingNote(
	s ladder.DigestSampling,
) string {
	switch s {
	case ladder.DigestBalanced:
		return ", balanced on SI/TI"
	case ladder.DigestTop:
		return ", the most complex scenes"
	case ladder.DigestUniform:
		return ", evenly spaced"
	}

	return ""
}

// probingNote describes adaptive probing in the page subtitle.
func probingNote(
	p ladder.ProbingReport,
) string {
	var parts []string

	if p.Mode == ladder.ProbingAdaptive {
		state := "budget reached"
		if p.Converged {
			state = "converged"
		}

		parts = append(parts, fmt.Sprintf("adaptive, %d rounds, %s", p.Rounds, state))
	}

	if p.Challengers > 0 {
		parts = append(parts, fmt.Sprintf("%d challenger probes", p.Challengers))
	}

	if len(parts) == 0 {
		return ""
	}

	return " (" + strings.Join(parts, ", ") + ")"
}

func rungCommands(
	rungs []ladder.Rung,
) []string {
	commands := make([]string, len(rungs))
	for i, rung := range rungs {
		commands[i] = rung.Command
	}

	for _, rung := range rungs {
		if rung.PerShot != nil {
			commands = append(commands, rung.PerShot.Command)
		}
	}

	return commands
}

func ladderCards(
	r *ladder.Result,
) []card {
	cards := []card{{Label: "Rungs", Value: strconv.Itoa(len(r.Rungs)), Detail: codecLabel(r.Codec) + presetLabel(r.Preset)}}

	if n := len(r.Rungs); n > 0 {
		top, bottom := r.Rungs[0], r.Rungs[n-1]
		cards = append(cards,
			card{Label: "Top rung", Value: bitrateLabel(float64(top.Bitrate)), Detail: rungLabel(top), Tone: vmafTone(top.PredictedVMAF), Meter: vmafMeter(top.PredictedVMAF)},
			card{Label: "Bottom rung", Value: bitrateLabel(float64(bottom.Bitrate)), Detail: rungLabel(bottom)},
		)
	}

	probing := cmp.Or(string(r.Probing.Mode), string(ladder.ProbingFixed))
	if r.Probing.Mode == ladder.ProbingAdaptive {
		probing += fmt.Sprintf(", %d rounds", r.Probing.Rounds)
	}

	return append(cards,
		card{Label: "Probe encodes", Value: strconv.Itoa(len(r.Probes)), Detail: probing},
		card{Label: "Digest", Value: fmt.Sprintf("%.1f%%", r.Digest.Share*100), Detail: plural(len(r.Digest.Segments), "segment") + " of the title"},
	)
}

// codecLabel names the codec, and the encoder when it is a GPU one.
func codecLabel(
	c encode.Codec,
) string {
	if c.Hardware == encode.HardwareCPU {
		return c.Name
	}

	return c.Name + " · " + c.Encoder
}

func presetLabel(
	preset string,
) string {
	if preset == "" {
		return ""
	}

	return " · preset " + preset
}

func rungLabel(
	r ladder.Rung,
) string {
	return fmt.Sprintf("%d×%d · VMAF %.1f · CRF %.1f", r.Width, r.Height, r.PredictedVMAF, r.CRF)
}
