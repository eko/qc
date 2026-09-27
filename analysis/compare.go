package analysis

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/quality"
)

// Comparison is the result of comparing a distorted video to its reference.
type Comparison struct {
	SchemaVersion int               `json:"schemaVersion"`
	GeneratedAt   time.Time         `json:"generatedAt"`
	Reference     *Report           `json:"reference"`
	Distorted     *Report           `json:"distorted"`
	VMAF          *quality.Result   `json:"vmaf"`
	Timings       map[string]string `json:"timings"`
}

// CompareOptions configures a comparison. The zero value is valid.
type CompareOptions struct {
	// Bitstream configures the packet analysis of the files a comparison
	// inspects (it never runs the frame analysis).
	Bitstream bitstream.Options
	// Quality configures the measurement.
	Quality quality.Options
	// Reference, when set, is the already inspected reference: repeated
	// comparisons against one reference skip inspecting it again.
	Reference *Report
	// Distorted, when set, is the already analysed distorted video: it is
	// not inspected again, and with a fixed budget (Quality.Sample) its shot
	// cuts become Quality.Cuts unless those are set. Only its inspection
	// (no frame analysis) is kept in the Comparison.
	Distorted *Report
}

// Compare inspects both files (no decoding) then measures VMAF of dist
// against ref. It returns ErrNoMeter when the Analyzer has no quality meter.
func (a *Analyzer) Compare(
	ctx context.Context,
	refPath, distPath string,
	opts CompareOptions,
) (*Comparison, error) {
	// Checked first: inspecting both files would be wasted work.
	if a.meter == nil {
		return nil, fmt.Errorf("compare: %w", ErrNoMeter)
	}

	cmp := &Comparison{SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC()}
	timings := newTimings()

	stop := timings.track("inspect")

	deferred, err := a.inspectPair(ctx, refPath, distPath, opts, cmp)
	if err != nil {
		return nil, fmt.Errorf("compare %s to %s: %w", distPath, refPath, err)
	}

	stop()

	refVideo, _ := cmp.Reference.Info.PrimaryVideo()
	distVideo, _ := cmp.Distorted.Info.PrimaryVideo()

	stop = timings.track("vmaf")

	group, gctx := errgroup.WithContext(ctx)

	var result *quality.Result

	group.Go(func() (err error) {
		result, err = a.meter.Measure(gctx,
			quality.Input{Path: refPath, Video: refVideo, Bitstream: cmp.Reference.Bitstream},
			quality.Input{Path: distPath, Video: distVideo, Bitstream: cmp.Distorted.Bitstream},
			qualityOptions(opts),
		)

		return err
	})

	// The measurement needs the colour of the videos, not their HDR
	// metadata: the first frames are read meanwhile.
	for _, report := range deferred {
		group.Go(func() (err error) {
			report.Info, err = a.completeHDR(gctx, report.Info)

			return err
		})
	}

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("compare %s to %s: %w", distPath, refPath, err)
	}

	stop()

	cmp.VMAF = result
	cmp.Timings = timings.snapshot()

	return cmp, nil
}

// inspectPair inspects the reference (unless opts carries it) and the
// distorted file concurrently, filling cmp.Reference and cmp.Distorted. It
// returns the reports it inspected, whose first frame's HDR metadata is
// still to read (see HDRProber).
func (a *Analyzer) inspectPair(
	ctx context.Context,
	refPath, distPath string,
	opts CompareOptions,
	cmp *Comparison,
) ([]*Report, error) {
	_, canDefer := a.prober.(HDRProber)
	inspectOpts := Options{Bitstream: opts.Bitstream, SkipVideo: true, DeferHDRMetadata: canDefer}

	group, ctx := errgroup.WithContext(ctx)

	if opts.Reference != nil {
		cmp.Reference = opts.Reference
	} else {
		group.Go(func() error {
			report, err := a.Analyze(ctx, refPath, inspectOpts)
			cmp.Reference = report

			return err
		})
	}

	if opts.Distorted != nil {
		cmp.Distorted = opts.Distorted.inspection()
	} else {
		group.Go(func() error {
			report, err := a.Analyze(ctx, distPath, inspectOpts)
			cmp.Distorted = report

			return err
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}

	var deferred []*Report

	if canDefer && opts.Reference == nil {
		deferred = append(deferred, cmp.Reference)
	}

	if canDefer && opts.Distorted == nil {
		deferred = append(deferred, cmp.Distorted)
	}

	return deferred, nil
}

// qualityOptions are the measurement options of a comparison: a fixed
// budget per scene takes the shot cuts of the analysed distorted video, as
// scene boundaries more faithful than keyframes alone.
func qualityOptions(
	opts CompareOptions,
) quality.Options {
	q := opts.Quality
	if !q.Sample.IsZero() && len(q.Cuts) == 0 && opts.Distorted != nil {
		q.Cuts = opts.Distorted.ShotCuts()
	}

	return q
}
