// Package audio analyses the audio of a track in one pass over its decoded
// samples: its loudness against a delivery target (package loudness: ITU-R
// BS.1770-5, EBU R 128, ATSC A/85) and its technical defects (package
// defect: silence, muted channels, clipping, DC offset, phase).
//
// An Analyzer consumes planar float32 samples (full scale ±1), as
// decode.FFmpeg.DecodeAudio delivers them, and returns a Track. The
// package is pure Go: it knows nothing of files or decoders.
package audio

import (
	"errors"
	"fmt"

	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/media"
)

// Options configures the analysis of a track. The zero value checks
// EBU R 128 with the default defect thresholds.
type Options struct {
	// Target is the loudness the track is checked against (zero:
	// loudness.DefaultTarget, EBU R 128).
	Target loudness.Target
	Defect defect.Options
}

// target is the target of the options, EBU R 128 when unset.
func (o Options) target() loudness.Target {
	if o.Target == (loudness.Target{}) {
		return loudness.DefaultTarget()
	}

	return o.Target
}

// Track is the analysis of one audio track.
type Track struct {
	// Stream is the index of the stream in the file (media.AudioStream's
	// Index).
	Stream int `json:"stream"`
	// Layout is the channel layout analysed and Channels its channels in
	// stream order; LayoutGuessed says the stream did not signal it.
	Layout        string   `json:"layout"`
	Channels      []string `json:"channels"`
	LayoutGuessed bool     `json:"layoutGuessed,omitempty"`
	// Start is the time of the first sample on the container's timeline
	// (the stream's start time): the times of the series and segments
	// below are relative to it.
	Start media.Duration `json:"start"`
	// Offset is Start minus the video's start: positive when the audio
	// starts after the picture. It says how the streams are placed, not
	// whether they are in sync (no lip-sync detection).
	Offset     media.Duration      `json:"offset"`
	Loudness   loudness.Result     `json:"loudness"`
	Compliance loudness.Compliance `json:"compliance"`
	Defects    defect.Result       `json:"defects"`
}

// Channel returns the name of channel i ("" when out of range).
func (t *Track) Channel(
	i int,
) string {
	if i < 0 || i >= len(t.Channels) {
		return ""
	}

	return t.Channels[i]
}

// Analyzer analyses the samples of a track. It is not safe for concurrent
// use.
type Analyzer struct {
	stream media.AudioStream
	layout Layout
	target loudness.Target
	meter  *loudness.Meter
	detect *defect.Detector
}

// NewAnalyzer returns an analyzer for the samples of stream, as decoded:
// at its sample rate, with its channels in stream order.
func NewAnalyzer(
	stream media.AudioStream,
	opts Options,
) *Analyzer {
	layout := ParseLayout(stream.ChannelLayout, stream.Channels)

	return &Analyzer{
		stream: stream,
		layout: layout,
		target: opts.target(),
		meter:  loudness.NewMeter(stream.SampleRate, layout.Weights()),
		detect: defect.New(stream.SampleRate, len(layout.Channels), layout.Pairs(), opts.Defect),
	}
}

// Add analyses the next samples: one slice per channel, all of the same
// length.
func (a *Analyzer) Add(
	samples [][]float32,
) error {
	// Both check the channel count, and only it can fail.
	if err := errors.Join(a.meter.Add(samples), a.detect.Add(samples)); err != nil {
		return fmt.Errorf("audio stream %d: %w", a.stream.Index, err)
	}

	return nil
}

// Result finishes the analysis. videoStart is the start time of the
// picture on the container's timeline, which the track's Offset is
// relative to.
func (a *Analyzer) Result(
	videoStart media.Duration,
) Track {
	level := a.meter.Result()

	channels := make([]string, len(a.layout.Channels))
	for i, c := range a.layout.Channels {
		channels[i] = string(c)
	}

	return Track{
		Stream:        a.stream.Index,
		Layout:        a.layout.Name,
		Channels:      channels,
		LayoutGuessed: a.layout.Guessed,
		Start:         a.stream.StartTime,
		Offset:        a.stream.StartTime - videoStart,
		Loudness:      level,
		Compliance:    a.target.Check(level),
		Defects:       a.detect.Result(),
	}
}
