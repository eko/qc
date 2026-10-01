package htmlreport

import (
	"fmt"
	"io"
	"slices"

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

// runPage stacks the pages of every result of a pipeline run, in areas: the
// video and audio of the analysis, the quality, one area per ladder, then
// the encoding commands of every ladder together, collapsed.
func runPage(
	r *pipeline.Report,
) page {
	p := analyzePage(r.Analysis)
	p.Subtitle += " · " + r.Elapsed

	if r.Comparison != nil {
		c := comparisonPage(r.Comparison)
		for i := range c.Sections {
			c.Sections[i].Area.Subtitle = c.Title + " · " + c.Subtitle
		}

		p.Sections = append(p.Sections, c.Sections...)
	}

	var commands []section

	for _, l := range r.Ladders {
		lp := ladderPage(l)

		for _, s := range lp.Sections {
			if s.Area == areaEncoding {
				s.Title = l.Codec.Name + " encoding commands"
				commands = append(commands, s)

				continue
			}

			s.Area.Subtitle = lp.Subtitle
			p.Sections = append(p.Sections, s)
		}
	}

	p.Sections = slices.Concat(p.Sections, commands)

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
			// The top rung as verified, when it was: what the ladder
			// delivers, and what ladders are compared on.
			top := l.Rungs[0]
			vmaf := top.PredictedVMAF

			if top.Measured != nil {
				vmaf = top.Measured.VMAF
			}

			c.Detail = fmt.Sprintf("top %s · VMAF %.1f", bitrateLabel(float64(rungBitrate(top))), vmaf)
			c.Meter = vmafMeter(vmaf)
		}

		cards = append(cards, c)
		list = append(list, scoped(name, ladderFindings(l))...)
	}

	return cards, append(list, scoped("Codecs", codecFindings(r.Ladders))...)
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
