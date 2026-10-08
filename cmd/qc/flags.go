package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio/defect"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// addOutputFlags registers the flags shared by every command producing a
// report.
func addOutputFlags(
	cmd *cobra.Command,
) {
	flags := cmd.Flags()
	flags.StringP("format", "f", formatText, "stdout format: text or json")
	flags.StringP("output", "o", "", "also write the full JSON report to this file")
	flags.String("html", "", "also write a self-contained HTML report with charts to this file")
	flags.String("cpuprofile", "", "write a Go CPU profile of the run to this file")
	_ = flags.MarkHidden("cpuprofile")
}

// addAnalysisFlags registers the flags of the technical analysis.
func addAnalysisFlags(
	cmd *cobra.Command,
) {
	flags := cmd.Flags()
	flags.Bool("fast", false, "container and bitstream only, no frame decoding (< 1s)")
	flags.Duration("bitrate-interval", time.Second, "bucket size of the bitrate series")
	flags.Duration("peak-window", time.Second, "sliding window of the peak bitrate")
	flags.Bool("no-motion", false, "skip the camera motion analysis (pan, tilt, zoom, shake per shot)")
	flags.Bool("audio", false, "analyse the audio even with --fast (it is decoded)")
	addAudioFlags(cmd)
}

// addAudioFlags registers the flags of the audio analysis (loudness and
// defects of the audio tracks, alongside the frame analysis).
func addAudioFlags(
	cmd *cobra.Command,
) {
	flags := cmd.Flags()
	flags.Bool("no-audio", false, "skip the audio analysis (loudness, silence, clipping, phase)")
	flags.String("loudness-target", loudness.TargetEBU,
		"loudness target: ebu (-23 LUFS ±0.5, -1 dBTP), ebu-live (±1), atsc (-24 ±2, -2 dBTP), streaming (-16 ±1, -1 dBTP), streaming-14, or a loudness in LUFS (-16)")
	flags.String("audio-tracks", audioTracksAll, "audio tracks analysed: all, default, or track numbers from 0 as ffmpeg's 0:a:N (0,2)")
	flags.Float64("silence-threshold", -60, "audio silence threshold, dBFS")
	flags.Duration("silence-duration", 2*time.Second, "shortest audio silence reported")
}

// addModelFlag registers --model and --model-dir, shared by VMAF
// measurements and ladders.
func addModelFlag(
	cmd *cobra.Command,
) {
	cmd.Flags().String("model", "auto", "VMAF model: auto (v1, 4K/HFR aware), a model name, a JSON path or a built-in version")
	cmd.Flags().StringSlice("model-dir", vmaf.DefaultModelDirs(), "directories searched for model files")
}

// addQualityFlags registers the flags of VMAF measurements.
func addQualityFlags(
	cmd *cobra.Command,
) {
	addModelFlag(cmd)

	flags := cmd.Flags()
	flags.Bool("exact", false, "score every frame instead of sampling")
	flags.Float64("precision", 0.5, "target half-width of the 95% confidence interval, in VMAF points")
	flags.Float64("max-share", 0.4, "sampling budget: above this share of frames, score every frame instead")
	flags.String("sample", "", "fixed budget instead of a precision, one round: a share of frames (5%) or clips per scene (2/scene)")
	flags.Int("workers", 0, "clips scored concurrently (0 = NumCPU/2)")
	flags.Int("vmaf-bit-depth", 0, "VMAF scoring depth: 8 or 10 (0 = 10 when either video has more than 8 bits)")
	addMetricFlags(cmd)
}

// addMetricFlags registers the metrics measured next to VMAF: on a
// comparison, and on the verification encodes of ladder rungs.
func addMetricFlags(
	cmd *cobra.Command,
) {
	flags := cmd.Flags()
	flags.StringSlice("metrics", defaultMetrics,
		"metrics measured with VMAF on the same frames: "+strings.Join(quality.Metrics()[1:], ", ")+" (--metrics= for VMAF only)")
	flags.Bool("av2-ctc", false, "add the AOM AV2 common test conditions metrics: PSNR (Y, Cb, Cr, YUV), PSNR-HVS, SSIM, MS-SSIM, CIEDE2000, CAMBI")
	flags.StringSlice("devices", nil, "also score the VMAF v1 model of these viewing devices: "+strings.Join(vmaf.Devices(), ", "))
	flags.String("hdr-metric", string(quality.HDRMetricPQ),
		"VMAF on HDR (PQ/HLG) references: pq (on the HDR signal, fast, not HDR-calibrated) or tonemap (on an SDR tone mapping, slower)")
}

// defaultMetrics are measured unless --metrics says otherwise: CAMBI is free
// next to a VMAF v1 model, XPSNR and PSNR cost a few percent of VMAF.
var defaultMetrics = []string{quality.MetricXPSNR, quality.MetricCAMBI, quality.MetricPSNR}

// addLadderFlags registers the flags of ladder builds.
func addLadderFlags(
	cmd *cobra.Command,
) {
	flags := cmd.Flags()
	flags.String("preset", "", "encoder preset of the rungs (default: a fast preset of the codec)")
	flags.String("probe-preset", "", "encoder preset of the probe encodes (default: --preset); a much faster one cuts probing, the probes being anchored at --preset by encoding the top and bottom rungs")
	flags.String("rungs", "auto", "ladder shape: auto, a rung count (6) or the rung resolutions, top first (1080,720,720,540,360)")
	flags.Float64("top-vmaf", 95, "quality of the top rung (the highest VMAF targeted)")
	flags.Float64("min-vmaf", 30, "lowest acceptable rung quality")
	flags.Float64("step", 6, "VMAF step between rungs (6 is a clearly visible step: measured just-noticeable differences average about 7 and vary with the content)")
	flags.Int("max-rungs", 8, "maximum number of rungs of the automatic shape")
	flags.Int64("min-bitrate", 145_000, "lowest rung bitrate (bits/s)")
	flags.Int64("max-bitrate", 0, "highest rung bitrate (bits/s, 0 = no cap)")
	flags.IntSlice("heights", ladder.DefaultHeights(), "candidate resolutions (heights above the source are dropped)")
	flags.Bool("no-verify", false, "skip the verification encode of each rung")
	flags.Bool("commands", false, "print the ffmpeg command of each rung")
	flags.Int("parallel", 2, "probe encodes run concurrently")
	flags.Int("encode-bit-depth", 8, "bit depth of the ladder encodes: 8 or 10 (Main10, best for HEVC/AV1)")
	flags.Float64("digest-duration", 0, "length of the digest in seconds (0: 40 s); a ladder costs in proportion to it")
	flags.String("digest", "balanced", "segments of the digest the ladder is estimated on: balanced (moved until the digest has the SI and TI of the title), top (the most complex scenes: a ladder for the demanding parts) or uniform (evenly spaced); balanced and top analyse the title first")
	flags.String("probing", "fixed", "probe placement: fixed (3 CRFs per resolution) or adaptive (uncertainty-driven)")
	flags.Bool("per-shot", false, "add a per-shot version of every rung: one CRF per shot at equal rate-quality slope")
	flags.Bool("per-shot-resolution", false, "experimental: per-shot rungs whose shots also pick their resolution among neighbouring rungs' (implies --per-shot)")
	flags.String("film-grain", "off", "AV1 film grain synthesis: off, auto (detect and calibrate) or a level 1-50")
	flags.String("encode-ladder", "", "encode every rung on the whole title into this directory (a subdirectory per codec), each checked against the source")
	flags.Bool("no-rendition-check", false, "skip measuring the encoded renditions against the source (--encode-ladder)")
}

// renditionOptions maps --encode-ladder to library options: the renditions
// are measured against the source with the VMAF settings, unless
// --no-rendition-check.
func renditionOptions(
	config Config,
) ladder.RenditionOptions {
	opts := ladder.RenditionOptions{Dir: config.Ladder.EncodeLadder}
	if !config.Ladder.NoRenditionCheck {
		q := qualityOptions(config)
		opts.Check = &q
	}

	return opts
}

// analysisOptions maps the analysis flags to library options.
func analysisOptions(
	config AnalysisConfig,
) analysis.Options {
	return analysis.Options{
		Bitstream: bitstream.Options{
			Interval:   config.BitrateInterval,
			PeakWindow: config.PeakWindow,
		},
		Video: analysis.VideoOptions{SkipMotion: config.NoMotion},
		Audio: audioOptions(config),
	}
}

// audioOptions maps the audio flags to library options.
func audioOptions(
	config AnalysisConfig,
) analysis.AudioOptions {
	// validate already rejected malformed values.
	target, _ := loudness.ParseTarget(config.LoudnessTarget)
	defaultTrack, tracks, _ := parseAudioTracks(config.AudioTracks)

	return analysis.AudioOptions{
		Skip:           config.NoAudio,
		WithInspection: config.Audio,
		DefaultTrack:   defaultTrack,
		Tracks:         tracks,
		Target:         target,
		Defect: defect.Options{
			SilenceThreshold: config.SilenceThreshold,
			SilenceDuration:  media.Duration(config.SilenceDuration),
		},
	}
}

// Values of --audio-tracks naming a selection.
const (
	audioTracksAll     = "all"
	audioTracksDefault = "default"
)

// ErrInvalidAudioTracks is returned for a malformed --audio-tracks value.
var ErrInvalidAudioTracks = errors.New("invalid --audio-tracks")

// parseAudioTracks reads --audio-tracks: "" or "all" (every track),
// "default" (the default track), or audio track numbers from 0.
func parseAudioTracks(
	s string,
) (bool, []int, error) {
	switch s = strings.TrimSpace(strings.ToLower(s)); s {
	case "", audioTracksAll:
		return false, nil, nil
	case audioTracksDefault:
		return true, nil, nil
	}

	var tracks []int

	for field := range strings.SplitSeq(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n < 0 {
			return false, nil, fmt.Errorf("%w %q: want all, default or track numbers from 0 (0,2)", ErrInvalidAudioTracks, s)
		}

		tracks = append(tracks, n)
	}

	return false, tracks, nil
}

// qualityOptions maps the VMAF flags to library options.
func qualityOptions(
	config Config,
) quality.Options {
	q := config.Quality
	// validate already rejected a malformed --sample.
	sample, _ := quality.ParseSample(q.Sample)

	return quality.Options{
		Model:     q.Model,
		ModelDirs: q.ModelDir,
		Exact:     q.Exact,
		Precision: q.Precision,
		MaxShare:  q.MaxShare,
		Sample:    sample,
		Workers:   q.Workers,
		BitDepth:  q.VMAFBitDepth,
		Metrics:   q.metrics(),
		Devices:   q.Devices,
		Backend:   gpuSettingsOf(config).backend,
		HDRMetric: q.hdrMetric(),
	}
}

// hdrMetric is --hdr-metric (validate rejected unknown values).
func (c QualityConfig) hdrMetric() quality.HDRMetric {
	metric, _ := quality.ParseHDRMetric(c.HDRMetric)

	return metric
}

// metrics is --metrics, plus the AV2 CTC set with --av2-ctc.
func (c QualityConfig) metrics() []string {
	if !c.AV2CTC {
		return c.Metrics
	}

	return append(slices.Clone(c.Metrics), quality.AV2CTCMetrics()...)
}

// ladderOptions maps the ladder flags to library options: the model and
// metrics of the VMAF flags apply to the verification of the rungs.
func ladderOptions(
	config Config,
) ladder.Options {
	l := config.Ladder
	// validate already rejected malformed values.
	rungCount, resolutions, _ := parseRungs(l.Rungs)
	filmGrain, _ := parseFilmGrain(l.FilmGrain)
	gpu := gpuSettingsOf(config)

	return ladder.Options{
		Preset:      l.Preset,
		ProbePreset: l.ProbePreset,
		Constraints: ladder.Constraints{
			TopVMAF:     l.TopVMAF,
			MinVMAF:     l.MinVMAF,
			Step:        l.Step,
			MaxRungs:    l.MaxRungs,
			Rungs:       rungCount,
			Resolutions: resolutions,
			MinBitrate:  l.MinBitrate,
			MaxBitrate:  l.MaxBitrate,
		},
		Heights:        l.Heights,
		SkipVerify:     l.NoVerify,
		Model:          config.Quality.Model,
		ModelDirs:      config.Quality.ModelDir,
		Metrics:        config.Quality.metrics(),
		Devices:        config.Quality.Devices,
		HDRMetric:      config.Quality.hdrMetric(),
		Parallel:       l.Parallel,
		Memory:         resourceLimits(config.Tools).Memory,
		BitDepth:       l.EncodeBitDepth,
		Probing:        ladder.Probing(l.Probing),
		DigestSampling: ladder.DigestSampling(l.Digest),
		DigestDuration: media.Seconds(l.DigestDuration),
		PerShot:        l.PerShot,
		// PerShotResolution implies PerShot in the ladder options.
		PerShotResolution: l.PerShotResolution,
		FilmGrain:         filmGrain,
		Encoder:           gpu.encoder,
		Backend:           gpu.backend,
	}
}

// ErrInvalidFilmGrain is returned for a malformed --film-grain value.
var ErrInvalidFilmGrain = errors.New("invalid --film-grain")

// parseFilmGrain reads --film-grain: "" or "off", "auto", or a level 0–50
// (0 is off).
func parseFilmGrain(
	s string,
) (int, error) {
	switch s = strings.TrimSpace(strings.ToLower(s)); s {
	case "", "off":
		return 0, nil
	case "auto":
		return ladder.FilmGrainAuto, nil
	}

	level, err := strconv.Atoi(s)
	if err != nil || level < 0 || level > 50 {
		return 0, fmt.Errorf("%w %q: want off, auto or a level 0-50", ErrInvalidFilmGrain, s)
	}

	return level, nil
}

// ErrInvalidRungs is returned for a malformed --rungs value.
var ErrInvalidRungs = errors.New("invalid --rungs")

// parseRungs reads the ladder shape: "" or "auto", a rung count ("6"), or
// rung resolutions top first ("1080,720,720,540,360"; a "p" suffix is
// accepted).
func parseRungs(
	s string,
) (int, []int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "auto" {
		return 0, nil, nil
	}

	if !strings.Contains(s, ",") && !strings.HasSuffix(s, "p") {
		count, err := strconv.Atoi(s)
		if err != nil || count < 1 {
			return 0, nil, fmt.Errorf("%w %q: want auto, a positive count or resolutions like 1080,720,540", ErrInvalidRungs, s)
		}

		return count, nil, nil
	}

	var heights []int

	for field := range strings.SplitSeq(s, ",") {
		h, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(field), "p"))
		if err != nil || h < 1 {
			return 0, nil, fmt.Errorf("%w %q: %q is not a resolution height", ErrInvalidRungs, s, field)
		}

		heights = append(heights, h)
	}

	return 0, heights, nil
}
