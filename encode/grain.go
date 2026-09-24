package encode

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/internal/ffexec"
)

// DecodeRaw decodes src to raw video in a NUT container at dst. withGrain
// false leaves AV1 film grain out (exported as side data instead of
// applied): the clean picture fidelity is scored against a denoised
// reference, since VMAF penalises synthesised grain for not matching the
// source's grain sample by sample.
func (f *FFmpeg) DecodeRaw(
	ctx context.Context,
	src, dst string,
	withGrain bool,
) error {
	args := []string{"-v", "error", "-nostdin", "-y"}
	if !withGrain {
		args = append(args, "-export_side_data", "film_grain")
	}

	args = append(args, "-i", src, "-an", "-sn", "-dn", "-c:v", "rawvideo", "-f", "nut", dst)

	if err := ffexec.Stream(ctx, f.bin, args, discard); err != nil {
		return fmt.Errorf("decode %s: %w", src, err)
	}

	return nil
}

// Noise measures the grain of path (frames of width×height), on
// grain.SampleFrames frames taken every step frames, decoded as they would
// be shown (AV1 film grain applied): ffmpeg decodes and scales, the pure
// estimator of package grain measures.
func (f *FFmpeg) Noise(
	ctx context.Context,
	path string,
	width, height, step int,
) (grain.Stats, error) {
	estimator, err := grain.NewEstimator(width, height)
	if err != nil {
		return grain.Stats{}, fmt.Errorf("noise of %s: %w", path, err)
	}

	var stats grain.Stats

	err = ffexec.Stream(ctx, f.bin, noiseArgs(path, width, height, step), func(r io.Reader) error {
		var err error
		stats, err = estimator.Measure(r)

		return err
	})
	if err != nil {
		return grain.Stats{}, fmt.Errorf("noise of %s: %w", path, err)
	}

	return stats, nil
}

// noiseArgs are the ffmpeg arguments of Noise: the sampled frames' luma,
// scaled to width×height, as 8-bit gray on stdout.
func noiseArgs(
	path string,
	width, height, step int,
) []string {
	return []string{
		"-v", "error", "-nostdin", "-i", path,
		"-vf", fmt.Sprintf("select='not(mod(n\\,%d))',scale=%d:%d,format=yuv420p,extractplanes=y", max(step, 1), width, height),
		"-frames:v", strconv.Itoa(grain.SampleFrames), "-fps_mode", "passthrough", "-f", "rawvideo", "-pix_fmt", "gray", "-",
	}
}
