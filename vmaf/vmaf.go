// Package vmaf describes VMAF measurements without depending on libvmaf:
// models and the resolution they are evaluated at (ModelSpec,
// ResolveModel, DeviceModel), extra feature extractors (Extractor), where
// the model features are extracted (Backend, ResolveBackend) and the
// contract of a scoring engine (Models, Scorer).
//
// It needs no cgo, so that packages orchestrating measurements build
// without a C toolchain. The libvmaf binding, which implements Models and
// Scorer, lives in vmaf/libvmaf.
package vmaf

import (
	"errors"

	"github.com/eko/qc/frame"
)

// ErrUnknownExtractor is returned for an extractor a scoring engine does not
// know how to read back.
var ErrUnknownExtractor = errors.New("unknown extractor")

// Extractor is a libvmaf feature extractor measured next to the model
// features, on the same pictures.
type Extractor string

// Extractors of the libvmaf feature set.
const (
	// ExtractorPSNR measures PSNR per plane (FeaturePSNRY, Cb, Cr).
	ExtractorPSNR Extractor = "psnr"
	// ExtractorPSNRHVS measures PSNR-HVS (FeaturePSNRHVS).
	ExtractorPSNRHVS Extractor = "psnr_hvs"
	// ExtractorSSIM measures SSIM on luma (FeatureSSIM).
	ExtractorSSIM Extractor = "float_ssim"
	// ExtractorMSSSIM measures multi-scale SSIM on luma (FeatureMSSSIM).
	ExtractorMSSSIM Extractor = "float_ms_ssim"
	// ExtractorCIEDE2000 measures the CIEDE2000 colour difference, as a
	// dB-like score (FeatureCIEDE2000).
	ExtractorCIEDE2000 Extractor = "ciede"
	// ExtractorCAMBI measures banding (FeatureCAMBI) with the options of
	// the VMAF v1 models: free when a v1 model is scored.
	ExtractorCAMBI Extractor = "cambi"
	// ExtractorCAMBISource is ExtractorCAMBI measuring the reference too,
	// on the same frames (FeatureCAMBISource): it tells the banding of the
	// encode from the banding it inherited, and costs CAMBI once more.
	ExtractorCAMBISource Extractor = "cambi_source"
)

// Feature outputs read back per frame, by name in Scores.Features.
const (
	FeaturePSNRY     = "psnr_y"
	FeaturePSNRCb    = "psnr_cb"
	FeaturePSNRCr    = "psnr_cr"
	FeaturePSNRHVS   = "psnr_hvs"
	FeatureSSIM      = "float_ssim"
	FeatureMSSSIM    = "float_ms_ssim"
	FeatureCIEDE2000 = "ciede2000"
	FeatureCAMBI     = "cambi"
	// FeatureCAMBISource is CAMBI on the reference frames.
	FeatureCAMBISource = "cambi_source"
)

// ScorerConfig describes what a Scorer measures next to its models.
type ScorerConfig struct {
	// Extractors are measured in the same pass as the models.
	Extractors []Extractor
	// Width and Height are the geometry of the 4:2:0 frames.
	Width, Height int
	// BitDepth is 8, or 10 with 16-bit samples.
	BitDepth int
	// Threads is the engine's worker count.
	Threads int
	// Encoded is the distorted video as it was encoded, before it was
	// scaled to Width × Height and converted to BitDepth: CAMBI reads
	// banding at that resolution and bit depth, for the models using it
	// (VMAF v1) and for ExtractorCAMBI. The zero value tells nothing, and
	// CAMBI then takes the frames as the encode.
	Encoded Encoded
	// Backend is where the model features are extracted: BackendCPU (the
	// zero value) or BackendCUDA. Resolve BackendAuto with ResolveBackend
	// first. Extractors always run on the CPU.
	Backend Backend
}

// Encoded describes a video as it was encoded.
type Encoded struct {
	Width, Height int
	// BitDepth is the bit depth of the encode (8, 10...).
	BitDepth int
}

// Scores are the per-frame results of a Scorer.
type Scores struct {
	// VMAF holds one score per pushed pair for each model, in the order
	// the models were loaded.
	VMAF [][]float64
	// Features holds one value per pushed pair for each extractor output.
	Features map[string][]float64
}

// Models are VMAF models loaded once and shared by the scorers they create,
// so that scoring many short clips does not reload them. They must outlive
// their scorers and are not safe for concurrent use.
type Models interface {
	// NewScorer returns a Scorer measuring every model and cfg.
	NewScorer(
		cfg ScorerConfig,
	) (Scorer, error)
	// Close releases the models.
	Close()
}

// Scorer computes per-frame VMAF, with one or more models, and extra
// features for one contiguous sequence of frame pairs.
type Scorer interface {
	// Push scores the next pair. Frames must carry chroma and match the
	// configured geometry and bit depth; they are not retained.
	Push(
		ref, dist *frame.Frame,
	) error
	// Collect flushes the scorer and returns the values of every pushed
	// pair. It can only be called once.
	Collect() (Scores, error)
	// Close releases the scorer.
	Close()
}
