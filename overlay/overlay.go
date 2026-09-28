// Package overlay turns analysis results into a debug overlay burnt into a
// copy of the video: an ASS subtitle script (Write) that libass renders
// through ffmpeg's subtitles filter, in the same encode that writes the
// copy (Renderer, through its Burner port, encode.FFmpeg in practice).
//
// ASS rather than frames drawn in Go: libass draws styled text and vector
// shapes during the encode, at frame-accurate times, so the overlay costs
// about as much as a plain encode, and the script stays small because an
// event only starts when what it shows changes.
//
// The overlay shows, for each frame: its timecode, number, size and
// keyframe flag, the bitrate over the last second, the shot and the camera
// work (with a vector of the camera move), SI/TI, luma levels, light
// levels of HDR videos, black, frozen, banded or out-of-range frames, the
// VMAF and other metrics of a comparison on scored frames, and a timeline
// of the title (VMAF or bitrate, shot cuts, a playhead).
package overlay

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/quality"
)

// Item is a part of the overlay.
type Item string

// Items of the overlay. An item without data (motion without the motion
// analysis, hdr on SDR, quality without a comparison...) is left out.
const (
	// ItemTime is the timecode, frame number, frame size and keyframe flag.
	ItemTime Item = "time"
	// ItemBitrate is the bitrate over the last second.
	ItemBitrate Item = "bitrate"
	// ItemShots is the shot number, and a marker at cuts.
	ItemShots Item = "shots"
	// ItemMotion is the camera work of the shot and the camera move of
	// the frame.
	ItemMotion Item = "motion"
	// ItemSITI is the spatial and temporal information of the frame.
	ItemSITI Item = "siti"
	// ItemLevels is the luma range and average of the frame.
	ItemLevels Item = "levels"
	// ItemHDR is the peak and average light level of an HDR frame.
	ItemHDR Item = "hdr"
	// ItemLoudness is the short-term and momentary loudness of the audio
	// (the default track) when the frame is shown.
	ItemLoudness Item = "loudness"
	// ItemFlags are badges for black, frozen, banded and out-of-range
	// frames, and letterboxing.
	ItemFlags Item = "flags"
	// ItemQuality is the VMAF and the other metrics of scored frames.
	ItemQuality Item = "quality"
	// ItemTimeline is a chart of the title with a playhead.
	ItemTimeline Item = "timeline"
)

// allItems lists every item, in display order.
var allItems = []Item{
	ItemTime, ItemBitrate, ItemShots, ItemMotion, ItemSITI,
	ItemLevels, ItemHDR, ItemLoudness, ItemFlags, ItemQuality, ItemTimeline,
}

// itemAliases are other names accepted by ParseItems.
var itemAliases = map[string]Item{"vmaf": ItemQuality, "light": ItemHDR, "luma": ItemLevels, "audio": ItemLoudness}

// ErrUnknownItem is returned by ParseItems for an unknown item name.
var ErrUnknownItem = errors.New("unknown overlay item")

// ErrNoFrames is returned by Write when the report has no frame timeline
// (no bitstream analysis, or no packet).
var ErrNoFrames = errors.New("overlay: the report has no frame timeline")

// Items returns every item, in display order.
func Items() []Item {
	return slices.Clone(allItems)
}

// ParseItems reads item names ("vmaf" is ItemQuality). Blank names are
// skipped; no name at all returns nil, which Options reads as every item.
func ParseItems(
	names []string,
) ([]Item, error) {
	var items []Item

	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}

		item, ok := itemAliases[name]
		if !ok {
			item = Item(name)
		}

		if !slices.Contains(allItems, item) {
			return nil, fmt.Errorf("%w %q (supported: %s)", ErrUnknownItem, name, itemList())
		}

		if !slices.Contains(items, item) {
			items = append(items, item)
		}
	}

	return items, nil
}

// itemList names every item, for error messages and help texts.
func itemList() string {
	names := make([]string, len(allItems))
	for i, item := range allItems {
		names[i] = string(item)
	}

	return strings.Join(names, ", ")
}

// Options configures the overlay. The zero value shows every item.
type Options struct {
	// Items are the parts shown; nil shows every item.
	Items []Item
	// Font is the font family of the overlay (default DefaultFont). A
	// monospaced font keeps the values from shifting as digits change.
	Font string
}

// has reports whether item is shown.
func (o Options) has(
	item Item,
) bool {
	return o.Items == nil || slices.Contains(o.Items, item)
}

// font is the font family, DefaultFont unless set.
func (o Options) font() string {
	if o.Font == "" {
		return DefaultFont
	}

	return o.Font
}

// Input is what the overlay shows.
type Input struct {
	// Report is the analysis of the video the overlay is burnt into: its
	// inspection at least (the bitstream gives the frame timeline), with
	// its frame analysis for every item but time, bitrate and timeline.
	Report *analysis.Report
	// Quality, when set, is the comparison of that video against its
	// reference: its per-frame scores (every frame with an exact
	// measurement, the sampled clips otherwise).
	Quality *quality.Result
}
