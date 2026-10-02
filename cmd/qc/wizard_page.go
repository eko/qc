package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/eko/qc/internal/tui"
)

// Page geometry. The main column is capped for readability; wide terminals
// get the summary panel beside it; tall ones some air around the header.
const (
	defaultPageWidth  = 80
	defaultPageHeight = 24
	pageMargin        = 2
	maxMainWidth      = 100
	panelWidth        = 36
	panelGap          = 4
	panelMinWidth     = 124
	roomyHeight       = 32
	minPageWidth      = 56
	minPageHeight     = 16
	// panelKeyWidth is the key column of the summary panel.
	panelKeyWidth = 10
	// kickerHeight is the section title above the form and its blank line.
	kickerHeight = 2
)

// hasPanel reports whether the summary panel fits beside the form.
func (m *wizardModel) hasPanel() bool {
	return m.width >= panelMinWidth
}

// roomy reports whether the terminal is tall enough for air around the
// header.
func (m *wizardModel) roomy() bool {
	return m.height >= roomyHeight
}

// contentWidth is the width of the page between its margins.
func (m *wizardModel) contentWidth() int {
	width := m.width - 2*pageMargin
	if m.hasPanel() {
		return min(width, maxMainWidth+panelGap+panelWidth)
	}

	return max(min(width, maxMainWidth), 1)
}

// mainWidth is the width of the form column.
func (m *wizardModel) mainWidth() int {
	if m.hasPanel() {
		return m.contentWidth() - panelGap - panelWidth
	}

	return m.contentWidth()
}

// headerHeight is the height of the step indicator and its rail.
func (m *wizardModel) headerHeight() int {
	if m.roomy() {
		return 4
	}

	return 3
}

// footerHeight is the height of the key hints and the line above them.
const footerHeight = 2

// bodyHeight is the height between the header and the footer.
func (m *wizardModel) bodyHeight() int {
	return max(m.height-m.headerHeight()-footerHeight, 1)
}

// formHeight is the height of the form, below the section title.
func (m *wizardModel) formHeight() int {
	return max(m.bodyHeight()-kickerHeight, 1)
}

// View implements tea.Model.
func (m *wizardModel) View() string {
	if m.phase == phaseDone || m.phase == phaseAborted {
		return ""
	}

	if m.width < minPageWidth || m.height < minPageHeight {
		return m.tooSmallView()
	}

	page := m.headerView() + "\n" + fitHeight(m.bodyView(), m.bodyHeight()) + "\n" + m.footerView()
	page = lipgloss.NewStyle().PaddingLeft(pageMargin).Render(page)

	if m.ctx.style.g.rail == asciiGlyphs.rail {
		return asciiText.Replace(page)
	}

	return page
}

// asciiText replaces the typography of labels and key hints with ASCII,
// one character for one so that the layout holds (file names excepted).
var asciiText = strings.NewReplacer(
	"·", "-", "×", "x", "≈", "~", "±", "~", "…", ".", "–", "-", "—", "-",
	"↑", "^", "↓", "v", "←", "<", "→", ">", "Δ", "D", "“", `"`, "”", `"`,
)

// fitHeight pads or cuts s to h lines.
func fitHeight(
	s string,
	h int,
) string {
	return lipgloss.NewStyle().Height(h).MaxHeight(h).Render(s)
}

// tooSmallView asks for a larger terminal.
func (m *wizardModel) tooSmallView() string {
	s := m.ctx.style

	text := s.text.Render(fmt.Sprintf("The terminal is %d×%d: the wizard needs %d×%d.", m.width, m.height, minPageWidth, minPageHeight)) +
		"\n" + s.muted.Render("Enlarge it, or press ctrl+c to quit.")

	return lipgloss.NewStyle().Padding(1, 2).Width(m.width).Render(s.title.Render("qc") + "\n\n" + text)
}

// current is the section shown.
func (m *wizardModel) current() section {
	if m.phase == phaseReview {
		return sectionReview
	}

	return m.form.current()
}

// headerView is the brand, the step indicator and the progress rail.
func (m *wizardModel) headerView() string {
	top := ""
	if m.roomy() {
		top = "\n"
	}

	return top + m.stepperView(m.contentWidth()) + "\n" + m.railView(m.contentWidth()) + "\n"
}

// brandView is the brand mark.
func (m *wizardModel) brandView() string {
	mark := "◆ qc"
	if m.ctx.style.g.rail == asciiGlyphs.rail {
		mark = "qc"
	}

	return m.ctx.style.title.Render(mark)
}

// stepperView names the sections, marking those done, the current one and
// those skipped; on narrow terminals it tells the step number instead.
func (m *wizardModel) stepperView(
	width int,
) string {
	s := m.ctx.style
	visible := visibleSections(m.all)
	current := m.current()
	steps := make([]string, 0, sectionReview+1)

	for sec := sectionSource; sec <= sectionReview; sec++ {
		steps = append(steps, m.stepView(sec, current, visible[sec]))
	}

	full := m.brandView() + "   " + strings.Join(steps, s.faint.Render(" "+s.g.sep+" "))
	if lipgloss.Width(full) <= width {
		return full
	}

	position, total := 0, 0

	for sec := sectionSource; sec <= sectionReview; sec++ {
		if visible[sec] {
			total++
			if sec <= current {
				position++
			}
		}
	}

	return m.brandView() + "   " + s.muted.Render(fmt.Sprintf("Step %d of %d %s ", position, total, s.g.dot)) + s.title.Render(current.String())
}

// stepView is a section of the step indicator.
func (m *wizardModel) stepView(
	sec, current section,
	visible bool,
) string {
	s := m.ctx.style
	name := sec.String()

	switch {
	case sec == current:
		return s.title.Render(s.g.current + " " + name)
	case !visible:
		return s.faint.Render(s.g.skipped + " " + name)
	case m.reviewed || sec < current:
		return s.accent.Render(s.g.done) + " " + s.text.Render(name)
	}

	return s.faint.Render(s.g.pending + " " + name)
}

// railView is a thin progress rail under the step indicator.
func (m *wizardModel) railView(
	width int,
) string {
	var order []section

	visible := visibleSections(m.all)

	for sec := sectionSource; sec <= sectionReview; sec++ {
		if visible[sec] {
			order = append(order, sec)
		}
	}

	position := 0

	for i, sec := range order {
		if sec == m.current() {
			position = i
		}
	}

	ratio := float64(position+1) / float64(len(order))

	s := m.ctx.style
	if s.plain || s.g.rail == asciiGlyphs.rail {
		filled := int(ratio * float64(width))

		return s.accent.Render(strings.Repeat(s.g.rail, filled)) + s.faint.Render(strings.Repeat(s.g.rule, width-filled))
	}

	return tui.GradientBar(ratio, width)
}

// bodyView is the section title, then the form (with the summary panel on
// wide terminals) or the review.
func (m *wizardModel) bodyView() string {
	current := m.current()
	kicker := m.kickerView(current)

	if m.phase == phaseReview {
		return kicker + "\n\n" + m.reviewView(m.contentWidth(), m.bodyHeight()-kickerHeight)
	}

	main := kicker + "\n\n" + fitHeight(m.form.form.View(), m.formHeight())
	if !m.hasPanel() {
		return main
	}

	main = lipgloss.NewStyle().Width(m.mainWidth()).Render(main)

	return lipgloss.JoinHorizontal(lipgloss.Top, main, strings.Repeat(" ", panelGap), m.panelView(m.bodyHeight()))
}

// kickerView is the section title and what the section is about.
func (m *wizardModel) kickerView(
	sec section,
) string {
	s := m.ctx.style

	return s.title.Render(strings.ToUpper(sec.String())) + s.faint.Render("  "+s.g.dot+"  "+sectionInfo[sec].blurb)
}

// panelView summarises the sections answered so far.
func (m *wizardModel) panelView(
	height int,
) string {
	s := m.ctx.style
	inner := panelWidth - 3
	lines := []string{s.kicker.Render("YOUR RUN"), ""}
	current := m.current()

	for _, sec := range m.answers.reviewSections(m.ctx) {
		if !m.reviewed && sec.section >= current {
			break
		}

		lines = append(lines, s.accent.Render(sec.section.String()))

		if sec.skipped != "" {
			lines = append(lines, s.faint.Render(truncate(sec.skipped, inner, s.g.ellipsis)))
		}

		for _, r := range sec.rows {
			value := s.text.Width(inner - panelKeyWidth).Render(r.value)
			lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, s.faint.Render(fmt.Sprintf("%-*s", panelKeyWidth, r.key)), value))
		}

		lines = append(lines, "")
	}

	if len(lines) == 2 {
		lines = append(lines, s.faint.Render("Your answers show up here."))
	}

	border := lipgloss.NormalBorder()
	if s.g.rail == asciiGlyphs.rail {
		border = lipgloss.ASCIIBorder()
	}

	return lipgloss.NewStyle().Border(border, false, false, false, true).BorderForeground(toneFaint).PaddingLeft(2).
		Width(panelWidth - 1).Height(height).MaxHeight(height).Render(strings.Join(lines, "\n"))
}

// footerView is the key hints, or the error of the focused field.
func (m *wizardModel) footerView() string {
	s := m.ctx.style
	quit := s.faint.Render("ctrl+c quit")
	room := m.contentWidth() - lipgloss.Width(quit) - 2

	left := m.hintsView(room)

	if m.phase == phaseForm {
		if f := m.form.form.GetFocusedField(); f != nil && !isPicker(f) && f.Error() != nil {
			left = s.danger.Render(truncate(s.g.cross+" "+f.Error().Error(), room, s.g.ellipsis))
		}
	}

	gap := max(2, m.contentWidth()-lipgloss.Width(left)-lipgloss.Width(quit))

	return "\n" + left + strings.Repeat(" ", gap) + quit
}

// hintsView renders the keys of what is shown.
func (m *wizardModel) hintsView(
	width int,
) string {
	m.help.Width = width

	if m.phase == phaseReview {
		return m.help.ShortHelpView(fitHints(m.reviewBindings(), width))
	}

	return m.help.ShortHelpView(fitHints(formHints(m.form.form.KeyBinds()), width))
}

// fitHints keeps the first hints that fit width: the help view overflows
// when it has no room for its ellipsis.
func fitHints(
	bindings []key.Binding,
	width int,
) []key.Binding {
	total := 0

	for i, b := range bindings {
		if !b.Enabled() {
			continue
		}

		w := lipgloss.Width(b.Help().Key) + 1 + lipgloss.Width(b.Help().Desc)
		if total > 0 {
			w += hintGap
		}

		if total+w > width {
			return bindings[:i]
		}

		total += w
	}

	return bindings
}

// hintGap is the width of the separator between two key hints.
const hintGap = 2

// formHints are the keys of the focused field as the page tells them: esc
// goes back a page (shift+tab still goes back a field), and the last enter
// leads to the review.
func formHints(
	bindings []key.Binding,
) []key.Binding {
	hints := make([]key.Binding, 0, len(bindings)+1)

	for _, b := range bindings {
		switch {
		case slices.Contains(b.Keys(), "shift+tab"), slices.Contains(b.Keys(), "ctrl+a"):
			continue
		case b.Help().Desc == "submit":
			b = withHelp(b, "review")
		}

		hints = append(hints, b)
	}

	if !usesEsc(bindings) {
		hints = append(hints, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")))
	}

	return hints
}

// reviewBindings are the keys of the review.
func (m *wizardModel) reviewBindings() []key.Binding {
	k := reviewKeys
	if m.choosing {
		return []key.Binding{k.left, withHelp(k.press, "edit"), k.sections, withHelp(k.back, "cancel edit")}
	}

	k.up.SetEnabled(m.scrollable)

	return []key.Binding{k.left, k.press, k.run, k.edit, k.sections, k.up, k.cancel}
}

// withHelp relabels a binding.
func withHelp(
	b key.Binding,
	desc string,
) key.Binding {
	b.SetHelp(b.Help().Key, desc)

	return b
}

// sectionNumber is the number of a section, as typed to edit it.
func sectionNumber(
	sec section,
) string {
	return strconv.Itoa(int(sec) + 1)
}
