package audio

import (
	"fmt"
	"strings"
)

// Channel is a loudspeaker position, named as ffmpeg names it (FL, FR,
// FC, LFE, BL, BR, SL, SR...).
type Channel string

// Channel positions with a role in the analysis.
const (
	FrontLeft   Channel = "FL"
	FrontRight  Channel = "FR"
	FrontCentre Channel = "FC"
	LFE         Channel = "LFE"
	LFE2        Channel = "LFE2"
	BackLeft    Channel = "BL"
	BackRight   Channel = "BR"
	SideLeft    Channel = "SL"
	SideRight   Channel = "SR"
	DownmixLeft Channel = "DL"
	// DownmixRight is the right channel of a stereo downmix.
	DownmixRight Channel = "DR"
)

// IsLFE reports whether c is a low-frequency effects channel, which
// BS.1770 leaves out of the loudness.
func (c Channel) IsLFE() bool {
	return c == LFE || c == LFE2
}

// Layout is the channel layout of a stream: its name and its channels in
// stream order.
type Layout struct {
	Name     string
	Channels []Channel
	// Guessed is true when the stream does not signal its layout and the
	// usual one for its channel count was assumed, or none is known
	// (channels named C1, C2...).
	Guessed bool
}

// layouts are ffmpeg's named layouts, with their channels in stream order.
var layouts = map[string]string{
	"mono":           "FC",
	"stereo":         "FL+FR",
	"2.1":            "FL+FR+LFE",
	"3.0":            "FL+FR+FC",
	"3.0(back)":      "FL+FR+BC",
	"4.0":            "FL+FR+FC+BC",
	"quad":           "FL+FR+BL+BR",
	"quad(side)":     "FL+FR+SL+SR",
	"3.1":            "FL+FR+FC+LFE",
	"5.0":            "FL+FR+FC+BL+BR",
	"5.0(side)":      "FL+FR+FC+SL+SR",
	"4.1":            "FL+FR+FC+LFE+BC",
	"5.1":            "FL+FR+FC+LFE+BL+BR",
	"5.1(side)":      "FL+FR+FC+LFE+SL+SR",
	"6.0":            "FL+FR+FC+BC+SL+SR",
	"6.0(front)":     "FL+FR+FLC+FRC+SL+SR",
	"hexagonal":      "FL+FR+FC+BL+BR+BC",
	"6.1":            "FL+FR+FC+LFE+BC+SL+SR",
	"6.1(back)":      "FL+FR+FC+LFE+BL+BR+BC",
	"6.1(front)":     "FL+FR+LFE+FLC+FRC+SL+SR",
	"7.0":            "FL+FR+FC+BL+BR+SL+SR",
	"7.0(front)":     "FL+FR+FC+FLC+FRC+SL+SR",
	"7.1":            "FL+FR+FC+LFE+BL+BR+SL+SR",
	"7.1(wide)":      "FL+FR+FC+LFE+BL+BR+FLC+FRC",
	"7.1(wide-side)": "FL+FR+FC+LFE+FLC+FRC+SL+SR",
	"octagonal":      "FL+FR+FC+BL+BR+BC+SL+SR",
	"downmix":        "DL+DR",
}

// defaultLayouts are assumed for streams that do not signal their layout,
// by channel count: the usual layouts of those counts.
var defaultLayouts = map[int]string{1: "mono", 2: "stereo", 6: "5.1", 8: "7.1"}

// ParseLayout returns the layout of a stream from ffprobe's channel_layout
// (a name such as "5.1(side)", or channels joined by "+") and its channel
// count. A missing or inconsistent layout gets the usual one for the count,
// or channels named C1, C2... (Guessed is then true).
func ParseLayout(
	name string,
	channels int,
) Layout {
	name = strings.TrimSpace(name)

	spec, ok := layouts[name]
	if !ok && strings.Contains(name, "+") {
		spec, ok = name, true
	}

	if ok {
		if list := parseChannels(spec); len(list) == channels {
			return Layout{Name: name, Channels: list}
		}
	}

	if fallback, known := defaultLayouts[channels]; known {
		return Layout{Name: fallback, Channels: parseChannels(layouts[fallback]), Guessed: true}
	}

	list := make([]Channel, channels)
	for i := range list {
		list[i] = Channel(fmt.Sprintf("C%d", i+1))
	}

	return Layout{Name: fmt.Sprintf("%d channels", channels), Channels: list, Guessed: true}
}

func parseChannels(
	spec string,
) []Channel {
	var out []Channel
	for name := range strings.SplitSeq(spec, "+") {
		out = append(out, Channel(strings.TrimSpace(name)))
	}

	return out
}

// surroundWeight is the BS.1770 weight of the surround channels (+1.5 dB).
const surroundWeight = 1.41

// Weights returns the BS.1770-5 weight of every channel: 0 for the LFE,
// 1.41 for the surround channels (60° to 120° off centre: the side
// channels, and the back ones when there are no side channels, as in
// 5.1, where they stand at the surround positions), 1 for the others
// (front, centre, rear channels of 7.1 at 135° to 150°, elevated ones).
// ffmpeg's ebur128 filter weights the rear channels of 7.1 1.41 as well.
func (l Layout) Weights() []float64 {
	sides := l.has(SideLeft) || l.has(SideRight)
	weights := make([]float64, len(l.Channels))

	for i, c := range l.Channels {
		switch {
		case c.IsLFE():
			weights[i] = 0
		case c == SideLeft || c == SideRight:
			weights[i] = surroundWeight
		case (c == BackLeft || c == BackRight) && !sides:
			weights[i] = surroundWeight
		default:
			weights[i] = 1
		}
	}

	return weights
}

// Pairs returns the pairs of channels whose phase is checked: the front
// left and right (or a downmix's), and the surround pair.
func (l Layout) Pairs() [][2]int {
	var pairs [][2]int

	for _, p := range [][2]Channel{{FrontLeft, FrontRight}, {DownmixLeft, DownmixRight}, {SideLeft, SideRight}, {BackLeft, BackRight}} {
		left, right := l.index(p[0]), l.index(p[1])
		if left >= 0 && right >= 0 {
			pairs = append(pairs, [2]int{left, right})
		}
	}

	return pairs
}

func (l Layout) has(
	c Channel,
) bool {
	return l.index(c) >= 0
}

// index is the position of c in the layout, -1 when absent.
func (l Layout) index(
	c Channel,
) int {
	for i, ch := range l.Channels {
		if ch == c {
			return i
		}
	}

	return -1
}
