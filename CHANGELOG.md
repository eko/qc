# Changelog

All notable changes to qc are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
The release notes of each GitHub release are taken from its section here
(see [RELEASING.md](RELEASING.md)).

## [Unreleased]

First public release: a Go library and the `qc` CLI.

### Added

- **Technical analysis** (`qc analyze`): container and stream info, bitrate
  over time with peaks, GOP structure and HDR metadata read from the
  bitstream without decoding (`--fast`, under a second). Then one decode
  fanned out to SI/TI (ITU-T P.910), shot detection, black and frozen
  segments, letterbox/pillarbox detection and luma levels.
- **VMAF with a confidence interval** (`qc vmaf`): short clips sampled
  across shots (stratified, two-stage) until the 95% interval is narrower
  than `--precision`, with a real coverage validated by replaying thousands
  of runs (`bench/vmafsim`). Fixed budgets with `--sample` (`5%` of the
  frames, `2/scene`), and `--exact` to score every frame, bit-exact with
  Netflix's `vmaf` tool at 8 and 10 bits. VMAF v1 models (v1.0.16) picked
  automatically, 4K and high frame rate aware.
- **Metrics beyond VMAF**, measured on the same frames with their own
  confidence intervals (`--metrics`): XPSNR (a pure-Go port matching
  ffmpeg's filter, also usable on its own as `quality/xpsnr`), CAMBI banding
  with banded segments, PSNR, PSNR-HVS, SSIM, MS-SSIM, CIEDE2000, and the
  whole AOM AV2 common test conditions set with `--av2-ctc`.
- **VMAF per viewing device** (`--devices phone,tv,4k`), each scored with its
  VMAF v1 model on the same frames.
- **Per-title ladders** (`qc ladder`) for H.264 (libx264), HEVC (libx265)
  and AV1 (SVT-AV1): probe encodes of a representative digest, rate-quality
  curves per resolution, their upper envelope, rungs one just-noticeable
  difference apart, then a verification encode of every rung. Lands on the
  exhaustive optimum (−0.04 VMAF, −0.7% bitrate on average) in a fraction
  of the time (`bench/ladderval`). The curve of the resolution below the top
  rung's is extended when it might reach the top quality at least 10%
  cheaper, so easy content gets a cheaper lower-resolution top rung.
  - Imposed shapes: a rung count or the rung resolutions (`--rungs
    1080,720,540,360`), with the bitrates computed; top and minimum VMAF,
    bitrate bounds, 8 or 10-bit encodes (`--encode-bit-depth`).
  - Adaptive probing (`--probing adaptive`): extra probes where the rungs
    are uncertain.
  - Per-shot rungs (`--per-shot`): one CRF per shot at an equal
    rate-quality slope, with the per-shot ladder view (every shot's CRF,
    bitrate and VMAF per rung, and each rung's pooled bitrate over the
    title) in the terminal, HTML and JSON reports. Experimental per-shot
    resolution (`--per-shot-resolution`, implies `--per-shot`): shots also
    pick their resolution among neighbouring rung resolutions, at equal
    slope.
  - AV1 film grain synthesis (`--film-grain auto` or a level), detected and
    calibrated, fidelity scored against a denoised reference. It cannot be
    combined with per-shot rungs: the combination is rejected before any
    work.
  - Verified rungs checked for banding and for VMAF/XPSNR ranking
    disagreements.
- **HDR** ([docs/hdr.md](docs/hdr.md)): HDR10, PQ and HLG detected from the
  stream and its first frame (HDR10 metadata in SEI, HDR10+), Dolby Vision
  and HDR10+ reported without processing their dynamic metadata; signalling
  checks (BT.2020 primaries and matrix, 10 bits, narrow range, missing
  mastering display or content light level). MaxCLL and MaxFALL measured
  during the frame analysis (CTA-861.3, on a grid of 10-bit samples that
  ffmpeg writes to a second pipe of the same decode, robust to 4:2:0 chroma
  overshoots) and compared with the signalled
  values, with the peak and average light of every frame charted. wPSNR
  (JVET HDR test conditions) and ΔE ITP (ITU-R BT.2124, mean and 99th
  percentile) measured in pure Go on the frames VMAF scores, with their
  confidence intervals (`quality/hdr`). VMAF on HDR is labelled as not
  HDR-calibrated, or scored on an SDR tone mapping with `--hdr-metric
  tonemap`. HDR ladders encode in 10 bits and carry the colour description
  and HDR10 metadata on every probe, rung and rendered command (x265,
  SVT-AV1; colour tags for x264 and NVENC). The wizard asks the tone mapping
  question for HDR sources.
- **`qc run`**: analysis, VMAF against a reference (`-r`) and ladders for
  several codecs in one command and one report.
- **Terminal UI**: a live dashboard with progress, ETA and panels showing
  VMAF converging and probes landing on a braille rate-quality chart; an
  interactive wizard (`qc` without arguments) that prints the equivalent
  command.
- **Reports**: text in the terminal, JSON (`-f json`, `-o report.json`,
  `schemaVersion` 1) and self-contained interactive HTML (`--html`) with
  zoomable charts, per-frame tooltips and findings.
- **NVIDIA GPUs** (`--gpu`): NVDEC decoding (`--hwaccel cuda`, frames
  identical to a CPU decode, or `cuda-scale` with GPU scaling), NVENC
  ladders (`--encoder nvenc`: `h264_nvenc`, `hevc_nvenc`, `av1_nvenc`, same
  engine, without per-shot rungs or film grain synthesis) and CUDA VMAF
  feature extraction (`--vmaf-backend cuda|auto`, VMAF v0.6.1 family; VMAF
  v1 stays on the CPU and the report says why). ffmpeg's NVIDIA support,
  the device and each NVENC encoder are checked before any work. The CUDA
  image (`Dockerfile.cuda`, `ghcr.io/eko/qc:<version>-cuda`) ships libvmaf
  and ffmpeg built for it, and the validation kit (`bench/gpuval`, `make
  gpu-validate`) measures NVDEC, CUDA VMAF and NVENC on your GPU. See
  [docs/gpu.md](docs/gpu.md).
- Every flag can be set through a `QC_` environment variable, or in a YAML,
  TOML or JSON configuration file keyed by flag name (`--config`,
  `QC_CONFIG`); flags win over the environment, which wins over the file.
- **`qc version`**: the versions of qc, Go and libvmaf; `--check` also checks
  ffmpeg, ffprobe, the libx264, libx265 and libsvtav1 encoders, NVENC and a
  loadable VMAF v1 model, for bug reports.
- **Packaging**: a Docker image (`ghcr.io/eko/qc`, linux/amd64 and
  linux/arm64) with qc, libvmaf 3.2.1 and its models, and ffmpeg with
  libx264, libx265, SVT-AV1 and dav1d; a Homebrew formula for the
  `eko/tap` tap; `go install` with version information.

### Library API

The packages of the first release, for Go programs that embed qc:

- **Wiring**: services depend on small ports declared by their consumers
  and are built with constructor injection (see
  [docs/architecture.md](docs/architecture.md#ports)).
  `quality.NewMeter(decoder, engine)` takes the scoring engine as a
  `quality.Engine` (`libvmaf.NewEngine()`); `analysis.New` takes an
  `analysis.Meter` (`quality.Meter`); `ladder.NewEngine(inspector, encoder,
  digester, opts...)` takes `ladder.Inspector`, `ladder.Encoder` and
  `ladder.Digester` (all but the first satisfied by `encode.FFmpeg`), and
  film grain synthesis needs `ladder.WithGrainLab` (`ladder.ErrNoGrainLab`
  otherwise).
- **VMAF without cgo**: package `vmaf` is pure Go (models, devices,
  backends, the `Models`/`Scorer` contract); the cgo binding lives in
  `vmaf/libvmaf` (`LoadModel(spec)`, `New(models, vmaf.ScorerConfig)`,
  `Version`, `InitCUDA`, `CUDABuilt`), so `analysis`, `ladder`, `pipeline`
  and `quality` build with `CGO_ENABLED=0`. `vmaf.ResolveBackend` takes the
  CUDA initialisation to use.
- **No mutable package state**: defaults are functions returning copies
  (`vmaf.DefaultModelDirs()`, `vmaf.Devices()`, `quality.Metrics()`,
  `quality.AV2CTCMetrics()`, `ladder.DefaultHeights()`).
- **Encoding**: `encode.FFmpeg` encodes and extracts digests
  (`FFmpeg.Digest(ctx, encode.DigestSpec)`); codecs are looked up with
  `encode.Lookup` and `encode.LookupFor(name, encode.HardwareNVENC)`, and
  say what their encoder supports (`Codec.Supports`); noise measurements
  are `grain.Stats` (package `analyze/grain`).
- **Comparisons**: `analysis.CompareOptions{Bitstream, Quality, Reference,
  Distorted}` reuses reports already computed; `quality.ErrNoFramesToCompare`
  is returned when an input has no bitstream report or no frame to score.
- **Runs**: `pipeline.Runner` hands each stage's typed result to
  `Hooks.Done` (`pipeline.StageResult`: the analysis, comparison or ladder),
  and `pipeline.Options.LadderSource` builds the ladders from another video
  than the one analysed (the mezzanine of an encode under test).
- **Ladders**: `ladder.Result.ShotLadder(rung)` iterates over a per-shot
  rung's allocation shot by shot, `PerShot.PooledBitrate` prices it over the
  title, and `ladder.CalibrationTolerance` is the prediction gap beyond which
  a rung is calibrated.
- **HDR**: `media.Color.IsHDR`, `media.VideoStream.MeasurableHDR`,
  `media.MasteringDisplay` primaries and white point, `media.HDR.HDR10Plus`
  and `media.DynamicRangeHDR10Plus`; `analysis.VideoReport.Light`
  (`analyze/light`: MaxCLL, robust MaxCLL, MaxFALL) and the `peakNits`,
  `robustPeakNits`, `averageNits` columns; `quality.Options.HDRMetric`
  (`quality.HDRMetricPQ`, `HDRMetricToneMap`) and `SkipHDRMetrics`,
  `quality.Result.HDR` (`quality.HDRReport`), the `quality.SeriesWPSNR*` and
  `SeriesDeltaEITP*` series; package `quality/hdr`; `decode.Request.ToneMap`,
  `frame.PoolOptions.SampleStep`, `frame.Pool.GridSize` and `SamplePlanes`;
  `probe.FFprobe.ProbeStreams` and `ProbeHDR`, `analysis.HDRProber` and
  `analysis.Options.DeferHDRMetadata` (the first frame's HDR metadata read
  while decoding or measuring); `encode.Signal`, `encode.SignalOf` and
  `encode.Params.Signal`; `ladder.Options.HDRMetric`, `ContentLight` and
  `ladder.Result.HDR` (`ladder.HDRLadder`).
- **GPUs**: `decode.WithHWAccel` (reported through `decode.HWAccelReporter`),
  `quality.Options.Backend`, `ladder.Options.Encoder` and `Backend`,
  `quality.Result.GPUSummary`, and package `nvidia` to check the GPU before
  a long run (`nvidia.Check`, `nvidia.Available`).

[Unreleased]: https://github.com/eko/qc/commits/main
