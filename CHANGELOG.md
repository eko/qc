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
- **Audio quality control**, alongside the frame analysis and without
  adding to its wall time: every audio track (`--audio-tracks`) decoded by
  ffmpeg to float samples and measured in pure Go (packages `audio`,
  `audio/loudness`, `audio/defect`): integrated loudness, loudness range,
  true peak (4× oversampling) and momentary/short-term series per ITU-R
  BS.1770-5 and EBU Tech 3341/3342, checked against `--loudness-target`
  (`ebu`, `ebu-live`, `atsc`, `streaming`, `streaming-14` or a LUFS value);
  silence of the mix and of each channel (`--silence-threshold`,
  `--silence-duration`), leading and trailing silence, muted channels,
  empty LFE or centre, clipping, DC offset, out-of-phase segments,
  inverted polarity, mono as stereo, sample rate, bit depth, layout and
  stream start offsets. Terminal, HTML (loudness, levels and phase charts
  per track) and JSON reports, and a `loudness` item for annotated videos;
  `--no-audio` leaves it out, `--fast --audio` adds it to an inspection.
  Validated against the synthetic EBU conformance cases (all within
  tolerance), ffmpeg's `ebur128` on real content (within 0.01 LU / dB) and
  synthetic defects (`bench/audioval`, [docs/audio.md](docs/audio.md)).
- **Camera motion** (`analyze/motion`, on by default, `--no-motion` to skip):
  the global motion of every frame estimated on the shared thumbnails
  (integral-projection predictor, block matching with a Lucas–Kanade
  sub-pixel step, robust similarity fit), and each shot's camera work:
  static, pan, tilt, zoom, tracking (parallax), handheld, mixed, with its
  direction and a shake measure. JSON columns (`motionPan`, `motionTilt`,
  `motionZoom`, `motionRoll`, `motionShake`, `motionConfidence`),
  `video.motion` and `shots[].camera`; a camera section and a shot column in
  the terminal report; a camera motion chart, card and shot column in the
  HTML report; a note on shaky shots. About 0.17 ms of one core per frame;
  validated on synthetic moves of known speed (`bench/motionval`).
- **Annotated videos** (`--overlay annotated.mp4` on `analyze`, `vmaf` and
  `run`, and a question of the wizard): a copy of the video with the
  analysis burnt in as a debug overlay: timecode, frame number, size and
  keyframes, bitrate, shot and cut markers, camera work with a motion
  vector, SI/TI, luma levels, HDR light levels, black/frozen/banded/
  out-of-range badges, the VMAF and other metrics of each scored frame
  (`--exact` for every frame), and a timeline with a playhead
  (`--overlay-items` to choose, `--overlay-height` for a smaller, faster
  copy). Package `overlay` writes an ASS script, frame-accurate at any
  frame rate below 100 fps, that libass draws in the H.264 encode of the
  copy (`encode.FFmpeg.Burn`); same frames, timestamps and audio as the
  source. The copy is encoded by the media engine when there is one
  (`--overlay-encoder auto`: VideoToolbox on macOS after a test encode,
  NVENC with `--gpu`, x264 otherwise), in segments split at keyframes and
  rendered concurrently (`--overlay-workers`), each with its slice of the
  script, joined without re-encoding (a segment that does not render the
  frames planned falls back to one pass): 59 minutes of 1080p25 annotated
  in 217 s on an M2 Max (778 s with x264, 6371 s of CPU against 387 s).
  ffmpeg's libass, and an explicit hardware encoder, are checked before any
  work; `qc version --check` lists VideoToolbox; the Docker images ship
  libass with DejaVu Sans Mono.
- **Fast frame analysis**: on macOS the video is split at keyframes into
  segments decoded concurrently by VideoToolbox (`--hwaccel auto`, the
  default; `videotoolbox` and `none` also accepted), each fed to forks of
  every analyzer (`analyze.Forker`, `analyze.RunSegments`) whose per-frame
  series are merged in order: the report is identical to a single pass, to
  the bit, and a segment that does not start where planned falls back to
  one. Segments seek on the container's timeline, so a video starting after
  its audio (some concatenations) no longer falls back. NEON loops on arm64 for SI, TI, luma statistics and thumbnails
  (checked against the portable Go ones), and Unix sockets instead of pipes
  for the raw frames. 59 minutes of 1080p25 H.264 analysed in 62–71 s at
  26 Mbit/s (225 s before) and 49–52 s at 6 Mbit/s (230 s) on an M2 Max; the
  single CPU pass (other systems, `--hwaccel none`) is 1.1× to 2.7× faster
  too, depending on the share of the decode. See
  [analysis.md](docs/analysis.md#performance).
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
- **Faster VMAF on macOS**: both videos are decoded by concurrent
  VideoToolbox sessions (`--hwaccel auto`, the default), sweeps are split
  into concurrent runs, and exact measurements are scored in segments split
  at keyframes, three libvmaf contexts at once, with two warm-up frames
  before each so that every frame scores as in a single pass. XPSNR has
  NEON loops (3.8× faster at 1080p). Results are identical to a CPU run,
  frame by frame. On an M2 Max: 26.5 → 18.6 s for the ±0.5 measurement of
  a 10:36 title, 58.7 → 42.1 s for a 59-minute one (184 → 108 s at
  `--sample 5%`), with a third of the CPU; exact measurements, bound by
  libvmaf's CPU, 149 → 131 s on the 10:36 title and 897 → 704 s on the
  59-minute one. See [vmaf.md](docs/vmaf.md#performance).
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
  - Probes at a faster preset (`--probe-preset`): the rungs are planned on
    them, then the top and bottom rungs encoded at `--preset` anchor the
    probes onto it (bitrate ratio and CRF offset between the presets); for
    slow delivery presets (SVT-AV1 preset 4 probed at 8: −24% time, same
    rungs).
  - Per-shot rungs (`--per-shot`): one CRF per shot at an equal
    rate-quality slope, with the per-shot ladder view (every shot's CRF,
    bitrate and VMAF per rung, and each rung's pooled bitrate over the
    title) in the terminal, HTML and JSON reports. The chunks of an encode
    and the per-shot verifications run concurrently, and the source's
    shots come from the analysis `qc run` already made, or from one run
    alongside the probes (per-shot stage −20% on the drama). Experimental per-shot
    resolution (`--per-shot-resolution`, implies `--per-shot`): shots also
    pick their resolution among neighbouring rung resolutions, at equal
    slope.
  - AV1 film grain synthesis (`--film-grain auto` or a level), detected and
    calibrated (the three calibration levels encoded concurrently),
    fidelity scored against a denoised reference. It cannot be
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
  interactive wizard (`qc` without arguments), opened by the pixel logo in
  square half-block pixels with a light sweeping across it once (still with
  `QC_NO_ANIMATION`, `NO_COLOR` or `ACCESSIBLE`): full screen, with a step
  indicator (Source › Analysis › Quality › Ladder › Outputs › Review) and a
  progress rail, a video browser with type-to-filter and the metadata of
  the highlighted file (codec, resolution, frame rate, bit depth, duration,
  HDR format, audio tracks, read by ffprobe) that refuses unreadable files
  and files without a video stream, a summary of the answers beside the
  form on wide terminals, and a review of every choice with the exact
  equivalent command before running (run, edit a section, or cancel). It
  adapts to light and dark terminals and to their size (80×24 and up),
  falls back to ASCII without colours (`NO_COLOR`, `TERM=dumb`, non-UTF-8
  locale), and asks plain prompts for screen readers (`ACCESSIBLE=1`).
- **Reports**: text in the terminal, JSON (`-f json`, `-o report.json`,
  `schemaVersion` 1) and self-contained interactive HTML (`--html`): a
  single offline file with an overall verdict (pass, needs attention, fail:
  the blocking rule lives in `internal/findings`), key numbers with
  sparklines and meters, findings filterable by kind, sections grouped by
  area (video, audio, quality, one per ladder, encoding) behind a sidebar
  navigation and a quick search (`/`, ⌘K), zoomable charts with exact
  per-frame tooltips, a crosshair synchronised across time charts and
  labelled targets and thresholds, light and dark themes, and a clean A4
  print. Set in an embedded Inter subset (SIL OFL, 43 KB).
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

### Fixed

- **Rungs at a lower resolution than the optimum's**: a rung can only take a
  resolution probed at its bitrate, and fixed probe CRFs could stop a higher
  resolution well above the lower rungs (an AV1 1080p curve at 1 Mb/s on
  content that compresses well), which then fell to 720p, 540p or 360p where
  1080p or 720p was 30–78% cheaper. Challenger probes now probe the higher
  resolution at such rungs' bitrates before the rungs are placed
  (`probing.challengers`). `bench/ladderval` extends its grid until every
  resolution covers the rungs' bitrates (its optimum had the same blind
  spot: a drama's AV1 ladder read +0.7% and was +7.4%), and
  `bench/ladderreplay` replays the engine on exact grids to compare probing
  changes in milliseconds.

- **Ladder quality level**: every probe and rung was scored on the same
  sampled frames, whose error the measurements shared (up to about 1 VMAF);
  at the top of a curve that is 25–30% of bitrate. A top rung encoded once
  and scored both sampled and exactly now puts every probe and sampled rung
  measurement on the exact scale (`probing.level`), the top rung is verified
  on every frame and corrected to within 0.5 VMAF of its target (a second
  secant step when the first falls short), and the top-rung finding reports
  the verified quality. On a 59-minute title at `--top-vmaf 94`, the AV1 top
  rung went from 5.00 Mb/s (about 94.6) to 4.23 Mb/s (93.83), 22% below
  H.264 instead of above it.

- **An encoder freezing no longer hangs a ladder**: an encode whose output
  stops growing for 5 minutes is stopped and run once more, then reported
  (`encode.ErrStalled`). SVT-AV1 at its fastest presets was seen to freeze
  with no CPU use, once in a few dozen builds.

- **Ladders of a video whose first frame is not the container's start**: a
  video starting after its audio (some concatenations: the first video
  frame at 0.04 s, the audio at 0), or a container starting before 0 (a
  Matroska file keeping its AAC priming at −21 ms). ffmpeg counts `-ss`
  from the container's start, and the digest's segments and the chunks of
  per-shot rungs were planned on the video's own timeline, so each one
  started early by the offset (a frame or more at 50 fps). The digest
  missed its segments' first frames (misplacing the shots' pieces), and the
  per-shot commands (and `bench/ladderval`'s chunked encodes of the title)
  duplicated frames at every chunk join. Every seek is now absolute
  (`-seek_timestamp 1 -ss origin+t`, from the video's first frame), as
  decoding's already were: the per-shot commands all carry
  `-seek_timestamp 1`.
- **Per-shot rungs of Matroska sources at 60, 59.94, 29.97 or 23.976
  fps** had a hole of one frame in their timestamps at some chunk joins
  (every frame present and right, but a variable frame rate, e.g. an
  average of 1350/23 instead of 60 fps, which players and packagers
  mishandle). Each chunk's time was counted from its output seek, half a
  frame before its first frame: with Matroska's millisecond timestamps,
  the frames fell on either side of the encoder's half ticks, and a frame
  rounded up left a gap. Chunks now trim their preroll in their filter
  chain and restart their timestamps at their first frame
  (`trim=start=…,setpts=PTS-STARTPTS`, replacing the output `-ss`), so the
  concat and MPEG-TS joins are constant frame rate at the source's rate.

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
  (`FFmpeg.Digest(ctx, encode.DigestSpec)`, whose `Origin` is the time of
  the video's first frame on the container's timeline) and encodes chunk
  by chunk (`FFmpeg.EncodeChunks` and `Codec.ChunkCommandLine`, reading an
  `encode.ChunkSource`: path, frame rate and origin); codecs are looked up with
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
- **Decoding**: `decode.Request.Origin` (seeks on the container's
  timeline), `decode.SegmentHWAccelReporter` (the hardware mode of segment
  decodes, reported in `quality.Result.HWAccel`), and the `segments` plan
  of `quality.Result.Plans`.
- **GPUs**: `decode.WithHWAccel` (reported through `decode.HWAccelReporter`),
  `quality.Options.Backend`, `ladder.Options.Encoder` and `Backend`,
  `quality.Result.GPUSummary`, and package `nvidia` to check the GPU before
  a long run (`nvidia.Check`, `nvidia.Available`).

[Unreleased]: https://github.com/eko/qc/commits/main
