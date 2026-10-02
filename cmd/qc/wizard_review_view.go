package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Review table columns: the numbered section, then the key of each answer.
const (
	reviewLabelWidth = 13
	reviewKeyWidth   = 11
)

// reviewView is the card of every answer, the equivalent command and the
// actions, within width × height.
func (m *wizardModel) reviewView(
	width, height int,
) string {
	command := m.commandView(width)
	actions := m.actionsView()
	fixed := lipgloss.Height(command) + lipgloss.Height(actions) + 2

	return m.reviewCard(width, height-fixed) + "\n\n" + command + "\n\n" + actions
}

// commandView is the command the wizard runs, wrapped with shell line
// continuations so that it can be copied as it is.
func (m *wizardModel) commandView(
	width int,
) string {
	s := m.ctx.style
	lines := wrapCommand(m.answers.command(), width-2)
	title := s.kicker.Render("COMMAND") + s.faint.Render("  "+s.g.dot+"  what runs, to run it again without the wizard")

	for i, line := range lines {
		prompt := "  "
		if i == 0 {
			prompt = s.accent.Render("$ ")
		}

		lines[i] = prompt + s.text.Render(line)
	}

	return title + "\n" + strings.Join(lines, "\n")
}

// wrapCommand renders args as a command line cut into lines of at most
// width cells, each but the last ending with a shell continuation. A flag
// stays on the line of its value.
func wrapCommand(
	args []string,
	width int,
) []string {
	var lines []string

	line := ""

	for _, unit := range commandUnits(args) {
		switch {
		case line == "":
			line = unit
		case lipgloss.Width(line)+1+lipgloss.Width(unit)+2 > width:
			lines = append(lines, line+` \`)
			line = "  " + unit
		default:
			line += " " + unit
		}
	}

	return append(lines, line)
}

// commandUnits are the quoted arguments, a flag joined with its value.
func commandUnits(
	args []string,
) []string {
	var units []string

	for i := 0; i < len(args); i++ {
		unit := quoteArg(args[i])

		if strings.HasPrefix(args[i], "-") && !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			unit += " " + quoteArg(args[i])
		}

		units = append(units, unit)
	}

	return units
}

// reviewCard frames the review table, scrolled when taller than height.
func (m *wizardModel) reviewCard(
	width, height int,
) string {
	s := m.ctx.style
	inner := width - 4
	room := max(height-2, 1)

	lines := m.reviewLines(inner, true)
	if len(lines) > room {
		lines = m.reviewLines(inner, false)
	}

	m.scrollable = len(lines) > room
	if m.scrollable {
		visible := max(room-1, 1)
		m.scroll = min(m.scroll, len(lines)-visible)
		more := len(lines) - visible - m.scroll
		hint := fmt.Sprintf("%s %d more %s %s ↑↓ scroll", s.g.ellipsis, more, map[bool]string{true: "line", false: "lines"}[more == 1], s.g.dot)

		if more == 0 {
			hint = "↑ scroll back"
		}

		lines = append(lines[m.scroll:m.scroll+visible:m.scroll+visible], s.faint.Render(hint))
	}

	return lipgloss.NewStyle().Border(s.g.border).BorderForeground(toneFaint).Padding(0, 1).
		Width(width - 2).Render(strings.Join(lines, "\n"))
}

// reviewLines are the lines of the review table; spaced separates the
// sections with a blank line.
func (m *wizardModel) reviewLines(
	width int,
	spaced bool,
) []string {
	s := m.ctx.style
	valueWidth := max(width-reviewLabelWidth-reviewKeyWidth, 8)
	blankLabel := strings.Repeat(" ", reviewLabelWidth+reviewKeyWidth)

	var lines []string

	for i, sec := range m.answers.reviewSections(m.ctx) {
		if spaced && i > 0 {
			lines = append(lines, "")
		}

		label := s.faint.Render(sectionNumber(sec.section)+"  ") + s.title.Render(sec.section.String())
		label += strings.Repeat(" ", max(1, reviewLabelWidth-lipgloss.Width(label)))

		if sec.skipped != "" {
			lines = append(lines, s.faint.Render(sectionNumber(sec.section)+"  "+sec.section.String()+
				strings.Repeat(" ", max(1, reviewLabelWidth-3-len(sec.section.String())))+s.g.skipped+" "+sec.skipped))

			continue
		}

		for j, r := range sec.rows {
			if j > 0 {
				label = strings.Repeat(" ", reviewLabelWidth)
			}

			key := s.muted.Render(fmt.Sprintf("%-*s", reviewKeyWidth, r.key))
			lines = append(lines, label+key+s.text.Render(truncate(r.value, valueWidth, s.g.ellipsis)))

			if r.detail != "" {
				lines = append(lines, blankLabel+s.faint.Render(truncate(r.detail, valueWidth, s.g.ellipsis)))
			}
		}
	}

	return lines
}

// actionsView is the row of review buttons, or the sections to edit.
func (m *wizardModel) actionsView() string {
	s := m.ctx.style
	focused, blurred := m.theme.Focused.FocusedButton, m.theme.Focused.BlurredButton

	if m.choosing {
		chips := []string{s.bold.Render("Edit which section?") + "  "}

		for _, sec := range m.editable() {
			style := blurred
			if sec == m.chosen {
				style = focused
			}

			chips = append(chips, style.Render(sectionNumber(sec)+" "+sec.String()))
		}

		return lipgloss.JoinHorizontal(lipgloss.Center, chips...)
	}

	labels := [buttonCount]string{buttonRun: "Run", buttonEdit: "Edit a section", buttonCancel: "Cancel"}
	buttons := make([]string, buttonCount)

	for i, label := range labels {
		style := blurred
		if i == m.button {
			style = focused
		}

		buttons[i] = style.Render(label)
	}

	return lipgloss.JoinHorizontal(lipgloss.Center, buttons...)
}
