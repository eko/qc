package overlay

import (
	"cmp"
	"path/filepath"
	"slices"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// bitrateWindow is the window of the bitrate shown for each frame: the
// bits of the frames of the last second, the usual unit of bitrates.
const bitrateWindow = media.Duration(time.Second)

// title is the input of the overlay reshaped frame by frame. Every
// per-frame column has one value per frame of the bitstream, in
// presentation order; the frame analysis columns may be shorter (or nil),
// and are read through its accessors.
type title struct {
	name     string
	width    int
	height   int
	pts      []media.Duration
	sizes    []int
	keys     []bool
	duration media.Duration
	// offset is the time of the first frame on the timeline ffmpeg's
	// filters see (see bitstream.Report.Start).
	offset media.Duration
	// bitrate is the bitrate over the window ending at each frame.
	bitrate []float64
	frames  *analysis.FrameSeries
	video   *analysis.VideoReport
	levels  media.Levels
	// shot is the index of the shot of each frame, -1 outside any shot.
	shot []int
	// score is the index in quality.Frames of each frame's score, -1 for
	// frames that were not scored.
	score   []int
	quality *quality.Result
	// loudness is the audio track of the loudness row (nil without
	// audio).
	loudness *audio.Track
}

// newTitle reshapes in; it fails without a frame timeline.
func newTitle(
	in Input,
) (*title, error) {
	report := in.Report
	if report == nil || report.Bitstream == nil || len(report.Bitstream.PTS) == 0 {
		return nil, ErrNoFrames
	}

	bs := report.Bitstream
	t := &title{
		pts:      bs.PTS,
		sizes:    bs.FrameSizes,
		keys:     bs.KeyFlags,
		duration: bs.Duration,
		frames:   report.Frames,
		video:    report.Video,
		quality:  in.Quality,
	}

	if info := report.Info; info != nil {
		t.name = filepath.Base(info.Path)
		t.offset = max(bs.Start-info.StartTime, 0)

		if v, ok := info.PrimaryVideo(); ok {
			t.width, t.height = v.Width, v.Height
			t.levels = media.LevelsFor(v.Color.Range)
		}
	}

	if t.video != nil {
		t.levels = t.video.Levels.Levels
	}

	t.loudness = loudnessTrack(in)
	t.bitrate = slidingBitrate(t.pts, t.sizes)
	t.shot = t.shotIndexes()
	t.score = t.scoreIndexes()

	return t, nil
}

// n is the number of frames.
func (t *title) n() int {
	return len(t.pts)
}

// slidingBitrate is, for each frame, the bits of the frames of the window
// ending with it, per second: the usual "bits in the last second", which
// ramps up over the first second of the title.
func slidingBitrate(
	pts []media.Duration,
	sizes []int,
) []float64 {
	out := make([]float64, len(pts))
	sum, first := 0, 0

	for i := range pts {
		if i < len(sizes) {
			sum += sizes[i]
		}

		for pts[i]-pts[first] >= bitrateWindow {
			if first < len(sizes) {
				sum -= sizes[first]
			}

			first++
		}

		out[i] = float64(sum*8) / bitrateWindow.Seconds()
	}

	return out
}

// shotIndexes maps every frame to its shot.
func (t *title) shotIndexes() []int {
	out := make([]int, t.n())
	for i := range out {
		out[i] = -1
	}

	if t.video == nil {
		return out
	}

	for s, shot := range t.video.Shots {
		for i := max(shot.FirstFrame, 0); i <= shot.LastFrame && i < len(out); i++ {
			out[i] = s
		}
	}

	return out
}

// scoreIndexes maps every frame to its score: the score whose time is the
// closest to the frame's, when it is closer than half a frame. Scores are
// timed on the reference, whose timeline the compared video shares.
func (t *title) scoreIndexes() []int {
	out := make([]int, t.n())
	for i := range out {
		out[i] = -1
	}

	if t.quality == nil {
		return out
	}

	for s, score := range t.quality.Frames {
		i, _ := slices.BinarySearch(t.pts, score.PTS)
		best := nearest(t.pts, i, score.PTS)

		if best >= 0 && 2*absDuration(t.pts[best]-score.PTS) <= t.frameDuration(best) {
			out[best] = s
		}
	}

	return out
}

// nearest returns the index of the time of pts closest to d, i being where
// d would be inserted.
func nearest(
	pts []media.Duration,
	i int,
	d media.Duration,
) int {
	switch {
	case i == 0:
		return 0
	case i >= len(pts):
		return len(pts) - 1
	case d-pts[i-1] < pts[i]-d:
		return i - 1
	}

	return i
}

// frameDuration is how long frame i shows: until the next frame, or the
// previous interval for the last one.
func (t *title) frameDuration(
	i int,
) media.Duration {
	switch {
	case i+1 < t.n():
		return t.pts[i+1] - t.pts[i]
	case i > 0:
		return t.pts[i] - t.pts[i-1]
	}

	return cmp.Or(t.duration, bitrateWindow)
}

func absDuration(
	d media.Duration,
) media.Duration {
	if d < 0 {
		return -d
	}

	return d
}

// column returns frame i of a frame analysis column, and whether it has
// one.
func column(
	values []float64,
	i int,
) (float64, bool) {
	if i < len(values) {
		return values[i], true
	}

	return 0, false
}

// inSegments reports whether time d is in one of the intervals.
func inSegments(
	segments []media.Interval,
	d media.Duration,
) bool {
	for _, s := range segments {
		if d >= s.Start && d < s.End {
			return true
		}
	}

	return false
}
