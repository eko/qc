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
	// SkipVideo only runs stage 1 (no decoding).
	SkipVideo bool
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
	Timings       map[string]string `json:"timings"`
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
	out.Video, out.Frames = nil, nil

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

	if err := a.inspect(ctx, path, opts, report, timings); err != nil {
		return nil, fmt.Errorf("analyze %s: %w", path, err)
	}

	if !opts.SkipVideo {
		stop := timings.track("video")

		if err := a.analyzeVideo(ctx, path, opts, report, progress); err != nil {
			return nil, fmt.Errorf("analyze %s: %w", path, err)
		}

		stop()
	}

	report.Timings = timings.snapshot()

	a.logger.Debug("analysis done", "path", path, "timings", report.Timings)

	return report, nil
}

// inspect runs stage 1: probe and packet analysis, concurrently.
func (a *Analyzer) inspect(
	ctx context.Context,
	path string,
	opts Options,
	report *Report,
	timings *timings,
) error {
	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error {
		defer timings.track("probe")()

		info, err := a.prober.Probe(ctx, path)
		if err != nil {
			// Prober implementations name the file: no extra prefix.
			return err
		}

		report.Info = info

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
		return err
	}

	// A prober returning no information is treated as a file without video.
	if report.Info == nil || len(report.Info.Video) == 0 {
		return ErrNoVideo
	}

	return nil
}

// progress returns the progress callback, or a no-op when none is set.
func (o Options) progress() func(Progress) {
	if o.Progress == nil {
		return func(Progress) {}
	}

	return o.Progress
}
