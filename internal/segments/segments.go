// Package segments turns per-frame flags into time intervals.
package segments

import "github.com/eko/qc/media"

// Detect returns the runs of consecutive flagged frames lasting at least
// minLength. pts[i] is the presentation time of frame i (one entry per flag)
// and end the end time of the last frame.
func Detect(
	flags []bool,
	pts []media.Duration,
	end media.Duration,
	minLength media.Duration,
) []media.Interval {
	var (
		out   []media.Interval
		start = -1
	)

	closeRun := func(i int) {
		stop := end
		if i < len(pts) {
			stop = pts[i]
		}

		if run := (media.Interval{Start: pts[start], End: stop}); run.Length() >= minLength {
			out = append(out, run)
		}

		start = -1
	}

	for i, flagged := range flags {
		switch {
		case flagged && start < 0:
			start = i
		case !flagged && start >= 0:
			closeRun(i)
		}
	}

	if start >= 0 {
		closeRun(len(flags))
	}

	return out
}
