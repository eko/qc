package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/eko/qc/ladder"
)

// Per-shot ladder layout.
const (
	// maxShotLadderRows bounds the shots listed: longer titles list their most
	// and least expensive shots, half each, in time order.
	maxShotLadderRows = 16
	// shotCellWidth is a rung's cell: CRF and predicted bitrate, then
	// shotVMAFWidth more for the predicted VMAF when the terminal is wide
	// enough.
	shotCellWidth = 10
	shotVMAFWidth = 6
	// shotHeightWidth holds a shot's height (per-shot resolution).
	shotHeightWidth = 5
	// shotCellGap separates the rung cells.
	shotCellGap = 2
)

// shotRung is a per-shot rung of the ladder with its title-wide pooled
// predicted bitrate, which shot costs are relative to.
type shotRung struct {
	index  int
	rung   ladder.Rung
	pooled float64
}

// perShotLadder is the per-shot ladder: for each shot, its time range,
// complexity and cost, and for each per-shot rung the shot's CRF, predicted
// bitrate and (when the width allows) predicted VMAF.
func perShotLadder(
	res *ladder.Result,
	width int,
) string {
	rungs := shotRungs(res)
	if len(rungs) == 0 {
		return ""
	}

	times := make([]string, len(res.Shots))
	timeWidth := 0

	for i, s := range res.Shots {
		iv := res.ShotInterval(s)
		// Shots start on the GOP grid: whole seconds read well, and the
		// HTML report and JSON keep the exact times.
		times[i] = Clock(iv.Start, true) + "–" + Clock(iv.End, true)
		timeWidth = max(timeWidth, len([]rune(times[i])))
	}

	layout := shotLayoutFor(timeWidth, len(rungs), width, res.ShotProbing != nil && res.ShotProbing.Resolution)

	var head strings.Builder

	head.WriteString(layout.prefix("#", " ", "time", "source", "TI", "cost"))

	for _, r := range rungs {
		fmt.Fprintf(&head, "%*s%-*s", shotCellGap, "", layout.cell(), fmt.Sprintf("#%d %dp", r.index+1, r.rung.Height))
	}

	costs := shotCosts(res.Shots, rungs)
	lines := []string{section.Render("Per-shot ladder") + Subtle.Render(shotLadderScope(len(res.Shots))), Subtle.Render(head.String())}
	previous := -1

	for _, i := range shownShots(costs) {
		if skipped := i - previous - 1; skipped > 0 {
			lines = append(lines, Subtle.Render(fmt.Sprintf("  %3s %d shot(s)", "⋯", skipped)))
		}

		previous = i
		lines = append(lines, shotRow(res.Shots[i], i, times[i], costs[i], rungs, layout))
	}

	if skipped := len(res.Shots) - previous - 1; skipped > 0 {
		lines = append(lines, Subtle.Render(fmt.Sprintf("  %3s %d shot(s)", "⋯", skipped)))
	}

	note := "  per rung: CRF · predicted bitrate"
	if layout.resolution {
		note += " · resolution"
	}

	if layout.vmaf {
		note += " · predicted VMAF"
	}

	note += "; ● shot measured on the digest, ○ predicted from similar shots; cost: bitrate over the rung's average"

	return strings.Join(append(lines, Subtle.Render(note)), "\n")
}

// shotLayout sizes the per-shot ladder to the terminal: the predicted VMAF,
// then the shots' complexity, give way when the rungs do not fit.
type shotLayout struct {
	timeWidth  int
	complexity bool
	vmaf       bool
	// resolution adds each shot's height to its cells (per-shot resolution).
	resolution bool
}

// shotLayoutFor picks the richest layout fitting width.
func shotLayoutFor(
	timeWidth, rungs, width int,
	resolution bool,
) shotLayout {
	for _, l := range []shotLayout{
		{timeWidth: timeWidth, complexity: true, vmaf: true, resolution: resolution},
		{timeWidth: timeWidth, complexity: true, resolution: resolution},
	} {
		if len([]rune(l.prefix("#", " ", "time", "source", "TI", "cost")))+rungs*(l.cell()+shotCellGap) <= width {
			return l
		}
	}

	return shotLayout{timeWidth: timeWidth, resolution: resolution}
}

// prefix lays out the columns describing a shot.
func (l shotLayout) prefix(
	index, marker, time, source, ti, cost string,
) string {
	if !l.complexity {
		return fmt.Sprintf("  %3s %s %-*s %6s", index, marker, l.timeWidth, time, cost)
	}

	return fmt.Sprintf("  %3s %s %-*s %6s %5s %6s", index, marker, l.timeWidth, time, source, ti, cost)
}

// cell is the width of a rung's cell.
func (l shotLayout) cell() int {
	width := shotCellWidth
	if l.resolution {
		width += shotHeightWidth
	}

	if l.vmaf {
		width += shotVMAFWidth
	}

	return width
}

// shotRungs are the rungs carrying a per-shot allocation of every shot.
func shotRungs(
	res *ladder.Result,
) []shotRung {
	var out []shotRung

	for i, r := range res.Rungs {
		if r.PerShot == nil || len(res.Shots) == 0 || len(r.PerShot.Shots) != len(res.Shots) {
			continue
		}

		if pooled := r.PerShot.PooledBitrate(res.Shots); pooled > 0 {
			out = append(out, shotRung{index: i, rung: r, pooled: pooled})
		}
	}

	return out
}

// shotCosts are the shots' predicted bitrates relative to their rung's
// average, averaged over the rungs.
func shotCosts(
	shots []ladder.Shot,
	rungs []shotRung,
) []float64 {
	costs := make([]float64, len(shots))

	for i := range shots {
		for _, r := range rungs {
			costs[i] += float64(r.rung.PerShot.Shots[i].PredictedBitrate) / r.pooled
		}

		costs[i] /= float64(len(rungs))
	}

	return costs
}

// shownShots are the indices of the listed shots, in time order: all of
// them, or the maxShotLadderRows/2 most and least expensive.
func shownShots(
	costs []float64,
) []int {
	order := make([]int, len(costs))
	for i := range order {
		order[i] = i
	}

	if len(costs) <= maxShotLadderRows {
		return order
	}

	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(costs[b], costs[a]) })

	half := maxShotLadderRows / 2
	shown := slices.Concat(order[:half], order[len(order)-half:])
	slices.Sort(shown)

	return shown
}

// shotLadderScope says which shots the table lists.
func shotLadderScope(
	shots int,
) string {
	if shots <= maxShotLadderRows {
		return fmt.Sprintf("  (%d shots × rungs)", shots)
	}

	return fmt.Sprintf("  (%d of %d shots: the %d most and %d least expensive; all of them in the JSON and HTML reports)",
		maxShotLadderRows, shots, maxShotLadderRows/2, maxShotLadderRows/2)
}

// shotRow is one shot of the per-shot ladder.
func shotRow(
	s ladder.Shot,
	i int,
	time string,
	cost float64,
	rungs []shotRung,
	layout shotLayout,
) string {
	marker, source, ti := "○", "–", "–"
	if s.Measured {
		marker = "●"
	}

	if s.SourceBitrate > 0 {
		source = compactBitrate(float64(s.SourceBitrate))
	}

	if s.TI > 0 {
		ti = fmt.Sprintf("%.1f", s.TI)
	}

	var line strings.Builder

	line.WriteString(layout.prefix(strconv.Itoa(i+1), marker, time, source, ti, fmt.Sprintf("×%.2f", cost)))

	for _, r := range rungs {
		a := r.rung.PerShot.Shots[i]
		fmt.Fprintf(&line, "%*s%4.1f %5s", shotCellGap, "", a.CRF, compactBitrate(float64(a.PredictedBitrate)))

		if layout.resolution {
			fmt.Fprintf(&line, " %4s", heightLabel(a.Height, r.rung.Height))
		}

		if layout.vmaf {
			fmt.Fprintf(&line, " %5.1f", a.PredictedVMAF)
		}
	}

	return line.String()
}

// heightLabel is a shot's height in a per-shot resolution cell: the
// rung's own reads "·", so that resolution moves stand out.
func heightLabel(
	height, rung int,
) string {
	if height == 0 || height == rung {
		return "·"
	}

	return strconv.Itoa(height)
}
