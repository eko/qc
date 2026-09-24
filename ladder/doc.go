// Package ladder builds a per-title adaptive streaming ladder: probe encodes
// of a representative digest of the title give rate-quality curves per
// resolution, whose upper envelope drives the choice of rungs.
//
// # Building a ladder
//
// NewEngine wires the engine on its ports: an Inspector (analysis.Analyzer)
// measures, an Encoder and a Digester (encode.FFmpeg) encode, and the
// optional GrainLab (encode.FFmpeg, WithGrainLab) enables AV1 film grain
// synthesis. Engine.Build then runs the stages on a source; Options needs
// only its Codec, every other field has a useful zero value.
//
// Rungs are chosen automatically (one quality step at a time) or to an
// imposed shape (Constraints.Rungs, Constraints.Resolutions), then verified
// by encoding the digest with their final settings: VMAF, plus the metrics
// and viewing devices of Options.Metrics and Options.Devices, which feed
// BandedRungs and RankConflicts.
//
// # Curve extension
//
// Fixed probing encodes every candidate resolution at the codec's probe
// CRFs, whose curves may stop below the top quality (Constraints.TopVMAF).
// Two curves are then extended by one probe each, marked Probe.Extra: the
// top resolution's, so that the top rung is capped by the content rather
// than by the probes; and the curve of the resolution just below the top
// rung's, when projected past its probes it would reach the top quality
// at least 10% cheaper, within three times its highest probed bitrate.
// Easy content (cartoons, static shots) often reaches the top quality
// cheaper at 720p than at 1080p, which the probes alone would not show;
// smaller projected savings seldom materialise at ±1 VMAF per probe, so
// they are not worth the extra encode. Adaptive probing
// (Options.Probing = ProbingAdaptive) places its probes by uncertainty
// instead.
//
// # Per-shot rungs
//
// Options.PerShot adds a per-shot version of every rung (Rung.PerShot):
// one CRF per shot of the title at an equal rate-quality slope, for the
// same pooled quality. Result.Shots lists the shots, Result.ShotLadder
// iterates over a rung's allocation shot by shot (the per-shot ladder) and
// PerShot.PooledBitrate gives its predicted bitrate over the whole title.
// Options.PerShotResolution (experimental) also lets each shot pick its
// resolution among the rung's and the neighbouring rungs'.
//
// # Encoders and GPUs
//
// Options.Encoder selects the implementation of the codec's encoder: x264,
// x265 and SVT-AV1 on the CPU (the zero value), or NVIDIA NVENC
// (encode.HardwareNVENC), with the same stages. NVENC supports neither
// per-shot rungs nor film grain synthesis: Build rejects them with
// ErrHardwareEncoder before any work. Check the GPU first with package
// nvidia, since an NVENC encode that fails has no CPU fallback.
// Options.Backend moves VMAF feature extraction to CUDA when the model
// allows it (see quality.Options.Backend).
//
// Film grain synthesis (Options.FilmGrain) is an SVT-AV1 feature, scored
// against a denoised reference; it cannot be combined with per-shot rungs
// (ErrFilmGrain).
package ladder
