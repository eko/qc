package htmlreport

import (
	"fmt"
	"html/template"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/media"
)

// RenderAnalysis writes the HTML report of a technical analysis.
func RenderAnalysis(
	w io.Writer,
	r *analysis.Report,
) error {
	p := analyzePage(r)
	p.Kind = "Technical analysis"
	p.summarize(analysisCards(r), analysisFindings(r))

	return render(w, p)
}

func analyzePage(
	r *analysis.Report,
) page {
	v, _ := r.Info.PrimaryVideo()

	p := page{
		Title: filepath.Base(r.Info.Path),
		Subtitle: fmt.Sprintf("%s %s · %d×%d · %.3f fps · %s · %s",
			v.Codec, v.Profile, v.Width, v.Height, v.AvgFrameRate.Float(), v.HDR.DynamicRange, clock(r.Info.Duration)),
	}

	bands := segmentBands(r.Video)
	p.Sections = append(p.Sections, bitrateSection(r.Bitstream, r.Frames, bands))

	if r.Video != nil && r.Frames != nil {
		p.Sections = append(p.Sections, complexitySection(r.Video, r.Frames, bands))
	}

	if s, ok := lightSection(r, bands); ok {
		p.Sections = append(p.Sections, s)
	}

	if s, ok := motionSection(r, bands); ok {
		p.Sections = append(p.Sections, s)
	}

	p.Sections = append(p.Sections, audioSections(r)...)

	return p
}

// segmentBands shades black and frozen segments on time charts.
func segmentBands(
	v *analysis.VideoReport,
) []svg.Band {
	if v == nil {
		return nil
	}

	var bands []svg.Band

	for _, s := range v.Black.Segments {
		bands = append(bands, svg.Band{From: s.Start.Seconds(), To: s.End.Seconds(), Color: blackBand, Label: "black"})
	}

	for _, s := range v.Freeze.Segments {
		bands = append(bands, svg.Band{From: s.Start.Seconds(), To: s.End.Seconds(), Color: frozenBand, Label: "frozen"})
	}

	return bands
}

// bitrateSection charts the bitrate and, when frames were decoded, the
// size of every frame with its keyframes.
func bitrateSection(
	bs *bitstream.Report,
	f *analysis.FrameSeries,
	bands []svg.Band,
) section {
	bitrate := make([][2]float64, len(bs.Bitrate))
	for i, b := range bs.Bitrate {
		bitrate[i] = [2]float64{b.Start.Seconds(), float64(b.Bitrate)}
	}

	charts := []template.HTML{svg.Chart{
		Title: "Bitrate over time", Width: chartWidth, Height: timeChartHeight,
		Series: []svg.Series{
			{Name: "bitrate", Color: orange, Points: bitrate, Area: true},
			{Name: "average " + bitrateLabel(float64(bs.AverageBitrate)), Color: foreground, Points: referenceLine(bitrateTimes(bitrate), float64(bs.AverageBitrate)), NoTip: true, Guide: true},
		},
		X: svg.UnitTime, Y: svg.UnitBitrate, Bands: bands,
	}.HTML()}

	if f != nil && len(f.Size) > 0 {
		charts = append(charts, frameSizeChart(f, bands))
	}

	return section{
		Title: "Bitrate",
		Area:  areaVideo,
		Topic: findings.TopicBitrate,
		Stats: []stat{
			{"Average", bitrateLabel(float64(bs.AverageBitrate))},
			{"Peak (" + bs.PeakWindow.String() + ")", bitrateLabel(float64(bs.PeakBitrate))},
			{"Peak / average", fmt.Sprintf("%.2f", bs.PeakToAverage)},
			{"Keyframes", fmt.Sprintf("%d · every %.2fs", bs.GOP.KeyframeCount, bs.GOP.MeanInterval.Seconds())},
			{"Frame size p95", bytesLabel(float64(bs.FrameSize.P95))},
		},
		Charts: charts,
	}
}

// frameSizeChart plots the size of every frame, keyframes as markers.
func frameSizeChart(
	f *analysis.FrameSeries,
	bands []svg.Band,
) template.HTML {
	pts, frames := durations(f.PTS), frameNumbers(len(f.PTS))
	sizes := make([]float64, len(f.Size))

	for i, s := range f.Size {
		sizes[i] = float64(s)
	}

	var keys svg.Samples

	for i, key := range f.Keyframe {
		if key && i < len(pts) && i < len(sizes) {
			keys.X, keys.Y, keys.Frames = append(keys.X, pts[i]), append(keys.Y, sizes[i]), append(keys.Frames, i)
		}
	}

	return svg.Chart{
		Title: "Frame sizes and keyframes", Width: chartWidth, Height: lumaChartHeight, MaxPoints: maxFramePoints,
		Series: []svg.Series{
			{Name: "frame size", Color: aqua, Samples: &svg.Samples{X: pts, Y: sizes, Frames: frames}},
			{Name: "keyframe", Color: amber, Samples: &keys, Markers: true},
		},
		X: svg.UnitTime, Y: svg.UnitBytes, Bands: bands,
	}.HTML()
}

// frameNumbers are 0…n-1: frame series hold every decoded frame in order.
func frameNumbers(
	n int,
) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}

	return out
}

func complexitySection(
	v *analysis.VideoReport,
	f *analysis.FrameSeries,
	bands []svg.Band,
) section {
	pts, frames := durations(f.PTS), frameNumbers(len(f.PTS))
	series := func(values []float64) *svg.Samples {
		return &svg.Samples{X: pts, Y: values, Frames: frames}
	}

	return section{
		Title:    "Complexity",
		Subtitle: "ITU-T P.910 spatial and temporal information",
		Area:     areaVideo,
		Topic:    findings.TopicComplexity,
		Stats: []stat{
			{"SI mean", fmt.Sprintf("%.1f (%s)", v.SITI.SISummary.Mean, v.Complexity.Spatial)},
			{"TI mean", fmt.Sprintf("%.1f (%s)", v.SITI.TISummary.Mean, v.Complexity.Temporal)},
			{"Shots", strconv.Itoa(len(v.Shots))},
			{"Content", fmt.Sprintf("%d×%d", v.Crop.Content.Width, v.Crop.Content.Height)},
		},
		Charts: []template.HTML{svg.Chart{
			Title: "Spatial and temporal information", Width: chartWidth, Height: timeChartHeight, MaxPoints: maxFramePoints,
			Series: []svg.Series{
				{Name: "SI", Color: blue, Samples: series(f.SI), Digits: 1},
				{Name: "TI", Color: amber, Samples: series(f.TI), Digits: 1},
			},
			X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
		}.HTML(), svg.Chart{
			Title: "Mean luma", Width: chartWidth, Height: lumaChartHeight, MaxPoints: maxFramePoints,
			Series: []svg.Series{{Name: "luma mean", Color: green, Samples: series(f.LumaMean), Digits: 1}},
			YMax:   maxLuma, X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
		}.HTML()},
		Table: shotTable(v.Shots),
	}
}

func shotTable(
	shots []analysis.ShotReport,
) *table {
	t := &table{Head: []string{"#", "start", "end", "frames", "SI", "TI", "bitrate", "camera"}, LinkCol: 1}

	for i, s := range shots {
		t.Spans = append(t.Spans, span{s.Start.Seconds(), s.End.Seconds()})
		t.Rows = append(t.Rows, []string{
			strconv.Itoa(i + 1), clock(s.Start), clock(s.End), strconv.Itoa(s.Frames),
			fmt.Sprintf("%.1f", s.SIMean), fmt.Sprintf("%.1f", s.TIMean), bitrateLabel(float64(s.Bitrate)), cameraCell(s.Camera),
		})
	}

	return t
}

func analysisCards(
	r *analysis.Report,
) []card {
	v, _ := r.Info.PrimaryVideo()

	cards := []card{
		{Label: "Duration", Value: clock(r.Info.Duration), Detail: frameCountLabel(r, v)},
		{Label: "Video", Value: fmt.Sprintf("%d×%d", v.Width, v.Height), Detail: fmt.Sprintf("%.3f fps · %s", v.AvgFrameRate.Float(), v.HDR.DynamicRange)},
		{Label: "Codec", Value: v.Codec, Detail: strings.TrimSpace(fmt.Sprintf("%s %s", v.Profile, bitDepthLabel(v.BitDepth)))},
	}

	if bs := r.Bitstream; bs != nil {
		rates := make([]float64, len(bs.Bitrate))
		for i, b := range bs.Bitrate {
			rates[i] = float64(b.Bitrate)
		}

		cards = append(cards, card{
			Label: "Bitrate", Value: bitrateLabel(float64(bs.AverageBitrate)),
			Detail: fmt.Sprintf("peak %s (%.2f×)", bitrateLabel(float64(bs.PeakBitrate)), bs.PeakToAverage),
			Spark:  svg.Sparkline(rates),
		})
	}

	if vr := r.Video; vr != nil {
		cards = append(cards, card{
			Label: "Shots", Value: strconv.Itoa(len(vr.Shots)),
			Detail: fmt.Sprintf("SI %.0f · TI %.0f", vr.SITI.SISummary.Mean, vr.SITI.TISummary.Mean),
		})
	}

	if c, ok := lightCard(r); ok {
		cards = append(cards, c)
	}

	if c, ok := motionCard(r); ok {
		cards = append(cards, c)
	}

	if c, ok := audioCard(r); ok {
		cards = append(cards, c)
	}

	return cards
}

func frameCountLabel(
	r *analysis.Report,
	v media.VideoStream,
) string {
	switch {
	case r.Video != nil:
		return plural(r.Video.FramesDecoded, "frame") + " decoded"
	case v.FrameCount > 0:
		return plural(int(v.FrameCount), "frame")
	}

	return ""
}

func bitDepthLabel(
	depth int,
) string {
	if depth == 0 {
		return ""
	}

	return fmt.Sprintf("· %d-bit", depth)
}

// bitrateTimes are the start times of the bitrate windows.
func bitrateTimes(
	points [][2]float64,
) []float64 {
	out := make([]float64, len(points))
	for i, p := range points {
		out[i] = p[0]
	}

	return out
}
