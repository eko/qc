package analysis

import (
	"context"
	"fmt"
	"slices"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/media"
)

// AudioDecoder is the optional part of a decode.Source (decode.FFmpeg)
// that decodes audio streams into planar float32 samples. With a decoder
// lacking it, the analysis leaves the audio out.
type AudioDecoder interface {
	DecodeAudio(
		ctx context.Context,
		req decode.AudioRequest,
		fn func(samples [][]float32) error,
	) error
}

// AudioOptions configures the audio analysis: loudness and defects of the
// audio tracks, decoded while the video is (package audio). The zero value
// analyses every track of a frame analysis against EBU R 128.
type AudioOptions struct {
	// Skip leaves the audio out.
	Skip bool
	// WithInspection analyses the audio even with SkipVideo, which
	// otherwise decodes nothing.
	WithInspection bool
	// DefaultTrack analyses the default track only (media.Info's
	// DefaultAudio); otherwise Tracks lists the tracks analysed (indices
	// into media.Info.Audio, out-of-range ones ignored), every track when
	// empty.
	DefaultTrack bool
	Tracks       []int
	// Target is the loudness target (zero: EBU R 128).
	Target loudness.Target
	// Defect tunes the defect detection (silence, clipping, phase).
	Defect defect.Options
}

// AudioReport is the audio analysis: every analysed track, checked against
// the same target.
type AudioReport struct {
	Target loudness.Target `json:"target"`
	Tracks []audio.Track   `json:"tracks"`
}

// wantsAudio reports whether the options ask for the audio analysis.
func (o Options) wantsAudio() bool {
	return !o.Audio.Skip && (!o.SkipVideo || o.Audio.WithInspection)
}

// selected returns the indices into info.Audio of the tracks to analyse.
func (o AudioOptions) selected(
	info *media.Info,
) []int {
	if o.DefaultTrack {
		i := info.DefaultAudio()
		if i < 0 || info.Audio[i].SampleRate <= 0 || info.Audio[i].Channels <= 0 {
			return nil
		}

		return []int{i}
	}

	var out []int

	for i, a := range info.Audio {
		// A stream without rate or channels (unknown codec) cannot be
		// decoded to PCM.
		if (len(o.Tracks) == 0 || slices.Contains(o.Tracks, i)) && a.SampleRate > 0 && a.Channels > 0 {
			out = append(out, i)
		}
	}

	return out
}

// target is the target of the options, EBU R 128 when unset.
func (o AudioOptions) target() loudness.Target {
	if o.Target == (loudness.Target{}) {
		return loudness.DefaultTarget()
	}

	return o.Target
}

// analyzeAudio decodes and analyses the selected audio tracks of path
// concurrently, and fills report.Audio. It does nothing without an audio
// decoder or an audio track.
func (a *Analyzer) analyzeAudio(
	ctx context.Context,
	path string,
	info *media.Info,
	opts AudioOptions,
) (*AudioReport, error) {
	decoder, ok := a.decoder.(AudioDecoder)
	tracks := opts.selected(info)

	if !ok || len(tracks) == 0 {
		return nil, nil
	}

	video, _ := info.PrimaryVideo()
	out := &AudioReport{Target: opts.target(), Tracks: make([]audio.Track, len(tracks))}
	group, ctx := errgroup.WithContext(ctx)

	for i, track := range tracks {
		group.Go(func() error {
			res, err := analyzeTrack(ctx, decoder, path, info.Audio[track], video.StartTime, opts)
			out.Tracks[i] = res

			return err
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}

	return out, nil
}

// analyzeTrack decodes one audio stream into its analyzer.
func analyzeTrack(
	ctx context.Context,
	decoder AudioDecoder,
	path string,
	stream media.AudioStream,
	videoStart media.Duration,
	opts AudioOptions,
) (audio.Track, error) {
	analyzer := audio.NewAnalyzer(stream, audio.Options{Target: opts.target(), Defect: opts.Defect})

	req := decode.AudioRequest{Path: path, Stream: stream.Index, SampleRate: stream.SampleRate, Channels: stream.Channels}
	if err := decoder.DecodeAudio(ctx, req, analyzer.Add); err != nil {
		return audio.Track{}, fmt.Errorf("audio: %w", err)
	}

	return analyzer.Result(videoStart), nil
}
