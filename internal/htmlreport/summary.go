package htmlreport

import (
	"fmt"
	"strings"

	"github.com/eko/qc/internal/findings"
)

// Card tones colour the key numbers of the page header.
const (
	toneGood = "good"
	toneWarn = "warn"
)

// card is a key number of the page header.
type card struct {
	Label, Value, Detail string
	Tone                 string
	// Href makes the card a link (to the findings).
	Href string
}

// findingsCard counts the findings, linking to their list.
func findingsCard(
	list []finding,
) card {
	counts := map[findings.Level]int{}
	for _, f := range list {
		counts[f.Level]++
	}

	c := card{Label: "Findings", Value: "All clear", Tone: toneGood, Href: "#findings"}
	if n := counts[findings.Warn]; n > 0 {
		c.Value, c.Tone = plural(n, "warning"), toneWarn
	}

	var detail []string
	if n := counts[findings.Info]; n > 0 {
		detail = append(detail, plural(n, "note"))
	}

	if n := counts[findings.OK]; n > 0 {
		detail = append(detail, fmt.Sprintf("%d passed", n))
	}

	c.Detail = strings.Join(detail, " · ")

	if len(list) == 0 {
		c.Value, c.Href, c.Tone = "None", "", ""
	}

	return c
}

// vmafTone colours a VMAF: good from goodVMAF, a warning under fairVMAF.
func vmafTone(
	score float64,
) string {
	switch {
	case score >= goodVMAF:
		return toneGood
	case score < fairVMAF:
		return toneWarn
	}

	return ""
}

// goodVMAF and fairVMAF colour the VMAF cards.
const (
	goodVMAF = 90
	fairVMAF = 75
)
