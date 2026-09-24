package htmlreport

import (
	"fmt"
	"io"

	"github.com/eko/qc/pipeline"
)

// RenderRun writes the HTML report of a pipeline run: every result on one
// page.
func RenderRun(
	w io.Writer,
	r *pipeline.Report,
) error {
	p := runPage(r)
	p.Kind = "Full run"
	p.summarize(runSummary(r))

	return render(w, p)
}

// runPage stacks the pages of every result of a pipeline run. The first
// section of each embedded page carries that page's title.
func runPage(
	r *pipeline.Report,
) page {
	p := analyzePage(r.Analysis)
	p.Subtitle += " · " + r.Elapsed

	if r.Comparison != nil {
		c := comparisonPage(r.Comparison)
		c.Sections[0].Title = c.Title + " · " + c.Subtitle
		p.Sections = append(p.Sections, c.Sections...)
	}

	for _, l := range r.Ladders {
		lp := ladderPage(l)
		lp.Sections[0].Title = lp.Title
		p.Sections = append(p.Sections, lp.Sections...)
	}

	return p
}

// runSummary combines the summaries of a pipeline run, each finding scoped
// to its report.
func runSummary(
	r *pipeline.Report,
) ([]card, []finding) {
	cards, list := analysisCards(r.Analysis), scoped("Source", analysisFindings(r.Analysis))

	if c := r.Comparison; c != nil {
		cards = append(cards, comparisonCards(c.VMAF)[0])
		list = append(list, scoped("VMAF", comparisonFindings(c.VMAF))...)
	}

	for _, l := range r.Ladders {
		name := l.Codec.Name + " ladder"
		c := card{Label: name, Value: fmt.Sprintf("%d rungs", len(l.Rungs))}

		if len(l.Rungs) > 0 {
			top := l.Rungs[0]
			c.Detail = fmt.Sprintf("top %s · VMAF %.1f", bitrateLabel(float64(top.Bitrate)), top.PredictedVMAF)
		}

		cards = append(cards, c)
		list = append(list, scoped(name, ladderFindings(l))...)
	}

	return cards, list
}

func scoped(
	scope string,
	list []finding,
) []finding {
	for i := range list {
		list[i].Scope = scope
	}

	return list
}
