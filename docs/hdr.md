# HDR

qc detects and measures HDR video: HDR10 and plain PQ (SMPTE ST 2084), HLG
(ARIB STD-B67), and reports Dolby Vision and HDR10+ without processing
their dynamic metadata. Everything below runs only on PQ and HLG streams:
SDR analyses, measurements and ladders are unchanged (checked, see
[validation](validation.md#hdr)).

| Stage | What HDR adds | Cost (1080p, M2 Max) |
|---|---|---|
| Inspection | dynamic range, mastering display, content light level, HDR10+ and Dolby Vision from the first frame | read during the frame analysis or the measurement; +65 ms for a stand-alone inspection (`--fast`) |
| Frame analysis | measured MaxCLL and MaxFALL, peak and average light of every frame | +4–5% at 1080p, none at 4K (decode-bound) |
| VMAF | wPSNR and ΔE ITP on the frames VMAF scores; an explicit label on VMAF | 7–14 ms of CPU per frame (8–16% of libvmaf's), +6% wall time |
| `--hdr-metric tonemap` | VMAF on an SDR tone mapping, HDR metrics in a second pass | +55–67% on the comparison |
| Ladders | 10-bit encodes carrying the colour description and HDR10 metadata; HDR metrics on the rungs | as the VMAF line, on the verifications only |

## 1. Detection and signalling

`ffprobe -show_streams` gives the colour description (`color_primaries`,
`color_transfer`, `color_space`, `color_range`) and the stream-level side
data. Containers often keep HDR10 metadata in the bitstream only (HEVC SEI,
AV1 metadata OBUs, in MP4 in particular), and HDR10+ never is at stream
level: for PQ, HLG and Dolby Vision streams only, a second call reads the
side data of the first frame (`-read_intervals %+#1 -show_frames`). That
call costs about 65 ms, mostly ffprobe's start, as much as the whole
inspection of a short file, so it runs while something slower does: the
frame analysis reads it while decoding, a comparison while measuring, and
`qc run`'s inspection leaves it to the frame analysis that follows
(`analysis.HDRProber`, `analysis.Options.DeferHDRMetadata`). A
stand-alone inspection (`qc analyze --fast`) still waits for it; repeated
probes of a file are served from a cache. Merging it into the first call
was measured and rejected: asking one ffprobe for streams and a few first
packets' frames decodes them for SDR files too (+30–50 ms on 1080p H.264,
+1.1 s on a 4K ProRes file). Relying on stream-level side data alone would
miss SEI-only HDR10 (the Sol Levante MP4 encode read as plain PQ) and every
HDR10+ stream. The dynamic range is then:

| Signal | Classification |
|---|---|
| Dolby Vision configuration record | `DolbyVision` (profile, level, RPU/EL/BL, compatibility id) |
| `arib-std-b67` transfer | `HLG` |
| `smpte2084` + ST 2094-40 dynamic metadata | `HDR10+` |
| `smpte2084` + SMPTE ST 2086 mastering display | `HDR10` (with MaxCLL/MaxFALL when signalled) |
| `smpte2084` only | `PQ` |
| otherwise | `SDR` |

The mastering display keeps its primaries and white point (CIE xy) and its
luminance range, which the ladder writes back on every encode.

**What is measured.** PQ and HLG streams whose decoded frames are BT.2100
Y′CbCr. Dolby Vision profiles 8.1, 8.4 and 7 carry an HDR10, HLG or SDR
base layer: it is measured like any stream of its transfer, and the report
says so. Profile 5 (compatibility id 0) codes IPTPQc2, which only its RPU
turns into a picture: it is reported, never measured. HDR10+ metadata is
reported; the static HDR10 layer is measured.

**Consistency findings** (`internal/findings`):

| Finding | Level | Rule |
|---|---|---|
| HDR primaries | warning | PQ/HLG with primaries other than BT.2020 (P3 masters are delivered in a BT.2020 container) |
| HDR matrix | warning | PQ/HLG with a matrix other than BT.2020 NCL/CL or ICtCp |
| HDR bit depth | warning | PQ/HLG on fewer than 10 bits: bands visibly |
| HDR full range | note | HDR10 and HLG delivery use narrow range |
| Missing mastering display | warning | PQ without SMPTE ST 2086: not HDR10 |
| Missing content light level | warning | HDR10 without MaxCLL/MaxFALL (or 0, 0); the measured values are given |
| Content brighter than signalled | warning | measured robust MaxCLL above the signalled one by more than 25%, or MaxFALL by more than 15% |
| Content dimmer than signalled | note | signalled MaxCLL (MaxFALL) more than 25% (15%) above anything measured |
| Light levels match | passed | both within those tolerances |
| Dolby Vision, HDR10+ | note | reported only |

## 2. Light levels (MaxCLL, MaxFALL)

CTA-861.3 defines both on the display light of max(R, G, B) of each pixel:
**MaxCLL** is its maximum over every pixel of every frame, **MaxFALL** the
maximum over frames of its average over the active picture (letterbox
bars excluded). qc measures them during the frame analysis, on the decode
the other analyzers already share:

- For PQ and HLG streams, the one decode has two outputs
  (`split` in a filter graph): stdout carries the 8-bit luma of the other
  analyzers, exactly as for SDR, and a second pipe (file descriptor 3,
  `ffexec.StreamPair`) a **grid** of 10-bit Y′CbCr samples point-sampled by
  ffmpeg's neighbour scaler (`scale=…:flags=neighbor,format=yuv444p10le`):
  one point per 4×4 cell of pixels at 1080p and per 8×8 cell at 2160p
  (~130 000 points), the pixel at the centre of the cell and the chroma
  sample covering it, code values untouched (`TestFFmpegDecodeSampleGridPoints`
  checks every point of a frame coding its positions). The grid is 0.78 MB
  per 1080p frame, where piping whole 10-bit frames (a first version) tripled
  the bytes (6.2 MB) and cost 6–7% of the analysis in copies and a Go
  conversion. A 4:4:4 source (ProRes 4444) keeps its full chroma in the grid.
- The light analyzer converts each grid point with lookup tables built
  once: code values to E′Y, E′Cb, E′Cr (BT.2100 narrow or full range), the
  BT.2020 NCL matrix to R′G′B′, and the PQ EOTF of the largest component
  (PQ is monotonic: the brightest non-linear component is the brightest in
  light), tabulated on 16 384 steps. HLG goes through the inverse OETF and
  the OOTF (γ 1.2) of a 1 000 cd/m² display, the BT.2100 reference: its
  "cd/m²" are those of that display.
- Per frame: the maximum (strict peak), the 99.9th percentile (robust peak,
  from a 10-bit PQ histogram) and the mean of max(R, G, B).

**Why a robust peak.** max(R, G, B) is very sensitive to chroma: at a
saturated edge, a 4:2:0 chroma sample shared by four pixels makes one of
them overshoot towards the peak once upsampled. On the Sol Levante excerpt
(1 000 cd/m² master), the 4:4:4 master peaks at 1 567 cd/m², its 1080p
4:4:4 downscale at 1 557, but the 4:2:0 HEVC encode of that downscale at
7 906 cd/m² (strict maximum at full resolution, nearest chroma) — 5 831
even with bicubic chroma upsampling. Its 99.9th percentile is 1 512. The
report therefore gives **MaxCLL as the highest robust peak** of a frame,
with the strict maximum next to it; the "brighter than signalled" check
uses the robust value, the "dimmer" check the strict one, so neither fires
on chroma overshoots. MaxFALL, an average, is not affected (972.6 on the
4:2:0 encode, 972.4 on the master).

**Active picture.** Letterbox bars are black (0 cd/m²): the averages over
the active picture are the averages over the frame scaled by the frame
area over the content area that the crop detector found
(`light.Result.Active`).

**Accuracy** ([validation](validation.md#light-levels)): on the 1080p HDR10
encode, the grid gives exactly what numpy computes on the same points, and
MaxFALL within 0.01% and the robust MaxCLL within 0.3% of a full-resolution
computation; on the 2160p 4:4:4 master (grid step 8), MaxFALL within 0.3%
and the robust MaxCLL within 0.5% of the full 4:2:0 computation.

**Cost**: about 1.5 ms of CPU per 1080p frame: 0.45 ms for ffmpeg's
neighbour downscale, about 0.6 ms for the second output (its muxing thread,
the pipe copies on both sides), 0.4 ms for the light analyzer on its own
goroutine. When decoding saturates the CPU, as the frame analysis does at
1080p, that is 4–5% of wall time; at 2160p the decode dominates and the
difference is within noise. End to end, see the [cost summary](#cost-summary).

The JSON report holds the summary (`video.light`: `maxCLL`, `maxCLLRobust`,
`maxFALL`, the frames reaching them, `sampleStep`, `activeShare`) and three
columns (`frames.peakNits`, `robustPeakNits`, `averageNits`).

## 3. HDR quality metrics

Measured in pure Go (`quality/hdr`) on the frames VMAF already decodes, at
its evaluation resolution and depth (10 bits for any 10-bit source), for
every PQ and HLG reference: same clips, same strata, same estimator and
confidence intervals as the other metrics ([vmaf.md](vmaf.md#5-other-metrics-and-devices)).
No extra decoding, no colour conversion by ffmpeg: the scale filter keeps
code values.

### wPSNR (PQ)

The luma-weighted PSNR of the JVET HDR common test conditions (JVET-H1002
Annex D; the VTM reference software's `xFindDistortionPlaneWPSNR` and
`initLumaLevelToWeightTable`):

```
y  = clip(0.015·Y − 1.5 − 6, −3, 6)     Y: the reference luma code value, expressed at 10 bits
w  = 2^(y / 3)
wMSE_plane = Σ w · (ref − dist)² / N     chroma samples weighted by the luma at their top-left luma position
wPSNR      = 10·log10(1020² / wMSE)      1020 = 255 × 4, the VTM's peak at 10 bits
```

Errors in bright PQ regions, where one code value spans more light, weigh up
to 4×, dark ones down to 0.5×. Frames are averaged in dB, as the JVET tools
do; identical planes are capped at 100 dB. Squared errors are summed as
integers per reference code value and weighted once per frame (exact, and
2.5× faster than weighting every sample).

wPSNR is defined on PQ: the JVET conditions convert HLG to PQ first, a
per-pixel conversion qc does not pay for, so **HLG references get ΔE ITP
only**.

### ΔE ITP (PQ and HLG)

The colour difference of ITU-R BT.2124: each pixel goes to display light
(PQ EOTF; HLG on the 1 000 cd/m² reference display, BT.2124 conversion 4),
to LMS, through the inverse PQ, to ICtCp; T = Ct/2, and

```
ΔE ITP = 720 · √(ΔI² + ΔT² + ΔP²)          1 ≈ one just noticeable difference
```

It is evaluated on every second chroma sample of every second chroma row
(with its co-sited luma sample): each point costs two full conversions,
and scoring every chroma sample would cost four times as much, about a
quarter of VMAF v1's CPU; a quarter of them moves the mean by 0.2–0.5% and
the 99th percentile by 0.3–0.7% on the validation encodes. The inverse PQ, applied to continuous LMS values, is a
table indexed by the exponent and top mantissa bits of a float32 with
linear interpolation (5 634 entries, error below 3·10⁻⁶ in PQ units,
0.002 ΔE ITP). Per frame: the mean (`deltae_itp`) and the **99th
percentile** (`deltae_itp_p99`, from a histogram of 0.02 steps), which
shows the colour errors a sky or a face concentrates and the mean dilutes.

ΔE ITP is very sensitive by design (it assumes the most critical adaptation
state): a near-lossless encode already scores ~2.3, since the quantisation
of 10-bit PQ itself is about one unit. Read it as a ranking and a trend, and
its p99 for localised damage.

### Not included

PSNR-L100 and ΔE100 (CIEDE2000 in the CIELab of a 100 cd/m² white), the
other HDR metrics of JVET-H1002, need CIELab and CIEDE2000 per pixel: libvmaf's
CIEDE2000 already costs 9× VMAF. ΔE ITP, standardised since, replaces ΔE100
here.

**Cost**: 7 ms per 1080p frame pair on one thread (about 40% for wPSNR over
3.1 M samples, 60% for ΔE ITP over 130 000 points), against 63–85 ms for
VMAF v1; in a full comparison, where caches are shared with libvmaf's
threads, 7–14 ms, 8–16% of libvmaf's CPU. On the Sol Levante comparison (288 frames, exact),
the HDR metrics took 1.98 s of CPU against 24.7 s for libvmaf, and the
comparison 6% more wall time. Results do not depend on the number of
threads (row sums combined in order, integer counts).

## 4. VMAF on HDR

VMAF's models were trained on SDR (BT.709, BT.1886 displays); there is no
public HDR model (Netflix reports an internal HDR VMAF for its HDR
streaming). On the LIVE HDRvsSDR database, full-reference VMAF on HDR10
reaches a Spearman correlation of 0.55–0.60 with subjective scores per TV
(0.67 on the combined databases), the best of the classic metrics (PSNR
0.04–0.14, SSIM 0.38–0.48) but far from its SDR performance (Ebenezer et
al., IEEE TIP 2023, arXiv:2304.13162); HDR-aware features (HDRMAX) raise it.

`--hdr-metric` (library: `quality.Options.HDRMetric`) picks how VMAF sees
an HDR reference:

| Mode | VMAF scores | Cost | Read it as |
|---|---|---|---|
| `pq` (default) | the PQ or HLG code values, as decoded | none | a ranking of encodes of one title; not an absolute score on PQ. On HLG, closer to meaningful: HLG follows a gamma curve over the SDR range (backward compatible) |
| `tonemap` | an SDR (BT.709) tone mapping of both videos | +55–67% | what an SDR display shows of the HDR picture; highlights the mapping compresses weigh less than on an HDR display |

Every HDR result says which (`vmaf.hdr`: `transfer`, `metric`,
`vmafCalibrated`, `note`), and the reports label VMAF accordingly.

**Tone mapping** is done by ffmpeg while decoding, identically on both
sides: the scale filter converts to BT.709 primaries, transfer and matrix
with swscale's perceptual intent (`intent=perceptual`, the tone and gamut
mapping ported from libplacebo in FFmpeg 8), given the input colour
explicitly (the ladder's raw digest carries no tags). No `zscale` (libzimg)
is needed: the Docker image's ffmpeg has none. The curve is swscale's and
may change between ffmpeg versions; compare tone-mapped scores produced by
the same ffmpeg. The HDR metrics need the HDR frames: in this mode they are
measured in a second decode of the scored clips (like a 4K device pass).
On the Sol Levante excerpt (exact, 288 frames), a comparison took 8.9 s
against 5.8 s in `pq` mode (5.4 s before HDR support).

The two modes rank the validation encodes identically (see
[validation](validation.md#hdr)); tone-mapped VMAF is lower on degraded
encodes (52.3 against 62.0 at 720p CRF 32).

## 5. HDR ladders

For a PQ or HLG source, `ladder.Engine` (and `qc ladder`, `qc run`):

- **encodes in 10 bits**: HDR10 requires it and 8-bit PQ bands. An 8-bit
  request, including the default `--encode-bit-depth 8`, is upgraded, and
  the report says so (`ladder.Result.HDR.BitDepthUpgraded`), rather than
  refused: the ladder of an HDR source works with the defaults.
- **carries the source's signal** on every probe, rung and rendered command
  (`encode.Params.Signal`, `ladder.Result.HDR.Signal`): the colour
  description and, for HDR10, the mastering display and the content light
  level. When the source signals no content light level, `qc run` uses the
  one its analysis measured (robust MaxCLL, MaxFALL;
  `ladder.Options.ContentLight`).
- **keeps code values**: the digest is `format=yuv420p10le` (raw NUT or
  FFV1), the scale filter converts neither transfer nor primaries (YUV to
  YUV), and the digest's inspection is given the source's colour so its
  measurements know they score HDR.
- measures the verified rungs with the HDR metrics too (probes stay
  VMAF-only), in the mode of `--hdr-metric`.

**Colour tags on frames.** ffmpeg's encoders take the colour description
from their input frames: `-color_trc` and the like are ignored when the
frames say otherwise, and frames read from a raw digest carry none (checked
with ffmpeg 9: `-color_trc arib-std-b67` on PQ frames produced a PQ
stream). Every encode therefore ends its filter chain with
`setparams=color_primaries=…:color_trc=…:colorspace=…:range=…`, which the
encoder wrappers turn into VUI / sequence header fields.

| Encoder | Colour description | HDR10 metadata |
|---|---|---|
| x265 | `setparams` | `-x265-params hdr10-opt=1:hdr10=1:master-display=G(…)B(…)R(…)WP(…)L(max,min):max-cll=cll,fall` (chromaticities in 0.00002, luminances in 0.0001 cd/m²); `hdr10-opt` for any PQ |
| SVT-AV1 | `setparams` | `-svtav1-params mastering-display=G(x,y)B(x,y)R(x,y)WP(x,y)L(max,min):content-light=cll,fall` (decimals, SVT-AV1 4.x; `enable-hdr` no longer exists), merged with film grain parameters |
| x264 | `setparams` (HLG and PQ VUI) | none: HDR10 in H.264 (High 10) is rarely decoded as HDR by players; the ladder warns |
| NVENC | `setparams` | no option: ffmpeg's nvenc writes the SEI/OBUs only from the side data ffmpeg forwards from the source (`decoded_side_data`), so rendered commands carry them when the source has them; the ladder notes it (not verified on a GPU) |

HLG gets the colour description only: its metadata is the transfer itself.

**`repeat-headers` is not used.** x265 writes the mastering display and
content light level SEI with every keyframe by itself (checked with
`trace_headers`: 5 SEI of each for 4 keyframes plus the extradata, with or
without `repeat-headers`), so every ABR segment carries them.
`repeat-headers` would only put the parameter sets in-band, which an `hvc1`
track (the sample entry Apple requires) must not have. SVT-AV1 writes its
metadata OBUs with every keyframe (6 pairs for 6 keyframes).

**Checked end to end** on the rendered commands of `qc run` (HEVC and AV1
720p rungs of the Sol Levante source): `bt2020 / smpte2084 / bt2020nc /
tv`, HDR10 with the source's mastering display and MaxCLL 1567 / MaxFALL
972 read back by qc's own probe, metadata at every keyframe.

**Which metric drives the rungs.** The ladder spaces its rungs by VMAF on
the HDR signal by default. The decisions a ladder takes are relative within
one title — which resolution wins at a bitrate, where the curve flattens,
one step between neighbouring rungs — and VMAF on PQ ranks encodes of one
title consistently (identically to tone-mapped VMAF, wPSNR and ΔE ITP on
the validation encodes). Its absolute levels are not calibrated for HDR:
the top VMAF 95 and the 6-point step are SDR conventions, which is why the
rung table also shows wPSNR and ΔE ITP for every rung, and `--hdr-metric
tonemap` is there for ladders tuned on what SDR screens show (at the cost
of every probe's tone mapping). Neither is an HDR perceptual model: that
is the main limit of HDR ladders here.

## 6. Reports

- Terminal: an `[HDR10]` (`[HLG]`…) badge on the analysis header, a *Light
  levels* block (MaxCLL robust and strict, MaxFALL, the signalled values,
  sparklines of the robust peak and the average), the HDR findings, the
  HDR metrics in the metrics table, and on VMAF "VMAF on PQ, not
  HDR-calibrated" or "VMAF on an SDR tone mapping".
- HTML: a *Light* card, a *Light levels* section charting the robust peak
  and the average light of every frame against the measured and signalled
  MaxCLL and MaxFALL (tooltips give the exact time, the frame and the strict
  peak), the VMAF card labelled the same way, the HDR metrics with their
  intervals and a note on how to read them, and the ladder's HDR findings;
  the rung quality table shows wPSNR Y and ΔE ITP.

## 7. CLI

| Flag | Default | Meaning |
|---|---|---|
| `--hdr-metric` (`vmaf`, `ladder`, `run`) | `pq` | VMAF on HDR references: `pq` (on the HDR signal) or `tonemap` (on an SDR tone mapping, slower) |

The wizard asks this one question when the picked video (or the reference)
is HDR and something measures VMAF; the dynamic range it detected is in the
question.

## Cost summary

Measured before and after on an Apple M2 Max, on the 12 s Sol Levante
excerpt and on SDR clips: [validation](validation.md#cost). SDR analyses,
comparisons and ladders give identical reports in the same time.

## Limitations and open questions

- **No HDR perceptual model.** VMAF on PQ ranks; tone-mapped VMAF answers an
  SDR question. HDR-VMAF-like models (HDRMAX features, Netflix's internal
  HDR VMAF) are not public or not packaged.
- **MaxCLL on 4:2:0** is inherently ambiguous: the strict maximum depends on
  chroma upsampling. qc reports both and checks the robust one; a
  4:4:4 master should be measured for the value to signal.
- **HLG** light levels and ΔE ITP assume the 1 000 cd/m² reference display;
  wPSNR is not computed on HLG.
- **Tone mapping** depends on the ffmpeg version (swscale's perceptual
  intent is documented as subject to change).
- **Dolby Vision** RPUs and **HDR10+** dynamic metadata are neither measured
  nor carried to the rungs: DV and HDR10+ ladders need the RPU / ST 2094-40
  metadata re-attached (e.g. with `dovi_tool` / `hdr10plus_tool`).
- **NVENC HDR10 metadata** relies on ffmpeg forwarding the source's side
  data; not verified on a GPU.
- **Validation corpus**: one real HDR10 title (animation, Sol Levante) and
  synthetic clips; no HLG camera content yet.

## References

- SMPTE ST 2084 (PQ), ARIB STD-B67 / ITU-R BT.2100 (HLG, ICtCp, Tables 4–9)
- ITU-R BT.2124-0 (2019), *Objective metric for the assessment of the
  potential visibility of colour differences in television*
- ITU-R BT.2408 (reference levels: PQ 58% / HLG 75% for 203 cd/m²)
- CTA-861.3-A (2016), *HDR Static Metadata Extensions*, MaxCLL/MaxFALL
- SMPTE ST 2086 (mastering display colour volume), ST 2094-40 (HDR10+)
- JVET-H1002, *Joint Call for Proposals on Video Compression with
  Capability beyond HEVC*, Annex D (wPSNR, deltaE100, PSNR-L100); VTM
  `EncGOP.cpp` / `RdCost.cpp` (wPSNR implementation)
- Ebenezer, Shang, Wu, Wei, Sethuraman, Bovik, *HDR or SDR? A Subjective and
  Objective Study of Scaled and Compressed Videos*, IEEE TIP 2023
  (arXiv:2304.13162)
- Shang et al., *A Study of Subjective and Objective Quality Assessment of
  HDR Videos* (LIVE HDR, HDRMAX), IEEE TIP 33, 2024
- x265 documentation (`--master-display`, `--max-cll`, `--hdr10`,
  `--hdr10-opt`, `--repeat-headers`); SVT-AV1 `Docs/Parameters.md`
  (`mastering-display`, `content-light`)
- FFmpeg swscale colour management (`intent`, cms.c, ported from libplacebo)
- Netflix Open Content, *Sol Levante* (CC BY 4.0)
