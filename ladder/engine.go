package ladder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/vmaf"
)

// ErrNoRungs is returned when no rung satisfies the constraints.
var ErrNoRungs = errors.New("no rung satisfies the constraints")

// ErrInvalidSource is returned when the source has no video stream with a
// known geometry, frame rate and duration.
var ErrInvalidSource = errors.New("source has no usable video stream")

// defaultHeights are the candidate resolutions, kept when not above the
// source (read-only: see DefaultHeights).
var defaultHeights = []int{2160, 1440, 1080, 720, 540, 360, 270}

// DefaultHeights returns the default candidate resolutions, highest first:
// a copy, which the caller may change.
func DefaultHeights() []int {
	return slices.Clone(defaultHeights)
}

// Stages reported through Progress. StageProbe is the probe encodes of the
// ladder, unrelated to analysis.StageProbe (container probing), although
// both read "probe".
const (
	StageDigest = "digest"
	StageProbe  = "probe"
	StageVerify = "verify"
	// StageGrain measures the digest's grain, calibrates the film grain
	// level and builds the denoised reference (Options.FilmGrain).
	StageGrain = "grain"
)

// Defaults of Options.
const (
	defaultSegmentSeconds = 2
	defaultDigestSeconds  = 40
	defaultGOPSeconds     = 2
	defaultPrecision      = 1.0
	defaultParallel       = 2
	defaultProbeClips     = 16
	defaultProbing        = ProbingFixed
)

// envelopePoints is the number of log-spaced bitrates the envelope samples.
const envelopePoints = 200

// Progress reports the advancement of a ladder build. Probe and Rung carry
// the measurement that just completed, when any.
type Progress struct {
	Stage string
	Done  int
	Total int
	Probe *Probe
	Rung  *Rung
}

// Options configures a ladder build. The zero value is valid except Codec.
type Options struct {
	// Codec is h264, hevc or av1.
	Codec string
	// Encoder is the encoder implementation of the codec: x264, x265 or
	// SVT-AV1 on the CPU (the zero value), or NVIDIA NVENC. NVENC ladders
	// support neither film grain synthesis nor per-shot rungs.
	Encoder encode.Hardware
	// Backend is where VMAF's model features are extracted for every
	// measurement (see quality.Options.Backend).
	Backend vmaf.Backend
	// Preset overrides the codec's default (fast) preset.
	Preset      string
	Constraints Constraints
	// Heights are the candidate resolutions (default DefaultHeights()).
	Heights []int
	// SegmentDuration is the length of each digest segment. Default 2s.
	SegmentDuration media.Duration
	// DigestDuration caps the digest length. Default 40s.
	DigestDuration media.Duration
	// GOPDuration is the keyframe interval of the encodes. Default 2s.
	GOPDuration media.Duration
	// Precision is the target VMAF confidence half-width of each probe.
	// Default 1 (a sixth of a rung step).
	Precision float64
	// ProbeClips sizes the sampling pilot of each measurement. Default 16:
	// within one encode quality varies little across the digest. Every probe
	// shares the same fixed GOP, hence the same strata and, with the same
	// seed, the same frames (common random numbers): differences between
	// probes, which shape the curves, are far more precise than each value.
	ProbeClips int
	// Probing places the probe encodes: fixed (every resolution at the
	// codec's probe CRFs) or adaptive (see ProbingAdaptive). Default fixed.
	Probing Probing
	// Tolerance is the interpolation error (VMAF) under which adaptive
	// probing considers a rung known. Default 0.5.
	Tolerance float64
	// BitrateTolerance is the same criterion on the rung's bitrate at its
	// quality, as a fraction: a rung within either is known. Default 0.03.
	BitrateTolerance float64
	// MaxProbes caps adaptive probing. Default: the size of the fixed
	// design, so adaptive never costs more encodes.
	MaxProbes int
	// FilmGrain is the AV1 film grain synthesis level: 1–50, 0 off, or
	// FilmGrainAuto to detect grain on the digest and calibrate the level.
	// With grain, fidelity is scored against a denoised reference and every
	// rung gets a grain check (see GrainReport).
	FilmGrain int
	// PerShot adds a per-shot version of every rung: one CRF per shot at
	// equal rate-quality slope, same pooled quality (see PerShot).
	PerShot bool
	// PerShotResolution lets each shot of a per-shot rung also pick its
	// resolution among the rung's and the neighbouring rung resolutions,
	// at equal slope (the Dynamic Optimizer's per-shot convex hull over
	// resolution and CRF). It implies PerShot. Experimental: renditions then
	// change resolution mid-stream (see docs/ladder.md).
	PerShotResolution bool
	// Verify encodes each rung and measures it. Default on unless SkipVerify.
	SkipVerify bool
	// Parallel is the number of probes run concurrently. Default 2.
	Parallel int
	// Metrics and Devices are measured next to VMAF on the verification
	// encodes of the rungs (see quality.Options): the delivered renditions
	// get XPSNR, banding (CAMBI) or per-device VMAF; probes stay VMAF-only.
	Metrics []string
	Devices []string
	// Model is the VMAF model (see vmaf.ResolveModel).
	Model string
	// ModelDirs are searched for model files (default vmaf.DefaultModelDirs).
	ModelDirs []string
	// BitDepth of the encodes: 8 (default) or 10. The digest keeps the
	// source depth, so 10-bit sources are measured at 10 bits.
	BitDepth int
	// WorkDir holds temporary files (default: a new directory in os.TempDir).
	WorkDir string
	// Progress, when set, is called as the build advances. Calls never
	// overlap, and Done only increases within a batch of measurements.
	Progress func(Progress)
}

// Engine builds ladders.
type Engine struct {
	inspector Inspector
	encoder   Encoder
	digester  Digester
	// grainLab is optional: only film grain synthesis needs it.
	grainLab GrainLab
}

// NewEngine returns an Engine measuring with inspector, encoding with
// encoder and extracting digests with digester. Film grain synthesis also
// needs WithGrainLab.
func NewEngine(
	inspector Inspector,
	encoder Encoder,
	digester Digester,
	opts ...Option,
) *Engine {
	e := &Engine{inspector: inspector, encoder: encoder, digester: digester}
	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Build computes the ladder of source: digest, probe encodes, curves and
// envelope, rung selection, then verification of every rung (see
// docs/ladder.md).
func (e *Engine) Build(
	ctx context.Context,
	source string,
	opts Options,
) (*Result, error) {
	started := time.Now()

	codec, opts, err := e.validate(opts)
	if err != nil {
		return nil, err
	}

	res := &Result{
		SchemaVersion: analysis.SchemaVersion,
		GeneratedAt:   started.UTC(),
		Codec:         codec,
		Preset:        opts.Preset,
		Constraints:   opts.Constraints,
	}

	res.Source, err = e.inspector.Analyze(ctx, source, analysis.Options{SkipVideo: true})
	if err != nil {
		return nil, fmt.Errorf("ladder: inspect %s: %w", source, err)
	}

	video, duration, err := usableVideo(res.Source)
	if err != nil {
		return nil, err
	}

	if err := opts.Constraints.Validate(video.Height); err != nil {
		return nil, fmt.Errorf("ladder: %w", err)
	}

	res.Shape = opts.Constraints.Shape()

	dir, cleanup, err := workDir(opts.WorkDir)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	run := &build{engine: e, codec: codec, opts: opts, source: source, workDir: dir, video: video}

	if res.Timings, err = run.stages(ctx, res, duration); err != nil {
		return nil, err
	}

	res.Elapsed = media.Duration(time.Since(started))

	return res, nil
}

// validate checks opts against the codec they name and returns the codec
// and the options completed with their defaults.
func (e *Engine) validate(
	opts Options,
) (encode.Codec, Options, error) {
	opts.PerShot = opts.PerShot || opts.PerShotResolution

	codec, err := encode.LookupFor(opts.Codec, opts.Encoder)
	if err != nil {
		return encode.Codec{}, opts, fmt.Errorf("ladder: %w", err)
	}

	if err := encoderOptions(opts, codec); err != nil {
		return encode.Codec{}, opts, fmt.Errorf("ladder: %w", err)
	}

	if _, err := ParseProbing(string(opts.Probing)); err != nil {
		return encode.Codec{}, opts, fmt.Errorf("ladder: %w", err)
	}

	if opts, err = grainOptions(opts, codec); err != nil {
		return encode.Codec{}, opts, fmt.Errorf("ladder: %w", err)
	}

	if opts.FilmGrain != 0 && e.grainLab == nil {
		return encode.Codec{}, opts, fmt.Errorf("ladder: %w", ErrNoGrainLab)
	}

	return codec, opts.withDefaults(codec), nil
}

// ErrHardwareEncoder is returned for options the codec's encoder cannot
// honour. Only hardware encoders lack a feature the ladder relies on today,
// hence the name and message.
var ErrHardwareEncoder = errors.New("not supported with a hardware encoder")

// encoderOptions rejects what the codec's encoder cannot do: film grain
// synthesis of AV1 needs an encoder synthesising it (SVT-AV1), and per-shot
// rungs join chunks without re-encoding, which only some encoders are
// verified for (see encode.FeatureChunkJoin).
func encoderOptions(
	opts Options,
	codec encode.Codec,
) error {
	switch {
	case opts.FilmGrain != 0 && codec.Name == "av1" && !codec.Supports(encode.FeatureFilmGrain):
		return fmt.Errorf("film grain synthesis is %w %s (use the CPU encoder, SVT-AV1)", ErrHardwareEncoder, codec.Encoder)
	case opts.PerShot && !codec.Supports(encode.FeatureChunkJoin):
		return fmt.Errorf("per-shot rungs are %w %s", ErrHardwareEncoder, codec.Encoder)
	}

	return nil
}

// stage is one step of a build, filling part of the result.
type stage struct {
	// name times the stage in Result.Timings; an unnamed stage is untimed.
	name string
	// announce reports the stage's start through Progress, for stages that
	// report nothing else until they end.
	announce bool
	// skip leaves the stage out.
	skip bool
	run  func(ctx context.Context, res *Result) error
}

// stages runs the stages of the build in order and returns their timings:
// digest, grain (film grain synthesis), probes, rung selection,
// verification and per-shot rungs.
func (b *build) stages(
	ctx context.Context,
	res *Result,
	duration media.Duration,
) (stageTimings, error) {
	timings := stageTimings{}

	for _, s := range b.stageList(duration) {
		if s.skip {
			continue
		}

		stop := func() {}
		if s.name != "" {
			stop = timings.start(s.name)
		}

		if s.announce {
			b.opts.report(Progress{Stage: s.name})
		}

		if err := s.run(ctx, res); err != nil {
			return nil, err
		}

		stop()
	}

	return timings, nil
}

// stageList is the sequence of stages of the build. The curves of the rung
// selection are kept for the per-shot rungs, which read them.
func (b *build) stageList(
	duration media.Duration,
) []stage {
	var curves []Curve

	return []stage{
		{name: StageDigest, announce: true, run: func(ctx context.Context, res *Result) (err error) {
			res.Digest, err = b.makeDigest(ctx, duration)

			return err
		}},
		{name: StageGrain, announce: true, skip: b.opts.FilmGrain == 0, run: func(ctx context.Context, res *Result) (err error) {
			res.Grain, err = b.prepareGrain(ctx, res.Digest)

			return err
		}},
		{name: StageProbe, run: func(ctx context.Context, res *Result) (err error) {
			res.Probes, err = b.probe(ctx)

			return err
		}},
		{run: func(_ context.Context, res *Result) (err error) {
			curves, err = b.selectRungs(res)

			return err
		}},
		{name: StageVerify, skip: b.opts.SkipVerify, run: func(ctx context.Context, res *Result) error {
			return b.verifyAndCalibrate(ctx, res.Rungs, res.Probes)
		}},
		{name: StageShots, skip: !b.opts.PerShot, run: func(ctx context.Context, res *Result) (err error) {
			res.Shots, res.ShotProbing, err = b.perShot(ctx, res.Rungs, curves, res.Probes, res.Digest)

			return err
		}},
	}
}

// selectRungs builds the curves of the probes and their envelope, and
// places the rungs on it. It returns the curves, which per-shot rungs read.
func (b *build) selectRungs(
	res *Result,
) ([]Curve, error) {
	curves := b.curves(res.Probes)
	res.Hull = Envelope(curves, envelopePoints)

	targets, err := PlanRungs(res.Hull, curves, b.opts.Constraints)
	if err != nil {
		return nil, fmt.Errorf("ladder: %w", err)
	}

	if len(targets) == 0 {
		if len(res.Hull) > 0 && b.opts.Constraints.MaxBitrate > 0 && b.opts.Constraints.MaxBitrate < res.Hull[0].Bitrate {
			return nil, fmt.Errorf("ladder: %w: max bitrate %d b/s is below the lowest probed bitrate %d b/s",
				ErrNoRungs, b.opts.Constraints.MaxBitrate, res.Hull[0].Bitrate)
		}

		return nil, fmt.Errorf("ladder: %w", ErrNoRungs)
	}

	res.Probing = b.probing
	res.Rungs = b.rungs(targets, curves)

	return curves, nil
}

func (o Options) withDefaults(
	codec encode.Codec,
) Options {
	if o.Preset == "" {
		o.Preset = codec.DefaultPreset
	}

	o.Constraints = o.Constraints.WithDefaults()

	if len(o.Heights) == 0 {
		o.Heights = DefaultHeights()
	}

	if o.SegmentDuration <= 0 {
		o.SegmentDuration = media.Seconds(defaultSegmentSeconds)
	}

	if o.DigestDuration <= 0 {
		o.DigestDuration = media.Seconds(defaultDigestSeconds)
	}

	if o.GOPDuration <= 0 {
		o.GOPDuration = media.Seconds(defaultGOPSeconds)
	}

	if o.Precision <= 0 {
		o.Precision = defaultPrecision
	}

	if o.Parallel <= 0 {
		o.Parallel = defaultParallel
	}

	if o.ProbeClips <= 0 {
		o.ProbeClips = defaultProbeClips
	}

	if o.Probing == "" {
		o.Probing = defaultProbing
	}

	if o.Tolerance <= 0 {
		o.Tolerance = defaultTolerance
	}

	if o.BitrateTolerance <= 0 {
		o.BitrateTolerance = defaultBitrateTolerance
	}

	return o
}

func (o Options) report(
	p Progress,
) {
	if o.Progress != nil {
		o.Progress(p)
	}
}

// usableVideo returns the primary video stream of the inspected source and
// the title duration. The average frame rate falls back to the nominal one
// and the container duration to the stream one: both drive the digest
// timeline and the GOP length, and a zero would silently yield an empty
// digest or all-intra encodes.
func usableVideo(
	report *analysis.Report,
) (media.VideoStream, media.Duration, error) {
	if report == nil || report.Info == nil {
		return media.VideoStream{}, 0, fmt.Errorf("ladder: %w", ErrInvalidSource)
	}

	video, ok := report.Info.PrimaryVideo()
	if !ok || video.Width <= 0 || video.Height <= 0 {
		return media.VideoStream{}, 0, fmt.Errorf("ladder: %w", ErrInvalidSource)
	}

	if video.AvgFrameRate.Float() <= 0 {
		video.AvgFrameRate = video.FrameRate
	}

	if video.AvgFrameRate.Float() <= 0 {
		return media.VideoStream{}, 0, fmt.Errorf("ladder: %w: unknown frame rate", ErrInvalidSource)
	}

	duration := report.Info.Duration
	if duration <= 0 {
		duration = video.Duration
	}

	if duration <= 0 {
		return media.VideoStream{}, 0, fmt.Errorf("ladder: %w: unknown duration", ErrInvalidSource)
	}

	return video, duration, nil
}

// workDir returns dir, or a new temporary directory removed by cleanup.
func workDir(
	dir string,
) (string, func(), error) {
	if dir != "" {
		return dir, func() {}, nil
	}

	dir, err := os.MkdirTemp("", "qc-ladder-*")
	if err != nil {
		return "", nil, fmt.Errorf("ladder: work dir: %w", err)
	}

	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// stageTimings records the wall time of each stage, as Result.Timings.
type stageTimings map[string]string

// start begins timing stage; calling the returned function records it.
func (s stageTimings) start(
	stage string,
) func() {
	from := time.Now()

	return func() {
		s[stage] = time.Since(from).Round(time.Millisecond).String()
	}
}
