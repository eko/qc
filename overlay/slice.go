package overlay

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

// sliceMargin widens the window of a slice on both sides: events are kept
// a second beyond the segment's frames, far more than any timing slack.
const sliceMargin = media.Duration(time.Second)

// dialogue starts the event lines of a script (see event.write).
var dialogue = []byte("Dialogue: ")

// window is the part of the script's timeline a slice covers, in
// centiseconds: from (included) to to (excluded).
type window struct {
	from, to centis
}

// sliceWindows are the windows of the segments of a burn whose script is
// timed from start (encode.BurnSpec.Start): each segment's frames,
// sliceMargin wider.
func sliceWindows(
	segs []encode.BurnSegment,
	start media.Duration,
) []window {
	windows := make([]window, len(segs))

	for k, seg := range segs {
		windows[k] = window{from: centis((seg.From - start - sliceMargin).Std() / centisecond), to: math.MaxInt64}
		if seg.To > 0 {
			windows[k].to = ceilCentis(seg.To - start + sliceMargin)
		}
	}

	return windows
}

// sliceScript writes into dir, for each window, a copy of the script at
// path holding its header and the events shown during the window: the
// others cannot draw on the window's frames. libass reads every event of
// its script and scans them all on every frame, which on a long title
// costs as much as the drawing; each segment of a burn then loads its own
// slice (docs/overlay.md). It returns the paths of the slices.
func sliceScript(
	path, dir string,
	windows []window,
) (paths []string, err error) {
	src, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("overlay: %w", err)
	}

	defer src.Close()

	files := make([]*os.File, len(windows))
	writers := make([]*bufio.Writer, len(windows))
	paths = make([]string, len(windows))

	defer func() {
		for _, f := range files {
			if closeErr := f.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("overlay: %w", closeErr)
			}
		}
	}()

	for k := range windows {
		paths[k] = filepath.Join(dir, "overlay-"+strconv.Itoa(k+1)+".ass")
		if files[k], err = os.Create(paths[k]); err != nil {
			return nil, fmt.Errorf("overlay: %w", err)
		}

		writers[k] = bufio.NewWriter(files[k])
	}

	if err := distribute(bufio.NewReader(src), writers, windows); err != nil {
		return nil, err
	}

	for _, w := range writers {
		if err := w.Flush(); err != nil {
			return nil, fmt.Errorf("overlay: %w", err)
		}
	}

	return paths, nil
}

// distribute copies each line of the script r to the writers of the
// windows it belongs to: header lines to all, events to the windows they
// overlap.
func distribute(
	r *bufio.Reader,
	writers []*bufio.Writer,
	windows []window,
) error {
	for {
		// Lines are copied whole, however long (the timeline's drawings).
		line, err := r.ReadBytes('\n')

		start, end, isEvent := eventTimes(line)
		for k, w := range writers {
			if !isEvent || (start < windows[k].to && end > windows[k].from) {
				_, _ = w.Write(line) // Flush reports write errors.
			}
		}

		switch {
		case errors.Is(err, io.EOF):
			return nil
		case err != nil:
			return fmt.Errorf("overlay: read script: %w", err)
		}
	}
}

// eventTimes reads the start and end of a Dialogue line ("Dialogue:
// layer,0:00:01.20,0:00:01.24,..."); ok is false for other lines.
func eventTimes(
	line []byte,
) (start, end centis, ok bool) {
	rest, isEvent := bytes.CutPrefix(line, dialogue)
	if !isEvent {
		return 0, 0, false
	}

	fields := bytes.SplitN(rest, []byte(","), 4)
	if len(fields) < 4 {
		return 0, 0, false
	}

	start, okStart := parseCentis(fields[1])
	end, okEnd := parseCentis(fields[2])

	if !okStart || !okEnd {
		return 0, 0, false
	}

	return start, end, true
}

// parseCentis reads an ASS time, h:mm:ss.cc as centis.String writes it.
func parseCentis(
	b []byte,
) (centis, bool) {
	// Each number of h:mm:ss.cc, the separator after it and its unit in
	// centiseconds.
	parts := [...]struct {
		sep  string
		unit int64
	}{{":", 360_000}, {":", 6000}, {".", 100}, {"", 1}}

	var total int64

	for _, p := range parts {
		number := b
		if p.sep != "" {
			var found bool
			if number, b, found = bytes.Cut(b, []byte(p.sep)); !found {
				return 0, false
			}
		}

		n, err := strconv.ParseUint(string(number), 10, 32)
		if err != nil {
			return 0, false
		}

		total += int64(n) * p.unit
	}

	return centis(total), true
}
