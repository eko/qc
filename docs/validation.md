# Validation

A fast estimate is only useful if its error is known. Every speed-up in
qc was kept only after being checked against ground truth
computed the slow way. The tools below make those checks reproducible on your
own content.

## Exactness

| Check | Result |
|---|---|
| Per-frame VMAF, 8-bit, `vmaf_v1.0.16_3d0h`, 300 frames vs the `vmaf` CLI | max difference 0.000000 |
| Same at 10 bits (FFV1 reference, SVT-AV1 10-bit rendition) | max difference 0.000000 |
| Clip scores (4 frames + 1 warm-up frame each side) vs exact run | 0.000 on every sampled frame |
| Seek-run decoding vs exact run (after the open-GOP fix) | 0 frames differ out of 2 913 |
| XPSNR (Go) vs ffmpeg's `xpsnr` filter, synthetic content, every code path (≤ 480p min-smoothing, HD, 60 fps second-order activity, above-HD downsampling with short and empty edge blocks, 8 and 10 bits, pictures too small to weight) — `TestMatchesFFmpeg`, in CI | per frame and pooled within 10⁻⁴ dB (ffmpeg's printed precision) |
| XPSNR through `qc vmaf --exact` vs ffmpeg on identically scaled frames: drama 1 501 frames, 8 bits | max 2.4·10⁻⁶ dB per frame; pooled Y/U/V identical (35.2536 / 39.6899 / 39.7123) |
| Same, 300 frames at 10 bits (FFV1 reference, SVT-AV1 10-bit rendition) | max 2.3·10⁻⁶ dB per frame; pooled identical (34.4220 / 38.6963 / 38.7574) |
| Every series of a sampled run (XPSNR, CAMBI, PSNR, device VMAF) vs the exact run, 539 scored frames | difference 0 on every frame |
| Phone VMAF scored next to the TV model vs scored alone | identical per frame (`TestPhoneDeviceMatchesItsModel`) |

## VMAF sampling: replay simulation (`bench/vmafsim`)

```sh
qc vmaf reference.mov distorted.mp4 --exact -f json > exact.json
go run ./bench/vmafsim -runs 1000 -precisions 0.25,0.5,1 exact.json
```

`vmafsim` runs the **production sampling loop** (the same `sample` function,
fed with the exact per-frame scores instead of libvmaf), once per seed. For
each target precision it reports:

- the RMSE of the estimate against the true mean,
- the mean reported half-width,
- the **real coverage**: how often the reported 95% interval contains the
  truth. It should be ~95%. Coverage over sampled runs only excludes runs that
  fell back to exact scoring.
- the share of frames scored, and the number of fallbacks.

Designs rejected because replay proved them wrong:

| Design | Real coverage at ±0.5 |
|---|---|
| Stop as soon as the interval looks narrow enough | 68–88% |
| Per-stratum variances from 2 clips each | 68–88% |
| Bitstream control variate (distorted/reference packet size ratio) | useless: correlation 0.02 with VMAF on CRF encodes |
| **Pooled variance + two-stage design (current)** | **94–96%** |

Current results (1 000 replays each):

| Content | Precision | Frames scored | RMSE | Coverage |
|---|---|---|---|---|
| Cartoon 10:36 | ±0.25 | 22% | 0.10 | 94.8% |
| Cartoon 10:36 | ±0.5 | 5.4% | 0.24 | 94.5% |
| Cartoon 10:36 | ±1 | 5.2% | 0.24 | 94.2% |
| Drama 1 min | ±0.5 | 36% | 0.12 | 95.7% |

A synthetic test (`TestSimulateCoverage`) runs the same check in CI on
shot-structured scores.

### Other metrics and devices

The other metrics and the device VMAFs are estimated from the clips the
primary VMAF selects (see [VMAF engine](vmaf.md#5-other-metrics-and-devices)).
`-series` replays them on the same runs, from an exact report measured with
them (`qc vmaf --exact --metrics xpsnr --av2-ctc --devices phone,tv,4k`); XPSNR
is replayed on its distortion √WSSE, the quantity it pools:

```sh
go run ./bench/vmafsim -runs 1000 -precisions 0.5 -series all exact.json
```

Drama 1 min, ±0.5 (36% of the frames scored, 1 000 replays):

| Series | Real coverage | Series | Real coverage |
|---|---|---|---|
| VMAF (primary, tv) | 95.7% | PSNR Y / Cb / Cr | 93.3% / 93.6% / 92.9% |
| VMAF phone | 95.8% | PSNR-YUV | 93.4% |
| VMAF 4K (2160p pass) | 95.9% | PSNR-HVS | 93.6% |
| XPSNR Y / U / V | 94.4% / 95.1% / 93.9% | SSIM / MS-SSIM | 92.2% / 92.7% |
| CAMBI | 93.4% | CIEDE2000 | 92.9% |

Cartoon 10:36, ±0.5 (5.4% of the frames scored, 1 000 replays; exact report
with `--metrics xpsnr,cambi,psnr,psnr-hvs,ssim --devices phone`):

| Series | Real coverage | Series | Real coverage |
|---|---|---|---|
| VMAF (primary, tv) | 94.5% | PSNR Y / Cb / Cr | 93.9% / 93.5% / 93.4% |
| VMAF phone | 94.9% | PSNR-YUV | 93.9% |
| XPSNR Y / U / V | 95.0% / 94.0% / 93.8% | PSNR-HVS | 94.5% |
| CAMBI | 93.0% | SSIM | 93.6% |

VMAF's own coverage is unchanged by the new series (95.7% and 94.5%, as
before). The device VMAFs, strongly correlated with the primary, keep the nominal
coverage. The other metrics come out 0–3 points below 95%: their clip means
spread differently across strata than VMAF's, and the pooled variance fits
VMAF best. Their intervals are honest to within a few points, but only VMAF's
is guaranteed; `--exact` gives every metric exactly. `TestSimulateSeriesCoverage`
checks two synthetic series (one correlated with VMAF, one independent) in
CI.

### Fixed budgets

`-samples` replays the [fixed budgets](vmaf.md#fixed-budgets---sample) through
the same production code. `-shots` adds the shot cuts of a technical analysis
of each distorted file (`qc analyze distorted.mp4 -o analysis.json`, one file
per measurement, in order), as `qc run` does:

```sh
go run ./bench/vmafsim -runs 2000 -precisions= -samples 1%,2%,5%,10%,1/scene,2/scene,3/scene \
    [-shots analysis.json] exact.json
```

2 000 replays each. The share is the share of frames scored. Scenes are the
distorted keyframes (241 on the cartoon, 38 on the drama), or those plus the
detected shot cuts (259 and 40 strata):

| Content | Budget | Frames scored | RMSE | Mean \|error\| | Mean ± | Coverage | Coverage with shot cuts |
|---|---|---|---|---|---|---|---|
| Cartoon 10:36 | ±0.5 (precision, reference) | 5.5% | 0.24 | 0.19 | 0.46 | 94.9% | – |
| Cartoon 10:36 | 1% | 1.0% | 0.58 | 0.47 | 1.23 | 95.1% | 95.1% |
| Cartoon 10:36 | 2% | 2.0% | 0.41 | 0.32 | 0.81 | 94.5% | 95.2% |
| Cartoon 10:36 | 5% | 5.0% | 0.24 | 0.19 | 0.47 | 94.0% | 94.8% |
| Cartoon 10:36 | 10% | 10.1% | 0.15 | 0.12 | 0.28 | 94.0% | 94.2% |
| Cartoon 10:36 | 1/scene | 6.3% | 0.18 | 0.14 | 0.44 | 98.6% | 99.1% |
| Cartoon 10:36 | 2/scene | 12.5% | 0.12 | 0.10 | 0.26 | 95.8% | 96.3% |
| Cartoon 10:36 | 3/scene | 18.8% | 0.10 | 0.08 | 0.19 | 95.3% | 96.0% |
| Drama 1 min | ±0.5 (precision, reference) | 36% | 0.13 | 0.10 | 0.24 | 94.5% | – |
| Drama 1 min | 1% | 1.1% | 1.27 | 1.00 | 4.64 | 95.2% | 95.2% |
| Drama 1 min | 2% | 2.1% | 0.89 | 0.70 | 2.28 | 95.2% | 95.2% |
| Drama 1 min | 5% | 5.1% | 0.55 | 0.44 | 1.20 | 95.1% | 95.9% |
| Drama 1 min | 10% | 10.3% | 0.36 | 0.29 | 0.78 | 95.9% | 95.9% |
| Drama 1 min | 1/scene | 10.5% | 0.26 | 0.21 | 0.89 | 99.9% | 100% |
| Drama 1 min | 2/scene | 21.1% | 0.18 | 0.14 | 0.39 | 94.8% | 94.7% |
| Drama 1 min | 3/scene | 31.6% | 0.14 | 0.11 | 0.26 | 93.8% | 93.2% |

- **Shares** keep the nominal coverage (94.0–95.9%) from 1% to 10%. Their
  error falls like 1/√clips. At an equal share, a fixed 5% is as accurate as
  the precision loop on the cartoon (RMSE 0.24 for both), in one round.
- **One clip per scene** is conservative, as expected from collapsed strata:
  98.6–100% coverage, with intervals 2.5–3.4 times the RMSE. Its estimate is
  still the most accurate per frame scored, since every scene is sampled.
- **Two or three clips per scene** use each scene's own variance: 93.2–96.3%.
  The pooled variance was tried first and under-covered (93.6% on the cartoon
  at 2/scene over 4 000 replays, 88–91% on shot strata). Long scenes vary more
  than short ones, and a pooled variance misses that. The drama's 3/scene
  (93.2–93.8%) is the weak spot: 40 scenes of about 37 frames, a third of
  their clips drawn, too few strata for the t approximation.
- **Shot cuts next to keyframes** lower the per-scene error (cartoon 2/scene
  RMSE 0.118 against 0.124). Shot cuts alone were worse (0.157). On the
  drama, the detector missed a gradual transition the encoder had marked
  with a keyframe, and the merged stratum dropped 3/scene to 92.5%.

With `-series all`, the other metrics and devices estimated from the same
clips (1 000 replays) cover 93–95% at 5% on the cartoon and 91–96% on the
drama (CAMBI lowest), 96–100% at 1/scene, and 92–97% at 2/scene. The
exception is the drama's PSNR family and CIEDE2000 at 2/scene: 89.5–92.9%.
Their clip values have heavy tails within a scene, and two clips rarely catch
them. A pooled variance is worse there (76–87%). As in the precision loop,
only VMAF's interval is validated to the nominal level.
`TestSimulateBudgetCoverage` checks the three kinds of budget on synthetic
scores in CI.

## Ladders: exhaustive optimum (`bench/ladderval`)

```sh
qc ladder source.mov -c h264 -f json > ladder.json
go run ./bench/ladderval ladder.json               # dense grid on the full title, exact VMAF
go run ./bench/ladderval -rungs-only -precision 0.25 ladder.json
```

- **Full mode** encodes the whole title on a dense grid (every probed
  resolution, CRF every 3 points) with exact VMAF, and builds the true
  envelope. It then encodes the whole title with each rung's settings, and
  reports the quality gap to the envelope at the rung's bitrate and the
  bitrate overhead at the rung's quality.
- **Rungs-only mode** skips the grid. It re-encodes each rung on the full title
  and compares its quality with the prediction made on the digest, which
  measures how representative the digest is.

Results:

| Check | Result |
|---|---|
| Drama 1 min, H.264, full mode | mean ΔVMAF to the optimum −0.04, mean bitrate overhead −0.7% (within measurement noise); worst rung +3.1% (540p chosen where 720p was optimal) |
| Cartoon 10:36, H.264, rungs-only | mean \|predicted − full title\| 0.86 VMAF (full title ~0.5 better: conservative); full-title bitrate 3–10% above prediction |

`-cache grid.json` stores every full-title measurement (keyed by resolution,
CRF and rate cap) and reuses it: the exhaustive grid is computed once per
title and codec, then several ladders are checked against it. Each run also
prints the ladder's encode count (probes, verifications, calibrations).
AV1 encodes are re-timed to a constant rate before measurement (SVT-AV1 keeps
source timestamps, and a source with a timestamp gap yields an average frame
rate the VMAF engine refuses).

### Curve extension below the top rung

The resolution just below the top rung's gets one extra probe when its
projected curve might reach the top quality at least 10% cheaper
([ladder engine](ladder.md#2-probe-encodes)). H.264, full-title exact VMAF
against the exhaustive optimum (CRF grid step 3):

| Title | Extension | Top rung | Top-rung overhead | Mean overhead | Encodes |
|---|---|---|---|---|---|
| Cartoon, 40 s excerpt, before | – | 1080p, 4.23 Mb/s | +15.7% | +2.6% (ΔVMAF +0.32) | 22 |
| Cartoon, 40 s excerpt, after | 720p CRF 13 | 720p, 3.29 Mb/s | −2.9% | −0.9% (ΔVMAF −0.09) | 23 |
| Drama, 30 s and 1 min | none (projected saving ≤ 1%) | unchanged | – | – | unchanged |

On the cartoon, the 720p probes stopped at VMAF 94.4, so the envelope fell
back to 1080p for the top rung; one probe at CRF 13 showed 720p reaching 95
23% cheaper. A negative overhead means the rung beats the grid optimum between
its grid points. A first version extrapolating the last segment in a straight
line, without the 10% threshold, also fired on both dramas, where 720p then
lost: one probe (about 7% of probing) wasted per title.

### Adaptive probing

Fixed (`--probing fixed`) and adaptive (`--probing adaptive`) ladders of the
same titles, checked against the same cached grid. ΔVMAF and overhead are the
means over the rungs the grid covers (a rung below every grid bitrate of its
optimal resolution is excluded, noted †).

| Title, codec | Probing | Encodes (probes + verify + calib.) | Mean ΔVMAF to optimum | Mean overhead | Worst rung | Mean \|predicted − full title\| |
|---|---|---|---|---|---|---|
| Drama 1 min, H.264 | fixed | 15 + 8 + 0 = 23 | −0.01 | −0.7% | +0.64 (540p for 720p) | 1.12 |
| Drama 1 min, H.264 | adaptive | 14 + 8 + 0 = **22** | −0.12 † | −1.2% † | +0.38 | **0.80** |
| Drama 1 min, AV1 | fixed | 15 + 7 + 0 = 22 | +1.94 | +5.3% | +7.57 (270p for 360p) | 0.34 |
| Drama 1 min, AV1 | adaptive | 14 + 7 + 1 = 22 | **−0.05** † | **−0.6%** † | +0.56 | 0.73 |
| Cartoon 10:36, H.264 (rungs-only) | fixed | 15 + 6 + 0 = 21 | – | – | – | 0.80 |
| Cartoon 10:36, H.264 (rungs-only) | adaptive | 14 + 6 + 1 = 21 | – | – | – | **0.33** |

- The AV1 fixed design lands far from the optimum on its lowest rungs: its
  probe CRFs stop 540p and 720p above ~250 kb/s, and the envelope never
  extrapolates, so the lowest rungs fall on 360p and 270p. Adaptive probing
  extends a higher resolution below its lowest probe when it could win there
  (the † rung, 540p at 226 kb/s, beat the grid's own envelope, which did not
  go that low at 540p).
- Adaptive predictions are unbiased where linear interpolation is biased low
  (the fixed ladders over-deliver: rungs come out 0.5–2 VMAF above their
  target on the full title). The flip side: an adaptive rung can land up to
  ~1 VMAF under its target (top rung 94.0–94.7 for a target of 95 on the
  cartoon and the AV1 ladder), and one rung missed its prediction by more
  than 1.5 on two titles out of three: its calibration encode cancelled the
  probe saved.
- Adaptive probing always stopped at its budget (14): the ±0.5 shape tolerance
  is tighter than what 4 extra probes reach. In replays on a real title's
  dense grid (smooth fits of the H.264 drama grid, ±0.6 noise, 20 seeds) the
  budget of the fixed design minus two was the smallest one keeping the
  prediction error below the fixed design's.

Verdict: always one probe fewer, equal or fewer encodes in total, closer to
the optimum on every title and much closer on AV1 — but not fewer encodes on
two titles out of three, so adaptive probing stays **opt-in**
(`--probing adaptive`, recommended for AV1).

## Per-shot rungs (`ladderval -per-shot`, `-shot-optimum`)

```sh
qc ladder source.mov -c h264 --per-shot -f json > ladder.json
go run ./bench/ladderval -per-shot ladder.json                  # per-shot vs per-title, full title
go run ./bench/ladderval -shot-optimum 2 ladder.json            # + exhaustive per-shot optimum
qc ladder source.mov -c h264 --per-shot-resolution -f json > res.json
go run ./bench/ladderval -shot-optimum 2 -shot-resolutions res.json   # optimum over (resolution, CRF)
```

- `-per-shot` encodes the full title with each rung's per-title settings (one
  encode, as delivered), with its per-shot chunks, and per-title one CRF step
  richer (for the local slope), all scored exactly. It reports the bitrate
  per-shot saves **at equal pooled VMAF** (the VMAF difference turned into
  bitrate by the slope).
- `-shot-optimum n` encodes, for the first n rungs, the whole title chunk by
  chunk at every CRF of a grid around the rung (±6, step 1 by default), reads
  every shot's bitrate and VMAF, and traces the **exhaustive per-shot optimum**
  (the Lagrangian convex hull the Dynamic Optimizer searches). Per-title,
  per-shot and optimum are compared at the per-shot rung's pooled VMAF, all
  chunked, so only the allocation differs.
- `-shot-resolutions` extends that grid to the rung resolutions just above
  and below the rung's, each around the CRF reaching the rung's quality on
  that resolution's probe curve, and adds the optimum over (resolution,
  CRF) next to the CRF-only one.

Results (full title, exact VMAF):

| Title | Codec | Shots | Per-shot vs per-title at equal VMAF (mean over rungs) | Top rungs, chunked: per-shot / optimum |
|---|---|---|---|---|
| Drama, 30 s excerpt | H.264 | 15 | **+2.1%** (+1.0% to +3.4%) | 1080p +2.4% / +2.8%; 720p +1.7% / +2.2% |
| Drama, 30 s excerpt | AV1 | 15 | **+4.1%** (+2.3% to +7.3%) | 1080p +3.3% / +5.5% (grid step 2) |
| Cartoon, 40 s excerpt | H.264 | 7 | **+0.1%** (−0.6% to +0.4%) | 1080p +1.2% / +1.1%; 720p +0.6% / +0.5% |
| Cartoon 10:36 (digest only†) | H.264 | 198 (32 in the digest) | **−7.4%** (−12.0% to −4.0%) | – |
| Drama, first 30 s‡ | H.264 | 15 | **+0.8%** (−0.6% to +2.3%) | – |
| Cartoon, 40 s from 2:00‡ | H.264 | 7 | **+0.1%** (−0.6% to +0.8%) | – |
| Cartoon 10:36, full title‡ | H.264 | 198 (32 in the digest) | **−1.1%** (top 2 rungs: −0.7%, −1.6%; the digest said −7.5%, −12.1%) | – |

† Long title: 166 of 198 shots get models predicted from their analysis
features. Per-shot rungs came out 0.5–2 VMAF under the per-title rungs on
the digest: the regression does not predict shots well enough, so per-shot
rungs are **not recommended on long titles** yet.

‡ Later runs (other excerpts, and the whole long title), after the chunk
pre-roll fix below.

- **Chunks lost frames (fixed)**: the full-title check of the long cartoon
  first failed on a frame-rate mismatch (24.997 fps against 25), blamed on
  variable timestamps. The source is in fact constant-rate without gaps;
  its keyframes are irregular (scene cuts). A chunk seeked straight to its
  first frame starts decoding at the source keyframe before it, which need
  not be a clean random access point: the H.264 decoder then dropped the
  frames whose references it lacked (two frames of one chunk), the chunk
  took two later frames instead (encoded twice), and the joined file had
  timestamp gaps. Chunks now decode from 2 s before their first frame and
  drop the frames before it (an output seek): a lossless chunked encode of
  the whole 10:36 title then matches a straight decode of the source frame
  for frame (15,903 identical checksums, 25 fps).
  The full-title check of the long cartoon now runs: per-shot rungs lose
  1.1% there, much less than their digest verification says.

- **The ceiling is low on these titles**: the exhaustive per-shot optimum
  saves 0.5–5.5% over one CRF for every shot, far from the 10–17% reported for
  the Dynamic Optimizer. Here shots must stay on the fixed 2 s GOP grid (ABR
  keyframe alignment), the quality objective is the frame-weighted mean, and
  both titles are homogeneous (a TV drama, a cartoon). The allocation itself
  reaches 60–100% of the optimum.
- **Chunks cost what the allocation gains on H.264**: encoding shot by shot
  restarts rate control and lookahead, ≈3% bitrate at equal CRF on x264; the
  per-shot models are fitted on chunked probes so that rungs keep their
  quality, but the per-title rung, a single encode, does not pay it. On AV1
  (SVT-AV1) the net gain is larger (+4.1%).
- **Model**: a first version fitted the shot models on continuous probes and
  aimed at the per-title rung's measured VMAF; per-shot rungs then came out
  ~1 VMAF short (−2.4% at equal VMAF on the cartoon excerpt). Fitting on
  chunked probes and aiming at the rung as the same models see it removed the
  bias (per-shot within 0.1 VMAF of per-title on the cartoon's top rungs).

Per-shot rungs stay **opt-in**: they cost two exact probes per rung resolution
and a verification per rung (≈ +100% of the ladder's time on short titles)
for 0–4% at equal quality on short titles, and lose on the long one.

### Per-shot resolution (`ladderval -shot-resolutions`)

`qc ladder --per-shot-resolution` (experimental) lets each shot also pick
its resolution among the rung's and the neighbouring rung resolutions (see
[ladder](ladder.md#per-shot-resolution-experimental)). Same excerpts as the
‡ rows above, H.264, full title, exact VMAF, at equal pooled VMAF:

| Title | Per-shot CRF (mean, range) | Per-shot resolution (mean, range) | Extra exact probes | Build time |
|---|---|---|---|---|
| Drama, first 30 s | +0.8% (−0.6% to +2.3%) | **+2.0%** (−10.9% to +12.0%) | +8 (18 vs 10) | +22% |
| Cartoon, 40 s from 2:00 | +0.1% (−0.6% to +0.8%) | **+3.6%** (−2.8% to +15.0%) | +6 (16 vs 10) | +3% |

Top two rungs against the exhaustive optimum (`-shot-optimum 2
-shot-resolutions -shot-step 2`: every shot at every CRF of a grid at each
candidate resolution, all chunked, compared at the per-shot rung's pooled
VMAF; the per-title baseline is chunked too):

| Title, rung | Per-shot resolution | CRF-only optimum | (resolution, CRF) optimum |
|---|---|---|---|
| Drama 1080p | +4.7% | +2.1% | +5.4% |
| Drama 720p | −6.5% | +0.9% | +0.9% |
| Cartoon 1080p | +20.7% | +2.2% | +20.0% |
| Cartoon 720p | −0.8% | +0.6% | +0.7% |

- **Resolution raises the ceiling on some rungs only**. On the drama's top
  rung the (resolution, CRF) optimum saves 5.4% where CRF alone reaches
  2.1%; on the cartoon's it saves 20%, because the whole rung is better at
  720p: the per-title probes of 720p stop short of VMAF 95, so the envelope
  kept 1080p. Per-shot resolution found it (every shot at 720p, the
  rendition declares 720p) and matched the optimum (the grid's 2-CRF step
  leaves it slightly behind). That is a per-title correction made per shot.
  The 270p rungs likewise moved to 360p (−8% to −12%). On the 720p rungs
  the optimum itself is below 1%.
- **The allocation loses where the models are wrong**: on the drama's 720p
  rung the per-shot rung came out 1.2 VMAF under per-title (−10.9% at equal
  VMAF over the title). Every shot's model was about 1.8 VMAF optimistic
  there, at both candidate resolutions, far below the rate cap. The models
  of a resolution now span the qualities of the neighbouring rungs, with
  probes up to ~12 CRF apart, and the allocation picks the options the
  models flatter. A first version fitting one least-squares model per shot
  through all the probes did +2.6% on the drama (−1.4% to +12.6%): better on
  the 720p rung, worse on the top one (+1.4%); the version kept fits every
  segment between two probes exactly, as per-shot CRF does.
- **Encoding and measurement** work for the three codecs: chunks at several
  resolutions joined into one MP4 decode frame-exactly (x264 and SVT-AV1
  repeat their parameter sets at every keyframe; HEVC goes through MPEG-TS
  and `hev1`), and measurement keeps the ffmpeg filter graph across the
  resolution changes (a rebuilt one restarted the frame counter of the
  sampling `select` and scored the wrong frames: the first build failed with
  "too few frames decoded").

Per-shot resolution stays **experimental and opt-in**: +2–4% on average on
these excerpts, but from a few rungs whose per-title resolution was not the
best one, with losses up to 11% elsewhere, 6–8 more exact probes, and
renditions that change resolution mid-stream, which not every player
accepts.

## Film grain synthesis (`ladderval -grain-reference`)

```sh
qc ladder grainy.mov -c av1 --film-grain off  -f json > off.json
qc ladder grainy.mov -c av1 --film-grain auto -f json > fgs.json
go run ./bench/ladderval -grain-reference clean.mov off.json   # and fgs.json
```

With `-grain-reference`, each rung is encoded on the full title (film grain
included) and compared with the **clean** version of the title, before grain
was added: VMAF of the grain-free decode against it (how well the picture
under the grain survives), VMAF of the decode as shown against the grainy
source (what a naive measurement reports), and the grain given back (noise of
the decode as shown over the source's, at the rung's resolution).

Test content: 12 s of the 1080p drama (σ 0.40: detected as clean, synthesis
off) with synthetic temporal grain (ffmpeg `noise=alls=12:allf=t`, σ 5.8).
`auto` detected it and calibrated level 50 (levels 10, 25 and 50 gave back
13%, 42% and 80% of the grain).

| Ladder | Top rung (VMAF 95 on its own reference) | VMAF vs clean | VMAF vs source, as shown | Grain given back |
|---|---|---|---|---|
| no synthesis | 1080p, 104.6 Mb/s | 87.6 | 96.4 | 66% |
| no synthesis, rung at 5.8 Mb/s | 1080p, 5.81 Mb/s | 89.6 | 84.0 | 4% |
| synthesis, level 50 | 1080p, **5.77 Mb/s** | 89.5 | 81.6 | **81%** |
| no synthesis, rung at 2.0 Mb/s | 720p, 2.04 Mb/s | 87.8 | 81.2 | 9% |
| synthesis, level 50 | 1080p, 2.17 Mb/s | 86.5 | 77.1 | 81% |

- **At equal bitrate the picture under the grain is not better** with
  synthesis: at 5.8 Mb/s both keep it equally (89.5 vs 89.6 against the clean
  title); lower down, the level-50 denoiser costs 0.5–1.4 VMAF against the
  clean title. What synthesis buys is the **grain**: the encode without it
  wipes the grain out below ~40 Mb/s (4–13% given back), and needs 104.6 Mb/s
  to give back two thirds of it; with synthesis 81–106% comes back at every
  rung. At equal fidelity by each ladder's own measure (VMAF 95 against its
  reference), the top rung costs **94% less** (5.8 vs 104.6 Mb/s).
- A naive VMAF against the grainy source scores the synthesised encode 81.6
  where the grain-free version of the same encode scores 95.1 against the
  denoised reference: synthesised grain is noise to VMAF.
- The grain check (synthesised σ over the source's at the rung's
  resolution) flags the 1080p rungs at 80% (level 50 is SVT-AV1's maximum);
  the lag-1 residual correlation, a spectral signature, matches (−0.127
  source, −0.136 output).
- Real titles: the drama (σ 0.33–0.40) and the cartoon (σ 0.44) are clean;
  `auto` leaves synthesis off on both. Real grainy content remains to be
  validated.

## HDR

Ground truth on a real HDR10 title: a 12 s excerpt (77.2–89.2 s) of Netflix's
*Sol Levante* (CC BY 4.0), the 4K ProRes 4444 XQ HDR10 master (12-bit,
BT.2020 PQ, graded on a 1 000 cd/m² P3 D65 display), plus the 1080p HDR10
HEVC mezzanine made from it (x265 CRF 14, Main 10, the master's
mastering display, MaxCLL/MaxFALL 1567/972 measured on the 4:4:4 master) and
encodes of that mezzanine. The media stay out of the repository;
`testutil.HDRClip` synthesises tagged PQ and HLG clips for the tests. The
independent references are numpy scripts (float64, no lookup table,
written from BT.2100, BT.2124, CTA-861.3 and the VTM sources). Apple M2 Max,
ffmpeg 9.0.1, x265 4.3, SVT-AV1 4.2.

### Light levels

| Picture | Computation | MaxCLL strict | MaxCLL robust (p99.9) | MaxFALL |
|---|---|---|---|---|
| 4K 4:4:4 master | numpy, every pixel | 1 567 | — | 972.4 |
| 1080p 4:4:4 downscale | numpy, every pixel | 1 557 | 1 472 | 971.8 |
| 1080p 4:2:0 HEVC | numpy, every pixel, nearest chroma | 7 906 | 1 512 | 972.6 |
| 1080p 4:2:0 HEVC | numpy, every pixel, bicubic chroma upsampling | 5 831 | — | 972.5 |
| 1080p 4:2:0 HEVC | numpy, the grid's points (centre of each 4×4 cell) | 3 971 | 1 505 | 972.6 |
| 1080p 4:2:0 HEVC | **qc** (grid step 4) | 3 971 | **1 508** | **972.6** |
| 4K, decoded to 4:2:0 | numpy, every pixel | 5 934 | 1 475 | 973.1 |
| 4K 4:4:4 master | numpy, the grid's points (centre of each 8×8 cell) | 1 503 | 1 465 | 971.9 |
| 4K 4:4:4 master | **qc** (grid step 8, 4:4:4 chroma) | 1 499 | **1 469** | **975.7** |

- The grid is ffmpeg's neighbour downscale of the decoded frame, on a
  second output of the analysis decode: on a 4:2:0 source its points are
  exactly numpy's (strict peak 3 971 both, MaxFALL 972.6 both; the robust
  peak differs by the 10-bit PQ bins of qc's histogram, 0.2%).
- MaxFALL: within 0.01% of the full computation at 1080p. On the 4K 4:4:4
  master the grid keeps the source's full chroma (the numpy full
  computation decodes to 4:2:0): 975.7, 0.3% above both the 4:2:0
  computation and the master's 972.4.
- The robust peak is within 0.3% (1080p) and 0.5% (4K) of the full
  computation's 99.9th percentile, and within 4% and 6% of the master's
  strict MaxCLL; the strict maximum of a 4:2:0 picture is 3–5× the
  master's, because of chroma overshoots at saturated edges (see
  [hdr.md](hdr.md#2-light-levels-maxcll-maxfall)). On 4:4:4 the strict
  grid maximum (1 499) is back near the master's.
- The first version piped whole 10-bit frames and sampled the grid in Go
  on cell corners (numbers of that version: robust 1 522, MaxFALL 972.8 at
  1080p): both grids agree with the full computation within the same
  tolerances.
- `TestFFmpegDecodeSampleGridPoints` decodes a frame whose samples code
  their position and checks every grid point (luma at the cell centre,
  the chroma sample covering it).
- With the signalled 1567/972, qc reports the light levels as matching.
- Unit tests: PQ at BT.2408 reference points (100 cd/m² = 0.508, 1 000 =
  0.752), HLG 75% = 203 cd/m² on a 1 000 cd/m² display, letterboxed clip
  (active share 0.75).

### HDR metrics

| Check | Result |
|---|---|
| wPSNR Y/Cb/Cr vs numpy (VTM formula), 12 frames of the CRF 20 encode | identical to the printed precision (10⁻⁴ dB) |
| ΔE ITP mean vs numpy (float64 ICtCp, same points) | max difference 0.00017 |
| ΔE ITP 99th percentile vs numpy | within 0.018 (histogram bins of 0.02) |
| BT.2124 Annex 4 worked example | measured side ITP to 5·10⁻⁵; ΔE 2.363 from the printed ITP; the expected side's I is 3·10⁻⁴ above the printed 0.3554 (an independent Python evaluation agrees with qc) |
| wPSNR by hand (uniform frames, 4-code error at luma 300 and 800) | 51.14 and 43.62 dB (`Example`) |
| Identical frames | wPSNR capped at 100 dB, ΔE ITP 0 |
| 8-bit frames vs the same 10-bit frames | identical wPSNR, ΔE within 10⁻³ |
| Results vs number of threads | identical |
| ΔE on every 2nd chroma sample vs every chroma sample, 48 frames | mean +0.49% / +0.20%, p99 +0.69% / +0.29% (CRF 20 1080p / CRF 32 720p) |
| Inverse PQ table vs exact, 10⁻⁶–10⁴ cd/m² | < 3·10⁻⁶ PQ units |

Metric behaviour on the mezzanine's encodes (exact, 288 frames, VMAF v1
at 1080p):

| Encode | Bitrate | VMAF on PQ | VMAF tone mapped | PSNR Y | wPSNR Y | XPSNR Y | ΔE ITP | ΔE ITP p99 |
|---|---|---|---|---|---|---|---|---|
| 1080p CRF 8 | 28.6 Mb/s | 99.64 | 99.53 | 54.21 | 53.60 | 48.75 | 2.34 | 9.92 |
| 1080p CRF 20 | 7.8 Mb/s | 94.20 | 91.35 | 46.65 | 46.01 | 41.79 | 5.07 | 23.56 |
| 720p CRF 24 | 2.7 Mb/s | 83.07 | 76.81 | 42.42 | 41.79 | 37.99 | 7.79 | 37.19 |
| 720p CRF 32 | 1.1 Mb/s | 62.03 | 52.31 | 38.33 | 37.65 | 33.98 | 12.08 | 54.79 |

Every metric is monotonic with the degradation and ranks the four encodes
identically; tone-mapped VMAF sits 0.1–9.7 points below VMAF on PQ, more as
quality drops. wPSNR sits 0.6 dB below PSNR on this bright content (errors
in highlights weigh more). No subjective scores exist for these encodes:
this checks consistency, not perceptual accuracy.

### HDR ladders

`qc run` on the 1080p HDR10 mezzanine with `--codecs hevc,av1` (5.5 min):
10-bit rungs, the HDR findings, and the rendered commands of the 720p rungs
run on the source and read back:

| Check | HEVC (x265) | AV1 (SVT-AV1) |
|---|---|---|
| Colour description | bt2020 / smpte2084 / bt2020nc / tv | same |
| Dynamic range read by qc | HDR10, MaxCLL 1567, MaxFALL 972 | same |
| Mastering display | the source's (P3 D65, 0.0001–1000 cd/m²) | same |
| HDR10 metadata in the bitstream | 7 SEI of each kind for 6 keyframes (+ extradata) | 6 metadata OBU pairs for 6 keyframes |

`TestEncodeCarriesHDRSignal` checks the same on every CI run (HEVC and AV1
HDR10, H.264 HLG), from an untagged raw input as the ladder's digest is.

### Cost

Paired runs: each iteration runs the build without HDR support and the
current one back to back, alternating their order, so that load changes
hit both; medians of the per-pair ratios, with their interquartile range.
Wall time and the CPU of the process and its children (user + system).

| Run | Pairs | Before | After | Wall | CPU |
|---|---|---|---|---|---|
| `qc analyze`, 1080p HDR10 HEVC, 12 s | 20 | 1.21 s | 1.26 s | +4.3% (+3.4..+6.6) | +5.7% |
| `qc analyze`, same title looped to 2 min | 12 | 10.36 s | 10.94 s | +5.1% (+4.3..+7.0) | +4.8% |
| `qc analyze`, 4K ProRes 4444 HDR10, 12 s | 5 | 8.32 s | 8.13 s | −1.0% (−2.4..+6.9) | +1.1% |
| `qc analyze --fast`, 1080p HDR10 | 20 | 0.060 s | 0.125 s | +65 ms (the first-frame `ffprobe`, waited for) | |
| `qc vmaf`, HDR10, exact 288 frames | 6 | 5.94 s | 6.33 s | +6.0% (+5.8..+9.8) | +7.3% |
| `qc vmaf --hdr-metric tonemap`, same | 4 (hyperfine) | 5.36 s | 8.93 s | +67% | |
| `qc analyze`, 1080p SDR H.264 | 20 | 1.041 s | 1.042 s | +0.3% (−0.2..+0.8) | +0.2% |
| `qc vmaf`, 1080p SDR, exact | 6 | 4.27 s | 4.29 s | +0.2% (−2.4..+1.4) | −0.2% |
| `qc ladder -c h264 --rungs 720,360`, SDR | 3 | 20.97 s | 21.53 s | −0.4% (−3.9..+8.9) | −0.5% |

SDR reports are identical before and after (analysis, comparison scores
and the SDR ladder, commands included).

**Frame analysis: from +12% to +4%.** The first version piped whole
10-bit 4:2:0 frames (6.2 MB per 1080p frame, 3× the luma) and sampled the
grid in Go, and read the first frame's HDR metadata before the analysis:
+12% on the 12 s clip (+65 ms for the extra `ffprobe`, +6–7% on the frame
analysis). The current one keeps the 8-bit luma pipe untouched and adds a
0.78 MB grid on a second pipe, and reads the first frame's metadata while
decoding. What remains is about 1.5 ms of CPU per frame (see
[hdr.md](hdr.md#2-light-levels-maxcll-maxfall)); a second output costs
ffmpeg +2.3 s of user and +1.7 s of system time over 2 880 frames, of
which a larger pipe block size (`-blocksize`) saved nothing.

Micro-benchmarks (one thread, `b.Loop()`): light analyzer 0.4 ms per
1080p frame, HDR metrics 7.1 ms per frame pair.

An HEVC HDR10 ladder (`--rungs 1080,720,540 --encode-bit-depth 10`, one
run each) took 169 s before and 144 s after: the encodes and the HDR metrics
of the verifications cost nothing visible next to the run-to-run variation
of a ladder (calibration steps depend on the encodes, which now carry
`hdr10-opt`).

## What is not validated yet

- The corpus has only two real SDR titles and one HDR10 excerpt. It should
  grow to cover sport, real film grain, 3D animation, HLG camera content and
  screen content.
- HDR: VMAF is not an HDR metric; wPSNR and ΔE ITP are checked against
  their definitions, not against subjective scores; the tone mapping of
  `--hdr-metric tonemap` is not validated perceptually; NVENC HDR10
  metadata is not checked on a GPU.
- XPSNR at high frame rates in sampled mode: the first frame of each clip
  lacks one frame of history for the second-order temporal activity
  (approximated, not measured yet).
