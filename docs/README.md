# qc documentation

qc analyses a video as fast as the requested reliability allows:
technical metrics, VMAF and per-title adaptive streaming ladders. Every
shortcut it takes is measured against ground truth, and every estimate that
is not exact comes with its uncertainty.

| Document | Contents |
|---|---|
| [Architecture](architecture.md) | Packages, data flow, concurrency, external tools, library usage |
| [Technical analysis](analysis.md) | Probe, bitstream analysis without decoding, single-decode fan-out, SI/TI, shots, black/freeze, crop, luma levels |
| [VMAF engine](vmaf.md) | libvmaf binding, stratified sampling with honest confidence intervals, decoding plans, 10-bit |
| [HDR](hdr.md) | HDR10/PQ/HLG detection and signalling checks, MaxCLL/MaxFALL, wPSNR and ΔE ITP, VMAF on HDR, HDR ladders |
| [Audio](audio.md) | Loudness per ITU-R BS.1770-5 / EBU R 128 / ATSC A/85 (integrated, range, true peak), silence, clipping, phase and channel defects, decoded alongside the video |
| [Ladder engine](ladder.md) | Digest, probe encodes, rate-quality curves, envelope, rung selection, VBV, verification and calibration |
| [Validation](validation.md) | How speed-ups are proven: replay simulation, exhaustive ladder optimum, measured results |
| [CLI](cli.md) | Commands, flags, live dashboard, wizard, JSON and HTML reports |
| [NVIDIA GPUs](gpu.md) | NVDEC decoding, NVENC ladders, CUDA VMAF: what runs where, the CUDA image, the validation kit |
| [Annotated videos](overlay.md) | `--overlay`: the analysis and per-frame VMAF burnt into a copy of the video with libass, frame-accurate |

The landscape beyond VMAF (newer metrics, per-shot and network-aware
encoding, AI models) and the proposed roadmap are in
[innovation.md](innovation.md).

## At a glance

```mermaid
flowchart LR
    src[(source video)] --> inspect["Inspect<br/>probe + packets<br/>~0.1 s, no decoding"]
    inspect --> analysis["Frame analysis<br/>one decode, 6 analyzers"]
    inspect --> vmaf["VMAF<br/>sampled with 95% CI<br/>or exact"]
    ref[(reference)] --> vmaf
    inspect --> ladder["Ladder per codec<br/>digest → probes → envelope<br/>→ rungs → verification"]
    inspect --> audio["Audio<br/>loudness + defects<br/>alongside the frames"]
    audio --> report
    analysis --> report["Terminal · JSON · HTML"]
    vmaf --> report
    ladder --> report
```

Reference numbers on an Apple M2 Max, 1080p25 H.264 sources:

| Task | Exact / exhaustive | qc |
|---|---|---|
| VMAF, 10:36 title (x264 720p rendition) | 131 s (149 s with CPU decoding) | 18.6 s at ±0.5 (95% CI coverage measured at 94.5%) |
| H.264 ladder, 10:36 title | ≈ 2 h (dense grid on the full title) | 1 min 39 s, every rung verified |
| H.264 ladder, 1 min title, vs exhaustive optimum | 13 min | 2 min, mean −0.04 VMAF / −0.7% bitrate from the optimum |
