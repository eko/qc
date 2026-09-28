package htmlreport

import (
	"fmt"
	"html/template"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/light"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
)

// lightSection charts the light levels of an HDR video: the robust peak and
// the average max(R, G, B) of every frame, with the measured and signalled
// MaxCLL and MaxFALL as reference lines. ok is false for SDR.
func lightSection(
	r *analysis.Report,
	bands []svg.Band,
) (section, bool) {
	if r.Video == nil || r.Video.Light == nil || r.Frames == nil || len(r.Frames.PeakNits) == 0 {
		return section{}, false
	}

	l, f := r.Video.Light, r.Frames
	v, _ := r.Info.PrimaryVideo()
	pts, frames := durations(f.PTS), frameNumbers(len(f.PTS))
	span := [2]float64{pts[0], pts[len(pts)-1]}
	level := func(y float64) [][2]float64 { return [][2]float64{{span[0], y}, {span[1], y}} }

	series := []svg.Series{
		{Name: "peak (99.9%)", Color: orange, Samples: &svg.Samples{X: pts, Y: f.RobustPeakNits, Frames: frames}},
		{Name: "average", Color: blue, Samples: &svg.Samples{X: pts, Y: f.AverageNits, Frames: frames}, Area: true},
		{Name: "strict peak", Samples: &svg.Samples{X: pts, Y: f.PeakNits, Frames: frames}, TipOnly: true},
		{Name: "MaxCLL", Color: amber, Points: level(l.MaxCLLRobust), NoTip: true, Guide: true},
		{Name: "MaxFALL", Color: aqua, Points: level(l.MaxFALL), NoTip: true, Guide: true},
	}

	signalled := "none"
	if cll := v.HDR.ContentLightLevel; cll != nil && (cll.MaxCLL > 0 || cll.MaxFALL > 0) {
		signalled = fmt.Sprintf("%d / %d cd/m²", cll.MaxCLL, cll.MaxFALL)
		series = append(series,
			svg.Series{Name: "signalled MaxCLL", Color: foreground, Points: level(float64(cll.MaxCLL)), NoTip: true, Guide: true},
			svg.Series{Name: "signalled MaxFALL", Color: green, Points: level(float64(cll.MaxFALL)), NoTip: true, Guide: true})
	}

	return section{
		Title:    "Light levels",
		Subtitle: lightSubtitle(l),
		Area:     areaVideo,
		Topic:    findings.TopicLight,
		Stats: []stat{
			{"MaxCLL", fmt.Sprintf("%.0f cd/m²", l.MaxCLLRobust)},
			{"MaxCLL strict", fmt.Sprintf("%.0f cd/m²", l.MaxCLL)},
			{"MaxFALL", fmt.Sprintf("%.0f cd/m²", l.MaxFALL)},
			{"Signalled", signalled},
			{"Mastering display", masteringLabel(r)},
		},
		Charts: []template.HTML{svg.Chart{
			Title: "Light per frame (cd/m²)", Width: chartWidth, Height: timeChartHeight, MaxPoints: maxFramePoints,
			Series: series, YMin: 0, X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
		}.HTML()},
		Method: []string{fmt.Sprintf("max(R, G, B) of every frame in cd/m² (CTA-861.3), on a grid of one pixel every %d in each direction. "+
			"MaxCLL is the highest 99.9th percentile of a frame: the strict maximum of a 4:2:0 picture overshoots on saturated colour edges. "+
			"MaxFALL is taken over the picture without its black borders.", l.SampleStep)},
	}, true
}

// lightSubtitle says what the light levels are for the transfer.
func lightSubtitle(
	l *light.Result,
) string {
	if l.DisplayPeak > 0 {
		return fmt.Sprintf("HLG display light on a %.0f cd/m² display (BT.2100 reference)", l.DisplayPeak)
	}

	return "PQ display light, absolute"
}

// masteringLabel describes the signalled mastering display.
func masteringLabel(
	r *analysis.Report,
) string {
	v, _ := r.Info.PrimaryVideo()

	m := v.HDR.MasteringDisplay
	if m == nil {
		return "none"
	}

	return fmt.Sprintf("%.4g–%.0f cd/m²", m.MinLuminance, m.MaxLuminance)
}

// lightCard sums the light levels of an HDR video up in a header card.
func lightCard(
	r *analysis.Report,
) (card, bool) {
	if r.Video == nil || r.Video.Light == nil {
		return card{}, false
	}

	l := r.Video.Light

	return card{Label: "Light", Value: fmt.Sprintf("%.0f cd/m²", l.MaxCLLRobust), Detail: fmt.Sprintf("MaxCLL · MaxFALL %.0f", l.MaxFALL)}, true
}
