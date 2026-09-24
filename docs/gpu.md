# NVIDIA GPUs

qc can move three things to an NVIDIA GPU: **decoding** (NVDEC), **ladder
encodes** (NVENC: `h264_nvenc`, `hevc_nvenc`, `av1_nvenc`) and **VMAF feature
extraction** (libvmaf's CUDA extractors). Everything else stays on the CPU,
and the CPU stays the default.

> **Status.** This support was written on a machine without an NVIDIA GPU. It
> is built and unit-tested there with fakes, compiled for real in a CUDA
> container (including libvmaf's CUDA kernels and an ffmpeg with NVDEC/NVENC),
> and its NVENC options are parsed by that ffmpeg. **No figure on this page
> was measured on a GPU yet**: every performance number is *expected* and
> comes with its source. The [validation kit](#validation-kit) measures them
> in one command; its report replaces the expected numbers.

## Quick start

```sh
# Build the NVIDIA image (or pull ghcr.io/eko/qc:<version>-cuda)
docker build -f Dockerfile.cuda -t qc:cuda .

# Everything available on the GPU; the reports say what ran where
docker run --rm --gpus all -v "$PWD:/data" qc:cuda run source.mov --gpu --codecs h264,hevc --html report.html

# Validate on your GPU: writes gpu-validation/gpu-report.md to paste back
make gpu-validate
```

## What runs where

| Work | Default | `--gpu` | Notes |
|---|---|---|---|
| Decoding for the frame analysis and VMAF | CPU (ffmpeg) | **NVDEC** (`--hwaccel cuda`) | frames are identical to a CPU decode; codecs NVDEC does not decode stay on the CPU |
| Scaling to the VMAF resolution | CPU (swscale bicubic) | CPU | `--hwaccel cuda-scale` moves it to `scale_cuda`, at the cost of bit-exactness |
| Frame analyzers (SI/TI, shots, black, freeze, crop, levels) | CPU (Go) | CPU (Go) | only their decoding moves |
| VMAF v0.6.1 family (v0.6.1, NEG, 4K) features | CPU | **CUDA** (`--vmaf-backend auto`) | integer VIF, ADM, motion |
| VMAF v1 features (qc's default model, `--devices`) | CPU | CPU | libvmaf has no CUDA version of them: `auto` falls back and the report says why |
| XPSNR, PSNR, CAMBI, SSIM, MS-SSIM, PSNR-HVS, CIEDE2000 | CPU | CPU | libvmaf extractors run on the CPU next to the CUDA ones, in the same context |
| Ladder digest | CPU (raw video, no codec) | CPU | |
| Probe and verification encodes | x264, x265, SVT-AV1 | **NVENC** (`--encoder nvenc`) | same probes, curves, rungs and checks |
| Rung commands | `ffmpeg -i … -c:v libx264 …` | `ffmpeg -hwaccel cuda -i … -c:v h264_nvenc …` | the encoder that was measured |
| Per-shot rungs, AV1 film grain synthesis | CPU | rejected with NVENC | see [limitations](#limitations) |

The live dashboard shows the GPU parts in its title
(`run · GPU: NVDEC, NVENC, CUDA VMAF (auto)`), stage summaries end with
`· CUDA` or `· h264_nvenc`, the terminal and HTML reports add a *GPU* line or
card, and the JSON report carries `vmaf.backend`, `vmaf.backendNote`,
`vmaf.hwaccel` and `codec.hardware`.

## Flags

| Flag | Values | Meaning |
|---|---|---|
| `--gpu` | | Use the GPU where available: `--hwaccel cuda`, `--encoder nvenc` (when ladders are built), `--vmaf-backend auto`. Flags set explicitly win. |
| `--hwaccel` | `none` (default), `cuda`, `cuda-scale` | Hardware decoding ([details](#decoding-nvdec)) |
| `--encoder` | `cpu` (default), `nvenc` | Ladder encoders, for every codec of the run |
| `--vmaf-backend` | `cpu` (default), `cuda`, `auto` | VMAF feature extraction. `cuda` fails for models without CUDA features; `auto` falls back to the CPU |

Like every flag they can be set through the environment: `QC_GPU=true`,
`QC_HWACCEL=cuda`, `QC_ENCODER=nvenc`, `QC_VMAF_BACKEND=auto`. The wizard
asks *Use the NVIDIA GPU?* only when the ffmpeg in use (`QC_FFMPEG`, the
`QC_CONFIG` file or `PATH`) has NVDEC and NVENC and the device opens.

**Checked before any work**, so that a missing GPU fails in a second rather
than after an hour:

1. the ffmpeg in use lists the `cuda` hwaccel (and `scale_cuda`, `hwdownload`
   for `cuda-scale`) and the NVENC encoders of the requested codecs;
2. the CUDA device opens (`ffmpeg -init_hw_device cuda`): no GPU, no driver or
   a container started without `--gpus` fail here;
3. each NVENC encoder encodes one frame at the ladder bit depth: AV1 on a GPU
   older than Ada Lovelace, 10-bit H.264 before Blackwell, or no free NVENC
   session fail here;
4. `--vmaf-backend cuda` initialises libvmaf's CUDA state (binary built with
   the `cuda` tag).

Each error says what is missing and how to get it (the CUDA image, `--gpus
all`).

Library users run the same checks with package `nvidia`, before a long job
(decoding falls back to the CPU by itself, NVENC does not); `libvmaf.InitCUDA`
checks CUDA VMAF:

```go
err := nvidia.Check(ctx, "ffmpeg", nvidia.Requirements{
	HWAccel:  decode.HWAccelCUDA,       // decode.WithHWAccel of the decoder
	Codecs:   []string{"hevc", "av1"}, // ladder.Options.Encoder = encode.HardwareNVENC
	BitDepth: 10,                      // ladder.Options.BitDepth
})
// A *nvidia.CheckError says which part failed (PartDecoding, PartEncoding,
// PartDevice) and wraps nvidia.ErrMissing, ErrDevice or ErrEncoder.
```

`nvidia.Available` is the cheaper question the wizard asks: NVDEC, NVENC
and a device, without encoding.

## Decoding (NVDEC)

`decode.FFmpeg` takes a hardware mode (`decode.WithHWAccel`):

- **`cuda`**: `ffmpeg -hwaccel cuda -i …`. NVDEC decodes and ffmpeg copies each
  frame to system memory (NV12, or P010 at 10 bits); frame selection
  (`select`), luma extraction and bicubic scaling then run on the CPU exactly
  as without a GPU, after an exact conversion to planar YUV. H.264, HEVC and
  AV1 decoders are bit-exact by specification, so the frames, hence every
  score, are **identical** to a CPU decode (the kit checks frame hashes and
  VMAF, difference 0). Seeking (`-ss` before `-i`), the `select` sweeps and
  the seek runs of the [decoding plans](vmaf.md#3-decoding-plans) are
  unchanged. For profiles NVDEC does not decode (H.264 4:2:2, 4:4:4 before
  the GPU generations that added them) ffmpeg falls back to software by
  itself.
- **`cuda-scale`**: `-hwaccel cuda -hwaccel_output_format cuda`, then
  `scale_cuda=W:H:interp_algo=bicubic:format=yuv420p[10le],hwdownload`. Scaling
  and the 4:2:0 conversion run on the GPU and only scaled frames cross PCIe,
  but `scale_cuda`'s bicubic kernel does not round like swscale's: scores
  differ slightly from a CPU decode (the kit reports how much). `select` runs
  after the download, since it cannot handle GPU frames: every decoded frame
  is scaled and copied, which still costs less than scaling on the CPU.
- **Fallbacks.** Files whose codec NVDEC does not decode (ProRes, DNxHR, FFV1,
  the ladder's raw digest) are decoded on the CPU without trying. When a
  hardware decode fails before its first frame (no device, an unsupported
  profile in `cuda-scale`), it is retried one mode down (`cuda-scale` →
  `cuda` → CPU), a warning is logged, and later decodes of that file start
  there. A failure after frames were delivered is an error, as on the CPU.
- 10-bit sources arrive as P010 and reach the analyzers and libvmaf as the
  same 16-bit little-endian planes as a CPU decode. Odd sizes are cropped by
  ffmpeg as usual (NVDEC decodes the coded size).

*Expected*: decoding stops being a CPU cost. It matters most for sampled VMAF
of long or 4K titles, where the seek runs decode several streams in parallel,
and for the frame analysis, which decodes every frame once. NVDEC throughput
per GPU is in NVIDIA's NVDEC application note; the kit measures ffmpeg's CPU,
NVDEC→host and GPU-resident decode rates on your content (section a).

## VMAF on CUDA

### What libvmaf 3.2.1 can extract on a GPU

libvmaf's CUDA support (`-Denable_cuda=true`) comes with three feature
extractors ([libvmaf/src/feature/cuda](https://github.com/Netflix/vmaf/tree/v3.2.1/libvmaf/src/feature/cuda)):

| CUDA extractor | Provides | Missing compared with the CPU extractor |
|---|---|---|
| `vif_cuda` | integer VIF scales 0–3 | |
| `adm_cuda` | integer ADM up to `adm2` (options: enhancement gain limit, viewing distance, display height) | `adm3`, the CSF mode, DLM and noise weights of VMAF v1 |
| `motion_cuda` | integer `motion`, `motion2` | `motion3` |

When a CUDA state is imported into a context, `vmaf_use_features_from_model`
looks every model feature up **among CUDA extractors only**
([libvmaf.c](https://github.com/Netflix/vmaf/blob/v3.2.1/libvmaf/src/libvmaf.c),
`VMAF_FEATURE_EXTRACTOR_CUDA`). Hence:

- **VMAF v0.6.1, v0.6.1 NEG and their 4K variants run entirely on the GPU**
  (integer VIF, adm2, motion2).
- **VMAF v1 cannot run on CUDA**: its four features (`cambi`,
  `speed_chroma_uv`, `adm3`, `motion3`) have no CUDA extractor, and the model
  fails to load. qc's default model and every `--devices` model are v1, so by
  default `--gpu` keeps VMAF on the CPU (`--vmaf-backend auto`), and the
  report says why. `--vmaf-backend cuda` refuses them before decoding.
- Extra extractors registered by name (PSNR, CAMBI, SSIM, MS-SSIM, PSNR-HVS,
  CIEDE2000) are CPU extractors in the same context: libvmaf hands them host
  pictures and the CUDA extractors device pictures. XPSNR is Go code.

`vmaf.CUDAUnsupported` reads a model's feature list (JSON models, known
built-ins) and `vmaf.ResolveBackend` applies these rules before anything is
decoded. To measure VMAF on the GPU:

```sh
qc vmaf reference.mov encode.mp4 --gpu --model vmaf_v0.6.1 --metrics= --exact
```

A CUDA port of the v1 features (adm3, motion3, CAMBI, chroma speed) would
have to land in libvmaf first.

### Implementation

- Package `vmaf/libvmaf`, behind the `cuda` build tag (`vmaf/libvmaf/cuda.go`;
  the default build compiles `vmaf/libvmaf/nocuda.go` and is unchanged).
  `vmaf.ScorerConfig.Backend` selects `BackendCPU` or `BackendCUDA`; without
  the tag, CUDA returns `vmaf.ErrCUDAUnavailable`.
- **One CUDA state per scorer.** `vmaf_close` destroys the stream and
  releases the context retain of the state it imported, so a state cannot be
  shared by the per-clip contexts of the sampler. A process-wide state is
  created once (`libvmaf.InitCUDA`) and never released: it keeps the primary
  context alive, otherwise each clip would create and destroy it.
- A CUDA scorer locks its goroutine to its OS thread until `Close`.
- Frames are copied into host pictures as on the CPU, and libvmaf uploads
  them. libvmaf 3.2.1's *pinned* preallocation allocates pinned memory per
  picture fetched (`cuMemHostAlloc`), which costs more than a pageable copy
  of a 1080p frame, so it is not used.
- libvmaf never frees the small driver function table of each state: a
  bounded leak of a few kilobytes per scored clip.

*Expected*: NVIDIA measured VMAF-CUDA on an L4 against a dual Xeon 8480
(112 threads): **2.5× the FFmpeg throughput at 1080p (775 vs 176 fps) and
2.8× at 4K (178 vs 64 fps)**, and 26–37× lower per-frame latency
([NVIDIA blog](https://developer.nvidia.com/blog/calculating-video-quality-using-nvidia-gpus-and-vmaf-cuda/)).
Against a laptop-class CPU the gap should be larger. In qc, exact mode (one
context per title) should get closest to those figures; sampled mode creates
a context per 6-frame clip, whose GPU set-up is amortised over few frames.
The CUDA kernels port libvmaf's integer (fixed-point) code, so scores are
expected to agree with the CPU to about 1e-3 per frame (the unit test
accepts 0.01). The kit measures both (section b), with ffmpeg's
`libvmaf_cuda` filter as an independent cross-check.

## NVENC ladders

`--encoder nvenc` (library: `ladder.Options.Encoder = encode.HardwareNVENC`,
or `encode.LookupFor(codec, encode.HardwareNVENC)`) keeps the codec names
(h264, hevc, av1) and swaps the encoders. The ladder engine is unchanged:
same digest, probes, curves, envelope, rung selection, verification and
calibration.

### Settings

| Setting | Value | Why |
|---|---|---|
| Preset | `p5` (`--preset p1`…`p7`) | NVIDIA recommends P4–P7 for latency-tolerant transcoding; p5 is still hundreds of 1080p fps |
| Tuning | `-tune hq` | NVIDIA's tuning for OTT streaming and archiving ([programming guide](https://docs.nvidia.com/video-technologies/video-codec-sdk/13.0/nvenc-video-encoder-api-prog-guide/index.html)). `uhq` exists for HEVC/AV1 on Ada and newer only |
| Constant quality | `-rc vbr -cq N -b:v 0` | NVENC's constant-quality mode. `-b:v 0` matters: ffmpeg's NVENC default is 2 Mb/s, which older releases kept as an average target next to `-cq` |
| GOP | `-g N` (2 s) | NVENC's IDR period is its GOP length: every segment starts with an IDR |
| No scene-cut keyframes | `-rc-lookahead 20 -no-scenecut 1` | lookahead improves bit distribution; with it NVENC may insert I-frames at cuts, which `-no-scenecut` disables |
| Forced keyframes | `-forced-idr 1` | a `-force_key_frames` added to a rung command gives IDRs |
| `-strict_gop` | not used | it evens the rate per GOP towards a bitrate target, which constant quality has not |
| VBV | `-maxrate 2× -bufsize 4×` as on the CPU | ffmpeg 9 honours only `-maxrate` in CQ mode and drops the buffer size ([nvenc.c](https://github.com/FFmpeg/FFmpeg/blob/n9.0.2/libavcodec/nvenc.c), "CQ mode shall discard avg bitrate/vbv buffer size"): the peak cap holds, but not over the CPU encoders' 2 s window |
| 10-bit | `format=p010le` | NVENC takes P010, not `yuv420p10le`: HEVC Main10 and AV1 10-bit. H.264 High 10 needs a Blackwell GPU and NVENC API 13 headers (the image ships 12.2, see below) |
| Quality scale | integer CQ | NVENC takes fractional CQs; integers keep every driver on known ground |
| AQ, multipass | off (driver defaults) | spatial AQ trades PSNR/VMAF for perceived detail; multipass targets bitrates, not quality. Candidates for the calibration |

Probe CQs, **defaults to be calibrated on a GPU** (the kit's section d
suggests values from a CQ sweep):

| Codec | Encoder | Probe CQs | CPU probe CRFs, for reference |
|---|---|---|---|
| h264 | `h264_nvenc` | 21, 28, 35 | 20, 27, 34 (x264) |
| hevc | `hevc_nvenc` | 23, 30, 37 | 22, 29, 36 (x265) |
| av1 | `av1_nvenc` | 28, 38, 48 | 28, 40, 52 (SVT-AV1) |

They follow the CPU sets, which span VMAF ~97 at the top resolution to ~40 at
the bottom one: NVENC's CQ is a QP-like scale close to x264's CRF, and
`av1_nvenc`'s 0–63 scale is SVT-AV1's.

### Commands

Rung commands decode on the GPU and keep the CPU scaler, so the encoder sees
exactly the frames that were measured:

```sh
ffmpeg -hwaccel cuda -i source.mov -an -sn -dn -vf scale=1280:720:flags=bicubic,format=yuv420p \
  -c:v h264_nvenc -preset p5 -tune hq -rc vbr -cq 27 -b:v 0 -g 50 -maxrate 3000000 -bufsize 6000000 \
  -rc-lookahead 20 -no-scenecut 1 -forced-idr 1 01-720p.mp4
```

A fully GPU pipeline (`-hwaccel_output_format cuda` and
`-vf scale_cuda=1280:720:interp_algo=bicubic`) is faster in production but
not what the ladder measured: re-verify a rung before switching scalers.

*Expected*: NVENC encodes many times faster than the CPU encoders (NVIDIA's
application note lists 1080p H.264 on Ada from 910 fps at P1 to 211 fps at P7
VBR, [NVENC application note](https://docs.nvidia.com/video-technologies/video-codec-sdk/13.0/nvenc-application-note/index.html)),
so probe encodes stop dominating a ladder, and VMAF measurement becomes the
cost (hence `--gpu` also offloading decoding). Hardware encoders usually need
more bitrate than x264/x265/SVT-AV1 at their slower presets for the same
quality; how much, on your content, is what the kit's section c measures
(bitrate of NVENC at the VMAF of each CPU rung).

## Docker image

`Dockerfile.cuda` (published by the release workflow as
`ghcr.io/eko/qc:<version>-cuda`) is multi-stage:

| Stage | Contents |
|---|---|
| toolchain | `nvidia/cuda:12.9.2-devel-ubuntu24.04`, clang, meson, the x264/x265/dav1d headers |
| svtav1 | SVT-AV1 4.2.0 from source (Ubuntu's 1.7 is two majors behind and refuses frames under 64×64) |
| libvmaf | libvmaf 3.2.1 with `-Denable_cuda=true -Denable_nvcc=true`: kernels compiled to SASS for sm_75–sm_120 (Turing to Blackwell) plus PTX, and the VMAF v1 models |
| ffmpeg | ffmpeg 9.0.2 with `--enable-nvdec --enable-nvenc --enable-cuvid --enable-cuda-llvm` (`scale_cuda` compiled by clang), libx264, libx265, SVT-AV1, dav1d and libvmaf (hence `libvmaf_cuda`) |
| build | qc built with `-tags cuda`, and `gpuval` |
| test | `go vet -tags cuda ./...` and the tests of the GPU packages (GPU tests skip without a GPU): `docker build -f Dockerfile.cuda --target test .` |
| runtime | `nvidia/cuda:12.9.2-base-ubuntu24.04` + runtime libraries, ffmpeg, ffprobe, libvmaf, the models, qc, gpuval; `NVIDIA_DRIVER_CAPABILITIES=compute,video,utility`; non-root user |

- **Driver**: NVENC/NVDEC go through nv-codec-headers n12.2, which need a
  Linux driver ≥ 550.54.14. libvmaf 3.2.1 needs loader functions first
  shipped in nv-codec-headers n13.1 (`cuMemHostAlloc`, `cuMemFreeAsync`...);
  those CUDA driver functions exist since CUDA 11.2, so building libvmaf
  against n13.1 raises no driver requirement. Set `NV_CODEC_HEADERS=n13.0.19.0`
  (driver ≥ 570) for H.264 10-bit on Blackwell.
- **Redistributable**: no non-free component (`--enable-cuda-nvcc` and
  `--enable-libnpp` would make ffmpeg non-free, so `scale_cuda` is compiled
  with clang and `scale_npp` is left out). ffmpeg is GPL (x264, x265).
- **Size**: 692 MB (linux/arm64). Built and tested here on linux/arm64
  (Apple M2, Docker); linux/amd64 is a QEMU-emulated build, see the final
  report for its status.

## Validation kit

```sh
make gpu-validate                         # builds Dockerfile.cuda, runs the kit
QC_IMAGE=ghcr.io/eko/qc:<version>-cuda bench/gpu/validate.sh   # a published image
```

It runs `gpuval` (`bench/gpuval`) in the image with `--gpus all` and writes
`gpu-validation/gpu-report.md`, a Markdown report to paste back (estimated
15–30 minutes on a recent GPU, most of it in the CPU ladders it compares
against). Content is synthetic (ffmpeg lavfi: moving test
pattern, the same with grain, gradients, a fractal zoom; 4 × 8 s at 1080p25),
so nothing private is involved.

| Section | Measures | Pass criteria |
|---|---|---|
| a. NVDEC | ffmpeg decode fps on the CPU, NVDEC→host and GPU-resident, for H.264, HEVC Main10 and AV1; frame hashes CPU vs NVDEC; `qc analyze` time and exact VMAF per `--hwaccel` mode | identical hashes; VMAF difference 0 with `cuda` |
| b. CUDA VMAF | exact VMAF v0.6.1 at 8 and 10 bits, CPU vs CUDA features: time, speed-up, per-frame max/mean/p99 difference; ffmpeg `libvmaf_cuda` cross-check; qc's default measurement with and without `--gpu` | per-frame max difference ≲ 1e-2; identical default means |
| c. NVENC ladders | `qc ladder` per codec, CPU vs `--gpu`: time, and the NVENC bitrate at the VMAF of every CPU rung (NVENC envelope) | informative |
| d. CQ calibration | NVENC CQ sweeps at the top resolution and 360p, VMAF v1 sampled like the probes | suggested probe CQs to paste into the NVENC codec table (`encode/nvenc.go`) |

On real content: `bench/gpu/validate.sh -source /work/clip.mp4` with a public
clip copied into `gpu-validation/`, e.g. from
[Netflix Open Content](https://opencontent.netflix.com/) (*Meridian*, Creative
Commons licensed) or Blender's
[*Tears of Steel*](https://mango.blender.org/download/) (CC BY 3.0).
`-codecs`, `-skip-ladders`, `-skip-calibration` and `-seconds` shorten a run.

## Limitations

- VMAF v1, qc's default, cannot run on CUDA with libvmaf 3.2.1 (see above).
  With `--gpu`, only decoding moves unless a v0.6.1 model is chosen.
- With `--vmaf-backend cuda`, the default extra metrics (XPSNR, CAMBI, PSNR)
  stay on the CPU; `--metrics=` leaves VMAF alone.
- Per-shot rungs are rejected with NVENC: joining chunks without
  re-encoding is only verified for x264, x265 and SVT-AV1 (frame checksums),
  not for NVENC, whose parameter sets could differ between chunks of
  different CQs.
- AV1 film grain synthesis is an SVT-AV1 feature: rejected with
  `av1_nvenc`.
- In CQ mode ffmpeg enforces NVENC's `-maxrate` but not the 2 s buffer: peaks
  are capped, but less strictly than on the CPU encoders.
- GPUs outside NVIDIA's qualified list (GeForce) run at most 8 NVENC
  sessions per system (NVENC application note): keep `--parallel` below
  that, other processes included.
- Sampled VMAF on CUDA pays a GPU context set-up per 6-frame clip, and runs
  `--workers` contexts at once (GPU memory grows with them at 4K).
- A single GPU (device 0) is used.
- NVENC probe CQs are defaults until the kit's calibration is folded back.
