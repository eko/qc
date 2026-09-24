package htmlreport

import (
	"fmt"
	"time"

	"github.com/eko/qc/internal/htmlreport/svg"
	"github.com/eko/qc/media"
)

// durations are the times of a series, in seconds.
func durations(
	ds []media.Duration,
) []float64 {
	out := make([]float64, len(ds))
	for i, d := range ds {
		out[i] = d.Seconds()
	}

	return out
}

// clock is m:ss.ss, or h:mm:ss.ss from an hour on.
func clock(
	d media.Duration,
) string {
	t := d.Std()
	if t >= time.Hour {
		return fmt.Sprintf("%d:%02d:%05.2f", int(t.Hours()), int(t.Minutes())%60, (t % time.Minute).Seconds())
	}

	return fmt.Sprintf("%d:%05.2f", int(t.Minutes()), (t % time.Minute).Seconds())
}

// bitrateLabel writes a bitrate as the chart tooltips do.
func bitrateLabel(
	bps float64,
) string {
	return svg.UnitBitrate.Format(bps)
}

// bytesLabel writes a size as the chart tooltips do.
func bytesLabel(
	b float64,
) string {
	return svg.UnitBytes.Format(b)
}

// floor10 is the bottom of a VMAF axis showing v with some room below.
func floor10(
	v float64,
) float64 {
	return max(0, float64(int(v/10)*10)-10)
}

func nonEmpty(
	s ...string,
) []string {
	var out []string

	for _, v := range s {
		if v != "" {
			out = append(out, v)
		}
	}

	return out
}

func plural(
	n int,
	word string,
) string {
	if n == 1 {
		return "1 " + word
	}

	return fmt.Sprintf("%d %ss", n, word)
}
