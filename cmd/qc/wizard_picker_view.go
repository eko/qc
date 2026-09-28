package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/internal/tui"
)

// View implements huh.Field: the browser when focused, the picked file
// otherwise.
func (f *videoPicker) View() string {
	if !f.focused {
		return f.blurredView()
	}

	rows := f.rows()
	f.scroll(rows)

	var body string

	if f.sideBySide() {
		listWidth := f.width - previewWidth - 2
		body = lipgloss.JoinHorizontal(lipgloss.Top, f.listView(rows, listWidth), "  ", f.previewCard(rows))
	} else {
		body = f.listView(rows, f.width)
	}

	return f.headView() + "\n" + body + f.footView()
}

// sideBySide reports whether the preview goes beside the list.
func (f *videoPicker) sideBySide() bool {
	return f.width >= sideBySideWidth
}

// chromeHeight is the height of the picker around its file lines.
func (f *videoPicker) chromeHeight() int {
	return lipgloss.Height(f.headView()) + lipgloss.Height(f.footView())
}

// scroll keeps the cursor within the rows shown.
func (f *videoPicker) scroll(
	rows int,
) {
	if f.cursor < f.offset {
		f.offset = f.cursor
	}

	if f.cursor >= f.offset+rows {
		f.offset = f.cursor - rows + 1
	}

	f.offset = max(0, min(f.offset, len(f.visible)-rows))
}

// headView is the question, its help and the filter line with the folder.
func (f *videoPicker) headView() string {
	s := f.style
	lines := []string{s.bold.Render(f.title)}

	if f.description != "" {
		lines = append(lines, s.muted.Width(f.width).Render(f.description))
	}

	filter := s.accent.Render("/ ") + f.filter.View()
	folder := s.faint.Render(truncateLeft(homeRelative(f.dir), max(f.width-lipgloss.Width(filter)-2, 0), s.g.ellipsis))
	gap := max(1, f.width-lipgloss.Width(filter)-lipgloss.Width(folder))

	return strings.Join(lines, "\n") + "\n\n" + filter + strings.Repeat(" ", gap) + folder + "\n" + f.ruleView()
}

// ruleView is the rule under the filter, with the count of what is listed.
func (f *videoPicker) ruleView() string {
	width := f.width
	if f.sideBySide() {
		width -= previewWidth + 2
	}

	folders, videos := 0, 0

	for _, e := range f.visible {
		switch {
		case e.name == parentEntry:
		case e.dir:
			folders++
		default:
			videos++
		}
	}

	counts := []string{plural(videos, "video")}
	if folders > 0 {
		counts = append(counts, plural(folders, "folder"))
	}

	label := " " + strings.Join(counts, " "+f.style.g.dot+" ") + " "

	return f.style.faint.Render(strings.Repeat(f.style.g.rule, 2) + label + strings.Repeat(f.style.g.rule, max(0, width-2-lipgloss.Width(label))))
}

// plural counts things, e.g. "1 video", "3 folders".
func plural(
	n int,
	thing string,
) string {
	if n == 1 {
		return "1 " + thing
	}

	return fmt.Sprintf("%d %ss", n, thing)
}

// footView is the preview of stacked pages and the error line.
func (f *videoPicker) footView() string {
	var b strings.Builder

	if !f.sideBySide() {
		b.WriteString("\n" + f.style.faint.Render(strings.Repeat(f.style.g.rule, f.width)))

		for _, line := range f.previewLines(false) {
			b.WriteString("\n" + truncate(line, f.width, f.style.g.ellipsis))
		}
	}

	switch {
	case f.err != nil:
		b.WriteString("\n" + f.style.danger.Render(f.style.g.cross+" "+f.err.Error()))
	case f.listErr != nil:
		b.WriteString("\n" + f.style.danger.Render(f.style.g.cross+" cannot open this folder"))
	case f.pending != "":
		b.WriteString("\n" + f.style.muted.Render("checking "+filepath.Base(f.pending)+f.style.g.ellipsis))
	default:
		b.WriteString("\n")
	}

	return b.String()
}

// listView renders rows file lines, padded to the height.
func (f *videoPicker) listView(
	rows, width int,
) string {
	lines := make([]string, 0, rows)

	if len(f.visible) == 0 || (len(f.visible) == 1 && f.visible[0].name == parentEntry) {
		lines = append(lines, "  "+f.style.muted.Render(f.emptyMessage()))
	}

	for i := f.offset; i < len(f.visible) && len(lines) < rows; i++ {
		lines = append(lines, f.rowView(f.visible[i], i == f.cursor, width))
	}

	for len(lines) < rows {
		lines = append(lines, "")
	}

	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// emptyMessage tells what to do when nothing is listed.
func (f *videoPicker) emptyMessage() string {
	if f.filter.Value() != "" {
		return "Nothing matches “" + f.filter.Value() + "”: esc clears the filter."
	}

	return "No video here: ← goes up a folder."
}

// rowView renders an entry: the cursor, the name, and on the right what
// is known of it.
func (f *videoPicker) rowView(
	e pickerEntry,
	selected bool,
	width int,
) string {
	s := f.style
	cursor, name, style := "  ", e.name, s.text

	if e.dir {
		style = s.muted.Bold(true)
		if e.name != parentEntry {
			name += "/"
		}
	}

	if selected {
		cursor, style = s.accent.Render(s.g.cursor+" "), s.accent.Bold(true)
	}

	right := f.rowDetail(e)
	name = truncate(name, width-2-lipgloss.Width(right)-2, s.g.ellipsis)
	gap := max(1, width-2-lipgloss.Width(name)-lipgloss.Width(right))

	return cursor + style.Render(name) + strings.Repeat(" ", gap) + s.faint.Render(right)
}

// rowDetail is the right column of an entry: its size, with the height and
// duration of a file already probed.
func (f *videoPicker) rowDetail(
	e pickerEntry,
) string {
	switch {
	case e.name == parentEntry:
		return "parent"
	case e.dir:
		return ""
	}

	size := tui.Bytes(float64(e.size))
	if sum, ok := f.cache.cached(filepath.Join(f.dir, e.name)); ok && sum.hasVideo {
		return sum.short() + "   " + size
	}

	return size
}

// previewLines describe the highlighted entry: its name, then two lines
// (stacked) or one line per property (card).
func (f *videoPicker) previewLines(
	card bool,
) []string {
	s := f.style

	e, ok := f.current()
	if !ok {
		return []string{s.faint.Render("Nothing highlighted."), ""}
	}

	if e.dir {
		return []string{s.bold.Render(e.name + "/"), s.muted.Render("folder · enter opens it")}
	}

	name := s.bold.Render(e.name)
	sum, known := f.cache.cached(f.currentPath())

	switch {
	case !known:
		return []string{name, s.faint.Render("reading" + s.g.ellipsis)}
	case sum.validate() != nil:
		return []string{name, s.danger.Render(s.g.cross + " " + sum.validate().Error())}
	case card:
		return append([]string{name, ""}, f.propertyLines(sum, e.size)...)
	}

	return []string{name + s.faint.Render("  "+tui.Bytes(float64(e.size))), s.muted.Render(sum.format()) + s.faint.Render(" · ") + f.timingView(sum)}
}

// timingView is the timing line with the HDR format highlighted.
func (f *videoPicker) timingView(
	sum videoSummary,
) string {
	s := f.style
	dr := s.muted.Render("SDR")

	if sum.dynamicRange != "" {
		dr = s.hdr.Render(sum.dynamicRange)
	}

	sep := s.faint.Render(" · ")

	return s.muted.Render(tui.Clock(sum.duration, true)) + sep + dr + sep + s.muted.Render(audioTracksLabel(sum.audioTracks))
}

// propertyLines list the properties of a probed file, one per line.
func (f *videoPicker) propertyLines(
	sum videoSummary,
	size int64,
) []string {
	s := f.style
	dr := s.text.Render("SDR")

	if sum.dynamicRange != "" {
		dr = s.hdr.Render(sum.dynamicRange)
	}

	depth := "8-bit"
	if sum.bitDepth > 0 {
		depth = fmt.Sprintf("%d-bit", sum.bitDepth)
	}

	row := func(k, v string) string { return s.muted.Render(fmt.Sprintf("%-11s", k)) + v }

	return []string{
		row("Codec", s.text.Render(codecName(sum.codec)+" · "+depth)),
		row("Picture", s.text.Render(fmt.Sprintf("%d×%d", sum.width, sum.height))),
		row("Frame rate", s.text.Render(strings.TrimSuffix(fmt.Sprintf("%.3f", sum.fps), ".000")+" fps")),
		row("Duration", s.text.Render(tui.Clock(sum.duration, true))),
		row("Range", dr),
		row("Audio", s.text.Render(audioTracksLabel(sum.audioTracks))),
		row("Size", s.text.Render(tui.Bytes(float64(size)))),
	}
}

// previewCard frames the preview beside the list.
func (f *videoPicker) previewCard(
	rows int,
) string {
	s := f.style
	inner := previewWidth - 4

	lines := f.previewLines(true)
	for i, line := range lines {
		lines[i] = truncate(line, inner, s.g.ellipsis)
	}

	return lipgloss.NewStyle().Border(s.g.border).BorderForeground(toneFaint).Padding(0, 1).
		Width(previewWidth - 2).Height(max(rows-2, 1)).Render(strings.Join(lines, "\n"))
}

// blurredView is the question and the picked file, when another field of
// the page is focused.
func (f *videoPicker) blurredView() string {
	s := f.style
	value := s.faint.Render("No file picked.")

	if *f.value != "" {
		value = s.success.Render(s.g.check+" ") + s.text.Render(*f.value)
		if sum, ok := f.cache.cached(*f.value); ok && sum.hasVideo {
			value += s.faint.Render("  " + sum.format())
		}
	}

	// Indented like the blurred huh fields, whose focus bar is hidden.
	return lipgloss.NewStyle().PaddingLeft(blurredIndent).Render(
		s.muted.Bold(true).Render(f.title) + "\n" + truncate(value, f.width-blurredIndent, s.g.ellipsis))
}

// blurredIndent is the width of the hidden focus bar of blurred fields and
// of its padding.
const blurredIndent = 2

// homeRelative shortens a path below the home folder to ~/….
func homeRelative(
	path string,
) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}

	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}

	return path
}
