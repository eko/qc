package htmlreport

import (
	"fmt"
	"html/template"
	"regexp"
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
	// Spark draws the trend behind the number (bitrate, VMAF, loudness
	// over time), from svg.Sparkline.
	Spark template.HTML
	// Meter places the value on its scale, with its thresholds.
	Meter *meter
}

// meter is a value on a bounded scale, with marks at its thresholds.
type meter struct {
	Value, Min, Max float64
	Marks           []float64
}

// Fill is the share of the scale below the value, in percent.
func (m meter) Fill() float64 {
	return m.At(m.Value)
}

// At is the position of a value on the scale, in percent.
func (m meter) At(
	v float64,
) float64 {
	if m.Max <= m.Min {
		return 0
	}

	return max(0, min(100, (v-m.Min)/(m.Max-m.Min)*100))
}

// vmafMeter places a VMAF on its 0–100 scale, marked at the fair and good
// thresholds of the card tones.
func vmafMeter(
	score float64,
) *meter {
	return &meter{Value: score, Max: maxVMAF, Marks: []float64{fairVMAF, goodVMAF}}
}

// maxVMAF is the top of the VMAF scale.
const maxVMAF = 100

// valueUnit splits a card value into a number and the unit that follows
// it ("5.39 Mb/s", "-34.0 LUFS", "20 / 100"): the unit is set smaller.
var valueUnit = regexp.MustCompile(`^([-+−]?[0-9][0-9.,:×%]*)\s+(\S.*)$`)

// Number is the numeric part of the value, or the whole value.
func (c card) Number() string {
	if m := valueUnit.FindStringSubmatch(c.Value); m != nil {
		return m[1]
	}

	return c.Value
}

// Unit is what follows the number of the value, if anything.
func (c card) Unit() string {
	if m := valueUnit.FindStringSubmatch(c.Value); m != nil {
		return m[2]
	}

	return ""
}

// Long reports a value too long for the large type of the tiles.
func (c card) Long() bool {
	return len([]rune(c.Number())) > maxShortValue
}

// maxShortValue is the longest value set in the large type.
const maxShortValue = 12

// verdict is the overall outcome shown at the top of the page, with the
// counts behind it.
type verdict struct {
	State  findings.Verdict
	Title  string
	Text   string
	Counts []tally
}

// tally is a count of findings of one kind.
type tally struct {
	Class, Label string
	N            int
}

// Class names the verdict for the style.
func (v verdict) Class() string {
	return v.State.String()
}

// Icon is the verdict's symbol.
func (v verdict) Icon() string {
	switch v.State {
	case findings.Fail:
		return iconFail
	case findings.Attention:
		return iconWarn
	}

	return iconPass
}

// judge words the verdict of the findings (findings.Judge) and counts them
// by kind.
func judge(
	list []finding,
) verdict {
	typed := make([]findings.Finding, len(list))
	counts := map[string]int{}

	for i, f := range list {
		typed[i] = findings.Finding{Level: f.Level, Code: f.Code}
		counts[f.Filter()]++
	}

	v := verdict{State: findings.Judge(typed)}
	warnings := counts[filterBlocking] + counts[findings.Warn.String()]

	switch v.State {
	case findings.Fail:
		v.Title = "Fail"
		v.Text = plural(counts[filterBlocking], "blocking issue") + " to fix"
		if other := counts[findings.Warn.String()]; other > 0 {
			v.Text += fmt.Sprintf(", %s to review", plural(other, "other warning"))
		}
	case findings.Attention:
		v.Title, v.Text = "Needs attention", plural(warnings, "warning")+" to review, none blocking"
	default:
		v.Title, v.Text = "Pass", "No warning"
	}

	for _, k := range []struct{ class, label string }{
		{filterBlocking, "blocking"}, {findings.Warn.String(), "warnings"}, {findings.Info.String(), "notes"}, {findings.OK.String(), "passed"},
	} {
		if n := counts[k.class]; n > 0 {
			v.Counts = append(v.Counts, tally{Class: k.class, Label: strings.TrimSuffix(k.label, pluralSuffix(n)), N: n})
		}
	}

	return v
}

// pluralSuffix is the "s" to drop from a plural label counting one.
func pluralSuffix(
	n int,
) string {
	if n == 1 {
		return "s"
	}

	return ""
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
