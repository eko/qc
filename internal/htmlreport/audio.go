package htmlreport

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/media"
)

const (
	// loudnessFloor is the bottom of the loudness and level charts: the
	// absolute gate of BS.1770 is at -70 LUFS, and quieter reads as
	// silence (loudness.Floor) at the bottom edge.
	loudnessFloor = -60
	// phaseChartHeight is the height of the correlation chart.
	phaseChartHeight = 140
	// maxDefectRows bounds the rows of a track's defect table.
	maxDefectRows = 60
)

// Bands of the audio charts.
const (
	silenceBand = blackBand
	clipBand    = "var(--s8)"
	phaseBand   = "var(--s7)"
)

// audioSections are the sections of the audio tracks, one per track.
func audioSections(
	r *analysis.Report,
) []section {
	if r.Audio == nil {
		return nil
	}

	out := make([]section, 0, len(r.Audio.Tracks))
	for i := range r.Audio.Tracks {
		out = append(out, trackSection(&r.Audio.Tracks[i], audioStream(r.Info, r.Audio.Tracks[i].Stream), r.Audio.Target))
	}

	return out
}

// audioStream returns the description of an audio stream index.
func audioStream(
	info *media.Info,
	index int,
) media.AudioStream {
	for _, a := range info.Audio {
		if a.Index == index {
			return a
		}
	}

	return media.AudioStream{Index: index}
}

// trackSection charts a track: its loudness against the target, the
// levels and true peaks of its channels, the correlation of its pairs;
// silences, clipping and phase problems shaded.
func trackSection(
	t *audio.Track,
	stream media.AudioStream,
	target loudness.Target,
) section {
	s := section{
		Title:    fmt.Sprintf("Audio #%d", t.Stream),
		Subtitle: trackSubtitle(t, stream),
		Topic:    findings.AudioTopic(t.Stream),
		Stats:    trackStats(t, target),
		Table:    defectTable(t),
		Notes: []string{"Loudness per ITU-R BS.1770-5 (K-weighted, LFE excluded, surround channels +1.5 dB): momentary over 400 ms, " +
			"short-term over 3 s, every 100 ms; the first windows reach before the start, counted as silence. " +
			"Integrated loudness gated at -70 LUFS and 10 LU below the mean, loudness range per EBU Tech 3342, " +
			"true peak by 4× oversampling (BS.1770 Annex 2). Levels are RMS per 100 ms in dBFS (a full-scale sine reads -3). " +
			"Correlation over 400 ms: +1 same signal, 0 unrelated, -1 opposite polarity (0 while a channel is silent)."},
	}

	if t.Loudness.Integrated <= loudness.Floor {
		s.Notes = []string{"The track is silent: nothing above the absolute gate of -70 LUFS."}

		return s
	}

	bands := audioBands(t)
	s.Charts = []template.HTML{loudnessChart(t, target, bands), levelChart(t, target, bands)}

	if len(t.Defects.Pairs) > 0 {
		s.Charts = append(s.Charts, phaseChart(t, bands))
	}

	return s
}

// trackSubtitle describes the stream.
func trackSubtitle(
	t *audio.Track,
	stream media.AudioStream,
) string {
	parts := []string{strings.TrimSpace(stream.Codec + " " + stream.Profile), t.Layout, fmt.Sprintf("%g kHz", float64(stream.SampleRate)/1000)}

	if stream.Language != "" {
		parts = append(parts, stream.Language)
	}

	if stream.Default {
		parts = append(parts, "default")
	}

	return strings.Join(parts, " · ")
}

// trackStats are the loudness readings, marked against the target.
func trackStats(
	t *audio.Track,
	target loudness.Target,
) []stat {
	l, c := t.Loudness, t.Compliance
	if l.Integrated <= loudness.Floor {
		return []stat{{"Integrated", "silent"}, {"Sample peak", fmt.Sprintf("%.1f dBFS", l.SamplePeak)}}
	}

	stats := []stat{
		{"Integrated", fmt.Sprintf("%.1f LUFS %s", l.Integrated, mark(c.Loudness))},
		{"Target", fmt.Sprintf("%s: %g ±%g", target.Name, target.Integrated, target.Tolerance)},
		{"True peak", fmt.Sprintf("%.1f dBTP %s", l.TruePeak, mark(c.TruePeak))},
		{"Loudness range", fmt.Sprintf("%.1f LU", l.Range)},
		{"Max momentary / short-term", fmt.Sprintf("%.1f / %.1f LUFS", l.MaxMomentary, l.MaxShortTerm)},
	}

	if len(t.Defects.Pairs) > 0 {
		p := t.Defects.Pairs[0]
		stats = append(stats, stat{t.Channel(p.Left) + "/" + t.Channel(p.Right) + " correlation", fmt.Sprintf("%.2f", p.Correlation)})
	}

	return stats
}

// mark is ✓ for a passed check, ✗ otherwise.
func mark(
	ok bool,
) string {
	if ok {
		return "✓"
	}

	return "✗"
}

// audioBands shades the silences, the clipped segments and the
// out-of-phase segments of a track.
func audioBands(
	t *audio.Track,
) []svg.Band {
	var bands []svg.Band

	add := func(list []media.Interval, colour, label string) {
		for _, iv := range list {
			bands = append(bands, svg.Band{From: (t.Start + iv.Start).Seconds(), To: (t.Start + iv.End).Seconds(), Color: colour, Label: label})
		}
	}

	d := t.Defects
	add(d.Silence, silenceBand, "silence")

	for i, c := range d.Channels {
		add(c.Silence, silenceBand, t.Channel(i)+" silent")
		add(c.Clipping, clipBand, t.Channel(i)+" clipping")
	}

	for _, p := range d.Pairs {
		add(p.OutOfPhase, phaseBand, t.Channel(p.Left)+"/"+t.Channel(p.Right)+" out of phase")
	}

	return bands
}

// stepTimes are the times of the 100 ms steps of a track's series: each
// value at the end of its step, on the container's timeline.
func stepTimes(
	t *audio.Track,
	n int,
) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = t.Start.Seconds() + float64(i+1)*loudness.Step
	}

	return out
}

// referenceLine is a horizontal line across the track.
func referenceLine(
	x []float64,
	y float64,
) [][2]float64 {
	if len(x) == 0 {
		return nil
	}

	return [][2]float64{{x[0], y}, {x[len(x)-1], y}}
}

// loudnessChart plots the short-term and momentary loudness with the
// target and its tolerance.
func loudnessChart(
	t *audio.Track,
	target loudness.Target,
	bands []svg.Band,
) template.HTML {
	series := t.Loudness.Series
	x := stepTimes(t, len(series.ShortTerm))

	return svg.Chart{
		Width: chartWidth, Height: timeChartHeight, MaxPoints: maxFramePoints,
		Series: []svg.Series{
			// The momentary loudness first: the short-term one is drawn
			// over it.
			{Name: "momentary (LUFS)", Color: blue, Samples: &svg.Samples{X: x, Y: series.Momentary}, Digits: 1},
			{Name: "short-term (LUFS)", Color: orange, Samples: &svg.Samples{X: x, Y: series.ShortTerm}, Bold: true, Digits: 1},
			{Name: fmt.Sprintf("target %g", target.Integrated), Color: green, Points: referenceLine(x, target.Integrated), NoTip: true},
			{Name: fmt.Sprintf("±%g LU", target.Tolerance), Key: "tolerance", Color: aqua, Points: referenceLine(x, target.Integrated+target.Tolerance), NoTip: true},
			{Name: "-tolerance", Key: "tolerance", Color: aqua, Points: referenceLine(x, target.Integrated-target.Tolerance), NoTip: true, NoLegend: true},
			{Name: "integrated", Color: foreground, Points: referenceLine(x, t.Loudness.Integrated), NoTip: true},
		},
		YMin: loudnessFloor, X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
	}.HTML()
}

// levelChart plots the RMS level of every channel and marks the steps
// whose true peak exceeds the target's ceiling.
func levelChart(
	t *audio.Track,
	target loudness.Target,
	bands []svg.Band,
) template.HTML {
	tp := t.Loudness.Series.TruePeak
	x := stepTimes(t, len(tp))

	var series []svg.Series

	for i, levels := range t.Defects.Levels {
		series = append(series, svg.Series{
			Name: t.Channel(i) + " (dBFS)", Color: palette[i%len(palette)],
			Samples: &svg.Samples{X: x[:min(len(x), len(levels))], Y: levels}, Digits: 1,
		})
	}

	var over svg.Samples

	for i, v := range tp {
		if v > target.MaxTruePeak {
			over.X, over.Y = append(over.X, x[i]), append(over.Y, v)
		}
	}

	series = append(series,
		svg.Series{Name: "true peak (dBTP)", Samples: &svg.Samples{X: x, Y: tp}, TipOnly: true, Digits: 1},
		svg.Series{Name: "true peak over ceiling", Color: red, Samples: &over, Markers: true, Digits: 1},
		svg.Series{Name: fmt.Sprintf("ceiling %g dBTP", target.MaxTruePeak), Color: red, Points: referenceLine(x, target.MaxTruePeak), NoTip: true},
	)

	return svg.Chart{
		Width: chartWidth, Height: timeChartHeight, MaxPoints: maxFramePoints,
		Series: series, YMin: loudnessFloor, YMax: 0, X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
	}.HTML()
}

// phaseChart plots the correlation of every pair of the track.
func phaseChart(
	t *audio.Track,
	bands []svg.Band,
) template.HTML {
	var series []svg.Series

	for i, p := range t.Defects.Pairs {
		x := stepTimes(t, len(p.Series))
		series = append(series, svg.Series{
			Name: t.Channel(p.Left) + "/" + t.Channel(p.Right) + " correlation", Color: palette[(i+2)%len(palette)],
			Samples: &svg.Samples{X: x, Y: p.Series}, Digits: 2,
		})
	}

	return svg.Chart{
		Width: chartWidth, Height: phaseChartHeight, MaxPoints: maxFramePoints,
		Series: series, YMin: -1, YMax: 1, X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
	}.HTML()
}

// defectTable lists the silences, clipped and out-of-phase segments of a
// track, linked to the charts.
func defectTable(
	t *audio.Track,
) *table {
	tb := &table{Head: []string{"defect", "start", "end", "length"}, LinkCol: 1}

	add := func(what string, list []media.Interval) {
		for _, iv := range list {
			if len(tb.Rows) == maxDefectRows {
				return
			}

			from, to := t.Start+iv.Start, t.Start+iv.End
			tb.Spans = append(tb.Spans, span{from.Seconds(), to.Seconds()})
			tb.Rows = append(tb.Rows, []string{what, clock(from), clock(to), fmt.Sprintf("%.2fs", iv.Length().Seconds())})
		}
	}

	d := t.Defects
	add("silence", d.Silence)

	for i, c := range d.Channels {
		add(t.Channel(i)+" silent", c.Silence)
		add(t.Channel(i)+" clipping", c.Clipping)
	}

	for _, p := range d.Pairs {
		add(t.Channel(p.Left)+"/"+t.Channel(p.Right)+" out of phase", p.OutOfPhase)
	}

	if len(tb.Rows) == 0 {
		return nil
	}

	return tb
}

// audioCard sums the loudness of the default track up in a header card.
func audioCard(
	r *analysis.Report,
) (card, bool) {
	if r.Audio == nil || len(r.Audio.Tracks) == 0 {
		return card{}, false
	}

	t := r.Audio.Tracks[0]
	if i := r.Info.DefaultAudio(); i >= 0 {
		for _, track := range r.Audio.Tracks {
			if track.Stream == r.Info.Audio[i].Index {
				t = track
			}
		}
	}

	if t.Loudness.Integrated <= loudness.Floor {
		return card{Label: "Loudness", Value: "silent", Detail: fmt.Sprintf("audio #%d", t.Stream), Tone: toneWarn}, true
	}

	c := card{
		Label:  "Loudness",
		Value:  fmt.Sprintf("%.1f LUFS", t.Loudness.Integrated),
		Detail: fmt.Sprintf("TP %.1f dBTP · %s target %s", t.Loudness.TruePeak, r.Audio.Target.Name, mark(t.Compliance.OK())),
		Tone:   toneGood,
	}

	if !t.Compliance.OK() {
		c.Tone = toneWarn
	}

	return c, true
}
