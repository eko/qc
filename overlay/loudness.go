package overlay

import (
	"fmt"
	"math"

	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/loudness"
)

// loudnessTrack is the audio track the loudness row shows: the default
// track when it was analysed, else the first one; nil without audio.
func loudnessTrack(
	in Input,
) *audio.Track {
	r := in.Report
	if r.Audio == nil || len(r.Audio.Tracks) == 0 {
		return nil
	}

	if r.Info != nil {
		if i := r.Info.DefaultAudio(); i >= 0 {
			for n := range r.Audio.Tracks {
				if r.Audio.Tracks[n].Stream == r.Info.Audio[i].Index {
					return &r.Audio.Tracks[n]
				}
			}
		}
	}

	return &r.Audio.Tracks[0]
}

// loudnessRow is the short-term and momentary loudness at frame i: the
// meters' last values before the frame is shown.
func (t *title) loudnessRow(
	i int,
) string {
	series := t.loudness.Loudness.Series
	step := int(math.Floor((t.pts[i]-t.loudness.Start).Seconds()/loudness.Step)) - 1

	if step < 0 || step >= len(series.ShortTerm) || step >= len(series.Momentary) {
		return label("LUFS") + "–"
	}

	return label("LUFS") + fmt.Sprintf("S %-7s", lufs(series.ShortTerm[step])) + colour(colourMuted) + "M " + colour(colourWhite) + lufs(series.Momentary[step])
}

// lufs formats a loudness to the tenth, a dash for silence.
func lufs(
	v float64,
) string {
	if v <= loudness.Floor {
		return "–"
	}

	return fmt.Sprintf("%.1f", v)
}
