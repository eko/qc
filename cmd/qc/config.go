package main

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

// Output formats of the reports printed on stdout.
const (
	formatText = "text"
	formatJSON = "json"
)

// ErrInvalidFormat is returned for an unknown --format.
var ErrInvalidFormat = errors.New("invalid output format")

// ErrSampleConflict is returned when --sample is combined with --exact or an
// explicit --precision: a fixed budget replaces both.
var ErrSampleConflict = errors.New("--sample cannot be combined")

// ErrFilmGrainPerShot is returned when AV1 film grain synthesis is asked
// together with per-shot rungs, which the ladder engine cannot combine.
var ErrFilmGrainPerShot = errors.New("--film-grain cannot be combined with --per-shot")

// av1Codec is the only codec with film grain synthesis.
const av1Codec = "av1"

// Config is the CLI configuration, loaded from flags, QC_* environment
// variables and an optional configuration file (see loadConfig). Each
// command only defines the flags it uses: the others keep their zero value.
//
// It is grouped by concern, one group per set of flags, so that each
// service receives only its part (see module). The groups are squashed:
// keys stay the flag names, in the environment and in the file alike.
type Config struct {
	Tools    ToolsConfig    `mapstructure:",squash"`
	Output   OutputConfig   `mapstructure:",squash"`
	Analysis AnalysisConfig `mapstructure:",squash"`
	Quality  QualityConfig  `mapstructure:",squash"`
	Ladder   LadderConfig   `mapstructure:",squash"`
	Run      RunConfig      `mapstructure:",squash"`
	GPU      GPUConfig      `mapstructure:",squash"`
	Overlay  OverlayConfig  `mapstructure:",squash"`
}

// ToolsConfig locates the external tools and sets the log level: the
// persistent flags of every command.
type ToolsConfig struct {
	FFprobe  string `mapstructure:"ffprobe"`
	FFmpeg   string `mapstructure:"ffmpeg"`
	LogLevel string `mapstructure:"log-level"`
}

// OutputConfig is where and how reports are written (addOutputFlags), and
// the annotated copy of the video (addOverlayFlags).
type OutputConfig struct {
	Format     string `mapstructure:"format"`
	Output     string `mapstructure:"output"`
	HTML       string `mapstructure:"html"`
	CPUProfile string `mapstructure:"cpuprofile"`
	Overlay    string `mapstructure:"overlay"`
}

// AnalysisConfig tunes the technical analysis (addAnalysisFlags) and its
// audio analysis (addAudioFlags).
type AnalysisConfig struct {
	Fast             bool          `mapstructure:"fast"`
	BitrateInterval  time.Duration `mapstructure:"bitrate-interval"`
	PeakWindow       time.Duration `mapstructure:"peak-window"`
	NoMotion         bool          `mapstructure:"no-motion"`
	Audio            bool          `mapstructure:"audio"`
	NoAudio          bool          `mapstructure:"no-audio"`
	LoudnessTarget   string        `mapstructure:"loudness-target"`
	AudioTracks      string        `mapstructure:"audio-tracks"`
	SilenceThreshold float64       `mapstructure:"silence-threshold"`
	SilenceDuration  time.Duration `mapstructure:"silence-duration"`
}

// QualityConfig tunes VMAF measurements (addQualityFlags): the model and
// the metrics also apply to the verification of ladder rungs.
type QualityConfig struct {
	Model        string   `mapstructure:"model"`
	ModelDir     []string `mapstructure:"model-dir"`
	Exact        bool     `mapstructure:"exact"`
	Precision    float64  `mapstructure:"precision"`
	MaxShare     float64  `mapstructure:"max-share"`
	Sample       string   `mapstructure:"sample"`
	Workers      int      `mapstructure:"workers"`
	VMAFBitDepth int      `mapstructure:"vmaf-bit-depth"`
	Metrics      []string `mapstructure:"metrics"`
	AV2CTC       bool     `mapstructure:"av2-ctc"`
	Devices      []string `mapstructure:"devices"`
	HDRMetric    string   `mapstructure:"hdr-metric"`

	// precisionSet records that --precision was given (on the command
	// line, through QC_PRECISION or in the configuration file), which a
	// fixed budget contradicts.
	precisionSet bool
}

// LadderConfig tunes ladder builds (addLadderFlags, and --codec of the
// ladder command).
type LadderConfig struct {
	Codec             string  `mapstructure:"codec"`
	Preset            string  `mapstructure:"preset"`
	ProbePreset       string  `mapstructure:"probe-preset"`
	TopVMAF           float64 `mapstructure:"top-vmaf"`
	MinVMAF           float64 `mapstructure:"min-vmaf"`
	Step              float64 `mapstructure:"step"`
	MaxRungs          int     `mapstructure:"max-rungs"`
	Rungs             string  `mapstructure:"rungs"`
	MinBitrate        int64   `mapstructure:"min-bitrate"`
	MaxBitrate        int64   `mapstructure:"max-bitrate"`
	Heights           []int   `mapstructure:"heights"`
	NoVerify          bool    `mapstructure:"no-verify"`
	Commands          bool    `mapstructure:"commands"`
	Parallel          int     `mapstructure:"parallel"`
	EncodeBitDepth    int     `mapstructure:"encode-bit-depth"`
	Probing           string  `mapstructure:"probing"`
	Digest            string  `mapstructure:"digest"`
	PerShot           bool    `mapstructure:"per-shot"`
	PerShotResolution bool    `mapstructure:"per-shot-resolution"`
	FilmGrain         string  `mapstructure:"film-grain"`
	EncodeLadder      string  `mapstructure:"encode-ladder"`
	NoRenditionCheck  bool    `mapstructure:"no-rendition-check"`
}

// RunConfig holds the flags of the run command alone.
type RunConfig struct {
	Reference    string   `mapstructure:"reference"`
	SkipAnalysis bool     `mapstructure:"skip-analysis"`
	Codecs       []string `mapstructure:"codecs"`
}

// GPUConfig is what runs on an NVIDIA GPU (addGPUFlags). --gpu fills the
// others (see Config.withGPU).
type GPUConfig struct {
	GPU         bool   `mapstructure:"gpu"`
	HWAccel     string `mapstructure:"hwaccel"`
	Encoder     string `mapstructure:"encoder"`
	VMAFBackend string `mapstructure:"vmaf-backend"`
}

// OverlayConfig tunes the annotated copy written by --overlay
// (addOverlayFlags).
type OverlayConfig struct {
	Items   []string `mapstructure:"overlay-items"`
	Height  int      `mapstructure:"overlay-height"`
	Encoder string   `mapstructure:"overlay-encoder"`
	Workers int      `mapstructure:"overlay-workers"`
}

// validate rejects invalid values before any work starts, so that a typo
// does not surface after a long run.
func (c Config) validate() error {
	validators := []func() error{
		c.Tools.validate,
		c.Output.validate,
		c.Analysis.validate,
		c.Quality.validate,
		c.Ladder.validate,
		c.GPU.validate,
		c.Overlay.validate,
		c.validateCodecs,
		c.validateFilmGrain,
	}

	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}

	return nil
}

// validate checks the log level.
func (c ToolsConfig) validate() error {
	_, err := parseLogLevel(c.LogLevel)

	return err
}

// parseLogLevel reads a slog level name (debug, info, warn, error).
func parseLogLevel(
	name string,
) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("invalid log level %q: %w", name, err)
	}

	return level, nil
}

// validate checks the stdout format.
func (c OutputConfig) validate() error {
	if c.Format != "" && c.Format != formatText && c.Format != formatJSON {
		return fmt.Errorf("%w %q (supported: text, json)", ErrInvalidFormat, c.Format)
	}

	return nil
}

// ErrInvalidSilenceThreshold is returned for a --silence-threshold that is
// not below full scale.
var ErrInvalidSilenceThreshold = errors.New("invalid --silence-threshold")

// validate checks the audio settings: the loudness target, the tracks and
// the silence threshold.
func (c AnalysisConfig) validate() error {
	if _, err := loudness.ParseTarget(c.LoudnessTarget); err != nil {
		return fmt.Errorf("invalid --loudness-target: %w", err)
	}

	if _, _, err := parseAudioTracks(c.AudioTracks); err != nil {
		return err
	}

	if c.SilenceThreshold > 0 {
		return fmt.Errorf("%w %g: want a level in dBFS, below 0 (-60)", ErrInvalidSilenceThreshold, c.SilenceThreshold)
	}

	return nil
}

// validate checks the budget, the metrics and the devices.
func (c QualityConfig) validate() error {
	if err := c.validateSample(); err != nil {
		return err
	}

	if _, err := quality.ParseMetrics(c.Metrics); err != nil {
		return fmt.Errorf("invalid --metrics: %w", err)
	}

	if _, err := quality.ParseDevices(c.Devices); err != nil {
		return fmt.Errorf("invalid --devices: %w", err)
	}

	if _, err := quality.ParseHDRMetric(c.HDRMetric); err != nil {
		return fmt.Errorf("invalid --hdr-metric: %w", err)
	}

	return nil
}

// validateSample rejects a malformed --sample, or one combined with the
// modes it replaces.
func (c QualityConfig) validateSample() error {
	sample, err := quality.ParseSample(c.Sample)
	if err != nil {
		return fmt.Errorf("invalid --sample: %w", err)
	}

	switch {
	case sample.IsZero():
		return nil
	case c.Exact:
		return fmt.Errorf("%w with --exact", ErrSampleConflict)
	case c.precisionSet:
		return fmt.Errorf("%w with --precision: pick a budget or a precision", ErrSampleConflict)
	}

	return nil
}

// validate checks the ladder shape, the probing mode, the digest sampling
// and the film grain setting.
func (c LadderConfig) validate() error {
	if _, _, err := parseRungs(c.Rungs); err != nil {
		return err
	}

	if _, err := ladder.ParseProbing(c.Probing); err != nil {
		return err
	}

	if _, err := ladder.ParseDigestSampling(c.Digest); err != nil {
		return err
	}

	_, err := parseFilmGrain(c.FilmGrain)

	return err
}

// validateCodecs checks the codecs of every ladder the command builds.
func (c Config) validateCodecs() error {
	for _, codec := range c.ladderCodecs() {
		if _, err := encode.Lookup(codec); err != nil {
			return err
		}
	}

	return nil
}

// validateFilmGrain rejects film grain with per-shot rungs as soon as an
// AV1 ladder is requested: the build would otherwise fail only when it
// reaches that ladder, after the other stages ran.
func (c Config) validateFilmGrain() error {
	// LadderConfig.validate already rejected a malformed level.
	level, _ := parseFilmGrain(c.Ladder.FilmGrain)
	perShot := c.Ladder.PerShot || c.Ladder.PerShotResolution

	if level != 0 && perShot && slices.Contains(c.ladderCodecs(), av1Codec) {
		return fmt.Errorf("%w: choose one of them for the AV1 ladder", ErrFilmGrainPerShot)
	}

	return nil
}

// ladderCodecs are the codecs of the ladders the command builds: --codec
// of the ladder command, --codecs of the run command.
func (c Config) ladderCodecs() []string {
	var codecs []string

	for _, codec := range append([]string{c.Ladder.Codec}, c.Run.Codecs...) {
		if codec != "" {
			codecs = append(codecs, codec)
		}
	}

	return codecs
}
