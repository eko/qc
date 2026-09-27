# CLI

```
qc                      interactive wizard (in a terminal)
qc run <source>         everything: analysis, VMAF (with -r), ladders
qc analyze <file>       technical analysis
qc vmaf <ref> <dist>    VMAF of dist against ref
qc ladder <source>      per-title ladder for one codec
qc version [--check]    versions of qc, Go and libvmaf; --check: the environment
```

Every flag can also be set through a `QC_` environment variable (dashes become
underscores): `QC_FFMPEG=/opt/ffmpeg/bin/ffmpeg`, `QC_PRECISION=0.25`, or in
a [configuration file](#configuration-file).

## Live dashboard

In a terminal, every command draws a live dashboard on stderr:

```
 ◆ qc  run  ·  source.mov                                                 ⏱ 01:12

  ✓  Inspect               120ms  h264 · 1920×1080 · 25 fps · 10m36s
  ✓  Frame analysis          34s  212 shots · SI 38 · TI 11
  ⣷  Ladder · h264       ━━━━━━━━━━━━━━━━━━━━━━  73%  11/15 encodes · ETA 00:14 · probe encodes
  ○  Ladder · av1         waiting

 ╭──────────────────────────────────────────────────────────────────────────╮
 │ probe encodes  last: 360p crf 27 → 360 kb/s, VMAF 64.1   ━━ 1080p ━━ 720p │
 │        100 ┤                                        ⢀⣀⣀⣀⣀⣤⠤⠤⠤⠖⠒⠒⠋⠉⠉      │
 │            ┤                     ⣀⣀⣤⠤⠴⠒⠒⠋⠉⠉                            │
 │         60 ┤     ⣀⣀⡤⠴⠒⠚⠉⠉                                               │
 ╰──────────────────────────────────────────────────────────────────────────╯
 q quit
```

- Each stage shows a spinner, a gradient progress bar, a counter (frames or
  encodes), a rate and an ETA.
- The running stage has a live panel:
  - **VMAF**: the running estimate and its confidence interval on a 0–100
    gauge, updated after each sampling round;
  - **ladder probes**: every probe encode lands on a braille rate-quality
    chart, one colour per resolution;
  - **ladder verification**: predicted vs measured quality of each rung as it
    is verified.
- `q`, `esc` or `ctrl+c` cancel the run (the context is cancelled and
  subprocesses are killed).
- Without a terminal (pipes, CI) nothing is drawn and output stays clean.

Charts use braille characters (2×4 dots per cell) and bars use `━`: both are
single-width, so layouts stay aligned in every terminal.

## Wizard

`qc` without arguments, in a terminal:

1. pick the video in a file browser (sizes shown; only video files can be selected);
2. choose what to compute: technical analysis, VMAF against a reference,
   streaming ladder;
3. for VMAF: the reference, then the **VMAF mode**: a target precision
   (± VMAF, adaptive, the default), a fixed budget as a share of the frames
   (5% by default) or as clips per scene (2 by default), or exact scoring,
   each followed by its value; then the
   **metrics** to measure next to VMAF, ticked one by one with their CPU cost
   (defaults pre-ticked: XPSNR, CAMBI, PSNR; untick everything for VMAF
   only), and optional **per-device VMAF** (phone, TV, 4K). This screen also
   shows for ladders: the verified rungs get the same metrics;
4. for ladders: the codecs, then optionally **customise the ladder**:
   - shape: automatic, a number of rungs, or one rung per listed resolution;
   - top and minimum VMAF;
   - bitrate cap (kb/s), encoder preset, 8 or 10-bit, verification on or off;
5. when the picked video (or the reference) is HDR and VMAF is measured, how
   VMAF scores it: on the HDR signal or on an SDR tone mapping
   (`--hdr-metric`), with the detected format (HDR10, HLG…) in the question;
6. when an NVIDIA GPU is usable with the ffmpeg in use (`QC_FFMPEG`, the
   `QC_CONFIG` file or `PATH`; see [GPU](#nvidia-gpu)), whether to use it
   (`--gpu`);
7. optionally name an HTML report.

It prints the equivalent `qc run` command, only with the options that differ
from the defaults, and runs exactly that command with the dashboard.

## Flags

### Output (every command)

| Flag | Meaning |
|---|---|
| `-f, --format text\|json` | stdout format (default text) |
| `-o, --output file.json` | also write the full JSON report |
| `--html file.html` | also write a self-contained, interactive HTML report (see [Reports](#reports)) |
| `--ffmpeg`, `--ffprobe` | binaries (default from `PATH`) |
| `--log-level` | debug, info, warn, error |
| `--config file` | [configuration file](#configuration-file) (YAML, TOML or JSON; also `QC_CONFIG`) |

### Configuration file

`--config qc.yaml` (or `QC_CONFIG=qc.yaml`) reads flags from a YAML, TOML or
JSON file, picked by its extension. Keys are the flag names, values are what
the flags take (lists as lists or comma-separated strings, durations as
`2s`):

```yaml
# qc.yaml
ffmpeg: /opt/ffmpeg/bin/ffmpeg
codecs: [h264, av1]
heights: [1080, 720, 540, 360]
precision: 0.25
metrics: [xpsnr, cambi]
encode-bit-depth: 10
fast: true            # analyze only: each command reads the keys it has
```

A value set in several places is taken, in order, from the command line,
the `QC_` environment variable, the file, then the flag default: `qc run
source.mov --config qc.yaml --codecs hevc` builds an HEVC ladder only. One
file can serve every command: each reads the keys of its own flags and
ignores the others. An unknown key (a typo) or an invalid value is an
error naming the file and the key, before any work. A precision set in the
file counts as given, like `--precision`, and conflicts with `--sample`.

### `run`

| Flag | Meaning |
|---|---|
| `-r, --reference file` | also measure the VMAF of the source against this reference |
| `--codecs h264,hevc,av1` | ladders to build (default h264; empty to skip) |
| `--skip-analysis` | skip the frame analysis |
| plus the VMAF and ladder flags below | |

### `analyze`

| Flag | Default | Meaning |
|---|---|---|
| `--fast` | off | container and bitstream only, no decoding (< 1 s) |
| `--bitrate-interval` | 1s | bucket of the bitrate series |
| `--peak-window` | 1s | sliding window of the peak bitrate |

### VMAF (`vmaf`, `run`)

| Flag | Default | Meaning |
|---|---|---|
| `--exact` | off | score every frame |
| `--precision` | 0.5 | target half-width of the 95% CI |
| `--max-share` | 0.4 | above this share of frames, score every frame instead |
| `--sample` | none | fixed budget instead of a precision, scored in one round: a share of the frames (`5%`) or clips per scene (`2/scene`, also `2-per-scene`); reports the interval reached; excludes `--exact` and `--precision` — see [fixed budgets](vmaf.md#fixed-budgets---sample) |
| `--model` | auto | `auto`, a model name, a `.json` path or a built-in version |
| `--model-dir` | Homebrew/`/usr/local`/`/usr` model dirs | where model files are searched |
| `--vmaf-bit-depth` | 0 (auto) | 8 or 10 |
| `--workers` | 0 (NumCPU/2) | clips scored concurrently |
| `--metrics` | xpsnr,cambi,psnr | metrics measured on the frames VMAF decodes, each with its own CI: `xpsnr`, `cambi`, `psnr`, `psnr-hvs`, `ssim`, `ms-ssim`, `ciede2000`; `--metrics=` for VMAF only |
| `--av2-ctc` | off | add the AOM AV2 CTC set: PSNR Y/Cb/Cr and PSNR-YUV 14:1:1, PSNR-HVS, SSIM, MS-SSIM, CIEDE2000, CAMBI (MS-SSIM and CIEDE2000 cost 4× and 9× VMAF) |
| `--devices` | none | also score the VMAF v1 model of `phone`, `tv`, `4k` (HFR variants above 30 fps); `4k` against a 1080p primary is a second pass at 2160p |
| `--hdr-metric` | pq | VMAF on HDR (PQ/HLG) references: `pq` (on the HDR signal, fast, not HDR-calibrated) or `tonemap` (on an SDR BT.709 tone mapping of both videos, slower); HDR references always get wPSNR and ΔE ITP — see [HDR](hdr.md#4-vmaf-on-hdr) |

The costs behind these defaults, and how the primary VMAF is chosen, are in
[the VMAF engine](vmaf.md#5-other-metrics-and-devices).

### Ladder (`ladder`, `run`)

| Flag | Default | Meaning |
|---|---|---|
| `-c, --codec` (ladder only) | h264 | h264, hevc or av1 |
| `--model`, `--model-dir` (ladder only; `run` shares the VMAF ones) | auto | VMAF model of the probe measurements |
| `--preset` | codec default | encoder preset |
| `--rungs` | auto | ladder shape: `auto`, a rung count (`6`) or the rung resolutions top first (`1080,720,720,540,360`) — see [ladder shapes](ladder.md#5-rung-selection) |
| `--top-vmaf` | 95 | quality of the top rung (the highest VMAF targeted) |
| `--step` | 6 | VMAF step between rungs |
| `--min-vmaf` | 30 | lowest rung quality |
| `--max-rungs` | 8 | cap of the automatic shape |
| `--min-bitrate`, `--max-bitrate` | 145000, none | bits/s |
| `--heights` | 2160,1440,1080,720,540,360,270 | candidate resolutions |
| `--encode-bit-depth` | 8 | 10 for Main10 encodes |
| `--no-verify` | off | skip the verification encodes |
| `--commands` | off | print each rung's ffmpeg command |
| `--parallel` | 2 | probe encodes run concurrently |
| `--probing` | fixed | probe placement: `fixed` (3 CRFs per resolution) or `adaptive` (2 per resolution, then probes where the rungs and crossovers are least certain) — see [probing](ladder.md#adaptive-probing) |
| `--per-shot` | off | add a per-shot version of every rung: one CRF per shot at equal rate-quality slope, same pooled VMAF — see [per-shot](ladder.md#8-per-shot-rungs). The reports then show the per-shot ladder (every shot's CRF, bitrate and VMAF per rung) |
| `--per-shot-resolution` | off | experimental, implies `--per-shot`: each shot also picks its resolution among the rung's and the neighbouring rung resolutions; renditions change resolution mid-stream — see [per-shot resolution](ladder.md#per-shot-resolution-experimental) |
| `--film-grain` | off | AV1 only: `off`, `auto` (detect grain, calibrate the level) or a synthesis level `1`–`50`; fidelity is then scored against a denoised reference; cannot be combined with `--per-shot` on an AV1 ladder — see [film grain](ladder.md#9-film-grain-synthesis-av1) |
| `--metrics`, `--av2-ctc`, `--devices` (ladder only; `run` shares the VMAF ones) | xpsnr,cambi,psnr | measured on the verification encodes of the rungs, next to VMAF: flags banding-limited rungs and rungs VMAF and XPSNR order differently — see [rung quality](ladder.md#rung-quality) |
| `--hdr-metric` (ladder only; `run` shares the VMAF one) | pq | how VMAF scores the probes and rungs of an HDR source; HDR sources are always encoded in 10 bits with their colour description and HDR10 metadata — see [HDR ladders](hdr.md#5-hdr-ladders) |

### NVIDIA GPU

`analyze` takes `--gpu` and `--hwaccel`, `vmaf` adds `--vmaf-backend`,
`ladder` and `run` take all four. Requirements, what runs where and the
validation kit: [gpu.md](gpu.md).

| Flag | Default | Meaning |
|---|---|---|
| `--gpu` | off | use the GPU where available: `--hwaccel cuda`, `--encoder nvenc` for ladders, `--vmaf-backend auto`; flags set explicitly win. ffmpeg's NVIDIA support, the device and each NVENC encoder are checked before any work |
| `--hwaccel` | none | `cuda`: NVDEC decoding, frames identical to a CPU decode; `cuda-scale`: NVDEC and GPU scaling (not bit-exact); falls back to the CPU per file |
| `--encoder` | cpu | `nvenc`: ladders with `h264_nvenc`, `hevc_nvenc`, `av1_nvenc` (CQ probes, same engine); not with `--per-shot` or AV1 `--film-grain` |
| `--vmaf-backend` | cpu | `cuda`: VMAF features on the GPU (binaries built with `-tags cuda`; VMAF v0.6.1 family only, VMAF v1 has no CUDA features); `auto`: CUDA when possible, the CPU otherwise, with the reason in the report |

### `version`

| Flag | Default | Meaning |
|---|---|---|
| `--check` | off | also check ffmpeg and ffprobe, the libx264, libx265 and libsvtav1 encoders, NVENC (optional) and that the default VMAF v1 model loads; exits with an error when a requirement is missing |
| `-f`, `--format` | `text` | `text` or `json` |
| `--model-dir` | the libvmaf model directories | directories searched for the VMAF models |

Paste the output of `qc version --check` in bug reports. See
[install.md](install.md) for the fixes.

## Reports

- **Terminal**: cards, a bitrate chart, SI/TI/luma/frame-size sparklines, a
  timeline of shots, black/frozen segments and keyframes, the hardest shots,
  findings. HDR videos get a format badge and a light level block (MaxCLL,
  MaxFALL, peak and average light over time). For VMAF: a score gauge with its interval, and quality over time.
  For ladders: the rate-quality chart, the rung table (predicted vs measured)
  and findings.
- **JSON** (`schemaVersion` 1): the full results. Durations are in seconds, and
  per-frame series are stored as columns. `qc run` writes one document with
  `analysis`, `comparison` and `ladders`.
- **HTML**: one self-contained page per report (or a combined page for
  `qc run`): a single file with its style and script inline, no external
  request, so it opens offline and can be mailed or attached as is.
  - **Header**: key numbers as cards (duration, resolution, codec, bitrate,
    VMAF ± CI, worst frame, rungs, banding) and a findings count linking to
    the findings, listed by severity (warning, note, passed).
  - **Charts** (bitrate, frame sizes with keyframes, SI/TI, luma, light
    levels of HDR videos, VMAF, CAMBI, rate-quality): hovering shows the exact values at the pointer —
    time as hh:mm:ss.mmm and frame number, every series at that time, the
    other metrics of the same frame under VMAF, the black/frozen/banded
    segment under the cursor. Ladder points show bitrate, VMAF (± its
    interval), resolution and CRF, rungs their predicted and measured
    quality. Tooltips read every measured frame, not the drawn average;
    long titles store them gzipped (a two-hour title stays under 3 MB).
  - **Zoom**: drag across a time chart to zoom, double-click (or Reset) to
    go back; all time charts zoom together. Keyboard: focus a chart, arrows
    step frame by frame (Shift ×10), `+`/`-` zoom, Escape resets.
  - **Timestamps** in findings, shot and banding tables zoom the charts on
    their range and highlight it.
  - Legend entries toggle their series; table columns sort; encoding
    commands have copy buttons; sections collapse; the light/dark theme
    follows the system and a toggle remembers the choice. Printing opens
    every section in the light theme.
  - Without script, the page still shows every chart (static SVG), table and
    command.

A hidden `--cpuprofile file` flag writes a Go CPU profile of the run.
