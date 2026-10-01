package ladder

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

// probeShare caps the share of digest frames scored per measurement.
const probeShare = 0.25

// extraProbeCRFOffset is how far below the lowest probe CRF of a resolution
// the extra probe goes when its curve stops short of the top quality.
const extraProbeCRFOffset = 7

// build holds the state of one ladder build.
type build struct {
	engine       *Engine
	codec        encode.Codec
	opts         Options
	source       string
	digest       string
	digestReport *analysis.Report
	workDir      string
	video        media.VideoStream
	// origin is the presentation time of the source's first frame on its
	// container's timeline, which seeks in the source start from (see
	// encode.ChunkSource.Origin).
	origin media.Duration
	// signal is the colour signal of every encode (HDR sources).
	signal encode.Signal

	// probing records how the probes were placed.
	probing ProbingReport

	// level is the offset of the sampled measurements (see Level).
	level float64

	// sourceAnalysis returns the frame analysis of the source, which a
	// balanced digest and per-shot rungs read (see analyseSource); nil
	// when the build needs none.
	sourceAnalysis func(ctx context.Context) (*analysis.Report, error)
	// analysis is that frame analysis, once a balanced digest waited for
	// it (see StageAnalysis).
	analysis *analysis.Report

	// reference is what encodes are scored against: the digest, or its
	// denoised version when film grain is synthesised (grain > 0).
	reference       string
	referenceReport *analysis.Report
	grain           int
	// noiseEvery is the frame step of noise measurements on the digest.
	noiseEvery int

	// mu guards done and serialises Progress calls.
	mu   sync.Mutex
	done int
}

// geometry returns the frame size at height, keeping the source aspect
// ratio with an even width (4:2:0 needs even dimensions).
func (b *build) geometry(
	height int,
) (int, int) {
	width := int(math.Round(float64(height)*float64(b.video.Width)/float64(b.video.Height)/2)) * 2

	return width, height
}

// heights returns the candidate heights not above the source (each once),
// or the source height when every candidate is above it.
func (b *build) heights() []int {
	var out []int

	candidates := b.opts.Heights
	if len(b.opts.Constraints.Resolutions) > 0 {
		// Imposed rung resolutions: probing others would be wasted.
		candidates = b.opts.Constraints.Resolutions
	}

	for _, h := range candidates {
		if h <= b.video.Height && !slices.Contains(out, h) {
			out = append(out, h)
		}
	}

	if len(out) == 0 {
		out = []int{b.video.Height}
	}

	return out
}

// probe runs the probe encodes of the probing mode.
func (b *build) probe(
	ctx context.Context,
) ([]Probe, error) {
	if b.opts.Probing == ProbingAdaptive {
		return b.probeAdaptive(ctx)
	}

	return b.probeFixed(ctx)
}

// probeFixed encodes the digest at every candidate resolution and probe
// CRF, then extends the curves that stop short of the top quality where it
// matters (see extraProbes).
func (b *build) probeFixed(
	ctx context.Context,
) ([]Probe, error) {
	b.probing = ProbingReport{Mode: ProbingFixed, Rounds: 1}
	jobs := b.probeJobs()
	b.resetProgress()

	probes, err := b.measureAll(ctx, jobs, StageProbe, len(jobs), b.opts.ProbePreset)
	if err != nil {
		return nil, err
	}

	extra := b.extraProbes(probes)
	if len(extra) == 0 {
		return probes, nil
	}

	b.probing.Rounds++
	b.resetProgress()

	extended, err := b.measureAll(ctx, extra, StageProbe, len(extra), b.opts.ProbePreset)
	if err != nil {
		return nil, err
	}

	return append(probes, extended...), nil
}

// probeJobs lists the probe encodes: every resolution at every probe CRF.
func (b *build) probeJobs() []Probe {
	var jobs []Probe

	for _, height := range b.heights() {
		w, h := b.geometry(height)
		for _, crf := range b.codec.ProbeCRFs {
			jobs = append(jobs, Probe{Width: w, Height: h, CRF: crf})
		}
	}

	return jobs
}

// extraProbes returns the probes extending the curves that stop short of
// the top quality: the top resolution's, otherwise the top rung would be
// capped by the probes rather than by the content; and the resolution just
// below the top rung's, when it might reach the top quality cheaper (see
// promising): easy content, such as cartoons, often reaches it cheaper at
// 720p than at 1080p, which the probes alone would not show. At most one
// lower resolution is extended, which bounds the extra cost to two probes.
func (b *build) extraProbes(
	probes []Probe,
) []Probe {
	curves := curvesOf(probes)
	if len(curves) == 0 {
		return nil
	}

	target := b.opts.Constraints.TopVMAF
	reach, height := topReach(curves, target)

	var out []Probe

	for i, c := range curves {
		if _, best := c.QualityRange(); best >= target {
			continue
		}

		if i > 0 && (curves[i-1].Height != height || !promising(c, target, reach)) {
			continue
		}

		lowest := math.Inf(1)
		for _, p := range probes {
			if p.Height == c.Height {
				lowest = min(lowest, p.CRF)
			}
		}

		if crf := lowest - extraProbeCRFOffset; crf >= b.codec.MinCRF {
			out = append(out, Probe{Width: c.Width, Height: c.Height, CRF: crf, Extra: true})
		}
	}

	return out
}

// topReach is the bitrate at which the ladder reaches the target quality
// today, and the resolution reaching it there: the cheapest curve reaching
// it within its probes, or else the top curve extrapolated beyond them.
func topReach(
	curves []Curve,
	target float64,
) (float64, int) {
	reach, height := math.Inf(1), 0

	for _, c := range curves {
		if _, best := c.QualityRange(); best >= target {
			if b, _ := c.BitrateFor(target); b < reach {
				reach, height = b, c.Height
			}
		}
	}

	if height == 0 {
		reach, _ = curves[0].BitrateFor(target)
		height = curves[0].Height
	}

	return reach, height
}

// extensionReach bounds, as a multiple of a curve's highest probed bitrate,
// where an extension probe can land: extraProbeCRFOffset lower CRF costs
// about 2–3× the bitrate, so a target further away cannot be confirmed by it.
const extensionReach = 3.0

// extensionSaving is the saving on the top rung a lower resolution must
// promise for its curve to be extended. The extra probe costs about 7% of
// probing, and probes are measured at ±1 VMAF: smaller projected savings
// seldom materialise (see docs/validation.md: the dramas' projected savings,
// 1% or none, were misses; the cartoon's 29% gave a 23% cheaper top rung).
const extensionSaving = 0.1

// promising reports whether curve c, projected past its probes (see
// projectBitrate), would reach the target quality at least extensionSaving
// cheaper than reach, within extensionReach of its probes.
func promising(
	c Curve,
	target, reach float64,
) bool {
	b, ok := projectBitrate(c, target)
	_, hi := c.Range()

	return ok && b < reach*(1-extensionSaving) && b <= hi*extensionReach
}

// maxProjectionSteps bounds the projection of a curve past its probes.
const maxProjectionSteps = 50

// projectBitrate projects the bitrate at which curve c reaches target past
// its last probe. Rate-quality curves are concave in log bitrate: the slope
// of each next segment (as wide as the last one) shrinks by the ratio
// between the last two segments' slopes, capped at 1 (straight extension).
// ok is false when the projection levels off below target.
func projectBitrate(
	c Curve,
	target float64,
) (float64, bool) {
	n := len(c.vmaf)
	if n < 2 {
		return 0, false
	}

	width := c.logR[n-1] - c.logR[n-2]
	slope := (c.vmaf[n-1] - c.vmaf[n-2]) / width

	if width <= 0 || slope <= 0 {
		return 0, false
	}

	decay := 1.0
	if n >= 3 {
		if prev := (c.vmaf[n-2] - c.vmaf[n-3]) / (c.logR[n-2] - c.logR[n-3]); prev > 0 {
			decay = min(slope/prev, 1)
		}
	}

	x, gained := c.logR[n-1], c.vmaf[n-1]

	for range maxProjectionSteps {
		slope *= decay
		if gained+slope*width >= target {
			return math.Exp(x + (target-gained)/slope), true
		}

		x, gained = x+width, gained+slope*width
	}

	return 0, false
}

// measureAll encodes and measures jobs at preset, opts.Parallel at a time.
// Progress counts them against total, from where the current batch stands
// (see resetProgress).
func (b *build) measureAll(
	ctx context.Context,
	jobs []Probe,
	stage string,
	total int,
	preset string,
) ([]Probe, error) {
	out := make([]Probe, len(jobs))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(b.opts.Parallel)

	for i, job := range jobs {
		group.Go(func() error {
			m, err := b.measure(gctx, job, encode.Params{Preset: preset}, fmt.Sprintf("%s-%d", stage, i), scoreProbe, nil)
			if err != nil {
				return err
			}

			job.Bitrate, job.VMAF, job.HalfWidth = m.Bitrate, m.VMAF, m.HalfWidth
			out[i] = job

			b.tick(Progress{Stage: stage, Total: total, Probe: &job})

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("ladder: %s: %w", stage, err)
	}

	return out, nil
}

// measure encodes the digest with the probe settings (plus the rate cap and
// preset of rate, when set) and measures its bitrate and VMAF as mode says.
func (b *build) measure(
	ctx context.Context,
	p Probe,
	rate encode.Params,
	name string,
	mode scoring,
	after func(path string) error,
) (Measurement, error) {
	path := filepath.Join(b.workDir, name+".mp4")
	defer os.Remove(path)

	if err := b.engine.encoder.Encode(ctx, b.codec, b.digest, path, b.params(p, rate)); err != nil {
		return Measurement{}, fmt.Errorf("%s: %w", name, err)
	}

	m, err := b.scoreFile(ctx, path, mode)
	if err != nil {
		return Measurement{}, fmt.Errorf("%s: measure: %w", name, err)
	}

	return b.leveled(m, mode), b.inspect(path, name, after)
}

// scoreFile measures the encode at path as mode says: decoded without its
// synthesised grain when film grain is on.
func (b *build) scoreFile(
	ctx context.Context,
	path string,
	mode scoring,
) (Measurement, error) {
	if b.grain > 0 {
		return b.scoreGrainless(ctx, path, mode)
	}

	cmp, err := b.score(ctx, path, mode)
	if err != nil {
		return Measurement{}, err
	}

	return measurementOf(cmp), nil
}

// inspect runs after on the encode at path, when set, before it is removed.
func (b *build) inspect(
	path, name string,
	after func(path string) error,
) error {
	if after == nil {
		return nil
	}

	if err := after(path); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	return nil
}

// params are the encoding settings of probe p on the digest, with the rate
// cap and preset of rate when set (the delivery preset otherwise).
func (b *build) params(
	p Probe,
	rate encode.Params,
) encode.Params {
	return encode.Params{
		Width: p.Width, Height: p.Height, CRF: p.CRF, Preset: cmp.Or(rate.Preset, b.opts.Preset),
		GOP:     b.gop(),
		MaxRate: rate.MaxRate, BufSize: rate.BufSize,
		BitDepth: b.opts.BitDepth, FilmGrain: b.grain, Signal: b.signal,
	}
}

// scoring selects how an encode of the digest is measured.
type scoring int

const (
	// scoreProbe samples VMAF alone, within the probe budget.
	scoreProbe scoring = iota
	// scoreRung samples like a probe and adds Options.Metrics and Devices:
	// a rung is a delivered rendition.
	scoreRung
	// scoreExact scores every frame (per-shot curves need every frame).
	scoreExact
	// scoreRungExact scores every frame and adds Options.Metrics and
	// Devices: the top rung, whose quality the ladder promises.
	scoreRungExact
)

// score measures the encode at path against the digest as mode says.
func (b *build) score(
	ctx context.Context,
	path string,
	mode scoring,
) (*analysis.Comparison, error) {
	q := quality.Options{
		Model: b.opts.Model, ModelDirs: b.opts.ModelDirs, Precision: b.opts.Precision,
		InitialClips: b.opts.ProbeClips, Budget: true, MaxShare: probeShare, Backend: b.opts.Backend,
		HDRMetric: b.opts.HDRMetric, SkipHDRMetrics: true,
	}

	switch mode {
	case scoreRung:
		q.Metrics, q.Devices, q.SkipHDRMetrics = b.opts.Metrics, b.opts.Devices, false
	case scoreExact, scoreRungExact:
		q = quality.Options{
			Model: b.opts.Model, ModelDirs: b.opts.ModelDirs, Exact: true, Backend: b.opts.Backend,
			HDRMetric: b.opts.HDRMetric, SkipHDRMetrics: true,
		}

		if mode == scoreRungExact {
			q.Metrics, q.Devices, q.SkipHDRMetrics = b.opts.Metrics, b.opts.Devices, false
		}
	}

	cmp, err := b.engine.inspector.Compare(ctx, b.reference, path, analysis.CompareOptions{Reference: b.referenceReport, Quality: q})
	if err != nil {
		return nil, fmt.Errorf("compare: %w", err)
	}

	return cmp, nil
}

// measurementOf summarises a comparison, with the means of its extra
// metrics and devices when measured.
func measurementOf(
	cmp *analysis.Comparison,
) Measurement {
	v := cmp.VMAF
	m := Measurement{
		Bitrate:   cmp.Distorted.Bitstream.AverageBitrate,
		VMAF:      v.Mean,
		HalfWidth: v.HalfWidth,
	}

	for _, r := range v.Metrics {
		if m.Metrics == nil {
			m.Metrics = map[string]float64{}
		}

		m.Metrics[r.Name] = r.Mean
	}

	for _, d := range v.Devices {
		if m.Devices == nil {
			m.Devices = map[string]float64{}
		}

		m.Devices[d.Device] = d.Mean
	}

	if v.Banding != nil {
		m.BandedFrames, m.ScoredFrames = v.Banding.BandedFrames, v.FramesScored
	}

	return m
}

// gop is the fixed keyframe interval in frames.
func (b *build) gop() int {
	return max(1, int(math.Round(b.opts.GOPDuration.Seconds()*b.video.AvgFrameRate.Float())))
}

// resetProgress starts a new batch of measurements.
func (b *build) resetProgress() {
	b.mu.Lock()
	b.done = 0
	b.mu.Unlock()
}

// tick counts one completed measurement of the batch and reports it. The
// report runs under the lock: callbacks never overlap and Done increases.
func (b *build) tick(
	p Progress,
) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.done++
	p.Done = b.done
	b.opts.report(p)
}

// curvesOf groups probes by resolution into curves, highest first.
func curvesOf(
	probes []Probe,
) []Curve {
	byHeight := map[int][]Probe{}
	for _, p := range probes {
		byHeight[p.Height] = append(byHeight[p.Height], p)
	}

	curves := make([]Curve, 0, len(byHeight))
	for _, ps := range byHeight {
		curves = append(curves, NewCurve(ps))
	}

	slices.SortFunc(curves, func(a, c Curve) int { return cmp.Compare(c.Height, a.Height) })

	return curves
}
