// Package findings holds the rules that turn a report into noteworthy facts:
// the thresholds, the level of each fact, the time ranges and the rungs it
// concerns. Every presenter (the terminal reports, the HTML report) reads
// the same findings, so a threshold changes in one place.
//
// A Finding is a typed value, not a sentence: its wording belongs to the
// presenter, which words it for its medium (a terminal line listing every
// segment, an HTML item linking the first ones to a chart) and reads the
// details it shows (a rung's height, a crop rectangle) from the report.
package findings

import (
	"cmp"
	"slices"

	"github.com/eko/qc/media"
)

// Level is the weight of a finding, most severe first.
type Level int

// Levels of findings.
const (
	// Warn is an issue worth acting on.
	Warn Level = iota
	// Info is neutral context on how the result was obtained.
	Info
	// OK is a check that passed.
	OK
)

// String is the lower-case name of the level: warn, info or ok.
func (l Level) String() string {
	switch l {
	case Warn:
		return "warn"
	case Info:
		return "info"
	}

	return "ok"
}

// Topic is the kind of data a finding is about, for presenters that link a
// finding to the chart showing its time ranges.
type Topic string

// Topics of findings; empty when no chart shows the finding.
const (
	TopicBitrate    Topic = "bitrate"
	TopicComplexity Topic = "complexity"
	TopicQuality    Topic = "quality"
	TopicBanding    Topic = "banding"
)

// Code identifies the rule behind a finding. The documentation of each code
// says which fields of the Finding it sets.
type Code string

// Finding is a noteworthy fact of a report.
type Finding struct {
	Level Level
	Code  Code
	Topic Topic
	// Spans are the time ranges the finding concerns, in the order of the
	// report.
	Spans []media.Interval
	// Index is the rung (an index of ladder.Result.Rungs) or the frame
	// (quality.FrameScore.Index) the finding concerns; Other is the second
	// rung of a rank conflict.
	Index, Other int
	// Value is the measured quantity and Limit the threshold or target it
	// was checked against.
	Value, Limit float64
	// Text is a message of the measurement itself (a fallback reason) or
	// a name (a field order).
	Text string
}

// SortByLevel orders findings by level, most severe first, keeping the
// order of the report within a level.
func SortByLevel(
	findings []Finding,
) {
	slices.SortStableFunc(findings, func(a, b Finding) int { return cmp.Compare(a.Level, b.Level) })
}
