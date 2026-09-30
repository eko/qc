package decode

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/eko/qc/internal/ffexec"
)

// vtProbes are short reference clips of the formats VideoToolbox decodes
// for qc (vtCodecs, vtPixelFormats), each with the pixel format its frames
// are compared in.
var vtProbes = []struct {
	name   string
	clip   []byte
	format string
}{
	{name: "h264.mp4", clip: vtProbeH264, format: "yuv420p"},
	{name: "hevc.mp4", clip: vtProbeHEVC, format: "yuv420p10le"},
}

var (
	//go:embed vtprobe_h264.mp4
	vtProbeH264 []byte
	//go:embed vtprobe_hevc.mp4
	vtProbeHEVC []byte
)

// vtCheckTimeout bounds VideoToolboxExact when run on the first decode.
const vtCheckTimeout = 30 * time.Second

// VideoToolboxExact reports whether ffmpeg (bin) decodes with VideoToolbox
// exactly as on the CPU: every plane of every frame of reference H.264
// 8-bit and HEVC 10-bit clips. It holds on Apple silicon, where H.264 and
// HEVC decoding is bit-exact by specification; a virtualised GPU (a macOS
// virtual machine, a CI runner) was seen to return other chroma. Where
// VideoToolbox is unavailable, ffmpeg decodes on the CPU and the frames
// match.
func VideoToolboxExact(
	ctx context.Context,
	bin string,
) (bool, error) {
	dir, err := os.MkdirTemp("", "qc-vtprobe")
	if err != nil {
		return false, fmt.Errorf("videotoolbox check: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	for _, probe := range vtProbes {
		path := filepath.Join(dir, probe.name)
		if err := os.WriteFile(path, probe.clip, 0o600); err != nil {
			return false, fmt.Errorf("videotoolbox check: %w", err)
		}

		cpu, err := frameHashes(ctx, bin, path, probe.format, nil)
		if err != nil {
			return false, err
		}

		hardware, err := frameHashes(ctx, bin, path, probe.format, hwInputArgs(HWAccelVideoToolbox))
		if err != nil {
			return false, err
		}

		if !bytes.Equal(cpu, hardware) {
			return false, nil
		}
	}

	return true, nil
}

// frameHashes returns the MD5 of every frame of path decoded with the input
// options input and converted to format, without ffmpeg's header comments.
func frameHashes(
	ctx context.Context,
	bin, path, format string,
	input []string,
) ([]byte, error) {
	args := append(append([]string{"-v", "error", "-nostdin"}, input...), "-i", path, "-pix_fmt", format, "-f", "framemd5", "-")

	out, err := ffexec.Output(ctx, bin, args)
	if err != nil {
		return nil, fmt.Errorf("videotoolbox check: %w", err)
	}

	var hashes []byte

	for line := range bytes.Lines(out) {
		if !bytes.HasPrefix(line, []byte("#")) {
			hashes = append(hashes, line...)
		}
	}

	return hashes, nil
}

// vtChecks caches the outcome of VideoToolboxExact per ffmpeg binary: it
// runs once per process, however many sources decode.
var vtChecks sync.Map

// vtResult is the outcome of a cached check.
type vtResult struct {
	once sync.Once
	ok   bool
}

// vtExact reports whether VideoToolbox may decode for d: it checks once
// that its frames are those of the CPU (see VideoToolboxExact). When they
// are not, VideoToolbox is left out and a warning is logged; a check that
// cannot run leaves it in, the decodes falling back on their own.
func (d *FFmpeg) vtExact(
	ctx context.Context,
) bool {
	check := d.vtCheck
	result := &d.vt

	if check == nil {
		check = func(ctx context.Context) (bool, error) { return VideoToolboxExact(ctx, d.bin) }
		cached, _ := vtChecks.LoadOrStore(d.bin, &vtResult{})
		result = cached.(*vtResult) //nolint:forcetypeassert // the map only holds *vtResult
	}

	result.once.Do(func() {
		// The outcome is kept for the process: a cancelled decode must not
		// cut the check short.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), vtCheckTimeout)
		defer cancel()

		exact, err := check(ctx)
		result.ok = exact || err != nil

		if !result.ok && d.logger != nil {
			d.logger.Warn("VideoToolbox does not decode as the CPU on this machine (a virtualised GPU?): decoding on the CPU")
		}
	})

	return result.ok
}
