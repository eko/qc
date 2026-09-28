package tui

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/media"
)

const (
	// maxShotRows bounds the shot table.
	maxShotRows = 8
	// bitrateChartHeight is the height of the bitrate chart, in rows.
	bitrateChartHeight = 6
)

// RenderReport writes a human-friendly report, width columns wide.
func RenderReport(
	w io.Writer,
	report *analysis.Report,
	width int,
	jsonPath string,
) error {
	width = clampWidth(width, minReportWidth)

	blocks := []string{
		header(report),
		cards(report, width),
		bitrateSection(report.Bitstream, width),
	}

	if report.Video != nil {
		if report.Frames != nil {
			blocks = append(blocks, complexitySection(report, width), lightSection(report, width), cameraSection(report, width))
		}

		blocks = append(blocks,
			timelineSection(report, width),
			shotsSection(report.Video.Shots),
		)
	}

	blocks = append(blocks, findingsBlock(reportFindings(report)), footer(report, jsonPath))

	return writeBlocks(w, blocks)
}

// writeBlocks prints the non-empty blocks of a report, separated by a blank
// line.
func writeBlocks(
	w io.Writer,
	blocks []string,
) error {
	blocks = slices.DeleteFunc(blocks, func(b string) bool { return b == "" })

	if _, err := fmt.Fprintln(w, strings.Join(blocks, "\n\n")); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

func header(
	report *analysis.Report,
) string {
	dir, file := filepath.Split(report.Info.Path)

	return title.Render(brand) + Subtle.Render("  ·  "+dir) + Bold.Render(file) + hdrBadge(report)
}

// cards shows the container and the video stream side by side.
func cards(
	report *analysis.Report,
	width int,
) string {
	info := report.Info

	container := []string{
		kv("format", info.Format),
		kv("duration", Clock(info.Duration, false)),
		kv("size", Bytes(float64(info.Size))),
		kv("bitrate", Bitrate(float64(info.BitRate))),
	}

	for _, a := range info.Audio {
		container = append(container, kv(fmt.Sprintf("audio #%d", a.Index),
			fmt.Sprintf("%s %d Hz %dch %s", a.Codec, a.SampleRate, a.Channels, orDash(a.Language))))
	}

	v, _ := info.PrimaryVideo()
	video := []string{
		kv("codec", strings.TrimSpace(v.Codec+" "+v.Profile)),
		kv("resolution", fmt.Sprintf("%d×%d", v.Width, v.Height)),
		kv("frame rate", fmt.Sprintf("%.3f fps", v.AvgFrameRate.Float())),
		kv("pixel", fmt.Sprintf("%s · %d-bit", v.PixelFormat, v.BitDepth)),
		kv("color", fmt.Sprintf("%s / %s / %s", orDash(v.Color.Primaries), orDash(v.Color.Transfer), orDash(v.Color.Range))),
		kv("dynamic", dynamicRange(v.HDR)),
	}

	// Two cards and a one-column gap fill the width; lipgloss widths
	// exclude the border.
	cardWidth := (width-1)/2 - 2
	left := card.Width(cardWidth).Render(section.Render("Container") + "\n" + strings.Join(container, "\n"))
	right := card.Width(cardWidth).Render(section.Render("Video") + "\n" + strings.Join(video, "\n"))

	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func bitrateSection(
	bs *bitstream.Report,
	width int,
) string {
	series := make([]float64, len(bs.Bitrate))
	for i, p := range bs.Bitrate {
		series[i] = float64(p.Bitrate)
	}

	lines := []string{
		section.Render("Bitrate") + "  " +
			stat("avg", Bitrate(float64(bs.AverageBitrate))) +
			stat("peak", fmt.Sprintf("%s @ %s", Bitrate(float64(bs.PeakBitrate)), Clock(bs.PeakAt, true))) +
			stat("peak/avg", fmt.Sprintf("%.2f", bs.PeakToAverage)),
	}

	lines = append(lines, BarChart(series, width, bitrateChartHeight, Max, Scale{}, Cyan, compactBitrate)...)
	lines = append(lines, timeAxis(bs.Duration, width))

	gop := bs.GOP
	lines = append(lines,
		stat("GOP", fmt.Sprintf("%d keyframes · every %s (%s–%s)%s",
			gop.KeyframeCount, seconds(gop.MeanInterval), seconds(gop.MinInterval), seconds(gop.MaxInterval), fixed(gop.Fixed)))+
			stat("frames", fmt.Sprintf("p50 %s · p95 %s · max %s",
				Bytes(float64(bs.FrameSize.P50)), Bytes(float64(bs.FrameSize.P95)), Bytes(float64(bs.FrameSize.Max)))))

	return strings.Join(lines, "\n")
}

func complexitySection(
	report *analysis.Report,
	width int,
) string {
	v := report.Video
	frames := report.Frames
	sparkWidth := width - gutter

	// The first TI is undefined (no previous frame): it would flatten the
	// sparkline.
	ti := frames.TI[min(1, len(frames.TI)):]

	lines := []string{
		section.Render("Complexity") + "  " +
			stat("spatial", fmt.Sprintf("SI %.1f %s", v.SITI.SISummary.Mean, level(v.Complexity.Spatial))) +
			stat("temporal", fmt.Sprintf("TI %.1f %s", v.SITI.TISummary.Mean, level(v.Complexity.Temporal))),
		rowLabel("SI") + Sparkline(frames.SI, sparkWidth, Mean, Magenta),
		rowLabel("TI") + Sparkline(ti, sparkWidth, Max, Yellow),
		rowLabel("luma") + Sparkline(frames.LumaMean, sparkWidth, Mean, Green),
		rowLabel("frame size") + Sparkline(ints(frames.Size), sparkWidth, Max, Cyan),
	}

	return strings.Join(lines, "\n")
}

// timelineSection draws shots, black/freeze segments and keyframes on a
// shared time axis.
func timelineSection(
	report *analysis.Report,
	width int,
) string {
	v := report.Video
	bs := report.Bitstream
	cols := width - gutter
	col := timeColumn(bs.Duration, cols)

	shots := slices.Repeat([]string{" "}, cols)

	// Alternate colour and glyph so consecutive shots stay distinct without colours.
	shotGlyphs := []string{Accent.Render("█"), Cyan.Render("▓")}
	for i, shot := range v.Shots {
		for c := col(shot.Start); c <= col(shot.End-1); c++ {
			shots[c] = shotGlyphs[i%2]
		}
	}

	events := []rune(strings.Repeat("·", cols))
	for _, seg := range v.Black.Segments {
		fill(events, col(seg.Start), col(seg.End-1), '■')
	}

	for _, seg := range v.Freeze.Segments {
		fill(events, col(seg.Start), col(seg.End-1), '≡')
	}

	keys := []rune(strings.Repeat(" ", cols))
	for _, k := range bs.Keyframes {
		keys[col(k)] = '╵'
	}

	return strings.Join([]string{
		section.Render("Timeline") + "  " + stat("shots", strconv.Itoa(len(v.Shots))) +
			stat("black", strconv.Itoa(len(v.Black.Segments))) +
			stat("freeze", strconv.Itoa(len(v.Freeze.Segments))),
		rowLabel("shots") + strings.Join(shots, ""),
		rowLabel("black/frz") + Red.Render(string(events)),
		rowLabel("keyframes") + Subtle.Render(string(keys)),
		timeAxis(bs.Duration, width),
	}, "\n")
}

// shotsSection lists the hardest shots: they drive the top of the encoding
// ladder.
func shotsSection(
	shots []analysis.ShotReport,
) string {
	if len(shots) == 0 {
		return ""
	}

	// Sort shot numbers rather than shots, to keep their position in the title.
	order := make([]int, len(shots))
	for i := range order {
		order[i] = i
	}

	difficulty := func(s analysis.ShotReport) float64 { return s.SIMean * s.TIMean }
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(difficulty(shots[b]), difficulty(shots[a])) })

	lines := []string{
		section.Render("Hardest shots") + Subtle.Render(fmt.Sprintf("  (%d total)", len(shots))),
		Subtle.Render(fmt.Sprintf("  %-4s %-19s %7s %7s %7s %11s  %s", "#", "time", "frames", "SI", "TI", "bitrate", "camera")),
	}

	for _, i := range order[:min(len(order), maxShotRows)] {
		s := shots[i]
		lines = append(lines, fmt.Sprintf("  %-4d %-19s %7d %7.1f %7.1f %11s  %s",
			i+1, Clock(s.Start, true)+" → "+Clock(s.End, true), s.Frames, s.SIMean, s.TIMean, Bitrate(float64(s.Bitrate)), cameraLabel(s.Camera)))
	}

	return strings.Join(lines, "\n")
}

func footer(
	report *analysis.Report,
	jsonPath string,
) string {
	line := timingsLine(stageTimings(report.Timings, "probe", "bitstream", "video"))
	if v := report.Video; v != nil {
		line += Subtle.Render(fmt.Sprintf(" · %d frames decoded", v.FramesDecoded))
	}

	if jsonPath != "" {
		line += "\n" + writtenLine("full technical report", jsonPath)
	}

	return line
}

// stageTimings lists the timings of the given stages, in order, skipping
// the stages that did not run.
func stageTimings(
	timings map[string]string,
	stages ...string,
) string {
	var parts []string

	for _, stage := range stages {
		if d, ok := timings[stage]; ok {
			parts = append(parts, stage+" "+d)
		}
	}

	return strings.Join(parts, " · ")
}

func kv(
	k, v string,
) string {
	return key.Render(k) + v
}

func stat(
	k, v string,
) string {
	return Subtle.Render(k+" ") + Bold.Render(v) + "   "
}

func level(
	l string,
) string {
	switch l {
	case "low":
		return Green.Render("▁ low")
	case "medium":
		return Yellow.Render("▄ medium")
	}

	return Red.Render("█ high")
}

func dynamicRange(
	h media.HDR,
) string {
	s := string(h.DynamicRange)

	switch {
	case h.DolbyVision != nil:
		s += fmt.Sprintf(" (profile %d.%d)", h.DolbyVision.Profile, h.DolbyVision.Level)
	case h.ContentLightLevel != nil:
		s += fmt.Sprintf(" (MaxCLL %d / MaxFALL %d)", h.ContentLightLevel.MaxCLL, h.ContentLightLevel.MaxFALL)
	}

	if h.DynamicRange == media.DynamicRangeSDR {
		return s
	}

	return Magenta.Render(s)
}

func seconds(
	d media.Duration,
) string {
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func fixed(
	f bool,
) string {
	if f {
		return Green.Render(" fixed")
	}

	return Yellow.Render(" variable")
}

func orDash(
	s string,
) string {
	if s == "" {
		return "–"
	}

	return s
}

func ints(
	values []int,
) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = float64(v)
	}

	return out
}

// fill sets row[from..to] to r, within the row.
func fill(
	row []rune,
	from, to int,
	r rune,
) {
	for i := max(from, 0); i <= to && i < len(row); i++ {
		row[i] = r
	}
}
