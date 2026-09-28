package htmlreport

import (
	"html/template"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/internal/findings"
)

func TestJudge(
	t *testing.T,
) {
	var (
		blocking = finding{Level: findings.Warn, Code: findings.SilentTrack, Blocking: true}
		warning  = finding{Level: findings.Warn, Code: findings.PeakBitrate}
		note     = finding{Level: findings.Info, Code: findings.Fallback}
		passed   = finding{Level: findings.OK, Code: findings.NoBanding}
	)

	testCases := []struct {
		name  string
		list  []finding
		want  verdict
		class string
		icon  string
	}{
		{
			name:  "nothing to say",
			want:  verdict{State: findings.Pass, Title: "Pass", Text: "No warning"},
			class: "pass", icon: iconPass,
		},
		{
			name: "notes and passed checks",
			list: []finding{note, passed, passed},
			want: verdict{State: findings.Pass, Title: "Pass", Text: "No warning", Counts: []tally{
				{Class: "info", Label: "note", N: 1}, {Class: "ok", Label: "passed", N: 2},
			}},
			class: "pass", icon: iconPass,
		},
		{
			name: "warnings",
			list: []finding{warning, warning, note},
			want: verdict{State: findings.Attention, Title: "Needs attention", Text: "2 warnings to review, none blocking", Counts: []tally{
				{Class: "warn", Label: "warnings", N: 2}, {Class: "info", Label: "note", N: 1},
			}},
			class: "attention", icon: iconWarn,
		},
		{
			name: "a blocking finding alone",
			list: []finding{blocking},
			want: verdict{State: findings.Fail, Title: "Fail", Text: "1 blocking issue to fix", Counts: []tally{
				{Class: "blocking", Label: "blocking", N: 1},
			}},
			class: "fail", icon: iconFail,
		},
		{
			name: "blocking and other warnings",
			list: []finding{warning, blocking, blocking},
			want: verdict{State: findings.Fail, Title: "Fail", Text: "2 blocking issues to fix, 1 other warning to review", Counts: []tally{
				{Class: "blocking", Label: "blocking", N: 2}, {Class: "warn", Label: "warning", N: 1},
			}},
			class: "fail", icon: iconFail,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := judge(testCase.list)
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.class, got.Class())
			assert.Equal(t, testCase.icon, got.Icon())
		})
	}
}

func TestFindingKind(
	t *testing.T,
) {
	testCases := []struct {
		name                string
		finding             finding
		filter, label, icon string
	}{
		{name: "blocking", finding: finding{Level: findings.Warn, Blocking: true}, filter: "blocking", label: "Blocking", icon: iconFail},
		{name: "warning", finding: finding{Level: findings.Warn}, filter: "warn", label: "Warning", icon: iconWarn},
		{name: "note", finding: finding{Level: findings.Info}, filter: "info", label: "Note", icon: iconInfo},
		{name: "passed", finding: finding{Level: findings.OK}, filter: "ok", label: "Passed", icon: iconPass},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.filter, testCase.finding.Filter())
			assert.Equal(t, testCase.label, testCase.finding.LevelLabel())
			assert.Equal(t, testCase.icon, testCase.finding.Icon())
		})
	}

	// The worded findings carry the blocking rule of package findings.
	w := worded(findings.Finding{Level: findings.Warn, Code: findings.TruePeakOver}, nil, "over")
	assert.True(t, w.Blocking)
	assert.Equal(t, findings.TruePeakOver, w.Code)
}

func TestCardValue(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		value        string
		number, unit string
		long         bool
	}{
		{name: "bitrate", value: "5.39 Mb/s", number: "5.39", unit: "Mb/s"},
		{name: "negative loudness", value: "-34.0 LUFS", number: "-34.0", unit: "LUFS"},
		{name: "a ratio", value: "20 / 100", number: "20", unit: "/ 100"},
		{name: "a share and a word", value: "39% pan", number: "39%", unit: "pan"},
		{name: "a clock", value: "1:02:03.50", number: "1:02:03.50"},
		{name: "a resolution", value: "1920×1080", number: "1920×1080"},
		{name: "a name", value: "vmaf_v0.6.1", number: "vmaf_v0.6.1"},
		{name: "a long name", value: "vmaf_v1.0.16_5d0h_phone", number: "vmaf_v1.0.16_5d0h_phone", long: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c := card{Value: testCase.value}
			assert.Equal(t, testCase.number, c.Number())
			assert.Equal(t, testCase.unit, c.Unit())
			assert.Equal(t, testCase.long, c.Long())
		})
	}
}

func TestMeter(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		meter meter
		fill  float64
		at    float64
	}{
		{name: "vmaf", meter: *vmafMeter(91), fill: 91, at: 75},
		{name: "clamped", meter: meter{Value: 120, Max: 100}, fill: 100, at: 75},
		{name: "an empty scale", meter: meter{Value: 1, Min: 5, Max: 5}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.InDelta(t, testCase.fill, testCase.meter.Fill(), 1e-9)
			assert.InDelta(t, testCase.at, testCase.meter.At(fairVMAF), 1e-9)
		})
	}
}

func TestPageLists(
	t *testing.T,
) {
	p := page{
		Subtitle: "h264 · 1920×1080",
		Findings: []finding{{Level: findings.Warn, Text: "w"}, {Level: findings.Info, Text: "i"}, {Level: findings.OK, Text: "ok"}},
	}

	assert.Equal(t, []string{"h264", "1920×1080"}, p.Meta())
	assert.Nil(t, page{}.Meta())
	assert.Equal(t, []finding{p.Findings[0], p.Findings[1]}, p.Issues())
	assert.Equal(t, []finding{p.Findings[2]}, p.Passed())
	assert.Len(t, p.Findings, 3, "the lists leave the findings as they are")
}

func TestAreas(
	t *testing.T,
) {
	p := page{
		Sections: []section{
			{Title: "Bitrate", Area: areaVideo, Topic: findings.TopicBitrate, Charts: []template.HTML{"x"}},
			{Title: "Complexity", Area: areaVideo},
			{Title: "Rate / quality", Area: ladderArea("h264")},
			{Title: "Rate / quality", Area: ladderArea("av1")},
			{Title: "Encoding commands", Area: areaEncoding},
		},
		Findings: []finding{
			{Level: findings.Warn, Topic: findings.TopicBitrate}, {Level: findings.Info, Topic: findings.TopicBitrate},
			{Level: findings.Warn, Topic: findings.TopicQuality},
		},
	}

	p.link()

	var titles, anchors []string
	for _, a := range p.Areas {
		titles, anchors = append(titles, a.Title), append(anchors, a.Anchor)
	}

	assert.Equal(t, []string{"Video", "h264 ladder", "av1 ladder", "Encoding"}, titles)
	assert.Equal(t, []string{"a-video", "a-h264-ladder", "a-av1-ladder", "a-encoding"}, anchors)
	assert.Len(t, p.Areas[0].Sections, 2)
	assert.Equal(t, "s-rate-quality-2", p.Areas[2].Sections[0].Anchor)
	assert.Equal(t, 1, p.Areas[0].Warnings, "warnings linking to the area's sections, not its notes")
	assert.Equal(t, 1, p.Areas[0].Sections[0].Warnings)
	assert.Equal(t, "Bitrate", p.Findings[0].Where)
	assert.Empty(t, p.Findings[2].Where, "no section shows the finding")
	assert.Contains(t, string(icon(iconVideo)), `<svg class="i i-video" viewBox="0 0 24 24" aria-hidden="true"><rect`)
}
