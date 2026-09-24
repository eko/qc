//go:build cuda

package libvmaf

/*
#cgo pkg-config: libvmaf
#include <stdlib.h>
#include <libvmaf/libvmaf.h>
#include <libvmaf/libvmaf_cuda.h>

// qc_cuda_state opens a CUDA state on the primary context of device 0,
// with its own stream.
static int qc_cuda_state(VmafCudaState **state) {
	VmafCudaConfiguration cfg = {0};
	return vmaf_cuda_state_init(state, cfg);
}

// qc_use_cuda gives the context a CUDA state of its own: vmaf_close
// destroys the stream and releases the context retain of the state it
// imported, so one state cannot serve several contexts. The import copies
// the state, whose allocation is freed here (libvmaf never frees the small
// table of driver functions it points to: a bounded leak per context).
static int qc_use_cuda(VmafContext *vmaf) {
	VmafCudaState *state = NULL;
	int err = qc_cuda_state(&state);
	if (!err) {
		err = vmaf_cuda_import_state(vmaf, state);
	}

	free(state);

	return err;
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/eko/qc/vmaf"
)

// CUDABuilt reports whether the binary was built with the cuda tag, i.e.
// against a libvmaf with CUDA feature extractors.
const CUDABuilt = true

// cudaDevice holds the process-wide CUDA state: it is never released, so
// that its retain keeps the device's primary context alive. Without it,
// every scorer would create the primary context and the last one to close
// would destroy it, which costs far more than scoring a short clip.
var cudaDevice struct {
	once  sync.Once
	state *C.VmafCudaState
	err   error
}

// InitCUDA initialises the CUDA driver and device 0 once for the process
// and keeps its primary context alive. It fails with vmaf.ErrCUDAInit when no
// NVIDIA GPU is usable (no device, no driver, a container without --gpus).
// It is the device initialisation vmaf.ResolveBackend expects.
func InitCUDA() error {
	cudaDevice.once.Do(func() {
		if rc := C.qc_cuda_state(&cudaDevice.state); rc != 0 {
			C.free(unsafe.Pointer(cudaDevice.state))
			cudaDevice.state = nil
			cudaDevice.err = fmt.Errorf("%w: %w", vmaf.ErrCUDAInit, libvmafError(int(rc)))
		}
	})

	return cudaDevice.err
}

// useCUDA imports a CUDA state of its own into ctx: model features
// registered afterwards are extracted on the GPU.
func useCUDA(
	ctx *C.VmafContext,
) error {
	if err := InitCUDA(); err != nil {
		return err
	}

	if rc := C.qc_use_cuda(ctx); rc != 0 {
		return fmt.Errorf("libvmaf: %w: import state: %w", vmaf.ErrCUDAInit, libvmafError(int(rc)))
	}

	return nil
}
