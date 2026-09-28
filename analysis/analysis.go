// Package analysis orchestrates the analysis stages of a media file.
//
// Analyzer.Analyze runs the technical analysis of one file. Stage 1 (probe
// + bitstream) reads container metadata and packets without decoding and
// completes in well under a second. Stage 2 decodes the video once and fans
// frames out to every visual analyzer.
//
// Analyzer.Compare measures a distorted video against its reference (VMAF
// with a confidence interval, other metrics, viewing devices) through its
// Meter, quality.Meter in practice: both files are inspected (stage 1
// only), then measured as quality.Options says. Passing reports already
// computed (CompareOptions.Reference, CompareOptions.Distorted) skips their
// inspection.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
)

// SchemaVersion is the version of the JSON report layout.
const SchemaVersion = 1

// ErrNoVideo is returned when the input has no video stream.
var ErrNoVideo = errors.New("no video stream")

// ErrNoMeter is returned by Compare when the Analyzer has no quality meter.
var ErrNoMeter = errors.New("no quality meter configured")

// Stage names reported through Progress.
const (
	StageProbe  = "probe"
	StageDecode = "decode"
)

// Progress reports the advancement of a stage. Total is 0 when unknown.
type Progress struct {
	Stage string
	Done  int
	Total int
}

// Options configures an analysis. The zero value is valid.
type Options struct {
	Bitstream bitstream.Options
	Video     VideoOptions
	// Audio configures the audio analysis, which runs with the frame
	// analysis (and not with SkipVideo unless Audio.WithInspection).
	Audio AudioOptions
	// SkipVideo only runs stage 1 (no decoding).
	SkipVideo bool
	// DeferHDRMetadata leaves the HDR metadata of the first frame out of an
	// inspection (SkipVideo): the dynamic range then relies on the
	// container's signalling. Set it when a frame analysis of the same file
	// follows: that analysis reads the first frame while it decodes.
	DeferHDRMetadata bool
	// Progress, when set, is called as stages advance. It must be fast.
	Progress func(Progress)
}

// Report gathers the results of every stage.
type Report struct {
	SchemaVersion int               `json:"schemaVersion"`
	GeneratedAt   time.Time         `json:"generatedAt"`
	Info          *media.Info       `json:"info"`
	Bitstream     *bitstream.Report `json:"bitstream,omitempty"`
	Video         *VideoReport      `json:"video,omitempty"`
	Frames        *FrameSeries      `json:"frames,omitempty"`
	// Audio is the audio analysis (nil when skipped or without audio).
	Audio   *AudioReport      `json:"audio,omitempty"`
	Timings map[string]string `json:"timings"`
}

// ShotCuts returns the shot cuts found by the frame analysis: the start of
// every shot but the first. It is nil without a frame analysis.
func (r *Report) ShotCuts() []media.Duration {
	if r.Video == nil || len(r.Video.Shots) < 2 {
		return nil
	}

	cuts := make([]media.Duration, 0, len(r.Video.Shots)-1)
	for _, shot := range r.Video.Shots[1:] {
		cuts = append(cuts, shot.Start)
	}

	return cuts
}

// inspection returns r without its frame analysis: what inspecting the file
// alone gives.
func (r *Report) inspection() *Report {
	out := *r
	out.Video, out.Frames, out.Audio = nil, nil, nil

	return &out
}

// Analyzer runs the analysis pipeline.
type Analyzer struct {
	logger  *slog.Logger
	prober  probe.Prober
	packets bitstream.PacketReader
	decoder decode.Source
	// meter is nil when no quality meter was configured.
	meter Meter
}

// Meter measures the quality of a distorted video against its reference
// (*quality.Meter): the port Compare measures through.
type Meter interface {
	Measure(
		ctx context.Context,
		ref, dist quality.Input,
		opts quality.Options,
	) (*quality.Result, error)
}

// New returns an Analyzer. logger may be nil (nothing is logged) and meter
// may be nil when Compare is not used.
func New(
	logger *slog.Logger,
	prober probe.Prober,
	packets bitstream.PacketReader,
	decoder decode.Source,
	meter Meter,
) *Analyzer {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	a := &Analyzer{
		logger:  logger,
		prober:  prober,
		packets: packets,
		decoder: decoder,
	}

	// A typed nil pointer stored in the interface would not compare equal
	// to nil, and Compare would call it instead of returning ErrNoMeter.
	if !isNil(meter) {
		a.meter = meter
	}

	return a
}

// isNil reports whether meter is nil or a nil pointer.
func isNil(
	meter Meter,
) bool {
	if meter == nil {
		return true
	}

	v := reflect.ValueOf(meter)

	return v.Kind() == reflect.Pointer && v.IsNil()
}

// Analyze runs every stage on path: inspection, then, unless
// opts.SkipVideo, the decoded-frame analysis.
func (a *Analyzer) Analyze(
	ctx context.Context,
	path string,
	opts Options,
) (*Report, error) {
	report := &Report{SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC()}
	timings := newTimings()
	progress := opts.progress()

	progress(Progress{Stage: StageProbe})

	deferred, err := a.inspect(ctx, path, opts.DeferHDRMetadata || !opts.SkipVideo, opts, report, timings)
	if err != nil {
		return nil, fmt.Errorf("analyze %s: %w", path, err)
	}

	if err := a.decodeStreams(ctx, path, opts, report, progress, deferred, timings); err != nil {
		return nil, fmt.Errorf("analyze %s: %w", path, err)
	}

	report.Timings = timings.snapshot()

	a.logger.Debug("analysis done", "path", path, "timings", report.Timings)

	return report, nil
}

// decodeStreams runs what decodes the file, concurrently: the frame
// analysis (stage 2, unless SkipVideo) and the audio analysis (when the
// options ask for it). The audio costs little next to the video: it runs
// meanwhile, on a core of its own per track.
func (a *Analyzer) decodeStreams(
	ctx context.Context,
	path string,
	opts Options,
	report *Report,
	progress func(Progress),
	deferred bool,
	timings *timings,
) error {
	group, gctx := errgroup.WithContext(ctx)
	// The frame analysis replaces report.Info once it completed the HDR
	// metadata: the audio reads the inspection's.
	info := report.Info

	if !opts.SkipVideo {
		group.Go(func() error {
			stop := timings.track("video")
			if err := a.analyzeWithHDR(gctx, path, opts, report, progress, deferred); err != nil {
				return err
			}

			stop()

			return nil
		})
	}

	if opts.wantsAudio() {
		group.Go(func() error {
			stop := timings.track("audio")

			audioReport, err := a.analyzeAudio(gctx, path, info, opts.Audio)
			if err != nil || audioReport == nil {
				return err
			}

			report.Audio = audioReport

			stop()

			return nil
		})
	}

	return group.Wait()
}

// analyzeWithHDR runs stage 2, and when the inspection deferred it, reads
// the HDR metadata of the first frame meanwhile.
func (a *Analyzer) analyzeWithHDR(
	ctx context.Context,
	path string,
	opts Options,
	report *Report,
	progress func(Progress),
	deferred bool,
) error {
	if !deferred {
		return a.analyzeVideo(ctx, path, opts, report, progress)
	}

	group, gctx := errgroup.WithContext(ctx)

	var info *media.Info

	group.Go(func() error {
		return a.analyzeVideo(gctx, path, opts, report, progress)
	})

	group.Go(func() (err error) {
		info, err = a.completeHDR(gctx, report.Info)

		return err
	})

	if err := group.Wait(); err != nil {
		return err
	}

	report.Info = info

	return nil
}

// inspect runs stage 1: probe and packet analysis, concurrently. With
// deferHDR, the probe may leave the first frame's HDR metadata for later:
// deferred says it did.
func (a *Analyzer) inspect(
	ctx context.Context,
	path string,
	deferHDR bool,
	opts Options,
	report *Report,
	timings *timings,
) (deferred bool, err error) {
	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error {
		defer timings.track("probe")()

		info, later, err := a.probeInfo(ctx, path, deferHDR)
		if err != nil {
			// Prober implementations name the file: no extra prefix.
			return err
		}

		report.Info, deferred = info, later

		return nil
	})

	group.Go(func() error {
		defer timings.track("bitstream")()

		var packets []media.Packet

		err := a.packets.ReadPackets(ctx, path, func(pkt media.Packet) error {
			packets = append(packets, pkt)

			return nil
		})
		if err != nil {
			// PacketReader implementations name the file: no extra prefix.
			return err
		}

		bs := bitstream.Analyze(packets, opts.Bitstream)
		report.Bitstream = &bs

		return nil
	})

	if err := group.Wait(); err != nil {
		return false, err
	}

	// A prober returning no information is treated as a file without video.
	if report.Info == nil || len(report.Info.Video) == 0 {
		return false, ErrNoVideo
	}

	return deferred, nil
}

// progress returns the progress callback, or a no-op when none is set.
func (o Options) progress() func(Progress) {
	if o.Progress == nil {
		return func(Progress) {}
	}

	return o.Progress
}
