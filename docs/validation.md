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
| Exact VMAF in segments with VideoToolbox decoding vs a single CPU pass, every series (VMAF v1, XPSNR, CAMBI, PSNR): drama 1 501 frames, cartoon 15 903 frames, 4K reference with the 1080p model 1 501 frames, 59 min 88 500 frames | identical on every frame; mean, harmonic mean and metric estimates identical |
| Segments vs a single pass on synthetic open-GOP clips at 25 and 50 fps (`TestSegmentsBitExact`, in CI) | identical; with one warm-up frame instead of two, XPSNR differs at 50 fps (second-order activity) |
| Sampled measurements with VideoToolbox vs CPU decoding, fixed seed: drama ±0.5 and 5%, cartoon ±0.5, 5% and 2/scene, 59 min ±0.5, 1% and 5% | same clips, identical per-frame values, means and intervals |
| VideoToolbox decoding and bicubic scaling vs the CPU (`framemd5`): 8-bit H.264 at 1080p, 720p → 1080p and 2160p, 8-bit scored at 10 bits; 10-bit HEVC at 1080p and 720p → 1080p; PQ tone mapped to SDR | identical frames |
| XPSNR NEON row loops vs the portable Go loops (`TestRowKernels`, random rows up to 65 573 samples) and vs ffmpeg (`TestMatchesFFmpeg`) | identical integers; per-frame XPSNR of `qc vmaf --exact` unchanged |
| Frame analysis in segments of a video starting after its audio (`TestAnalyzeSegmentsVideoStartingLate`; a 59 min concatenation whose video starts at 0.04 s and audio at 0) | same report as a single pass, no fallback (65 s instead of 244 s) |

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
  envelope. The grid spans the probe CRFs one step wider, then extends at
  each resolution until it covers every rung's bitrate (10% margin): a
  resolution missing from the grid at a rung's bitrate cannot show that it
  would have been cheaper there. Until that extension, the grid stopped at
  the probes' range, and rungs placed at a lower resolution because a higher
  one was never probed that low went unseen (see
  [below](#resolutions-never-compared-ladderreplay)). It then encodes the whole title with each rung's settings, and
  reports the quality gap to the envelope at the rung's bitrate and the
  bitrate overhead at the rung's quality.
- **Rungs-only mode** skips the grid. It re-encodes each rung on the full title
  and compares its quality with the prediction made on the digest, which
  measures how representative the digest is.

Results:

| Check | Result |
|---|---|
| Drama 1 min, H.264, full mode | mean ΔVMAF to the optimum −0.04, mean bitrate overhead −0.7% (within measurement noise); worst rung +3.1% (540p chosen where 720p was optimal); unchanged on the extended grid (−0.6%), −0.8% with challenger probes |
| Drama 1 min, AV1, full mode, extended grid | +7.4% on average (the earlier grid read +0.7%): the 360p and 270p rungs belonged at 720p and 540p (+21%, +18%); with challenger probes −0.1%, worst rung +0.7% |
| Cartoon 10:36, H.264, rungs-only | mean \|predicted − full title\| 0.86 VMAF (full title ~0.5 better: conservative); full-title bitrate 3–10% above prediction |

`-cache grid.json` stores every full-title measurement (keyed by resolution,
CRF and rate cap) and reuses it: the exhaustive grid is computed once per
title and codec, then several ladders are checked against it. Each run also
prints the ladder's encode count (probes, verifications, calibrations).
AV1 encodes are re-timed to a constant rate before measurement (SVT-AV1 keeps
source timestamps, and a source with a timestamp gap yields an average frame
rate the VMAF engine refuses).

### Resolutions never compared (`ladderreplay`)

```sh
go run ./bench/ladderreplay grid.json...                     # ladderval caches
go run ./bench/ladderreplay -codec av1 -top-vmaf 94 -bias -0.8 -noise 0.5 -runs 20 grid.txt
```

`ladderreplay` runs the real engine on an exhaustive grid instead of
encoding: every encode it asks for is answered from the grid (interpolated
in CRF), with, on demand, the error of sampled measurements (`-bias`, the
level shared by the sampled frames; `-noise`, per measurement). A build
takes milliseconds, so probing modes and engine changes are compared on
several titles' grids; the rungs are placed on the grid and compared with
its optimum.

On a 59-minute reality-TV title that compresses well (exact grid of its
digest: 5 resolutions × 9 CRFs, H.264 and AV1), fixed probing put the AV1
rungs below 1 Mb/s at 720p, 540p, 360p and 270p where 1080p and 720p were
30–78% cheaper: the 1080p probes (CRF 28, 40, 52) stopped at 1 Mb/s, and a
rung can only take a resolution probed at its bitrate. Real ladders of the
title, checked on the grid, agreed with the replay (+35.5% and +6.8% for
AV1 and H.264 in fixed mode, +11.5% and +2.2% adaptive). Challenger probes
(see [ladder.md](ladder.md#challenger-probes)), replayed without and with
the sampling error of real measurements (bias −0.8, noise 0.5 VMAF, 20
replays):

| Grid | Probing | Before | Challengers | With sampling error (60 replays) |
|---|---|---|---|---|
| Reality TV, AV1 | fixed | +37.6% (worst rung +78%) | 0.0% | +0.4% |
| Reality TV, AV1 | adaptive | +11.5% (worst +29%) | +0.2% | +0.8% |
| Reality TV, H.264 | fixed | +6.8% (worst +25%) | +0.4% | +1.2% |
| Reality TV, H.264 | adaptive | +2.2% | +0.9% | +1.3% |
| Drama, H.264 / AV1 (extended grids) | both | – | 0.0–0.1% | 0.2–0.8% |

A rung whose resolution was never compared with several higher ones gets
all of them challenged at once: challenging only the nearest one climbed a
resolution per round (270p, then 360p, then 540p) and ran out of rounds on
the real AV1 adaptive ladder, whose lowest rung stayed 27% too expensive.
Two cheaper variants were tried and dropped. A tie probe (probing both
resolutions where two came within 1 VMAF across a wide gap between probes)
changed nothing measurable over 60 noisy replays per grid, the remaining
error being the measurements' noise. A threshold on the saving a challenger
promises (10%) saved one or two probes on average but left a drama's top
rung at 720p where 1080p was 13% cheaper, in a noiseless replay.

Real ladders of the reality-TV title, checked on its exact grid: AV1 fixed
+35.5% → +0.1% and AV1 adaptive +11.5% → 0.0% (every rung at its optimal
resolution), H.264 fixed +6.8% → +1.9%.

They cost 7–12 more encodes per ladder on these titles (30–36 in all instead of 23–24).

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

### Probes at a faster preset (`--probe-preset`)

The probes of a ladder at one preset were replayed against the probes of
the same digest at another (same CRFs, resolutions and sampled frames), on
both titles: rungs planned on the fast probes, moved by anchors, and read
on the slow preset's probes. Mean |predicted − delivered| VMAF of the rungs:

| Rungs ← probes | Drama | Cartoon |
|---|---|---|
| x264 fast ← ultrafast | 0.36–0.73 | 0.77–0.87 |
| x264 fast ← veryfast | 0.71–1.67 | – |
| SVT-AV1 8 ← 10 | 0.74–0.82 | 0.41–0.52 |
| SVT-AV1 8 ← 11 | 0.90–1.30 | – |
| x265 veryfast ← ultrafast | 0.87–2.17 | – |
| x265 veryfast ← x264 fast (another codec) | 0.63–0.88 | 1.84–6.51 |

The ranges span the anchoring models tried: a quality shift or a bitrate
and CRF shift, one anchor per rung resolution or two interpolated. Without
anchors, the misses were 1.3–6.9 VMAF. The replays read the slow preset
between its own probes (linear in CRF), which blurs anything below ~0.5.

On the full title against the exhaustive optimum (x264 fast, the drama),
the ladder probed at ultrafast landed at −0.23 VMAF and −1.8% bitrate on
average, every rung at its optimal resolution; probed at fast, −0.03 and
−0.7%. Timings are in [ladder.md](ladder.md#faster-probes-at-another-preset):
the option pays only for delivery presets several times slower than the
probes'.

## Digest: balanced or uniform (`bench/digestsim`)

```sh
qc analyze source.mov -f json > analysis.json                    # SI and TI of every frame
qc vmaf source.mov encode-1080p.mp4 --exact -f json > 1080.json  # encodes of the whole title
go run ./bench/digestsim -source analysis.json -digests 40 1080.json 720.json 360.json
```

A ladder is estimated on a digest and delivered on the title: what the
digest's frames cost and score must be what the title's do. `digestsim`
takes exact measurements of encodes of the **whole title** and reads, on
their frames, the VMAF and the bitrate of the frames a digest holds, without
encoding anything. It compares the digests the engine itself plans
(`ladder.PlanDigest`):

- **balanced**: the engine's default ([ladder engine](ladder.md#1-digest));
- **uniform**: the engine's evenly spaced segments, centred in their parts
  of the title;
- **every phase**: those segments shifted through 100 positions, from the
  start to the end of their parts. Its root mean square is the error uniform
  sampling makes on average; the centred digest is one of its draws;
- **top**: the most complex scenes (`--digest top`), which is meant to
  differ from the title ([below](#the-most-complex-scenes---digest-top)).

The digest's bitrate is read from the encode's bitrate per second
(`distorted.bitstream` of the measurement), each second counted for the
share of it a segment covers.

Cartoon (10:36, 1080p25), four x264 fast encodes of the whole title, digest
of 40 s. Title: SI 30.43, TI 11.98; balanced digest 30.43 and 11.98; uniform
digest 27.27 and 10.37.

| Encode | Title VMAF, bitrate | Balanced: ΔVMAF, Δbitrate | Uniform (centred) | Uniform, every phase (rms) |
|---|---|---|---|---|
| 1080p CRF 23 | 95.39, 2883 kb/s | −0.30, −3.2% | −0.83, −11.4% | 0.48, 10.4% |
| 720p CRF 28 | 87.86, 812 kb/s | +0.03, −2.2% | −0.59, −11.9% | 0.49, 11.7% |
| 720p, 484 kb/s | 79.75, 484 kb/s | +0.21, −3.3% | −0.91, −13.9% | 0.60, 16.3% |
| 360p CRF 33 | 58.48, 192 kb/s | +0.16, −1.2% | +0.29, −11.1% | 1.03, 11.4% |
| **rms** | | **0.20, 2.6%** | 0.70, 12.1% | 0.69, 12.6% |

One digest length is one draw of each design, and a lucky or unlucky one:
the same replay over twelve lengths from 20 s to 2 min (10 to 60 segments),
root mean square over the four encodes:

| Digest length | 20 s | 24 s | 30 s | 36 s | 40 s | 44 s | 50 s | 60 s | 70 s | 80 s | 100 s | 2 min | all |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Balanced Δbitrate | 4.6% | 7.3% | 11.1% | 3.4% | 2.6% | 2.2% | 5.1% | 3.6% | 1.8% | 1.8% | 0.9% | 0.9% | **4.7%** |
| Uniform, every phase | 13.8% | 12.0% | 9.6% | 9.9% | 12.6% | 7.9% | 8.7% | 5.4% | 5.4% | 5.3% | 3.7% | 5.3% | 8.9% |
| Balanced ΔVMAF | 1.32 | 1.29 | 1.05 | 0.31 | 0.20 | 0.19 | 0.30 | 0.57 | 0.61 | 0.37 | 0.21 | 0.11 | 0.69 |
| Uniform, every phase | 1.08 | 0.91 | 0.81 | 0.99 | 0.69 | 0.47 | 0.46 | 0.59 | 0.53 | 0.22 | 0.28 | 0.29 | 0.67 |

Drama (1 min, three encodes: 1080p CRF 24, 720p, 540p CRF 32), eight lengths
from 16 s to 50 s: balanced 0.51 VMAF and 3.0%, every phase 0.28 and 3.3%. At
40 s, where the digest is two thirds of the title: balanced 0.19 and 0.6%,
uniform 0.07 and 1.4%.

Reality TV (59:16, 1080p25), two x264 fast encodes of the whole title
(1080p CRF 25.5: VMAF 93.40, 5.23 Mb/s; 720p CRF 29: 81.53, 1.35 Mb/s). Title:
SI 52.07, TI 13.22; balanced digest of 40 s 52.07 and 13.23; uniform 52.86
and 12.68.

| Digest length | 30 s | 40 s | 50 s | 60 s | 70 s | 80 s | 98 s | 2 min | 160 s | 4 min | all |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Balanced Δbitrate | 0.9% | 2.1% | 7.2% | 0.7% | 4.8% | 1.8% | 3.6% | 2.2% | 3.3% | 2.7% | **3.5%** |
| Uniform, every phase | 12.0% | 14.2% | 12.0% | 8.1% | 7.6% | 8.8% | 6.7% | 7.0% | 6.7% | 4.9% | 9.2% |
| Balanced ΔVMAF | 0.06 | 0.38 | 0.13 | 0.10 | 0.11 | 0.22 | 0.03 | 0.15 | 0.10 | 0.14 | **0.17** |
| Uniform, every phase | 0.71 | 0.64 | 0.51 | 0.50 | 0.41 | 0.35 | 0.37 | 0.32 | 0.28 | 0.19 | 0.46 |

At 40 s the engine's uniform digest costs 13.9% less than the title on both
encodes, the balanced one 1.9% and 2.2% more.

- **The bitrate error is about halved** on the long title (4.7% against
  8.9% over the twelve lengths; 2.8% against 7.6% from 18 segments up), which
  is what its features can give: SI and TI explain 62–78% of the variance
  of the bits of a 2 s window of the cartoon's encodes (75–84% on the
  drama's), the balance removes that part, and the rest, which it cannot
  see, leaves √(1 − R²) ≈ half the error. The 2.6% of the 40 s digest is a
  good draw, the 11.1% at 30 s a bad one. The 3–10% of bitrate the full
  title cost above its predictions, measured on this title, came largely
  from its uniform digest.
- **A longer digest does not help a balanced one** on the title of an
  hour: 2.1% at 40 s, 3.6% at 98 s, 2.7% at 4 min (read frame by frame
  rather than per second: +0.7 to +1.3% at 40 s, −1.7 to −2.6% at 4 min).
  The uniform digest does improve with length, as sampling does, and needs
  4 min to do as well as the balanced one at 40 s. An automatic lengthening
  for long titles (40 s × √(duration / 10 min), up to 2 min) was built on
  the cartoon's figures, where the error falls with the length, and
  removed on these: it would have cost 2.4 times the ladder on this title
  for nothing measured. `--digest-duration` sets a length by hand.
- **The quality level is closer on the title of an hour** (0.17 against
  0.46 VMAF) **and not on the cartoon** (0.69 against 0.67). SI and TI explain 17–40% of the variance of the VMAF of a 2 s
  window there (9–72% on the drama). Mean luma raises that to 33–61% on the
  cartoon, but balancing on it too did not lower the VMAF error in replays
  (0.64 against 0.65) and was left out. The level correction of the probes
  ([ladder engine](ladder.md#quality-level-of-the-probes)) is about the
  frames VMAF samples inside the digest, not about the digest: it does not
  correct this.
- **Short digests** (under ~18 segments) and titles barely longer than their
  digest gain nothing: on the cartoon under 36 s the balanced digest was no
  better than a uniform draw (8.1% and 1.2 VMAF against 11.9% and 0.9), and
  on the drama under 30 s its VMAF was further off (0.40–1.15 against
  0.19–0.62). With few segments, each one weighs enough for the balance to
  pick unusual ones.
- **The source's own bitrate is no proxy**: both mezzanines are near
  constant bitrate (peak to average 1.3 and 1.5), and the bitrate of a 2 s
  window of the source explains 7% of what the same window costs once
  encoded (TI: 77%). Balancing therefore needs the frame analysis,
  one decode of the title: 13 s for this one.
- **Features tried**, in replays at three grids of candidate starts over
  the twelve lengths: TI alone, SI and TI, with √TI, log TI, SI × TI, mean
  luma, luma range, the density of scene cuts, and a penalty keeping each
  segment close to its own part of the title. Every set with TI gave
  3.9–5.8% of bitrate error against 9.5%, none standing out beyond what two
  titles can tell; SI, TI and √TI were kept as a small set among the best
  on both errors. Strata by TI quantiles, without a segment in every part
  of the title, beat uniform on the cartoon (5.1% against 12.7% at 40 s)
  and lost on the drama (7.8% against 2.9%).

### On real ladders (`ladderval -rungs-only`)

The replays read digests on encodes of the whole title; a ladder encodes its
digest on its own. H.264 ladders of the cartoon, every rung then encoded on
the whole title (VMAF at ±0.25), six rungs each:

| Digest | Top rung | Title bitrate over the digest's (verification encodes) | Title bitrate over the rung's planned one (mean of absolute gaps) | Mean \|predicted − full title\| VMAF | Title VMAF over the digest's |
|---|---|---|---|---|---|
| Uniform | 720p CRF 17, 3.05 Mb/s | **+12.7%** (+12.0 to +13.4%) | 5.4% (+3.0 to +7.4%) | 0.79 | +0.92 |
| Balanced | 1080p CRF 22.5, 3.14 Mb/s | **+7.1%** (+6.2 to +8.5%) | 3.4% (−1.3 to +6.2%) | 1.00 | +0.96 |
| Balanced, two segments elsewhere | 720p CRF 17.5, 2.86 Mb/s | **+1.9%** (+0.6 to +3.0%) | 2.1% (−4.7 to +1.1%) | 0.63 | +0.56 |

- The uniform digest undercosts the title by what the replay said (12.7%
  here, 12.1% there). The balanced one by 7.1%, more than the replay's
  2.6%: a digest encoded on its own is not the title's frames (every
  segment starts a scene, rate control and lookahead restart), and the
  rungs are not the replay's encodes. The error is about halved, as over
  the digest lengths above.
- The third row is the digest of an earlier build, whose segments started
  half a frame before their first frame instead of a quarter: the same
  frames, but a search that ended with two of the twenty segments elsewhere
  (at 524 s and 588 s). Probe bitrates moved by 4–5%, and the top rung went
  from 720p to 1080p, both 0.7 VMAF above the target on the title
  (95.66 and 95.65 for 95): near the top quality the two resolutions cost
  about the same on this title, and **two segments decide between them**.
  A digest of 40 s carries that uncertainty, balanced or not.
- The quality gap between the title and the digest is the same with both
  designs (the title scores 0.6 to 1.0 above its digest), as in the replays.
- **Cost**: 13 s of analysis on an idle M2 Max; the ladder went from
  2 min 53 s (20 probes) to 3 min 01 s (19 probes).

### Codecs at equal quality: when AV1 costs more than H.264

A run on a 59-minute reality-TV title with `--digest top --top-vmaf 93`
gave a 1080p top rung of 8.12 Mb/s in H.264 (x264 fast) and **9.33 Mb/s in
AV1** (SVT-AV1 preset 8): the newer codec costlier. The measurements were
checked before anything else: the same digest extracted again, encoded by
hand with ffmpeg, and scored by qc and by Netflix's `vmaf` tool.

| 1080p encode of the top digest | Bitrate | VMAF v1 (qc) | VMAF v1 (`vmaf`) | VMAF v0.6.1 (`vmaf`) | PSNR Y | PSNR Cb |
|---|---|---|---|---|---|---|
| x264 fast CRF 29.5 | 8.11 Mb/s | 92.77 | 92.77 | 92.20 | 31.67 | 41.98 |
| SVT-AV1 preset 8 CRF 43 | 9.18 Mb/s | 93.41 | 93.41 | 96.29 | 32.98 | 41.31 |
| x264 fast CRF 27 | 11.92 Mb/s | 96.54 | 96.54 | 97.05 | 33.33 | 42.95 |
| SVT-AV1 preset 8 CRF 40 | 11.97 Mb/s | 95.81 | 95.81 | 98.27 | 33.94 | 41.90 |

- **Nothing is mismeasured**: the run reproduces to the kb/s, and qc's
  scores are the reference tool's.
- **The model decides.** At 12 Mb/s SVT-AV1 keeps more luma (+0.6 dB) and
  less chroma (−1.05 dB on Cb, −1.14 on Cr) than x264. VMAF v0.6.1 scores
  luma only and puts AV1 1.2 points ahead there (and 4.1 ahead at the top
  rungs, for 13% more bitrate); **VMAF v1 counts chroma**
  (its chroma features read 5.7–6.7 for SVT-AV1 against 3.7–4.3 for x264
  here) and puts the two level, x264 slightly ahead. Which is right is a
  question for subjective tests, not for this tool; v1 is the default.
- **The digest decides too.** These are the 20 most complex segments of
  the title (SI 91, TI 37, for 52 and 13 over the title). On a balanced
  digest of the same title, the same grids give AV1 36% less bitrate at
  VMAF 91.7 (2.52 Mb/s against 3.93), and the ladders:

  | Digest | H.264 top rung (verified) | AV1 top rung (verified) | AV1 against H.264 at equal VMAF |
  |---|---|---|---|
  | `top` | 1080p, 8.12 Mb/s, VMAF 92.8 | 1080p, 9.18 Mb/s, VMAF 93.4 | **+7%** at 92.8, +11% at 87, −4% on average from 45 |
  | `balanced` | 1080p, 5.08 Mb/s, VMAF 93.3 | 1080p, 3.28 Mb/s, VMAF 92.9 | **−33%** at 92.9, −44% on average from 61 |

- **A slower preset does not change it** on those scenes: at CRF 43,
  SVT-AV1 preset 6 gives 8.42 Mb/s for VMAF 93.23 (level with x264 fast),
  preset 4 7.12 Mb/s for 91.79; 10 bits change nothing (9.00 Mb/s, 93.42)
  and `tune=0` costs more (9.62 Mb/s, 93.97).
- Top rungs within the 0.5 tolerance of their target can be 0.6 VMAF apart
  (92.8 and 93.4 here): 5% of bitrate at that quality. Ladders are
  compared at equal quality, not by their top rungs.

A run with several codecs now says so itself (`ladder.CompareRates`): every
newer codec against the oldest one at equal VMAF, read on the verified
rungs, as a warning when the newer one costs over 3% more at the top
quality or on average ("av1 needs 7% more bitrate than h264 at VMAF 92.8…"),
as a note otherwise ("av1 needs 33% less bitrate than h264 at VMAF 92.9…").
A ladder built on a top digest carries a note that its bitrates are those
scenes', and the summary cards show the top rungs as verified.

### The most complex scenes (`--digest top`)

`--digest top` builds the digest from the 2 s segments where SI × TI is
highest, one per shot at most. Same replays, digest of 40 s:

| Title | Top digest SI, TI (title) | Δbitrate against the title | ΔVMAF |
|---|---|---|---|
| Cartoon 10:36 | 36.98, 29.32 (30.43, 11.98) | **+68% to +109%** (1080p +68%, 720p +80%, 360p +80%) | +3.5 at 1080p, +3.2 and +3.7 at 720p, −0.8 at 360p |
| Drama 1 min | 37.64, 16.32 (33.92, 13.42) | +13% | +0.6 at 1080p, +0.4 at 720p, 0.0 at 540p |

- **These scenes cost the most, they do not look the worst.** At a given
  CRF their frames take 1.7 to 2.1 times the title's bitrate on the cartoon,
  and score **above** the title at 720p and 1080p. They only score below
  it at 360p (−0.8).
- **A ladder built on them is not a worst-case ladder for quality.** H.264
  ladder of the cartoon with `--digest top`, every rung encoded on the
  whole title (`ladderval -rungs-only`, VMAF at ±0.25):

  | Rung | Predicted on the digest | Whole title |
  |---|---|---|
  | 720p CRF 24.5 | VMAF 95.06, 2.17 Mb/s | **91.42**, 1.17 Mb/s |
  | 720p CRF 29 | 89.06, 1.45 Mb/s | 86.53, 733 kb/s |
  | 540p CRF 29 | 81.70, 965 kb/s | 80.57, 494 kb/s |
  | 360p CRF 27 | 72.89, 643 kb/s | 72.65, 349 kb/s |
  | 360p CRF 31.5 | 60.85, 429 kb/s | 62.62, 222 kb/s |
  | 360p CRF 36 | 47.20, 286 kb/s | 48.80, 145 kb/s |

  The busy scenes reach VMAF 95 at a CRF that leaves the title 3.6 points
  under it (the balanced ladder's top rung delivers 95.65), and the
  bitrates the ladder announces are 1.85 to 1.97 times what the title
  takes. It answers "what do the demanding scenes need" (peak bitrates,
  rate caps, a check of the action scenes), not "what does the title
  need".
- **Score**: over the 2 s windows of a title, the rank correlation with the
  encoded size is 0.74–0.87 for SI × TI across both titles and five
  encodes, 0.75–0.86 for TI alone (lower on the drama's 1080p encode, where
  SI × TI finds its three costliest windows and TI one), −0.07 to 0.48 for
  SI alone. None ranks windows by quality: the correlation of SI × TI with
  VMAF goes from −0.21 (360p) to +0.86 (drama 1080p).

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
the digest. The cause was found later, and it was not the regression: see
[long titles](#long-titles-pieces-features-and-the-verification-guard).

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
  drop the frames before it (an output seek, since then a `trim` filter
  restarting their timestamps at their first frame): a lossless chunked encode of
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

### Long titles: pieces, features and the verification guard

The loss on the long title was first blamed on the features predicting the
shots the digest misses. Replaying the allocation says otherwise. The
cartoon was encoded whole at 720p at CRF 28 and 34 (exact VMAF, the size of
every frame), which gives every one of its 199 shots a rate-quality model;
the engine's steps were then replayed on them: models of the digest's pieces,
prediction of the other shots, λ on the digest, allocation of every shot,
and the result read on the shots' own models, against one CRF for the whole
title at equal pooled VMAF (rung at CRF 31: VMAF 83.5, 613 kb/s).

| Digest (shots measured of 199) | Every shot measured | Before | Segments on the GOP grid | + log SI, log TI | Whole shots measured, same features |
|---|---|---|---|---|---|
| Balanced 40 s (21) | +2.50% | −0.95% | +0.51% | **+0.64%** | +1.25% |
| Uniform 40 s (20) | +2.53% | −1.48% | +0.50% | +0.43% | +1.46% |
| Balanced 80 s (40) | +2.49% | +0.04% | +0.29% | +0.52% | +1.51% |
| Balanced 2 min (60) | +2.48% | −0.44% | +0.28% | +0.45% | +1.56% |

- **The replay reproduces the loss measured on the title** (−0.95% for
  −1.1%), and the ceiling: +2.5% with every shot measured.
- **Features alone repair nothing.** With segments placed anywhere, no set
  tried gains: SI and TI, their logarithms, √TI, with the penalty of the
  regression at 0.001, 0.3 or 3, give −1.0 to −0.4% at 40 s. Giving every
  unmeasured shot the mean model, or leaving it at the rung's CRF, loses
  0.5 to 1.1% too: the shots the digest *does* measure are misplaced.
- **The pieces are the cause.** A segment starting anywhere cuts across
  shots and GOPs; each piece is read as the model of its whole shot. With
  the shots' own models in place of their pieces' (last column), the same
  predictions gain 1.2 to 1.6%, half the ceiling.
- **On the GOP grid**, a segment is one GOP of one shot: the loss becomes a
  gain of 0.3 to 0.5% with the features as they were, 0.4 to 0.6% with
  log SI and log TI on a standardised ridge (penalty 0.3). The engine does
  both. One GOP still stands for a whole shot: the rest of the way to the
  last column would take measuring whole shots.
- **The source's bitrate** explains 7% of what a 2 s window of this
  near-constant-bitrate mezzanine costs once encoded; it was the first
  feature of the predictions.

**The shot model.** With the digest on the grid, the rungs of the lower
resolutions gained and the top one lost 18% on the digest: its shots were
promised up to 6 VMAF and 25% of bitrate more than they delivered. The
model read VMAF as a parabola in ln(bitrate) with the title's coefficient,
and ln(bitrate) as linear in CRF: between probes 15 CRF apart, the bump of
that parabola grows as the square of the gap between a shot's two bitrates,
and reaches 100 where 94 is measured. Both are now parabolas **in CRF**,
through the shot's two probes, bent as the title's own curves are. On the
720p encodes of the whole title at CRF 22, 28 and 34, predicting every
shot at 28 from its two ends: VMAF error 0.35 (rms) instead of 0.83,
bitrate 1.7% instead of 5.4%. On the digest of the real ladder, measured
shots against their predictions:

| Rung | VMAF, measured − predicted | Bitrate, measured / predicted |
|---|---|---|
| 720p CRF 19 | −3.69 → **−0.67** | −16% → −6% |
| 720p CRF 26.5 | −2.64 → **+0.24** | −13% → −3% |
| 540p and below (four rungs) | −0.15 to +0.13 → −0.22 to +0.26 | −4 to −1% → −1 to 0% |

**On the whole title** (cartoon 10:36, H.264, 198 shots, 20 of them
measured; `ladderval -per-shot`, every rung encoded whole, exact VMAF),
bitrate saved by the per-shot rung at equal pooled VMAF, as verified on the
digest and as measured on the title:

| Rung | Digest on the grid, new features: digest → title | + shot model in CRF: digest → title |
|---|---|---|
| 720p CRF 19 | −18.0% → −4.6% | −1.2%: rejected, no per-shot version |
| 720p CRF 26.5 | +2.3% → +1.4% | +4.9% → **+1.2%** |
| 540p CRF 27.5 | +4.4% → +1.7% | +4.4% → **+1.6%** |
| 540p CRF 31.5 | +2.0% → +1.8% | +2.9% → **+1.9%** |
| 360p CRF 30 | +1.8% → +1.4% | +2.6% → **+1.3%** |
| 270p CRF 30 | +1.5% → +0.5% | +1.9% → **+0.6%** |
| **Mean of the rungs delivered** | +0.4% | **+1.3%** |

Before these changes, the same title lost on both rungs checked: 1080p
−1.1% (−1.2% on the digest) and 720p −0.6% (+2.3% on the digest); its other
rungs were not measured (the run was stopped).

- **Per-shot rungs gain on the long title**: 0.6 to 1.9% on the five rungs
  delivered, half the 2.5% the replay gives with every shot measured.
- **The gain comes from the grid**; the model in CRF changes little below
  the top rungs, where the first one was accurate, and makes the top rungs'
  predictions right. The top rung still does not gain: at VMAF 95 the
  curve is flat, and there is little to move between shots.
- **The verification's sign can be trusted, not its size**: the digest
  overstates the gain two to four times, but said whether a rung gains in
  14 of the 15 rungs checked on whole titles (the exception is the 720p
  rung above, before the grid). Hence the guard: a per-shot version its
  verification shows no cheaper than its rung is dropped, and reported.

Per-shot rungs stay **opt-in**: they cost two exact probes per rung resolution
and a verification per rung (≈ +100% of the ladder's time on short titles;
3 min 35 s of an 11 min ladder on the long one) for 0–4% at equal quality on
short titles and 1.3% on the long one.

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

## Camera motion

Three checks: synthetic moves of known speed (`bench/motionval`), a visual
review of real shots, and the cost. Apple Silicon (12 cores), ffmpeg 9.0.1.

### Synthetic moves (`bench/motionval`)

`go run ./bench/motionval -texture photo.jpg -dir /tmp/motionval` renders 35
clips at 1920×1080 (x264 CRF 18) through a camera path of known speed over a
still — here a 5496×3091 landscape photograph (sky, rock faces, forest) —
with ffmpeg's `zoompan`, plus overlays for moving objects and parallax
planes, `noise`, `gblur` and `fade`. Each clip is analysed twice through the
library: unsmoothed (`Options.Smooth` shorter than a frame) for the
per-frame error on the frames the analyzer trusts, and with the defaults for
the classification. Speeds are in % of the picture width per second
(%W/s); "px" are 1080p pixels.

| Clips | Reliable frames | Per-frame RMSE | Bias | Classified right |
|---|---|---|---|---|
| Pans and tilts, 3 to 100 %W/s, both directions, 24/25/50 fps (14) | 100% | 0.01–0.99 %W/s (≤ 0.60 px per frame) | ≤ 0.64 %W/s | 14/14 |
| Zooms in and out, 3, 10 and 25 %/s (6) | 100% | zoom 0.37–0.92 %/s | ≤ 0.62 %/s | 6/6 |
| Static, static with strong grain, 1 %W/s drift (expected static), fades in and out (4) | 96–100% | ≤ 0.57 %W/s | ≤ 0.15 %W/s | 4/4 |
| Handheld (tremor 2–7 Hz, ±6 px), light handheld (±1.5 px), shaky pan, pan + tilt, pan + zoom (5) | 100% | ≤ 0.82 %W/s | ≤ 0.40 %W/s | 5/5 |
| Moving object over 8% / 35% of a static shot, parallax plane (lower 40% moving 3× faster), heavy blur, heavy noise, a cut (6) | 100% | ≤ 0.79 %W/s | ≤ 0.57 %W/s | 6/6 |

All 35 clips get the expected class, direction and shake flag. Shake
measured against the true path's (same definition): handheld 0.32 vs 0.32%
of the width, shaky pan 0.31 vs 0.31, light handheld 0.09 vs 0.08 (under
the 0.25 threshold: static), steady moves ≤ 0.03. Most of the per-frame
error is the renderer's: `zoompan` places its window on whole texture
pixels, which alone adds about 0.4 px RMS to the true per-frame
displacement; the 24 fps pan, one thumbnail pixel per frame exactly, is
estimated within 0.01 %W/s. On sequences rendered exactly in Go (unit tests,
`analyze/motion`), the error is 0.025 thumbnail pixel RMS (0.2 px) on
pans, zoom and roll rates within 1% (0.00397 for 0.004 per frame), and
sub-pixel pans of 0.3 thumbnail pixel read 6% high.

Three choices came out of these clips and the real titles. A single
Lucas–Kanade step read
sub-pixel motion 12% high (zoom 0.0045 for 0.004): the second, warped
step removed it. An inlier tolerance of 1 pixel let a fake zoom absorb a
moving object covering a third of a static shot (classified tracking); at
0.5 pixel it is an outlier and the shot is static, with no loss elsewhere.
Starting Lucas–Kanade from a parabola fitted to the level-1 costs, instead
of a ±1 pixel search at level 0, saved a quarter of the CPU and passed all
35 clips, but on the real titles it lost fast zooms and pans (three
reviewed shots changed class, 1–2 points fewer reliable frames): the smooth
photograph hides what sharp animation and live action show, so the level-0
search stays.

### Real content: visual review

Both SDR titles of the corpus, every shot of the drama and 28 shots of the
cartoon sampled across classes, checked on strips of three frames (8%, 50%
and 92% of the shot) and, when in doubt, full-resolution crops of the first
and last frames. Stills show pans, tilts, zooms and parallax, not shake:
the shake flags were checked on the per-frame traces instead.

| | Shots reviewed | Agree | Plausible, not decidable from stills | Debatable | Wrong | Unknown (no answer) | Not checkable |
|---|---|---|---|---|---|---|---|
| Drama 1 min (live action, 39 shots) | 39 | 30 | 5 | 2 | 0 | 2 | 0 |
| Cartoon 10:36 (CG animation, 235 shots) | 28 | 11 | 5 | 1 | 3 | 4 | 4 |

- Drama: pans and tilts get the right direction (a tilt down onto a seated
  actress, a pan right across a lobby), push-ins and pull-backs read as
  zooms, and the orbits around plated desserts and lateral moves past
  foreground objects as tracking. Debatable: a handheld close-up labelled
  tracking rather than handheld, and a push-in over water under a burnt-in
  title labelled tracking in rather than zoom in (the static title and the
  moving water are the "parallax"). The two unknowns are fast handheld
  follows (a flamingo in flight over water, a character at a car). The four
  shaky shots are the handheld scenes of the trailer.
- Cartoon: 73% of its duration is static, as expected of the series. Wrong:
  a character's head turning against a flat sky read as a slow zoom
  (confidence 0.14), a vehicle driving into a static camera read as a
  dolly out, and a quick tilt followed by a long hold stays static (27% of
  moving frames). The four shots not checkable span wipe transitions the
  cut detector does not split (the series' badge wipes); the unknowns are
  flat skies and fast chases. The first version flagged 37 shaky shots and
  17 handheld: bursts from whip pans and wipes inflated an RMS shake. With
  the median jitter, counted only below 40 %W/s, 7 remain (3 handheld),
  mostly fast chases whose virtual camera does shake; one fast handheld pan
  of the drama lost its flag in the process.

### Cost

The estimator costs about 165 µs of CPU per frame (one thread, 1080p
thumbnail, measured with `getrusage` over 2 000 frames on a loaded machine;
`BenchmarkEstimate`), and nothing per frame is allocated. On the cartoon
(15 903 frames) that is about 2.7 s of CPU, **≈ 2.5% of the frame
analysis** (≈ 100–110 s of CPU for the whole analysis in the segmented
pipeline). It runs inside each segment's
decode loop, so it adds no stage and no decode. End-to-end runs of
`qc analyze` with and without `--no-motion` (5 alternated pairs) were
measured on a machine shared with other jobs (load average 30–57): wall
times ranged 15–37 s either way and the median user CPU was 72.9 s with the
analysis and 75.5 s without, a difference well inside the run-to-run noise.
The direct measure above is the reliable one; a quiet-machine A/B remains
to be done.

## Audio

`go run ./bench/audioval -aac [file ...]` checks the audio analysis
([audio.md](audio.md)) three ways. Apple M2 Max, ffmpeg 9.0.1.

### EBU conformance signals

The cases of EBU Tech 3341 (v4) and 3342 (v4) that the specifications
describe as synthetic signals, synthesised from their tables at 48 kHz
(`internal/audiotest`) and measured by `loudness.Meter` — also in CI
(`TestConformance`) — and, for reference, by ffmpeg's `ebur128` filter on
the same samples written as float WAV:

| Case | Signal | Reading | Expected | qc | ffmpeg `ebur128` |
|---|---|---|---|---|---|
| 3341-1 | stereo 1 kHz, -23 dBFS, 20 s | M / S / I | -23 ±0.1 | -22.99 / -22.99 / -22.99 | I -23.00 |
| 3341-2 | stereo 1 kHz, -33 dBFS, 20 s | M / S / I | -33 ±0.1 | -32.99 / -32.99 / -32.99 | I -33.00 |
| 3341-3 | -36 / -23 / -36 dBFS, 10 / 60 / 10 s | I | -23 ±0.1 | -23.01 | -23.02 |
| 3341-4 | -72 / -36 / -23 / -36 / -72 dBFS | I | -23 ±0.1 | -23.01 | -23.02 |
| 3341-5 | -26 / -20 / -26 dBFS, 20 / 20.1 / 20 s | I | -23 ±0.1 | -22.98 | -22.98 |
| 3341-6 | 5.0: L, R -28, C -24, Ls, Rs -30 dBFS | I | -23 ±0.1 | -23.02 | -23.02 |
| 3341-9 | 1.34 s at -20 / 1.66 s at -30 dBFS, ×5 | S (every complete window) | -23 ±0.1 | -22.99 (worst) | – |
| 3341-12 | 0.18 s at -20 / 0.22 s at -30 dBFS, ×25 | M (every complete window) | -23 ±0.1 | -22.96 (worst) | – |
| 3341-15 | sine fs/4, 0° | TP | -6 +0.2/−0.4 dBTP | -6.00 | -6.00 |
| 3341-16 | sine fs/4, 45° | TP | -6 +0.2/−0.4 | -5.96 | -6.00 |
| 3341-17 | sine fs/6, 60° | TP | -6 +0.2/−0.4 | -6.30 | -6.00 |
| 3341-18 | sine fs/8, 67.5° | TP | -6 +0.2/−0.4 | -6.01 | -6.00 |
| 3341-19 | sine fs/4, 45°, samples at 0 dBFS | TP | +3 +0.2/−0.4 | +3.04 | +3.00 |
| 3342-1 | 20 s at -20, then -30 dBFS | LRA | 10 ±1 LU | 10.00 | 10.00 |
| 3342-2 | -20, then -15 dBFS | LRA | 5 ±1 | 5.00 | 5.00 |
| 3342-3 | -40, then -20 dBFS | LRA | 20 ±1 | 20.00 | 20.00 |
| 3342-4 | -50, -35, -20, -35, -50 dBFS | LRA | 15 ±1 | 15.00 | 15.00 |

Every reading is within the specification's tolerance. The true-peak
signals are faded in and out over 20 ms: an abrupt onset at a non-zero
phase is not band-limited, and the interpolator rings up to 0.7 dB above
the waveform's peak on it (-5.30 dBTP at fs/8), where the specification
describes a steady sine. The interpolator's passband ripple (±0.1 dB)
explains -6.30 at fs/6; ffmpeg upsamples to 192 kHz with its resampler
instead. Not synthesised: Tech 3341 cases 7, 8 (programme excerpts), 10,
11, 13, 14 (sets of files) and 20 to 23 (recorded true-peak signals), Tech
3342 cases 5 and 6 (programme excerpts).

### Real content: ffmpeg `ebur128` and `loudnorm`

Every audio track of the corpus (AAC-LC stereo 48 kHz), measured by qc and
by ffmpeg's `ebur128` (readings of its last frame, three decimals) and
`loudnorm` (first pass):

| Title | Track | qc I / LRA / TP | `ebur128` | max Δ | `loudnorm` | max Δ |
|---|---|---|---|---|---|---|
| Cartoon 10:36 | 1 | -23.07 / 5.15 / -6.46 | -23.08 / 5.15 / -6.47 | 0.01 | -23.10 / 5.20 / -6.48 | 0.05 |
| Cartoon 10:36 | 2, 3 (silent, peak -87.2 dBFS) | silent / 0 / -87.20 | -70 (gate) / 0 / – | – | – / 0 / -87.20 | 0 |
| Drama 1 min | 1 | -19.90 / 9.40 / -4.69 | -19.91 / 9.40 / -4.70 | 0.01 | -19.80 / 8.70 / -4.70 | 0.70 |
| Cartoon ×6, 59 min | 1 | -23.07 / 5.19 / -6.46 | -23.07 / 5.19 / -6.47 | 0.01 | -23.10 / 5.20 / -6.48 | 0.03 |

qc agrees with `ebur128` within 0.01 LU and 0.01 dB on every track.
`loudnorm` measures its own way (its loudness range differs by 0.7 LU on
the one-minute drama). On clips of a few seconds, `ebur128`'s loudness
range also counts the short-term windows reaching before the start (2.68
against 2.28 LU on a 5 s clip): qc, like libebur128, only complete ones.

Time for the whole audio analysis of the three tracks at once (qc,
decoding included) against one track through each filter: 1.5 s against
3.7–3.9 s (`ebur128`) and 20–28 s (`loudnorm`) for 10:36; 7.6–8.0 s
against 19.5–20.9 s and 109–164 s for 59 minutes.

### Synthetic defects

A 60 s programme-like stereo signal (pink-ish noise and partials under a
syllabic envelope, -23 LUFS, channels correlated at 0.62) with one defect
each, written as float WAV and, with `-aac`, encoded to AAC-LC 256 kb/s,
then decoded by ffmpeg and analysed as `qc analyze` does:

| Case | Inserted | PCM | AAC 256k |
|---|---|---|---|
| clean | nothing | nothing found | nothing found |
| silence | both channels 20.0–23.0 s | 20.000–23.000 s | 20.010–23.000 s |
| edges | 0–2.5 s and 57–60 s | leading 2.500 s, trailing 3.000 s | 2.490 s, 2.990 s |
| muted | FR zero | FR muted | FR muted |
| dropout | FL zero 30.0–34.0 s | FL silent 30.000–34.000 s | 30.010–34.000 s |
| clipping | ×8 and hard-clipped 40.0–40.5 s | 410 / 383 samples in 70 / 68 runs, 40.006–40.371 s | 20 / 8 samples in 5 / 2 runs, 40.013–40.299 s |
| phase | FR inverted 10.0–20.0 s | out of phase 10.000–20.000 s | 10.000–20.000 s |
| polarity | FR inverted throughout | correlation -0.62, inverted | -0.62, inverted |
| mono | FR = FL | difference -120 dB, identical | -47.4 dB, identical |
| dc | +0.01 on FL | FL 0.0101, FR 0.0001 | 0.0100, 0.0002 |
| lfe | 5.1 without LFE | LFE muted (and only it) | LFE muted |

Every defect is found at its place, and nothing is found in the clean
signal nor, beyond what was inserted, in the others. Clipping survives AAC
only partly: the codec turns flat runs into overshooting waves (see
[limitations](audio.md#limitations)). On the real corpus, the audio
findings are the two silent tracks of the cartoon (under -87 dBFS), its
3.1 s trailing silence, and the drama's loudness (-19.9 LUFS, 3.1 LU above
EBU R 128, as ffmpeg measures it too): no clipping, phase, DC, silence or
channel finding on the programmes.

## What is not validated yet

- The balanced digest: three titles, all SDR and x264; a film with long
  takes and sport are missing. The features it balances on were chosen on
  the cartoon and the drama; the title of an hour, measured afterwards, is
  the only one that did not take part in that choice.

- Camera motion: the real-content check is a visual review of stills by
  one reviewer, not an annotated ground truth; shake on real content and
  the tracking heuristic are not measured against references.
- The corpus has only two real SDR titles and one HDR10 excerpt. It should
  grow to cover sport, real film grain, 3D animation, HLG camera content and
  screen content.
- HDR: VMAF is not an HDR metric; wPSNR and ΔE ITP are checked against
  their definitions, not against subjective scores; the tone mapping of
  `--hdr-metric tonemap` is not validated perceptually; NVENC HDR10
  metadata is not checked on a GPU.
- Audio: the EBU cases that are programme or recorded files, surround
  and 7.1 content from real programmes, and dialogue-gated loudness (not
  implemented).
- XPSNR at high frame rates in sampled mode: the first frame of each clip
  lacks one frame of history for the second-order temporal activity
  (approximated, not measured yet).
