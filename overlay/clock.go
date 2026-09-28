package overlay

import (
	"fmt"
	"time"

	"github.com/eko/qc/media"
)

// centis is a time of the script, in centiseconds: ASS times have no finer
// resolution.
type centis int64

// String formats c as an ASS time (h:mm:ss.cc).
func (c centis) String() string {
	const perHour, perMinute, perSecond = 360_000, 6000, 100

	return fmt.Sprintf("%d:%02d:%02d.%02d", c/perHour, c%perHour/perMinute, c%perMinute/perSecond, c%perSecond)
}

// centisecond is the resolution of ASS times.
const centisecond = 10 * time.Millisecond

// clock times the events of the script frame by frame.
//
// ffmpeg's subtitles filter renders a frame with the events active at its
// time t in milliseconds (start ≤ t < end). Frame i must see its own events
// only, so each event boundary is placed halfway between two frames and
// rounded to the centisecond: that stays at least 3 ms away from both
// frames up to 60 fps (11 ms at 30 fps), more than the millisecond lost
// when the filter truncates t, whatever the frame rate (23.976 and 29.97
// included). At 100 fps and more, frames closer than a centisecond cannot
// be told apart.
type clock struct {
	// starts[i] is when frame i starts showing, starts[n] when the last
	// one stops.
	starts []centis
}

// newClock times frames at pts (relative to the first frame) shown for
// duration in all, the first frame being at offset on the timeline the
// filter sees.
func newClock(
	pts []media.Duration,
	duration media.Duration,
	offset media.Duration,
) clock {
	n := len(pts)
	starts := make([]centis, n+1)

	for i := 1; i < n; i++ {
		mid := offset + pts[i-1] + (pts[i]-pts[i-1])/2
		starts[i] = roundCentis(mid)
	}

	end := offset + max(duration, pts[n-1]+media.Duration(centisecond))
	starts[n] = max(ceilCentis(end), starts[n-1]+1)

	return clock{starts: starts}
}

// frames is the number of frames.
func (c clock) frames() int {
	return len(c.starts) - 1
}

// start is when frame i starts showing (i = frames(): when the last frame
// stops).
func (c clock) start(
	i int,
) centis {
	return c.starts[i]
}

// end is when the whole overlay stops showing.
func (c clock) end() centis {
	return c.starts[len(c.starts)-1]
}

func roundCentis(
	d media.Duration,
) centis {
	return centis((d.Std() + centisecond/2) / centisecond)
}

func ceilCentis(
	d media.Duration,
) centis {
	return centis((d.Std() + centisecond - 1) / centisecond)
}

// millis converts c to the milliseconds of animation tags.
func (c centis) millis() int64 {
	return int64(c) * int64(centisecond/time.Millisecond)
}
