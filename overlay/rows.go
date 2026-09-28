package overlay

import (
	"fmt"
	"strconv"
)

// cutMarker is how long the shot row marks a cut, in seconds: long enough
// to be read at playback speed.
const cutMarker = 0.5

// leftRows are the rows of the frame panel, for the items shown and the
// data the title has.
func (t *title) leftRows(
	opts Options,
) []row {
	var rows []row

	add := func(item Item, available bool, r ...row) {
		if !opts.has(item) || !available {
			return
		}

		for _, one := range r {
			one.item = item
			rows = append(rows, one)
		}
	}

	f := t.frames
	add(ItemTime, true, row{height: bigRow, text: t.timeRow}, row{height: rowHeight, text: t.sizeRow})
	add(ItemBitrate, true, row{height: rowHeight, text: t.rateRow})
	add(ItemShots, t.video != nil && len(t.video.Shots) > 0, row{height: rowHeight, text: t.shotRow})
	add(ItemMotion, t.hasMotion(), row{height: rowHeight, text: t.cameraRow}, row{height: rowHeight, text: t.moveRow})
	add(ItemSITI, f != nil && len(f.SI) > 0, row{height: rowHeight, text: t.sitiRow})
	add(ItemLevels, f != nil && len(f.LumaMean) > 0, row{height: rowHeight, text: t.lumaRow})
	add(ItemHDR, f != nil && len(f.PeakNits) > 0, row{height: rowHeight, text: t.lightRow})
	add(ItemLoudness, t.loudness != nil, row{height: rowHeight, text: t.loudnessRow})

	return rows
}

// timeRow is the timecode (from the first frame) and the frame number
// (from 0, as ffmpeg and the JSON reports count them).
func (t *title) timeRow(
	i int,
) string {
	return fmt.Sprintf(`{\fs%d\b1}%s{\fs%d\b0}%s  #%d`, fontBig, timecode(t.pts[i]), fontText, colour(colourMuted), i)
}

// sizeRow is the size of the frame's packet, and whether it is a
// keyframe.
func (t *title) sizeRow(
	i int,
) string {
	size := "–"
	if i < len(t.sizes) {
		size = byteSize(t.sizes[i])
	}

	if i < len(t.keys) && t.keys[i] {
		return label("SIZE") + fmt.Sprintf("%-9s", size) + colour(colourOrange) + `{\b1}KEY{\b0}`
	}

	return label("SIZE") + size
}

// rateRow is the bitrate of the last second.
func (t *title) rateRow(
	i int,
) string {
	return label("RATE") + bitRate(t.bitrate[i])
}

// shotRow is the shot number out of the shot count, marked for the first
// half-second of every shot but the first.
func (t *title) shotRow(
	i int,
) string {
	s := t.shot[i]
	if s < 0 {
		return label("SHOT") + "–"
	}

	text := label("SHOT") + strconv.Itoa(s+1) + colour(colourMuted) + "/" + strconv.Itoa(len(t.video.Shots))

	if s > 0 && (t.pts[i]-t.video.Shots[s].Start).Seconds() < cutMarker {
		text += "  " + colour(colourOrange) + `{\b1}CUT{\b0}`
	}

	return text
}

// sitiRow is the spatial and temporal information of the frame.
func (t *title) sitiRow(
	i int,
) string {
	si, ok := column(t.frames.SI, i)
	if !ok {
		return ""
	}

	ti, _ := column(t.frames.TI, i)

	return label("SI") + fmt.Sprintf("%-5s", integer(si)) + colour(colourMuted) + "TI " + colour(colourWhite) + integer(ti)
}

// lumaRow is the luma range and average of the frame (8-bit code values),
// the range in red when it leaves the nominal levels.
func (t *title) lumaRow(
	i int,
) string {
	mean, ok := column(t.frames.LumaMean, i)
	if !ok {
		return ""
	}

	lo, _ := column(t.frames.LumaMin, i)
	hi, _ := column(t.frames.LumaMax, i)

	text := label("LUMA")
	if t.outOfRange(i) {
		text += colour(colourRed)
	}

	return text + fmt.Sprintf("%-8s", integer(lo)+"–"+integer(hi)) + colour(colourMuted) + "avg " + colour(colourWhite) + integer(mean)
}

// outOfRange reports whether frame i leaves the nominal levels: by the
// share of its samples outside them when the analysis kept it, by its
// extremes otherwise.
func (t *title) outOfRange(
	i int,
) bool {
	if t.video != nil && len(t.video.Levels.OutOfRange) > 0 {
		share, _ := column(t.video.Levels.OutOfRange, i)

		return share >= outOfRangeShare
	}

	lo, okLo := column(t.frames.LumaMin, i)
	hi, okHi := column(t.frames.LumaMax, i)

	return (okLo && lo < float64(t.levels.Black)) || (okHi && hi > float64(t.levels.White))
}

// lightRow is the peak and average light level of an HDR frame, in cd/m².
func (t *title) lightRow(
	i int,
) string {
	peak, ok := column(t.frames.PeakNits, i)
	if !ok {
		return ""
	}

	avg, _ := column(t.frames.AverageNits, i)

	return label("NITS") + fmt.Sprintf("%-5s", integer(peak)) + colour(colourMuted) + "peak  " +
		colour(colourWhite) + integer(avg) + colour(colourMuted) + " avg"
}
