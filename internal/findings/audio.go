package findings

import (
	"fmt"
	"math"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/media"
)

// AudioTopic links a finding to the audio section of a track, by its
// stream index: one section per track.
func AudioTopic(
	stream int,
) Topic {
	return Topic(fmt.Sprintf("audio-%d", stream))
}

// Codes of the audio analysis. Every audio finding sets Index to the track
// (an index of analysis.AudioReport.Tracks) and Topic to its AudioTopic;
// Spans are on the container's timeline.
const (
	// LoudnessOffTarget: the integrated loudness is outside the target's
	// tolerance. Value is the loudness (LUFS), Limit the target, Text the
	// target's name.
	LoudnessOffTarget Code = "loudness-off-target"
	// LoudnessOnTarget: the loudness and the true peak meet the target.
	// Value is the loudness, Limit the target, Text its name.
	LoudnessOnTarget Code = "loudness-on-target"
	// TruePeakOver: the true peak exceeds the target's ceiling. Value is
	// the true peak (dBTP), Limit the ceiling, Spans the 100 ms steps over
	// it, merged.
	TruePeakOver Code = "true-peak-over"
	// SilentTrack: the whole track is silent. Value is its sample peak
	// (dBFS).
	SilentTrack Code = "silent-track"
	// AudioSilence: silences of the whole mix within the programme. Spans
	// are the silences.
	AudioSilence Code = "audio-silence"
	// LeadingSilence and TrailingSilence: the programme starts or ends
	// with silence at least as long as the minimum reported. Value is
	// its length (seconds), Spans it.
	LeadingSilence  Code = "leading-silence"
	TrailingSilence Code = "trailing-silence"
	// ChannelSilence: a front channel is silent while the others play.
	// Text is the channel, Spans the silences.
	ChannelSilence Code = "channel-silence"
	// MutedChannel: a channel stays silent while the others play (a dead
	// or muted channel). Text is the channel.
	MutedChannel Code = "muted-channel"
	// EmptyLFE: the LFE channel of a surround track is silent throughout:
	// a note, as programmes without low-frequency effects are common.
	EmptyLFE Code = "empty-lfe"
	// EmptyCentre: the centre channel of a surround track is silent
	// throughout, where dialogue usually is.
	EmptyCentre Code = "empty-centre"
	// Clipping: runs of full-scale samples. Value is the number of
	// clipped samples, Text the channels, Spans the clipped segments.
	Clipping Code = "clipping"
	// DCOffset: a channel's mean is off zero. Value is the largest offset
	// (full scale 1), Limit MaxDCOffset, Text the channel.
	DCOffset Code = "dc-offset"
	// OutOfPhase: segments where a pair's correlation stays negative.
	// Text is the pair ("FL/FR"), Spans the segments.
	OutOfPhase Code = "out-of-phase"
	// InvertedPolarity: a pair's correlation over the whole track is
	// negative: one channel's polarity is inverted. Value is the
	// correlation, Text the pair.
	InvertedPolarity Code = "inverted-polarity"
	// MonoAsStereo: both channels of a pair carry the same signal. Value
	// is the level of their difference (dB), Text the pair.
	MonoAsStereo Code = "mono-as-stereo"
	// AudioSampleRate: a sample rate other than 48 kHz, the rate of
	// broadcast and video delivery (a warning under 32 kHz). Value is
	// the rate.
	AudioSampleRate Code = "audio-sample-rate"
	// AudioBitDepth: PCM or lossless samples coded on fewer than 16 bits.
	// Value is the depth.
	AudioBitDepth Code = "audio-bit-depth"
	// AudioLayoutGuessed: the stream does not signal its channel layout,
	// Text is the layout assumed.
	AudioLayoutGuessed Code = "audio-layout-guessed"
	// AudioOffset: the audio starts before or after the video on the
	// container's timeline (stream start times, not a lip-sync
	// measurement). Value is the offset (seconds, positive when the audio
	// starts later).
	AudioOffset Code = "audio-offset"
)

// Thresholds of the audio findings.
const (
	// MaxDCOffset is the DC offset worth fixing: -50 dBFS (0.3% of full
	// scale), far above what a high-pass in any chain leaves, and enough
	// to cost headroom and click at edits.
	MaxDCOffset = 0.00316
	// deliveryRate is the sample rate of video and broadcast delivery.
	deliveryRate = 48000
	// minRate is the rate under which the audio bandwidth (half the rate)
	// is below 16 kHz: audibly dull.
	minRate = 32000
	// minBitDepth is the depth of CD audio, the least for delivery.
	minBitDepth = 16
	// minOffset (seconds) is the stream offset worth mentioning: 1 ms,
	// below any audible or visible effect but not rounding noise.
	minOffset = 0.001
)

// Audio lists the findings of the audio analysis, track by track.
func Audio(
	r *analysis.Report,
) []Finding {
	if r.Audio == nil {
		return nil
	}

	var out []Finding

	for i := range r.Audio.Tracks {
		t := &r.Audio.Tracks[i]
		stream := streamOf(r.Info, t.Stream)
		out = append(out, trackFindings(i, t, stream, r.Audio.Target)...)
	}

	return out
}

// streamOf returns the stream description of an audio stream index.
func streamOf(
	info *media.Info,
	index int,
) media.AudioStream {
	for _, a := range info.Audio {
		if a.Index == index {
			return a
		}
	}

	return media.AudioStream{Index: index}
}

// trackFindings lists the findings of one track: its format, then its
// loudness, then its defects. A silent track has no loudness nor phase to
// speak of.
func trackFindings(
	index int,
	t *audio.Track,
	stream media.AudioStream,
	target loudness.Target,
) []Finding {
	out := formatFindings(t, stream)

	if t.Loudness.Integrated <= loudness.Floor {
		out = append(out, Finding{Level: Warn, Code: SilentTrack, Value: t.Loudness.SamplePeak})

		return tag(out, index, t.Stream)
	}

	out = append(out, loudnessFindings(t, target)...)
	out = append(out, silenceFindings(t)...)
	out = append(out, channelFindings(t)...)
	out = append(out, pairFindings(t)...)

	return tag(out, index, t.Stream)
}

// tag sets the track and topic of a track's findings.
func tag(
	list []Finding,
	index, stream int,
) []Finding {
	for i := range list {
		list[i].Index, list[i].Topic = index, AudioTopic(stream)
	}

	return list
}

// formatFindings checks the stream's rate, depth, layout and start.
func formatFindings(
	t *audio.Track,
	stream media.AudioStream,
) []Finding {
	var out []Finding

	switch rate := stream.SampleRate; {
	case rate > 0 && rate < minRate:
		out = append(out, Finding{Level: Warn, Code: AudioSampleRate, Value: float64(rate)})
	case rate > 0 && rate != deliveryRate:
		out = append(out, Finding{Level: Info, Code: AudioSampleRate, Value: float64(rate)})
	}

	if depth := stream.BitDepth; depth > 0 && depth < minBitDepth {
		out = append(out, Finding{Level: Warn, Code: AudioBitDepth, Value: float64(depth)})
	}

	if t.LayoutGuessed {
		out = append(out, Finding{Level: Info, Code: AudioLayoutGuessed, Text: t.Layout})
	}

	if offset := t.Offset.Seconds(); math.Abs(offset) >= minOffset {
		out = append(out, Finding{Level: Info, Code: AudioOffset, Value: offset})
	}

	return out
}

// loudnessFindings checks the loudness and the true peak against the
// target.
func loudnessFindings(
	t *audio.Track,
	target loudness.Target,
) []Finding {
	l, c := t.Loudness, t.Compliance

	var out []Finding

	if !c.Loudness {
		out = append(out, Finding{Level: Warn, Code: LoudnessOffTarget, Value: l.Integrated, Limit: target.Integrated, Text: target.Name})
	}

	if !c.TruePeak {
		out = append(out, Finding{
			Level: Warn, Code: TruePeakOver, Value: l.TruePeak, Limit: target.MaxTruePeak,
			Spans: overSteps(l.Series.TruePeak, target.MaxTruePeak, t.Start),
		})
	}

	if c.OK() {
		out = append(out, Finding{Level: OK, Code: LoudnessOnTarget, Value: l.Integrated, Limit: target.Integrated, Text: target.Name})
	}

	return out
}

// overSteps merges the 100 ms steps whose value exceeds limit into
// intervals, from start.
func overSteps(
	values []float64,
	limit float64,
	start media.Duration,
) []media.Interval {
	var out []media.Interval

	at := func(i int) media.Duration { return start + media.Seconds(float64(i)*loudness.Step) }

	for i := 0; i < len(values); i++ {
		if values[i] <= limit {
			continue
		}

		from := i
		for i < len(values) && values[i] > limit {
			i++
		}

		out = append(out, media.Interval{Start: at(from), End: at(i)})
	}

	return out
}

// silenceFindings reports the silences of the mix: at the edges, then
// within the programme.
func silenceFindings(
	t *audio.Track,
) []Finding {
	var (
		out    []Finding
		inside []media.Interval
	)

	d := t.Defects

	for _, s := range d.Silence {
		switch {
		case s.Start == 0:
			out = append(out, Finding{Level: Info, Code: LeadingSilence, Value: s.Length().Seconds(), Spans: shift(t.Start, s)})
		case s.End >= d.Duration:
			out = append(out, Finding{Level: Info, Code: TrailingSilence, Value: s.Length().Seconds(), Spans: shift(t.Start, s)})
		default:
			inside = append(inside, s)
		}
	}

	if len(inside) > 0 {
		out = append(out, Finding{Level: Warn, Code: AudioSilence, Spans: shift(t.Start, inside...)})
	}

	return out
}

// shift moves intervals relative to a track's first sample onto the
// container's timeline.
func shift(
	start media.Duration,
	list ...media.Interval,
) []media.Interval {
	out := make([]media.Interval, len(list))
	for i, s := range list {
		out[i] = media.Interval{Start: s.Start + start, End: s.End + start}
	}

	return out
}

// channelFindings reports the defects of the channels: muted or empty
// channels, silences of a front channel, clipping, DC offset.
func channelFindings(
	t *audio.Track,
) []Finding {
	var (
		out      []Finding
		clipped  int64
		channels []string
		clips    []media.Interval
	)

	for i, c := range t.Defects.Channels {
		name := audio.Channel(t.Channel(i))
		out = append(out, channelSilenceFindings(t, name, c)...)

		if c.ClippedSamples > 0 {
			clipped += c.ClippedSamples
			channels = append(channels, string(name))
			clips = append(clips, shift(t.Start, c.Clipping...)...)
		}

		if math.Abs(c.DC) > MaxDCOffset {
			out = append(out, Finding{Level: Warn, Code: DCOffset, Value: c.DC, Limit: MaxDCOffset, Text: string(name)})
		}
	}

	if clipped > 0 {
		out = append(out, Finding{Level: Warn, Code: Clipping, Value: float64(clipped), Text: strings.Join(channels, ", "), Spans: clips})
	}

	return out
}

// channelSilenceFindings reports a muted or empty channel, or the
// silences of a front channel.
func channelSilenceFindings(
	t *audio.Track,
	name audio.Channel,
	c defect.Channel,
) []Finding {
	surround := len(t.Channels) > 2

	switch {
	case c.Muted && name.IsLFE():
		return []Finding{{Level: Info, Code: EmptyLFE, Text: string(name)}}
	case c.Muted && name == audio.FrontCentre && surround:
		return []Finding{{Level: Warn, Code: EmptyCentre, Text: string(name)}}
	case c.Muted:
		return []Finding{{Level: Warn, Code: MutedChannel, Text: string(name)}}
	case len(c.Silence) > 0 && front(name, surround):
		return []Finding{{Level: Warn, Code: ChannelSilence, Text: string(name), Spans: shift(t.Start, c.Silence...)}}
	}

	return nil
}

// front reports whether a channel's silences are defects: the front left
// and right, and every channel of a mono or unknown layout. The centre,
// surround and LFE channels of a surround mix are often silent by design.
func front(
	name audio.Channel,
	surround bool,
) bool {
	switch name {
	case audio.FrontLeft, audio.FrontRight, audio.DownmixLeft, audio.DownmixRight:
		return true
	}

	return !surround || unnamed(name)
}

// unnamed reports whether a channel has no position: the channels of an
// unknown layout (C1, C2...).
func unnamed(
	name audio.Channel,
) bool {
	return strings.HasPrefix(string(name), "C") && len(name) > 1 && name[1] >= '0' && name[1] <= '9'
}

// pairFindings reports the phase of the pairs: inverted polarity over the
// whole track, or out-of-phase segments, and identical channels.
func pairFindings(
	t *audio.Track,
) []Finding {
	var out []Finding

	for _, p := range t.Defects.Pairs {
		name := t.Channel(p.Left) + "/" + t.Channel(p.Right)

		switch {
		case p.Inverted:
			out = append(out, Finding{Level: Warn, Code: InvertedPolarity, Value: p.Correlation, Text: name})
		case len(p.OutOfPhase) > 0:
			out = append(out, Finding{Level: Warn, Code: OutOfPhase, Text: name, Spans: shift(t.Start, p.OutOfPhase...)})
		}

		if p.Identical {
			out = append(out, Finding{Level: Info, Code: MonoAsStereo, Value: p.Difference, Text: name})
		}
	}

	return out
}
