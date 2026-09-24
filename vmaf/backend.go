package vmaf

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Backend selects where the features of the models are extracted.
type Backend string

// Backends.
const (
	// BackendCPU extracts every feature on the CPU (the zero value).
	BackendCPU Backend = ""
	// BackendCUDA extracts the model features on an NVIDIA GPU. It needs a
	// binary built with the cuda tag (see vmaf/libvmaf) against a libvmaf
	// built with
	// -Denable_cuda=true, and models whose features all have a CUDA
	// extractor (see CUDAUnsupported). Extra extractors (PSNR, CAMBI...)
	// stay on the CPU, in the same context.
	BackendCUDA Backend = "cuda"
	// BackendAuto is BackendCUDA when it can score the models, BackendCPU
	// otherwise (see ResolveBackend). Scorers only take CPU or CUDA.
	BackendAuto Backend = "auto"
)

// backendCPUName is how BackendCPU is written on the command line and in
// reports.
const backendCPUName = "cpu"

var (
	// ErrBackend is returned for an unknown backend name.
	ErrBackend = errors.New("unknown backend")
	// ErrCUDAUnavailable is returned when CUDA is requested from a binary
	// built without the cuda tag.
	ErrCUDAUnavailable = errors.New("built without CUDA support (build with -tags cuda against a CUDA-enabled libvmaf)")
	// ErrCUDAInit is returned when the CUDA driver or device cannot be
	// initialised: no NVIDIA GPU, no driver, or a container started
	// without --gpus.
	ErrCUDAInit = errors.New("CUDA initialisation failed (no usable NVIDIA GPU or driver)")
	// ErrCUDAModel is returned when a model needs features libvmaf only
	// extracts on the CPU.
	ErrCUDAModel = errors.New("model features have no CUDA extractor")
)

// ParseBackend reads a backend name: cpu (or empty), cuda or auto.
func ParseBackend(
	s string,
) (Backend, error) {
	switch b := Backend(strings.ToLower(strings.TrimSpace(s))); b {
	case BackendCPU, backendCPUName:
		return BackendCPU, nil
	case BackendCUDA, BackendAuto:
		return b, nil
	}

	return BackendCPU, fmt.Errorf("%w %q (supported: cpu, cuda, auto)", ErrBackend, s)
}

// String returns the backend name, "cpu" for the zero value.
func (b Backend) String() string {
	if b == BackendCPU {
		return backendCPUName
	}

	return string(b)
}

// cudaFeatures are the features libvmaf 3.2.1 extracts on CUDA: the
// provided_features of its three CUDA extractors (integer VIF, ADM and
// motion, src/feature/cuda/*.c). ADM stops at adm2 and motion at motion2:
// adm3 and motion3 (VMAF v1), CAMBI, chroma speed and every float feature
// are CPU-only.
var cudaFeatures = []string{
	"VMAF_integer_feature_vif_scale0_score", "VMAF_integer_feature_vif_scale1_score",
	"VMAF_integer_feature_vif_scale2_score", "VMAF_integer_feature_vif_scale3_score",
	"VMAF_integer_feature_adm2_score",
	"VMAF_integer_feature_motion_score", "VMAF_integer_feature_motion2_score",
}

// cudaBuiltIns are the libvmaf built-in versions whose features all have a
// CUDA extractor (integer VIF, adm2 and motion2).
var cudaBuiltIns = []string{"vmaf_v0.6.1", "vmaf_v0.6.1neg", "vmaf_4k_v0.6.1", "vmaf_4k_v0.6.1neg"}

// modelFile is the part of a VMAF JSON model naming its features.
type modelFile struct {
	ModelDict struct {
		FeatureNames []string `json:"feature_names"`
	} `json:"model_dict"`
}

// CUDAUnsupported returns the features of the model that libvmaf cannot
// extract on CUDA, nil when the whole model runs on the GPU. With CUDA
// enabled, libvmaf looks every model feature up among its CUDA extractors
// only, so a single missing one makes the model fail to load: VMAF v1
// (cambi, speed_chroma_uv, adm3, motion3) cannot run on CUDA at all.
func CUDAUnsupported(
	spec ModelSpec,
) ([]string, error) {
	if !spec.FromPath {
		if slices.Contains(cudaBuiltIns, spec.Source) {
			return nil, nil
		}

		return []string{"built-in model " + spec.Source}, nil
	}

	data, err := os.ReadFile(spec.Source)
	if err != nil {
		return nil, fmt.Errorf("vmaf: read model: %w", err)
	}

	var model modelFile
	if err := json.Unmarshal(data, &model); err != nil {
		return nil, fmt.Errorf("vmaf: parse model %s: %w", spec.Source, err)
	}

	var missing []string

	for _, name := range model.ModelDict.FeatureNames {
		if !slices.Contains(cudaFeatures, name) {
			missing = append(missing, name)
		}
	}

	return missing, nil
}

// BackendChoice is the backend a measurement runs on, and why it differs
// from the requested one.
type BackendChoice struct {
	Backend Backend
	// Reason explains a fallback from BackendAuto to the CPU.
	Reason string
}

// ResolveBackend picks the backend scoring specs. BackendCUDA fails when
// CUDA cannot score them; BackendAuto then falls back to the CPU and says
// why. initCUDA initialises the device (libvmaf.InitCUDA): it is only called
// once every model has CUDA features, so a CPU-only model never touches the
// GPU.
func ResolveBackend(
	requested Backend,
	specs []ModelSpec,
	initCUDA func() error,
) (BackendChoice, error) {
	if requested != BackendCUDA && requested != BackendAuto {
		if _, err := ParseBackend(string(requested)); err != nil {
			return BackendChoice{}, err
		}

		return BackendChoice{Backend: BackendCPU}, nil
	}

	err := cudaReady(specs, initCUDA)
	switch {
	case err == nil:
		return BackendChoice{Backend: BackendCUDA}, nil
	case requested == BackendAuto:
		return BackendChoice{Backend: BackendCPU, Reason: err.Error()}, nil
	}

	return BackendChoice{}, err
}

// cudaReady checks that CUDA can score every model: first the models (no
// device needed), then the build and the device.
func cudaReady(
	specs []ModelSpec,
	initCUDA func() error,
) error {
	for _, spec := range specs {
		missing, err := CUDAUnsupported(spec)
		if err != nil {
			return err
		}

		if len(missing) > 0 {
			return fmt.Errorf("vmaf: %w: %s needs %s", ErrCUDAModel, spec.Name, strings.Join(missing, ", "))
		}
	}

	if err := initCUDA(); err != nil {
		return fmt.Errorf("vmaf: %w", err)
	}

	return nil
}
