package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/eko/qc/analysis"
)

// v0Model is the VMAF model with CUDA features in libvmaf 3.2.1: VMAF v1
// (qc's default) has none, so CUDA comparisons use v0.6.1.
const v0Model = "vmaf_v0.6.1"

// decoding measures NVDEC against the CPU decoder: speed, identical frames,
// and the same checks through qc (analysis time, VMAF unchanged).
func (v *validator) decoding(
	ctx context.Context,
	c content,
) error {
	rows := make([][]string, 0, len(c.clips))

	for _, cl := range c.clips {
		row, err := v.decodeRow(ctx, cl)
		if err != nil {
			row = []string{cl.label, "failed: " + oneLine(err.Error()), "", "", "", "", ""}
		}

		rows = append(rows, row)
	}

	v.report.line("ffmpeg alone: CPU decoding, NVDEC with frames copied to system memory (what qc's `--hwaccel cuda` does), " +
		"and NVDEC with frames left on the GPU (upper bound). Frames are hashed after conversion to the same planar format.")
	v.report.table([]string{"file", "frames", "CPU fps", "NVDEC→host fps", "NVDEC resident fps", "NVDEC used", "identical frames"}, rows)

	return v.qcDecoding(ctx, c)
}

// decodeRow benchmarks one file.
func (v *validator) decodeRow(
	ctx context.Context,
	cl clip,
) ([]string, error) {
	cpuHashes, err := v.frameHashes(ctx, cl, false)
	if err != nil {
		return nil, err
	}

	if len(cpuHashes) == 0 {
		return nil, errNoFrames
	}

	identical := "no hashes"
	if gpuHashes, err := v.frameHashes(ctx, cl, true); err == nil {
		identical = fmt.Sprintf("%d / %d", matching(cpuHashes, gpuHashes), len(cpuHashes))
	}

	frames := float64(len(cpuHashes))
	decode := []string{"-v", "error", "-nostdin", "-i", cl.path, "-map", "0:v:0", "-f", "null", "-"}
	host := append([]string{"-hwaccel", "cuda"}, decode...)
	resident := append([]string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}, decode...)

	cells := []string{cl.label, fmt.Sprintf("%.0f", frames)}

	for _, args := range [][]string{decode, host, resident} {
		cells = append(cells, v.fps(ctx, frames, args))
	}

	used := "no (software fallback)"
	check := []string{
		"-v", "error", "-nostdin", "-hwaccel", "cuda", "-hwaccel_output_format", "cuda", "-i", cl.path,
		"-map", "0:v:0", "-frames:v", "1", "-vf", "hwdownload,format=" + cl.hwFormat, "-f", "null", "-",
	}

	if _, err := v.run.run(ctx, v.opts.ffmpeg, check...); err == nil {
		used = "yes"
	}

	return append(cells, used, identical), nil
}

// fps times an ffmpeg decode of frames frames.
func (v *validator) fps(
	ctx context.Context,
	frames float64,
	args []string,
) string {
	_, elapsed, err := v.timed(ctx, v.opts.ffmpeg, args...)
	if err != nil {
		return "failed"
	}

	return fmt.Sprintf("%.0f", frames/max(elapsed.Seconds(), 1e-3))
}

// frameHashes returns the MD5 of every frame, decoded on the CPU or with
// NVDEC, in the clip's planar format.
func (v *validator) frameHashes(
	ctx context.Context,
	cl clip,
	nvdec bool,
) ([]string, error) {
	args := []string{"-v", "error", "-nostdin"}
	if nvdec {
		args = append(args, "-hwaccel", "cuda")
	}

	args = append(args, "-i", cl.path, "-map", "0:v:0", "-vf", "format="+cl.format, "-f", "framemd5", "-")

	out, err := v.run.run(ctx, v.opts.ffmpeg, args...)
	if err != nil {
		return nil, fmt.Errorf("framemd5: %w", err)
	}

	return parseFrameMD5(out), nil
}

// parseFrameMD5 reads the hashes of ffmpeg's framemd5 muxer (the last
// field of each non-comment line).
func parseFrameMD5(
	out []byte,
) []string {
	var hashes []string

	for line := range bytes.Lines(out) {
		text := strings.TrimSpace(string(line))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		fields := strings.Split(text, ",")
		hashes = append(hashes, strings.TrimSpace(fields[len(fields)-1]))
	}

	return hashes
}

// matching counts the positions where both lists hold the same hash.
func matching(
	a, b []string,
) int {
	n := 0

	for i, hash := range a {
		if i < len(b) && hash == b[i] {
			n++
		}
	}

	return n
}

// qcDecoding runs qc with each hardware decoding mode: the analysis time,
// and exact VMAF on the CPU, which must not change with NVDEC (identical
// frames) and may change slightly with GPU scaling.
func (v *validator) qcDecoding(
	ctx context.Context,
	c content,
) error {
	modes := []string{"none", "cuda", "cuda-scale"}
	rows := make([][]string, 0, len(modes))

	var baseline *analysis.Comparison

	for _, mode := range modes {
		_, analyzed, err := v.timed(ctx, v.opts.qc, "analyze", c.source, "--hwaccel", mode, "-f", "json")
		analyzeTime := seconds(analyzed)

		if err != nil {
			analyzeTime = "failed: " + oneLine(err.Error())
		}

		cmp, elapsed, err := v.qcVMAF(ctx, c.source, c.distorted, "--exact", "--model", v0Model, "--hwaccel", mode)
		if err != nil {
			rows = append(rows, []string{mode, analyzeTime, "failed: " + oneLine(err.Error()), "", ""})

			continue
		}

		if baseline == nil {
			baseline = cmp
		}

		d := compareFrames(baseline, cmp)
		rows = append(rows, []string{mode, analyzeTime, seconds(elapsed),
			fmt.Sprintf("%.4f", cmp.VMAF.Mean), fmt.Sprintf("%.6f / %.6f", d.maxAbs, d.meanAbs)})
	}

	v.report.line("qc: `qc analyze` (every frame, luma analyzers) and exact VMAF (%s, CPU features) of the H.264 720p encode, "+
		"per `--hwaccel` mode. Differences are per frame, against `none`: NVDEC (`cuda`) must give 0.", v0Model)
	v.report.table([]string{"--hwaccel", "analyze time", "exact VMAF time", "mean VMAF", "max / mean abs diff"}, rows)

	return nil
}

// qcVMAF runs qc vmaf and decodes its JSON report.
func (v *validator) qcVMAF(
	ctx context.Context,
	ref, dist string,
	extra ...string,
) (*analysis.Comparison, time.Duration, error) {
	args := append([]string{"vmaf", ref, dist, "-f", "json", "--metrics="}, extra...)

	out, elapsed, err := v.timed(ctx, v.opts.qc, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("qc vmaf: %w", err)
	}

	var cmp analysis.Comparison
	if err := json.Unmarshal(out, &cmp); err != nil {
		return nil, 0, fmt.Errorf("qc vmaf: %w", err)
	}

	if cmp.VMAF == nil {
		return nil, 0, fmt.Errorf("qc vmaf: %w", errNoFrames)
	}

	return &cmp, elapsed, nil
}

// frameDiff summarises per-frame VMAF differences.
type frameDiff struct {
	frames  int
	maxAbs  float64
	meanAbs float64
	// mean is the mean signed difference (b − a).
	mean float64
	p99  float64
}

// compareFrames compares the scores of the frames both measurements share.
func compareFrames(
	a, b *analysis.Comparison,
) frameDiff {
	scores := make(map[int]float64, len(a.VMAF.Frames))
	for _, f := range a.VMAF.Frames {
		scores[f.Index] = f.Score
	}

	var (
		d    frameDiff
		abss []float64
	)

	for _, f := range b.VMAF.Frames {
		ref, ok := scores[f.Index]
		if !ok {
			continue
		}

		diff := f.Score - ref
		abs := max(diff, -diff)
		abss = append(abss, abs)
		d.mean += diff
		d.meanAbs += abs
		d.maxAbs = max(d.maxAbs, abs)
	}

	if d.frames = len(abss); d.frames > 0 {
		d.mean /= float64(d.frames)
		d.meanAbs /= float64(d.frames)
		slices.Sort(abss)
		d.p99 = abss[min(d.frames-1, d.frames*99/100)]
	}

	return d
}

// seconds renders a duration for the report.
func seconds(
	d time.Duration,
) string {
	return fmt.Sprintf("%.1f s", d.Seconds())
}
