package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// vmaf compares CUDA VMAF with CPU VMAF on every frame, at 8 and 10 bits,
// cross-checks it with ffmpeg's libvmaf_cuda filter, and times qc's default
// measurement (VMAF v1, sampled) with and without --gpu.
func (v *validator) vmaf(
	ctx context.Context,
	c content,
) error {
	inputs := []struct {
		label, dist string
	}{
		{label: "H.264 720p, 8-bit", dist: c.distorted},
		{label: "HEVC 1080p, 10-bit", dist: c.distorted10},
	}

	rows := make([][]string, 0, len(inputs))

	for _, in := range inputs {
		rows = append(rows, v.backendRow(ctx, c.source, in.label, in.dist))
	}

	v.report.line("Exact %s on every frame, frames decoded with NVDEC in both cases (identical, see (a)): "+
		"only the feature extraction moves to the GPU. Expected: agreement to ~1e-3 (the CUDA kernels port the integer "+
		"CPU code) and a speed-up that grows with resolution (NVIDIA reports 2.5× FFmpeg throughput at 1080p on an L4).", v0Model)
	v.report.table([]string{"distorted", "CPU time", "CUDA time", "speed-up", "CPU mean", "CUDA mean",
		"max abs diff", "mean diff", "p99 abs diff"}, rows)

	v.report.line("ffmpeg's libvmaf_cuda filter on the 8-bit pair (scale_cuda bilinear upscale, so not the same frames "+
		"as qc's bicubic: a sanity check of the level only): %s", v.ffmpegCUDAVMAF(ctx, c))

	return v.defaultMeasurement(ctx, c)
}

// backendRow measures one pair on both backends.
func (v *validator) backendRow(
	ctx context.Context,
	ref, label, dist string,
) []string {
	common := []string{"--exact", "--model", v0Model, "--hwaccel", "cuda"}

	cpu, cpuTime, err := v.qcVMAF(ctx, ref, dist, append(common, "--vmaf-backend", "cpu")...)
	if err != nil {
		return []string{label, "failed: " + oneLine(err.Error()), "", "", "", "", "", "", ""}
	}

	gpu, gpuTime, err := v.qcVMAF(ctx, ref, dist, append(common, "--vmaf-backend", "cuda")...)
	if err != nil {
		return []string{label, seconds(cpuTime), "failed: " + oneLine(err.Error()), "", fmt.Sprintf("%.4f", cpu.VMAF.Mean), "", "", "", ""}
	}

	d := compareFrames(cpu, gpu)

	return []string{
		label, seconds(cpuTime), seconds(gpuTime), fmt.Sprintf("%.1f×", cpuTime.Seconds()/max(gpuTime.Seconds(), 1e-3)),
		fmt.Sprintf("%.4f", cpu.VMAF.Mean), fmt.Sprintf("%.4f", gpu.VMAF.Mean),
		fmt.Sprintf("%.6f", d.maxAbs), fmt.Sprintf("%+.6f", d.mean), fmt.Sprintf("%.6f", d.p99),
	}
}

// libvmafLog is the part of libvmaf's JSON log read back.
type libvmafLog struct {
	Pooled struct {
		VMAF struct {
			Mean float64 `json:"mean"`
		} `json:"vmaf"`
	} `json:"pooled_metrics"`
}

// ffmpegCUDAVMAF measures the 8-bit pair with ffmpeg's libvmaf_cuda filter.
func (v *validator) ffmpegCUDAVMAF(
	ctx context.Context,
	c content,
) string {
	logPath := v.path("libvmaf_cuda.json")
	scale := fmt.Sprintf("scale_cuda=%d:%d:format=yuv420p", c.width, c.height)
	graph := fmt.Sprintf("[0:v]%s[d];[1:v]%s[r];[d][r]libvmaf_cuda=model=version=%s:log_fmt=json:log_path=%s",
		scale, scale, v0Model, logPath)

	_, elapsed, err := v.timed(ctx, v.opts.ffmpeg, "-v", "error", "-nostdin",
		"-hwaccel", "cuda", "-hwaccel_output_format", "cuda", "-i", c.distorted,
		"-hwaccel", "cuda", "-hwaccel_output_format", "cuda", "-i", c.source,
		"-filter_complex", graph, "-f", "null", "-")
	if err != nil {
		return "failed: `" + oneLine(err.Error()) + "`"
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		return "failed: `" + oneLine(err.Error()) + "`"
	}

	var log libvmafLog
	if err := json.Unmarshal(data, &log); err != nil {
		return "failed: `" + oneLine(err.Error()) + "`"
	}

	return fmt.Sprintf("mean %.4f in %s.", log.Pooled.VMAF.Mean, seconds(elapsed))
}

// defaultMeasurement times qc's default measurement (VMAF v1, sampled to
// ±0.5, default metrics) on the CPU and with --gpu: VMAF v1 has no CUDA
// features, so --gpu only moves decoding, and the result must not change.
func (v *validator) defaultMeasurement(
	ctx context.Context,
	c content,
) error {
	rows := [][]string{}

	for _, mode := range []struct {
		label string
		args  []string
	}{
		{label: "CPU", args: []string{"--metrics", "xpsnr,cambi,psnr"}},
		{label: "--gpu", args: []string{"--metrics", "xpsnr,cambi,psnr", "--gpu"}},
	} {
		cmp, elapsed, err := v.qcVMAF(ctx, c.source, c.distorted, mode.args...)
		if err != nil {
			rows = append(rows, []string{mode.label, "failed: " + oneLine(err.Error()), "", ""})

			continue
		}

		rows = append(rows, []string{mode.label, seconds(elapsed),
			fmt.Sprintf("%.4f ± %.4f", cmp.VMAF.Mean, cmp.VMAF.HalfWidth), cmp.VMAF.GPUSummary()})
	}

	v.report.line("qc's default measurement (VMAF v1, sampled, XPSNR + CAMBI + PSNR): VMAF v1 has no CUDA features, " +
		"so `--gpu` falls back to CPU features and only offloads decoding. Means must be identical.")
	v.report.table([]string{"mode", "time", "VMAF", "GPU"}, rows)

	return nil
}
