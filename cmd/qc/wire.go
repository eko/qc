package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/pprof"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/overlay"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/sample"
	"github.com/eko/qc/vmaf/libvmaf"
)

// module is the composition root of the commands: it provides every
// service from config, each adapter behind the ports its consumers declare,
// then checks the GPU and the overlay's libass before any work (checkGPU,
// checkOverlay) and ties the CPU profile to the application lifecycle
// (registerProfile).
//
//	ToolsConfig ─┬─ newProber        → probe.Prober ─────────┐
//	             ├─ newPacketReader  → bitstream.PacketReader ┤
//	gpuSettings ─┼─ newDecoder       → decode.Source ─────────┼─ analysis.New → *Analyzer, ladder.Inspector, pipeline.Analyzer
//	             │   libvmaf.NewEngine → quality.Engine       │
//	             │   quality.NewMeter  → analysis.Meter ──────┘
//	             └─ newEncoder → ladder.Encoder, ladder.Digester, ladder.GrainLab, ladder.RenditionEncoder, overlay.Burner, sample.Cutter
//	                newLadderEngine → pipeline.LadderBuilder, pipeline.LadderEncoder
//	                overlay.NewRenderer → pipeline.Overlayer
//	                newRunner → *pipeline.Runner
//	                sample.NewEngine → *sample.Engine
//
// ctx bounds the preflights, which run while the application is built: a
// missing GPU or libass then fails before anything starts.
func module(
	ctx context.Context,
	config Config,
) fx.Option {
	return fx.Options(
		fx.Supply(config.Tools, config.Output, config.Overlay, gpuSettingsOf(config)),
		fx.Provide(
			func() context.Context { return ctx },
			newLogger,
			fx.Annotate(newProber, fx.As(new(probe.Prober))),
			fx.Annotate(newPacketReader, fx.As(new(bitstream.PacketReader))),
			fx.Annotate(newDecoder, fx.As(new(decode.Source))),
			fx.Annotate(libvmaf.NewEngine, fx.As(new(quality.Engine))),
			fx.Annotate(quality.NewMeter, fx.As(new(analysis.Meter))),
			fx.Annotate(analysis.New,
				fx.As(fx.Self()), fx.As(new(ladder.Inspector)), fx.As(new(pipeline.Analyzer)), fx.As(new(sample.Inspector))),
			fx.Annotate(newEncoder,
				fx.As(new(ladder.Encoder)), fx.As(new(ladder.Digester)), fx.As(new(ladder.GrainLab)),
				fx.As(new(ladder.RenditionEncoder)), fx.As(new(overlay.Burner)), fx.As(new(burnChecker)),
				fx.As(new(sample.Cutter))),
			fx.Annotate(newLadderEngine, fx.As(new(pipeline.LadderBuilder)), fx.As(new(pipeline.LadderEncoder))),
			fx.Annotate(overlay.NewRenderer, fx.As(new(pipeline.Overlayer))),
			newRunner,
			sample.NewEngine,
		),
		fx.Invoke(checkGPU, checkOverlay, registerProfile),
	)
}

// services are what the commands run on.
type services struct {
	analyzer *analysis.Analyzer
	runner   *pipeline.Runner
	sampler  *sample.Engine
}

// newApp builds the application of one command run: its services, checked
// against the GPU. Its errors are those of the GPU preflight, as is (they
// name the flag to change), or of a constructor. fx logs its events through
// the command's logger at debug level only, so that they never reach a
// regular run.
func newApp(
	ctx context.Context,
	config Config,
	svc *services,
) *fx.App {
	return fx.New(
		fx.WithLogger(newEventLogger),
		module(ctx, config),
		fx.Populate(&svc.analyzer, &svc.runner, &svc.sampler),
	)
}

// newEventLogger logs fx events at debug level, failures included: a
// failure is also returned to the command, which reports it once.
func newEventLogger(
	logger *slog.Logger,
) fxevent.Logger {
	events := &fxevent.SlogLogger{Logger: logger}
	events.UseLogLevel(slog.LevelDebug)
	events.UseErrorLevel(slog.LevelDebug)

	return events
}

func newLogger(
	tools ToolsConfig,
) (*slog.Logger, error) {
	level, err := parseLogLevel(tools.LogLevel)
	if err != nil {
		return nil, err
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})), nil
}

func newProber(
	tools ToolsConfig,
) *probe.FFprobe {
	return probe.NewFFprobe(tools.FFprobe)
}

func newPacketReader(
	tools ToolsConfig,
) *bitstream.FFprobeReader {
	return bitstream.NewFFprobeReader(tools.FFprobe)
}

func newDecoder(
	tools ToolsConfig,
	gpu gpuSettings,
	logger *slog.Logger,
) *decode.FFmpeg {
	return decode.NewFFmpeg(tools.FFmpeg, 0, decode.WithHWAccel(gpu.hwaccel), decode.WithLogger(logger))
}

// newEncoder is the ffmpeg adapter of every encoding port of the ladder
// engine (encodes, digests and grain measurements) and of the overlay's
// burns; it logs a hardware encoder falling back to x264.
func newEncoder(
	tools ToolsConfig,
	logger *slog.Logger,
) *encode.FFmpeg {
	return encode.NewFFmpeg(tools.FFmpeg, encode.WithLogger(logger))
}

// newLadderEngine gives the ladder engine a grain lab, so that AV1 film
// grain synthesis is available, and a rendition encoder, so that ladders
// can be encoded on the whole title.
func newLadderEngine(
	inspector ladder.Inspector,
	encoder ladder.Encoder,
	digester ladder.Digester,
	lab ladder.GrainLab,
	renditions ladder.RenditionEncoder,
) *ladder.Engine {
	return ladder.NewEngine(inspector, encoder, digester, ladder.WithGrainLab(lab), ladder.WithRenditionEncoder(renditions))
}

// newRunner gives the runner the ladder encoder and the overlayer, so that
// --encode-ladder can write the renditions and --overlay annotated copies.
func newRunner(
	analyzer pipeline.Analyzer,
	ladders pipeline.LadderBuilder,
	encoder pipeline.LadderEncoder,
	overlayer pipeline.Overlayer,
) *pipeline.Runner {
	return pipeline.NewRunner(analyzer, ladders, pipeline.WithLadderEncoder(encoder), pipeline.WithOverlayer(overlayer))
}

// registerProfile profiles the CPU while the application runs, when
// --cpuprofile is set.
func registerProfile(
	lc fx.Lifecycle,
	output OutputConfig,
) {
	if output.CPUProfile == "" {
		return
	}

	p := &cpuProfile{path: output.CPUProfile}
	lc.Append(fx.StartStopHook(p.start, p.stop))
}

// cpuProfile is a Go CPU profile written to path.
type cpuProfile struct {
	path string
	file *os.File
}

// start creates the profile file and starts profiling.
func (p *cpuProfile) start() error {
	f, err := os.Create(p.path)
	if err != nil {
		return fmt.Errorf("create cpu profile: %w", err)
	}

	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()

		return fmt.Errorf("start cpu profile: %w", err)
	}

	p.file = f

	return nil
}

// stop stops profiling and closes the file.
func (p *cpuProfile) stop() error {
	pprof.StopCPUProfile()

	if err := p.file.Close(); err != nil {
		return fmt.Errorf("close cpu profile: %w", err)
	}

	return nil
}
