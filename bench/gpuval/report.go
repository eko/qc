package main

import (
	"fmt"
	"strings"
)

// report accumulates the Markdown report.
type report struct {
	b strings.Builder
}

func (r *report) title(
	text string,
) {
	fmt.Fprintf(&r.b, "# %s\n\n", text)
}

func (r *report) section(
	text string,
) {
	fmt.Fprintf(&r.b, "\n## %s\n\n", text)
}

func (r *report) line(
	format string,
	args ...any,
) {
	fmt.Fprintf(&r.b, format+"\n", args...)
}

// table writes a Markdown table.
func (r *report) table(
	head []string,
	rows [][]string,
) {
	r.b.WriteString("\n| " + strings.Join(head, " | ") + " |\n")
	r.b.WriteString("|" + strings.Repeat("---|", len(head)) + "\n")

	for _, row := range rows {
		r.b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}

	r.b.WriteString("\n")
}

func (r *report) String() string {
	return r.b.String()
}
