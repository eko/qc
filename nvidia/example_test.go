package nvidia_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/nvidia"
)

// Check what a GPU run needs before starting it: NVDEC decoding, and Main10
// AV1 and HEVC ladders on NVENC. NVDEC would fall back to the CPU by itself,
// NVENC would fail at the first probe encode.
func ExampleCheck() {
	err := nvidia.Check(context.Background(), "ffmpeg", nvidia.Requirements{
		HWAccel:  decode.HWAccelCUDA,
		Codecs:   []string{"av1", "hevc"},
		BitDepth: 10,
	})

	var checkErr *nvidia.CheckError

	switch {
	case err == nil:
		fmt.Println("GPU ready")
	case errors.As(err, &checkErr) && checkErr.Part == nvidia.PartEncoding:
		fmt.Println("build the ladders on the CPU:", checkErr.Err)
	default:
		fmt.Println("stay on the CPU:", err)
	}
}
