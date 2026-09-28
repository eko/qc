package htmlreport

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/motion"
	"github.com/eko/qc/internal/findings"
	"github.com/eko/qc/internal/htmlreport/svg"
)

const (
	// maxMotionStats bounds the classes listed as stats of the section.
	maxMotionStats = 4
	// motionChartHeight is the height of the shake chart.
	motionChartHeight = 140
	// shakyBand shades the shaky shots on the camera charts.
	shakyBand = "var(--s4)"
)

// motionSection charts the camera move of every frame (pan and tilt in % of
// the width per second, zoom in % per second) and the jitter of the camera
// path, shaky shots shaded. ok is false when the motion analysis was
// skipped.
func motionSection(
	r *analysis.Report,
	bands []svg.Band,
) (section, bool) {
	v, f := r.Video, r.Frames
	if v == nil || v.Motion == nil || f == nil || len(f.MotionPan) == 0 {
		return section{}, false
	}

	bands = append(shakyBands(v.Shots), bands...)
	pts, frames := durations(f.PTS), frameNumbers(len(f.PTS))
	samples := func(values []float64) *svg.Samples { return &svg.Samples{X: pts, Y: values, Frames: frames} }

	move := svg.Chart{
		Width: chartWidth, Height: timeChartHeight, MaxPoints: maxFramePoints,
		Series: []svg.Series{
			{Name: "pan (%W/s)", Color: orange, Samples: samples(f.MotionPan), Digits: 1},
			{Name: "tilt (%W/s)", Color: blue, Samples: samples(f.MotionTilt), Digits: 1},
			{Name: "zoom (%/s)", Color: aqua, Samples: samples(f.MotionZoom), Digits: 1},
			{Name: "roll (°/s)", Samples: samples(f.MotionRoll), TipOnly: true, Digits: 2},
			{Name: "confidence", Samples: samples(f.MotionConfidence), TipOnly: true, Digits: 2},
		},
		X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
	}
	move.YMax = symmetricLimit(f.MotionPan, f.MotionTilt, f.MotionZoom)
	move.YMin = -move.YMax
	shake := svg.Chart{
		Width: chartWidth, Height: motionChartHeight, MaxPoints: maxFramePoints,
		Series: []svg.Series{{Name: "shake (%W)", Color: amber, Samples: samples(f.MotionShake), Area: true, Digits: 2}},
		YMin:   0, X: svg.UnitTime, Y: svg.UnitNumber, Bands: bands,
	}

	return section{
		Title:    "Camera motion",
		Subtitle: "global motion of the picture, per frame and per shot",
		Topic:    findings.TopicMotion,
		Stats:    motionStats(v.Motion),
		Charts:   []template.HTML{move.HTML(), shake.HTML()},
		Notes: []string{"Camera moves, low-passed over 0.5 s: pan and tilt in % of the picture width per second (positive right and up), " +
			"zoom in % of scale per second (positive in). Shake is the jitter of the camera path around its trend, in % of the width. " +
			"Estimated on the thumbnails with a robust similarity fit, so moving subjects do not count as camera motion; " +
			"frames without enough texture (flat pictures, fades) and the first frame of each shot are interpolated (confidence 0)."},
	}, true
}

// symmetricLimit is the bound of a y axis symmetric about zero showing
// every value: 1, 2 or 5 × 10^k, whose half is a tick step of the chart
// (niceStep), so zero is a gridline and moves both ways read alike.
func symmetricLimit(
	series ...[]float64,
) float64 {
	peak := 1.0
	for _, values := range series {
		for _, v := range values {
			peak = max(peak, math.Abs(v))
		}
	}

	mag := math.Pow(10, math.Floor(math.Log10(peak)))
	for _, m := range []float64{1, 2, 5} {
		if m*mag >= peak {
			return m * mag
		}
	}

	return 10 * mag
}

// motionStats lists the main classes of camera work and the shaky shots.
func motionStats(
	m *motion.Summary,
) []stat {
	stats := make([]stat, 0, maxMotionStats+2)
	for _, c := range m.Classes[:min(len(m.Classes), maxMotionStats)] {
		stats = append(stats, stat{capitalize(string(c.Class)), fmt.Sprintf("%.0f%% · %s", c.Share*100, plural(c.Shots, "shot"))})
	}

	return append(stats,
		stat{"Shaky shots", strconv.Itoa(m.ShakyShots)},
		stat{"Reliable frames", fmt.Sprintf("%.0f%%", m.Reliable*100)})
}

// shakyBands shades the shaky shots.
func shakyBands(
	shots []analysis.ShotReport,
) []svg.Band {
	var bands []svg.Band

	for _, s := range shots {
		if s.Camera != nil && s.Camera.Shaky {
			bands = append(bands, svg.Band{From: s.Start.Seconds(), To: s.End.Seconds(), Color: shakyBand, Label: "shaky"})
		}
	}

	return bands
}

// motionCard sums the camera work up in a header card: the main class and
// the shaky shots.
func motionCard(
	r *analysis.Report,
) (card, bool) {
	if r.Video == nil || r.Video.Motion == nil || len(r.Video.Motion.Classes) == 0 {
		return card{}, false
	}

	m := r.Video.Motion
	top := m.Classes[0]

	return card{
		Label:  "Camera",
		Value:  fmt.Sprintf("%.0f%% %s", top.Share*100, top.Class),
		Detail: plural(m.ShakyShots, "shaky shot"),
	}, true
}

// cameraCell is a shot's camera work in the shot table.
func cameraCell(
	c *motion.Shot,
) string {
	if c == nil {
		return "–"
	}

	label := strings.TrimSpace(string(c.Class) + " " + string(c.Direction))
	if c.Shaky && c.Class != motion.ClassHandheld {
		label += ", shaky"
	}

	return label
}

// motionFinding words a finding of the camera motion analysis.
func motionFinding(
	f findings.Finding,
) (finding, bool) {
	if f.Code != findings.ShakyShots {
		return finding{}, false
	}

	spans := make([]span, 0, min(len(f.Spans), maxSegmentFindings))
	for _, s := range f.Spans[:min(len(f.Spans), maxSegmentFindings)] {
		spans = append(spans, spanOf(s))
	}

	return worded(f, spans, "%s: camera jitter up to %.1f%% of the width (handheld or unstabilised camera, costly to encode)",
		capitalize(plural(len(f.Spans), "shaky shot")), f.Value), true
}

// capitalize upper-cases the first letter of an ASCII word.
func capitalize(
	s string,
) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}
