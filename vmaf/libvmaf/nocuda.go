//go:build !cuda

package libvmaf

// #include <libvmaf/libvmaf.h>
import "C"

import "github.com/eko/qc/vmaf"

// CUDABuilt reports whether the binary was built with the cuda tag, i.e.
// against a libvmaf with CUDA feature extractors.
const CUDABuilt = false

// InitCUDA initialises the CUDA device once for the process. Without the
// cuda build tag it always fails with vmaf.ErrCUDAUnavailable.
func InitCUDA() error {
	return vmaf.ErrCUDAUnavailable
}

// useCUDA would import a CUDA state into ctx: not available in this build.
func useCUDA(
	_ *C.VmafContext,
) error {
	return vmaf.ErrCUDAUnavailable
}
