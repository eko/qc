// Package quality measures the full-reference quality (VMAF) of a distorted
// video against its reference, as fast as the requested precision allows.
//
// In sampled mode the timeline is split into strata (snapped to keyframes, a
// zero-cost proxy for shots) and short clips are scored in parallel until the
// confidence interval of the mean is narrower than the target precision, or,
// with a fixed budget (Options.Sample), until a share of the frames or a
// number of clips per scene is scored. Exact mode scores every frame.
//
// Other metrics (XPSNR, CAMBI, PSNR, PSNR-HVS, SSIM, MS-SSIM, CIEDE2000) and
// the VMAF of other viewing devices are measured on the same decoded frames,
// in the same libvmaf contexts, and estimated from the same clips.
package quality

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
	"github.com/eko/qc/vmaf"
)

var (
	// ErrFrameRateMismatch is returned when the two videos do not share a
	// frame rate: frames would be paired with the wrong counterpart.
	ErrFrameRateMismatch = errors.New("reference and distorted frame rates differ")
	// ErrNoFramesToCompare is returned when an input has no bitstream
	// report or no frame to score.
	ErrNoFramesToCompare = errors.New("no frames to compare")

	// errShortDecode is returned when fewer frames than expected were
	// decoded for a clip, e.g. when a file has fewer frames than packets.
	errShortDecode = errors.New("too few frames decoded")
)

// Modes.
const (
	ModeSampled = "sampled"
	ModeExact   = "exact"
)

// Input is one side of the comparison.
type Input struct {
	Path      string
	Video     media.VideoStream
	Bitstream *bitstream.Report
}

// Options tunes the measurement. The zero value is valid.
type Options struct {
	// Model is a model name, JSON path or "auto" (see vmaf.ResolveModel).
	Model     string
	ModelDirs []string
	// Exact scores every frame instead of sampling. It takes precedence
	// over Sample.
	Exact bool
	// Sample, when set, replaces the precision target by a fixed budget
	// scored in a single round: Precision, MaxShare and Budget do not apply,
	// and the result reports the interval the budget reaches.
	Sample Sample
	// Cuts are the shot cuts of the distorted video, as timestamps relative
	// to its first frame (e.g. from its technical analysis). With a fixed
	// budget they cut strata, next to its keyframes: scenes then follow real
	// shots even when keyframes do not (fixed-GOP encodes).
	Cuts []media.Duration
	// Precision is the target half-width of the confidence interval of the
	// mean, in VMAF points. Default 0.5.
	Precision float64
	// Confidence level of the interval. Default 0.95.
	Confidence float64
	// MaxShare caps the share of frames scored in sampled mode. When reaching
	// the precision would need more, every frame is scored instead (sampling
	// then costs more than it saves). Default 0.4.
	MaxShare float64
	// Budget never falls back to exact scoring: when the precision would
	// need more than MaxShare of the frames, sampling stops at that budget
	// and reports the interval it reached.
	Budget bool
	// BitDepth is the depth frames are scored at: 8 or 10. 0 picks 10 when
	// either video has more than 8 bits (VMAF v1 needs it to see banding).
	BitDepth int
	// ClipFrames is the number of frames of a sampled clip. Default 4.
	ClipFrames int
	// InitialClips sizes the first round on long videos: strata grow so that
	// about this many clips are scored first. Default 120, enough for ±0.5
	// when clip scores spread by 3 VMAF points within strata.
	InitialClips int
	// Workers is the number of clips scored concurrently (0 = auto).
	Workers int
	// Seed makes clip selection reproducible.
	Seed uint64
	// Metrics lists the metrics measured next to VMAF (see Metrics). They
	// are estimated from the clips VMAF samples: VMAF alone drives the
	// sampling and its stopping rule.
	Metrics []string
	// Backend is where libvmaf extracts the model features: the CPU (zero
	// value), vmaf.BackendCUDA (an NVIDIA GPU; fails when the models or the
	// build cannot) or vmaf.BackendAuto (the GPU when possible, the CPU
	// otherwise, with the reason in Result.BackendNote). VMAF v1 models
	// have no CUDA features in libvmaf 3.2.1 (see vmaf.CUDAUnsupported).
	Backend vmaf.Backend
	// Devices lists viewing conditions (vmaf.Devices) whose VMAF v1 model
	// is scored next to the primary model. Models evaluated at the
	// primary resolution share its libvmaf contexts; the others (4K
	// against a 1080p primary, or the reverse) need a second pass on the
	// same clips at their resolution.
	Devices []string
	// HDRMetric is how VMAF is scored on a PQ or HLG reference (default
	// HDRMetricPQ). Such references also get the HDR metrics (wPSNR, ΔE
	// ITP, see package quality/hdr) on the scored frames.
	HDRMetric HDRMetric
	// SkipHDRMetrics measures VMAF without the HDR metrics on an HDR
	// reference, as the ladder's probes do: they only shape the curves.
	SkipHDRMetrics bool
	// Progress, when set, is called after each scored clip. Calls never
	// overlap.
	Progress func(Progress)
}

// Progress reports the advancement of a measurement. Mean and HalfWidth are
// the running estimate, set once a sampling round completes (Estimated).
type Progress struct {
	FramesScored int
	FramesTotal  int
	Round        int
	Estimated    bool
	Mean         float64
	HalfWidth    float64
	Mode         string
	// Sample is the fixed budget of the measurement, zero when sampling is
	// driven by a precision.
	Sample Sample
}

// Meter computes VMAF between two videos.
type Meter struct {
	decoder decode.Source
	engine  Engine
}

// NewMeter returns a Meter decoding with decoder and scoring with engine
// (libvmaf.NewEngine()). Neither may be nil.
func NewMeter(
	decoder decode.Source,
	engine Engine,
) *Meter {
	return &Meter{decoder: decoder, engine: engine}
}

// clip is a unit of work: frames [from, to) scored with warm-up frames around
// them so temporal features see real neighbours.
type clip struct {
	stratum  *stratum
	from, to int
}

// clipResult holds the scores of the frames of a clip (warm-up frames
// excluded), the raw values of the other series by name, and the frames
// decoded for it on both sides.
type clipResult struct {
	clip    clip
	scores  []float64
	values  map[string][]float64
	decoded int
}

// Measure computes VMAF of dist against ref.
func (m *Meter) Measure(
	ctx context.Context,
	ref, dist Input,
	opts Options,
) (*Result, error) {
	started := time.Now()

	r, err := m.newRun(ref, dist, opts.withDefaults())
	if err != nil {
		return nil, fmt.Errorf("quality: %w", err)
	}

	var res *Result
	if r.opts.Exact {
		res, err = r.exact(ctx)
	} else {
		res, err = r.sampled(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("quality: %w", err)
	}

	res.Elapsed = media.Duration(time.Since(started))
	res.Plans = r.plans

	return res, nil
}

// newRun validates the inputs and resolves the model and scoring depth.
func (m *Meter) newRun(
	ref, dist Input,
	opts Options,
) (*run, error) {
	if err := opts.Sample.Validate(); err != nil {
		return nil, err
	}

	if ref.Bitstream == nil || dist.Bitstream == nil {
		return nil, fmt.Errorf("%w: missing bitstream report", ErrNoFramesToCompare)
	}

	if !sameVideoRate(ref.Video, dist.Video) {
		return nil, fmt.Errorf("%w: %s vs %s", ErrFrameRateMismatch, ref.Video.AvgFrameRate, dist.Video.AvgFrameRate)
	}

	n := min(ref.Bitstream.PacketCount, dist.Bitstream.PacketCount)
	if n <= 0 {
		return nil, fmt.Errorf("%w: %s has %d packets, %s has %d",
			ErrNoFramesToCompare, ref.Path, ref.Bitstream.PacketCount, dist.Path, dist.Bitstream.PacketCount)
	}

	spec, err := vmaf.ResolveModel(opts.Model, ref.Video.Height, ref.Video.AvgFrameRate.Float(), opts.ModelDirs)
	if err != nil {
		return nil, err
	}

	// Frames are identified by their reference timestamps on both sides.
	ref.Bitstream = withPTS(ref.Bitstream, n, ref.Video.AvgFrameRate)

	r := &run{
		meter:    m,
		ref:      ref,
		dist:     dist,
		spec:     spec,
		models:   []vmaf.ModelSpec{spec},
		opts:     opts,
		n:        n,
		bitDepth: scoringDepth(opts.BitDepth, ref.Video, dist.Video),
		plans:    map[string]int{},
		decoders: segmentDecoders(m.decoder, ref, dist),
	}

	if err := r.configureMetrics(); err != nil {
		return nil, err
	}

	if r.backend, err = m.engine.ResolveBackend(opts.Backend, r.allModels()); err != nil {
		return nil, err
	}

	return r, nil
}

// allModels lists every model the measurement scores: the primary and
// device models of the primary contexts, and those of the extra passes.
func (r *run) allModels() []vmaf.ModelSpec {
	specs := slices.Clone(r.models)
	for _, pass := range r.passes {
		for _, d := range pass {
			specs = append(specs, d.spec)
		}
	}

	return specs
}

// scoringDepth is the requested depth, or 10 bits when either video has more
// than 8: VMAF v1 needs them for CAMBI to see banding.
func scoringDepth(
	requested int,
	ref, dist media.VideoStream,
) int {
	if requested != 0 {
		return requested
	}

	if max(ref.BitDepth, dist.BitDepth) > 8 {
		return 10
	}

	return 8
}

// Option defaults (see Options).
const (
	defaultPrecision    = 0.5
	defaultConfidence   = 0.95
	defaultMaxShare     = 0.4
	defaultClipFrames   = 4
	defaultInitialClips = 120
	defaultSeed         = 1
)

// rateTolerance is the largest frame rate difference, in frames per second,
// still considered the same rate (e.g. 30000/1001 vs 29.97).
const rateTolerance = 1e-3

func (o Options) withDefaults() Options {
	if o.Precision <= 0 {
		o.Precision = defaultPrecision
	}

	if o.Confidence <= 0 || o.Confidence >= 1 {
		o.Confidence = defaultConfidence
	}

	if o.MaxShare <= 0 {
		o.MaxShare = defaultMaxShare
	}

	if o.ClipFrames <= 0 {
		o.ClipFrames = defaultClipFrames
	}

	if o.InitialClips <= 0 {
		o.InitialClips = defaultInitialClips
	}

	if len(o.ModelDirs) == 0 {
		o.ModelDirs = vmaf.DefaultModelDirs()
	}

	if o.Seed == 0 {
		o.Seed = defaultSeed
	}

	return o
}

// run is the state of one measurement. Its counters are shared by the
// workers scoring clips concurrently.
type run struct {
	meter     *Meter
	ref, dist Input
	// spec is the primary model: it sets the evaluation resolution and its
	// scores drive the sampling.
	spec vmaf.ModelSpec
	// models are scored in every libvmaf context: the primary model first,
	// then the device models evaluated at the same resolution, whose
	// series are named in modelSeries ("" for the primary).
	models      []vmaf.ModelSpec
	modelSeries []string
	// aliases are the series of devices whose model is the primary one.
	aliases []string
	// extractors and xpsnr are the metrics measured with VMAF; series
	// lists their series in report order.
	extractors []vmaf.Extractor
	xpsnr      bool
	// hdr measures the HDR metrics on the scored frames; toneMap decodes
	// them tone mapped to SDR (HDRMetricToneMap), and hdrPass measures the
	// HDR metrics in a second pass on the HDR frames then.
	hdr     bool
	toneMap bool
	hdrPass bool
	series  []series
	// devices are the requested devices; passes groups the ones evaluated
	// at another resolution, one extra pass per resolution.
	devices []device
	passes  [][]device
	opts    Options
	// n is the number of frames compared.
	n int
	// bitDepth is the scoring depth (8 or 10).
	bitDepth int
	// backend is where libvmaf extracts the model features.
	backend vmaf.BackendChoice
	// decoders is how many decodes are worth running at once (see
	// segmentDecoders); segments is set while an exact measurement is
	// scored in segments (see exactPlan).
	decoders int
	segments bool

	mu sync.Mutex
	// reporting serialises the Progress callback (see notify).
	reporting sync.Mutex
	scored    int
	decoded   int
	// plans counts the decoding plans used, for reporting.
	plans map[string]int
	// sourceCAMBI is CAMBI on the reference at the banded frames, by frame
	// index (see sourceBandingPass).
	sourceCAMBI map[int]float64
	// live reports progress per frame pair (exact mode: a single long clip)
	// instead of per clip.
	live bool
}

// exact scores every frame: as a single clip spanning the whole video, on
// one libvmaf context using every CPU, or in concurrent segments with
// hardware decoding (see exactPlan), merged into that clip.
func (r *run) exact(
	ctx context.Context,
) (*Result, error) {
	st := newStratum(0, r.n, r.n)
	st.sampled = []int{0}

	r.mu.Lock()
	r.scored, r.live = 0, true
	r.mu.Unlock()

	clips, workers, threads := r.exactPlan(st)

	results, err := r.score(ctx, clips, workers, threads, 0)
	if err != nil {
		return nil, err
	}

	if err := r.devicePasses(ctx, results, workers, threads); err != nil {
		return nil, err
	}

	if err := r.hdrMetricsPass(ctx, results, workers, threads); err != nil {
		return nil, err
	}

	if err := r.sourceBandingPass(ctx, results, workers, threads); err != nil {
		return nil, err
	}

	whole := mergeSegments(results)
	scores := whole.scores
	st.addClip(stats.Mean(scores), len(scores))
	res := r.result([]*stratum{st}, []clipResult{whole}, ModeExact)
	res.Mean = stats.Mean(scores)
	res.Low, res.High = res.Mean, res.Mean
	res.Rounds = 1
	r.addPooling(res, []*stratum{st}, []clipResult{whole})
	r.addMetrics(res, []*stratum{st}, []clipResult{whole})

	return res, nil
}

// sampled runs the sampling loop, or spends the fixed budget, and falls back
// to exact scoring when the precision would cost more than MaxShare of the
// frames.
func (r *run) sampled(
	ctx context.Context,
) (*Result, error) {
	workers, threads := r.parallelism()

	scoreClips := func(ctx context.Context, clips []clip, round int) ([]clipResult, error) {
		return r.score(ctx, clips, workers, threads, round)
	}

	strata, out, err := sampling(ctx, r.n, r.ref.Bitstream.PTS, r.dist.Bitstream.Keyframes, r.opts, scoreClips, r.roundDone)
	if err != nil {
		return nil, err
	}

	if out.fallback {
		res, err := r.exact(ctx)
		if err != nil {
			return nil, err
		}

		res.Fallback = fallbackReason(r.opts.Precision, out.projected)

		return res, nil
	}

	if err := r.devicePasses(ctx, out.results, workers, threads); err != nil {
		return nil, err
	}

	if err := r.hdrMetricsPass(ctx, out.results, workers, threads); err != nil {
		return nil, err
	}

	if err := r.sourceBandingPass(ctx, out.results, workers, threads); err != nil {
		return nil, err
	}

	res := r.result(strata, out.results, ModeSampled)
	res.Mean = out.est.mean
	res.HalfWidth = out.est.halfWidth
	res.Low, res.High = out.est.mean-out.est.halfWidth, out.est.mean+out.est.halfWidth
	res.Rounds = out.rounds
	res.Sample = out.budget
	r.addPooling(res, strata, out.results)
	r.addMetrics(res, strata, out.results)

	return res, nil
}

// roundDone reports the running estimate once a sampling round completes.
func (r *run) roundDone(
	est estimate,
	round int,
) {
	if r.opts.Progress == nil {
		return
	}

	r.mu.Lock()
	scored := r.scored
	r.mu.Unlock()

	r.notify(Progress{
		FramesScored: scored,
		FramesTotal:  r.n,
		Round:        round,
		Mode:         ModeSampled,
		Sample:       r.opts.Sample,
		Estimated:    !math.IsInf(est.halfWidth, 1),
		Mean:         est.mean,
		HalfWidth:    est.halfWidth,
	})
}

// fallbackReason explains why every frame was scored: projected is the share
// of frames sampling would have needed (+Inf when the variance is unknown).
func fallbackReason(
	precision, projected float64,
) string {
	return fmt.Sprintf("reaching ±%.2f would need about %.0f%% of the frames: scored all of them",
		precision, min(projected, 1)*100)
}

// stratumFrames is the target stratum length. Strata are single GOPs (about a
// shot) unless the video is long enough for the pilot round to exceed
// InitialClips: then GOPs are grouped.
func stratumFrames(
	n int,
	opts Options,
) int {
	return int(math.Ceil(float64(n) / (float64(opts.InitialClips) / pilotClips)))
}

// pendingClips returns the sampled slots that have not been scored yet.
func pendingClips(
	strata []*stratum,
) []clip {
	var out []clip

	for _, s := range strata {
		for _, slot := range s.sampled[len(s.clipMeans):] {
			out = append(out, clip{stratum: s, from: s.slots[slot][0], to: s.slots[slot][1]})
		}
	}

	return out
}

// parallelism splits the CPU between concurrently scored clips and libvmaf
// threads per clip. Clips are short, so parallelism across clips pays more
// than libvmaf's per-frame threading.
func (r *run) parallelism() (workers, threads int) {
	cpus := runtime.NumCPU()
	workers = r.opts.Workers

	if workers <= 0 {
		workers = max(1, cpus/2)
	}

	return workers, max(1, cpus/workers)
}

// report counts a scored clip and reports progress.
func (r *run) report(
	cr clipResult,
	round int,
) {
	r.mu.Lock()
	if !r.live {
		r.scored += len(cr.scores)
	}

	r.decoded += cr.decoded
	progress := Progress{FramesScored: r.scored, FramesTotal: r.n, Round: round, Mode: r.mode(), Sample: r.sample()}
	r.mu.Unlock()

	r.notify(progress)
}

// notify reports progress, one report at a time: clips are scored by
// several workers, and a callback collecting what it is told must not be
// entered twice at once.
func (r *run) notify(
	p Progress,
) {
	if r.opts.Progress == nil {
		return
	}

	r.reporting.Lock()
	defer r.reporting.Unlock()

	r.opts.Progress(p)
}

// mode is the reported measurement mode. The caller holds r.mu.
func (r *run) mode() string {
	if r.live {
		return ModeExact
	}

	return ModeSampled
}

// sample is the reported fixed budget: none in live (exact) mode. The caller
// holds r.mu.
func (r *run) sample() Sample {
	if r.live {
		return Sample{}
	}

	return r.opts.Sample
}

// pairScored counts one frame pair in live mode.
func (r *run) pairScored() {
	r.mu.Lock()
	if !r.live {
		r.mu.Unlock()

		return
	}

	r.scored++
	progress := Progress{FramesScored: r.scored, FramesTotal: r.n, Mode: ModeExact}
	r.mu.Unlock()

	r.notify(progress)
}

// sameVideoRate reports whether two videos run at the same frame rate: the
// same average rate, or the same nominal rate. The average rate is the
// frame count over the stream's duration, which a gap in the timestamps
// lengthens: a source whose last frame comes two frames late averages 25
// fps in its container, but an encode of it, carrying the same timestamps,
// may be written with the full span as its duration and average 24.97. The
// frames are matched by their timestamps, so such a pair compares exactly.
func sameVideoRate(
	a, b media.VideoStream,
) bool {
	if sameRate(a.AvgFrameRate, b.AvgFrameRate) {
		return true
	}

	return a.FrameRate.Float() > 0 && sameRate(a.FrameRate, b.FrameRate)
}

// sameRate reports whether two frame rates are equal within rateTolerance.
func sameRate(
	a, b media.Rational,
) bool {
	return math.Abs(a.Float()-b.Float()) < rateTolerance
}

// withPTS returns bs with per-frame timestamps, synthesised from the frame
// rate when bs lacks them (e.g. a report decoded from JSON).
func withPTS(
	bs *bitstream.Report,
	n int,
	rate media.Rational,
) *bitstream.Report {
	if len(bs.PTS) >= n {
		return bs
	}

	out := *bs
	out.PTS = make([]media.Duration, n)

	if fps := rate.Float(); fps > 0 {
		for i := range out.PTS {
			out.PTS[i] = media.Seconds(float64(i) / fps)
		}
	}

	return &out
}
