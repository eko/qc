package ladder

import (
	"context"
	"fmt"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

// StageAnalysis reports the frame analysis of the source a digest placed on
// its content waits for (Options.DigestSampling: balanced or top), when
// Options.Analysis brings none.
const StageAnalysis = "analysis"

// analysed reports whether a is a frame analysis: an inspection has no
// shots.
func analysed(
	a *analysis.Report,
) bool {
	return a != nil && a.Video != nil && len(a.Video.Shots) > 0
}

// balances reports whether the digest of a title of this duration is
// placed on its content (balanced, or on its most complex scenes): a title
// used whole has nothing to place.
func (b *build) balances(
	duration media.Duration,
) bool {
	return b.opts.DigestSampling != DigestUniform && duration > b.opts.DigestDuration
}

// analyses reports whether the build waits for a frame analysis before its
// digest: of its source, when the digest is placed on its content and no
// analysis is at hand, or of the videos of its program.
func (b *build) analyses(
	duration media.Duration,
) bool {
	if b.isProgram() {
		return b.programAnalysed()
	}

	return b.balances(duration) && !analysed(b.opts.Analysis)
}

// analyseSource returns the frame analysis a balanced digest and per-shot
// rungs read: that of Options.Analysis when it is one, otherwise an analysis
// of the source started now, which the returned function waits for. A
// balanced digest waits for it before anything else, and its progress is
// then reported (visible); per-shot rungs alone let it decode the source
// while the digest is probed.
func (b *build) analyseSource(
	ctx context.Context,
	visible bool,
) func(ctx context.Context) (*analysis.Report, error) {
	if a := b.opts.Analysis; analysed(a) {
		return func(context.Context) (*analysis.Report, error) { return a, nil }
	}

	var (
		report *analysis.Report
		err    error
	)

	// The shots and the frame series only: neither the audio nor the camera
	// motion has a part in the ladder.
	opts := analysis.Options{
		Audio: analysis.AudioOptions{Skip: true},
		Video: analysis.VideoOptions{SkipMotion: true},
	}

	if visible {
		opts.Progress = func(p analysis.Progress) {
			if p.Stage == analysis.StageDecode {
				b.reportProgress(Progress{Stage: StageAnalysis, Done: p.Done, Total: p.Total})
			}
		}
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		report, err = b.engine.inspector.Analyze(ctx, b.source, opts)
		if err != nil {
			err = fmt.Errorf("ladder: analyse %s: %w", b.source, err)

			return
		}

		// The next builds of the source read this analysis.
		b.prepared.keepAnalysis(report)
	}()

	return func(wait context.Context) (*analysis.Report, error) {
		select {
		case <-done:
			return report, err
		case <-wait.Done():
			return nil, fmt.Errorf("ladder: analyse %s: %w", b.source, wait.Err())
		}
	}
}

// reportProgress reports p as is, never while another report runs.
func (b *build) reportProgress(
	p Progress,
) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.opts.report(p)
}
