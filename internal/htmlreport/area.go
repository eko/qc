package htmlreport

import (
	"strconv"

	"github.com/eko/qc/internal/findings"
)

// area is a part of the report — the video, the audio, the quality, a
// ladder, the encoding commands — under which the navigation and the page
// list its sections: a combined run with several ladders stays readable.
type area struct {
	Title    string
	Subtitle string
	Icon     string
}

// The areas of the report kinds; a ladder's area is named after its codec.
var (
	areaVideo    = area{Title: "Video", Icon: iconVideo}
	areaAudio    = area{Title: "Audio", Icon: iconAudio}
	areaQuality  = area{Title: "Quality", Icon: iconQuality}
	areaEncoding = area{Title: "Encoding", Subtitle: "commands producing every rung", Icon: iconEncoding}
)

// ladderArea is the area of a codec's ladder.
func ladderArea(
	codec string,
) area {
	return area{Title: codec + " ladder", Icon: iconLadder}
}

// areaView is an area as the page renders it: its sections, its anchor and
// the warnings linking to it.
type areaView struct {
	area

	Anchor   string
	Sections []section
	Warnings int
}

// anchors hands out unique ids: a repeated name gets a numbered suffix.
type anchors map[string]int

// newAnchors reserves the ids the page template uses.
func newAnchors() anchors {
	return anchors{"top": 1, "findings": 1, "main": 1}
}

// take returns a unique id for name.
func (a anchors) take(
	prefix, name string,
) string {
	id := prefix + slug(name)
	if a[id]++; a[id] > 1 {
		id += "-" + strconv.Itoa(a[id])
	}

	return id
}

// link gives every section and area a unique anchor, every finding the
// anchor and title of the first section of its topic showing a chart, and
// every section and area the count of warnings linking to it.
func (p *page) link() {
	ids := newAnchors()
	topics := map[findings.Topic]int{}

	for i := range p.Sections {
		s := &p.Sections[i]
		s.Anchor = ids.take("s-", s.Title)

		if _, ok := topics[s.Topic]; s.Topic != "" && !ok && len(s.Charts) > 0 {
			topics[s.Topic] = i
		}
	}

	for i := range p.Findings {
		f := &p.Findings[i]
		if at, ok := topics[f.Topic]; ok {
			f.Anchor, f.Where = p.Sections[at].Anchor, p.Sections[at].Title

			if f.Level == findings.Warn {
				p.Sections[at].Warnings++
			}
		}
	}

	p.Areas = nil

	for _, s := range p.Sections {
		if n := len(p.Areas); n == 0 || p.Areas[n-1].Title != s.Area.Title {
			p.Areas = append(p.Areas, areaView{area: s.Area, Anchor: ids.take("a-", s.Area.Title)})
		}

		last := &p.Areas[len(p.Areas)-1]
		last.Sections = append(last.Sections, s)
		last.Warnings += s.Warnings
	}
}
