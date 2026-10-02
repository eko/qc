package ladder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

// ErrPreparedSource is returned when a build is given the Prepared of
// another source.
var ErrPreparedSource = errors.New("prepared for another source")

// Prepared is a source the ladders of several codecs are built from, one
// build after the other (Options.Prepared): what one build makes of the
// source, the next ones read instead of making it again. That is its
// inspection, its frame analysis when a digest placed on the content or
// per-shot rungs needed one, and its digest, which builds asking for the
// same segments share as one file: every codec is then measured on the very
// same frames, by construction. Close removes its files.
//
// A Prepared is for builds run one at a time; it is safe to read from the
// goroutines of one build.
type Prepared struct {
	source     string
	inspection *analysis.Report

	mu sync.Mutex
	// frames is the frame analysis of the source, once a build made one.
	frames *analysis.Report
	// dir holds the digest and cleanup removes it; both are unset for the
	// Prepared a build makes for itself.
	dir     string
	cleanup func()
	// digest is the digest extracted last: its segments, file and
	// inspection.
	digest       []media.Interval
	digestPath   string
	digestReport *analysis.Report
}

// Prepare inspects source and returns what the builds of its ladders share
// (Options.Prepared). Nothing else is made yet: the first build analyses
// the source and extracts the digest as it needs them, reporting them
// through its own Progress and Timings, and the next builds reuse them. The
// digest is kept in a temporary directory of its own, which outlives the
// builds: the caller closes the Prepared once they are done.
func (e *Engine) Prepare(
	ctx context.Context,
	source string,
) (*Prepared, error) {
	prepared, err := e.inspect(ctx, source)
	if err != nil {
		return nil, err
	}

	if prepared.dir, prepared.cleanup, err = workDir(""); err != nil {
		return nil, err
	}

	return prepared, nil
}

// inspect returns a Prepared holding the inspection of source and nothing
// else: the one a build makes for itself, whose digest lives and dies in
// the work directory of that build.
func (e *Engine) inspect(
	ctx context.Context,
	source string,
) (*Prepared, error) {
	inspection, err := e.inspector.Analyze(ctx, source, analysis.Options{SkipVideo: true})
	if err != nil {
		return nil, fmt.Errorf("ladder: inspect %s: %w", source, err)
	}

	return &Prepared{source: source, inspection: inspection}, nil
}

// Close removes the files of the Prepared (its digest). Builds must not use
// it afterwards. A nil Prepared closes to nothing.
func (p *Prepared) Close() {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cleanup != nil {
		p.cleanup()
	}

	p.cleanup, p.dir, p.digest = nil, "", nil
}

// analysis returns the frame analysis a build already made of the source,
// nil before any.
func (p *Prepared) analysis() *analysis.Report {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.frames
}

// keepAnalysis records the frame analysis a build made of the source.
func (p *Prepared) keepAnalysis(
	report *analysis.Report,
) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.frames = report
}

// sharedDigest returns the file and inspection of the digest of these
// segments, when a build extracted it already.
func (p *Prepared) sharedDigest(
	segments []media.Interval,
) (string, *analysis.Report, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.digest == nil || !slices.Equal(p.digest, segments) {
		return "", nil, false
	}

	return p.digestPath, p.digestReport, true
}

// digestDir returns the directory the digest is written to: its own, or
// buildDir, the work directory of the build, for the Prepared that build
// made for itself.
func (p *Prepared) digestDir(
	buildDir string,
) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.dir == "" {
		return buildDir
	}

	return p.dir
}

// keepDigest records the digest a build extracted.
func (p *Prepared) keepDigest(
	segments []media.Interval,
	path string,
	report *analysis.Report,
) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.digest, p.digestPath, p.digestReport = slices.Clone(segments), path, report
}
