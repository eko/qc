package libvmaf_test

import (
	"errors"
	"fmt"

	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// The libvmaf linked in, and whether this binary can extract VMAF features
// on an NVIDIA GPU: it needs the cuda build tag (go build -tags cuda)
// against a libvmaf built with CUDA, then a usable device.
func ExampleVersion() {
	fmt.Println("libvmaf", libvmaf.Version())

	switch err := libvmaf.InitCUDA(); {
	case err == nil:
		fmt.Println("CUDA VMAF available")
	case !libvmaf.CUDABuilt:
		fmt.Println("built without -tags cuda:", errors.Is(err, vmaf.ErrCUDAUnavailable))
	default:
		fmt.Println("no usable GPU:", err)
	}
}
