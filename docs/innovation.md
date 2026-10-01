# Innovation landscape (September 2026)

What exists beyond VMAF for measuring video quality and optimising
per-title encoding, and which of it qc should adopt. Claims
marked *(unverified)* come from vendor material or secondary sources.

## 1. Quality metrics

| Metric | Kind | Why it matters | Accuracy | Cost | Licence | Integration |
|---|---|---|---|---|---|---|
| **VMAF v1** (June 2026, already used) | Full-reference | CAMBI banding and chroma built in, NEG by default, phone/TV/4K viewing-distance models | Netflix: better than v0 | Faster than v0 (no VIF) | BSD+Patent | Already bound through cgo |
| **libvmaf extra features**: CAMBI per frame, PSNR-HVS, CIEDE2000, MS-SSIM | Full-reference | Exactly the AOM AV2 common-test-conditions metric set | Standard | Cheap | BSD+Patent | Same cgo binding |
| **XPSNR** | Full-reference | Near VMAF on UHD sets (SROCC 0.93 MCML-4K) at roughly PSNR cost; used for convex hulls | High | ~PSNR | LGPL (ffmpeg filter) | ffmpeg subprocess |
| **SSIMULACRA2** / Butteraugli | Full-reference, per image | Most MOS-faithful image metrics in the AV1 community; an independent second opinion to VMAF | ~0.90–0.94 on images | CPU: a few fps *(unverified)*; GPU (Vship) ~150 fps | BSD-3 / MIT | Vship C API (Go bindings exist) or CLI on sampled frames |
| **ColorVideoVDP** | Full-reference, display-aware | Physical display model (nits, distance), SDR and **HDR PQ/HLG**: fills the gap left by the unreleased HDR-VMAF | Beats VMAF on XR-DAVID, trails it on LIVE-HDR | GPU | MIT | Vship or PyTorch subprocess |
| **FDIM** (2026) | Full-reference, deep + VMAF features | Built for neural/generative codec artifacts; beats VMAF on 4K and cross-codec sets | DCVQA 0.853 vs VMAF 0.777 | GPU | CC BY 4.0, code pending | Watch |
| **UVQ 1.5** (Google) | No-reference | Source/mezzanine QC without a reference | Good generalisation *(unverified numbers)* | ~real time on 4 cores at 1 fps sampling | **Apache-2.0** | ONNX (export to do) or Python subprocess |
| DOVER, FAST-VQA | No-reference | Strong no-reference leaders | PLCC 0.85–0.88 | ~1–4 s per clip on CPU | **Non-commercial (S-Lab)**: never bundle | User-supplied model only |
| Q-Align, VQAThinker, VisualQuality-R1 | No-reference, multimodal models (7–8B) | Score *and explain* quality in words | State of the art 2024–2025 | GPU/MLX | Mixed | HTTP sidecar (Ollama/MLX) |
| ITU-T P.1204.3 | No-reference, bitstream | Quality without decoding | Standardised | Very fast | Non-commercial; no AV1 | External tool only |

Caveats:

- **No-reference models do not replace VMAF for ladder decisions.** On
  compression-specific data they often get the direction of rate–quality
  changes wrong (LEHA-CVQAD, 2025).
- **Metrics can be gamed.** Sharpening can raise VMAF-NEG by up to 24% while
  perceived quality drops, and SVT-AV1 `--tune 5` optimises for VMAF directly.
  Cross-checking with an independent metric (SSIMULACRA2, XPSNR) exposes
  this.
- **Synthesised film grain is penalised by VMAF**: it must be scored against a
  denoised reference.

## 2. Per-title and per-shot encoding

| Technique | Gain | State |
|---|---|---|
| Per-shot constant-slope allocation (Netflix Dynamic Optimizer) | 10–17% over per-title *(secondary source)*; 28–38% vs fixed CRF | Needs a full grid of encodes per shot in its published form |
| Fast-preset / other-codec probes, final encode at the slow preset (Meta) | "Slight" loss, large probe savings | Production at Meta |
| Learned hull prediction | 0.26% BD-rate with 62% fewer encodes (RCN-Hull); 1–3% with zero encodes | Research |
| Network-optimal rung placement: ladder = quantisation of the bandwidth distribution, exact dynamic program (Reznik, Aug 2026) | Optimal expected quality for a given audience; principled rung count | Paper only, no implementation known |
| Multi-codec ladders with device share (MCBE, Reznik) | −56% encode energy, −95% storage energy | Research |
| AV1 film grain synthesis | −36% bitrate at 1080p+ on grainy titles (Netflix 2025) | Production at Netflix; no standard metric |
| Energy-aware ladders | −51% encode energy for −5% VMAF | Research |
| Uncertainty-driven (active-learning) probe placement | — | **No published work found** |
| Codecs | AV2 spec v1.0 (May 2026, −30% BD-rate vs AV1, no production encoder yet); VVC/VVenC; LCEVC enhancement layers | Watch |

Our current engine is within 0.04 VMAF / 0.7% bitrate of the exhaustive
optimum, which is already better than zero-encode predictors. The frontier
worth chasing is **the same accuracy with fewer encodes**, and **better
decisions than the "top 95 / step 6" heuristic**.

## 3. Roadmap proposal

### Status

| # | Item | Status | Measured (see [validation](validation.md)) |
|---|---|---|---|
| 1 | libvmaf feature set, AV2 CTC | Done: `--metrics`, `--av2-ctc`, banding findings | CAMBI free next to VMAF v1; CIs cover 92–96% |
| 2 | XPSNR | Done: pure Go, `quality/xpsnr`; ranking check on ladder rungs | Matches ffmpeg within 10⁻⁴ dB; ~10% of VMAF's cost |
| 3 | Device-aware VMAF | Done: `--devices phone,tv,4k`, on comparisons and rungs | Device CIs cover 94.9–95.9% |
| 4 | Network-optimal ladder | Not started | |
| 5 | Per-shot constant slope | Done, opt-in: `--per-shot` | −2 to −4% bitrate on short titles (60–100% of the per-shot optimum); not reliable on long titles yet |
| 6 | Uncertainty-driven probing | Done, opt-in: `--probing adaptive` | Same or better accuracy; fixes AV1 low rungs (+1.94 → −0.05 VMAF from optimum); saves encodes on 1 title of 3 |
| 7 | Grain-aware AV1 | Done, opt-in: `--film-grain auto` | Synthetic grain: top rung 104.6 → 5.8 Mb/s at equal fidelity; no real grainy title validated yet |
| 8–10 | Second opinions and AI | Deferred | |
| 11 | Content-aware digest | Done, default: `--digest balanced`; `--digest top` for the most complex scenes | Digest bitrate error about halved on a 10-minute title (4.7% against 8.9%; 2.6% against 12.1% at the default 40 s); VMAF level unchanged |

### Quick wins (low effort, no new dependency)

1. **Full libvmaf feature set**: per-frame CAMBI banding (flag banding-limited
   rungs), PSNR-HVS, CIEDE2000, MS-SSIM and an "AV2 CTC" report, with the
   same sampled confidence intervals.
2. **XPSNR as a fast control metric**: second opinion on every measurement,
   dense hull search at PSNR cost, and a flag when XPSNR and VMAF rankings
   disagree (metric gaming, grain).
3. **Device-aware quality**: score rungs with the VMAF v1 phone, TV and 4K
   models and report quality per device.

### Flagship innovations

4. **Network-optimal ladder**: given a bandwidth distribution (presets or
   player logs) and the envelope we already compute, place rungs with
   Reznik's exact dynamic program to maximise expected delivered quality, and
   choose the rung count from storage cost vs quality value. No known
   implementation exists, and it can be validated by exhaustive enumeration.
5. **Per-shot constant-slope encoding ("DO-lite")**: model each shot's curve
   as a shift of the title curve, estimated from 1–2 cheap probes per shot.
   Allocate CRF per shot at equal slope λ, bisecting λ per rung, and emit
   x265 `--zonefile` / SVT-AV1 `--qpfile`. Expected +10–17% over per-title;
   to validate against an exhaustive per-shot optimum.
6. **Uncertainty-driven probing**: monotone curves with uncertainty per
   resolution; place each next probe where it reduces the uncertainty of the
   rungs and crossovers most; stop when every rung is known to ±0.1 VMAF.
   Fewer encodes at the same accuracy; no published work found.
7. **Grain-aware AV1 ladders**: detect grain on the digest, enable film grain
   synthesis, score fidelity against the denoised reference and grain
   fidelity by noise-spectrum matching.

### Second opinions and AI (optional plugin tier)

8. **SSIMULACRA2 / ColorVideoVDP** on sampled frames (GPU when available), for
   a VMAF-independent opinion and display-aware HDR quality.
9. **No-reference source QC** with UVQ 1.5 (Apache-2.0) through ONNX Runtime
   loaded at run time (yalue/onnxruntime_go): advisory only, never drives
   decisions. Models downloaded on demand with pinned hashes; non-commercial
   models only from user-supplied files.
10. **Explained QC**: a local vision-language model (e.g. Qwen3-VL through
    Ollama/MLX over HTTP) turns the numbers and worst-case crops at native
    resolution into a written diagnosis. The numbers stay authoritative. No
    benchmark exists for small VLMs describing codec artifacts: building one
    would itself be a contribution.

### Watch list

FDIM (code pending), FUNQUE+ (a pure-Go port would be distinctive), AV2
encoders, VVC + LCEVC, HDR-VMAF (unreleased), DOVER/FAST-VQA (licence).

## Sources

- VMAF v1: <https://github.com/Netflix/vmaf/blob/master/resource/doc/models_v1.md>
- AV2 CTC metrics and AV2 results: <https://arxiv.org/abs/2605.15800>
- XPSNR for convex hulls: <https://arxiv.org/abs/2406.13712>; FDIM comparisons: <https://arxiv.org/html/2604.24123>
- SSIMULACRA2 / Butteraugli / CVVDP on GPU: <https://github.com/Line-fr/Vship>; ColorVideoVDP: <https://github.com/gfxdisp/ColorVideoVDP>
- UVQ: <https://github.com/google/uvq>; DOVER: <https://github.com/VQAssessment/DOVER>; LEHA-CVQAD: <https://arxiv.org/abs/2507.03990>
- Q-Align family: <https://huggingface.co/q-future/one-align>; VQAThinker: <https://arxiv.org/abs/2508.06051>
- ONNX Runtime for Go: <https://github.com/yalue/onnxruntime_go>; yzma (llama.cpp without cgo): <https://github.com/hybridgroup/yzma>
- Dynamic Optimizer: <https://netflixtechblog.com/dynamic-optimizer-a-perceptual-video-encoding-optimization-framework-e19f1e3a277f>; Meta AV1 Reels: <https://engineering.fb.com/2023/02/21/video-engineering/av1-codec-facebook-instagram-reels/>
- Ladder prediction benchmark: <https://arxiv.org/abs/2310.15163>; RCN-Hull: <https://arxiv.org/abs/2206.04877>; compression statistics: <https://arxiv.org/abs/2512.12952>; DQ-Ladder: <https://arxiv.org/abs/2603.13597>
- Network-optimal ladders: <https://arxiv.org/abs/2609.03745>; multi-codec (MCBE): <https://arxiv.org/abs/2310.09570>; energy: <https://arxiv.org/abs/2511.00707>
- Film grain synthesis: <https://github.com/Netflix/vmaf/issues/1192>; metric gaming: <https://arxiv.org/abs/2107.04510>
- Encoder hooks: SVT-AV1 parameters <https://gitlab.com/AOMediaCodec/SVT-AV1/-/blob/master/Docs/Parameters.md>, x265 zonefile <https://x265.readthedocs.io/en/master/cli.html>
