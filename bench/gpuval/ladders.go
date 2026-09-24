package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/eko/qc/ladder"
)

// ladderHeights are the candidate resolutions of the compared ladders:
// enough to see crossovers, few enough to keep the kit short.
const ladderHeights = "1080,720,540,360"

// ladders builds each codec's ladder with the CPU encoder and with NVENC
// (--gpu), and compares their cost and their bitrate at equal VMAF.
func (v *validator) ladders(
	ctx context.Context,
	c content,
) error {
	summary := make([][]string, 0, len(v.opts.codecs))

	for _, codec := range v.opts.codecs {
		v.progress("ladders: %s", codec)

		cpu, cpuTime, err := v.qcLadder(ctx, c.source, codec)
		if err != nil {
			summary = append(summary, []string{codec, "failed: " + oneLine(err.Error()), "", "", ""})

			continue
		}

		gpu, gpuTime, err := v.qcLadder(ctx, c.source, codec, "--gpu")
		if err != nil {
			summary = append(summary, []string{codec, seconds(cpuTime), "failed: " + oneLine(err.Error()), "", ""})

			continue
		}

		rows, mean := equalQuality(cpu, gpu)
		summary = append(summary, []string{
			codec + " (" + cpu.Codec.Encoder + " vs " + gpu.Codec.Encoder + ")",
			seconds(cpuTime), seconds(gpuTime), fmt.Sprintf("%.1f×", cpuTime.Seconds()/max(gpuTime.Seconds(), 1e-3)), mean,
		})

		v.report.line("**%s**: CPU rungs, and the NVENC bitrate reaching the same VMAF on the NVENC envelope.", codec)
		v.report.table([]string{"CPU rung", "CPU bitrate", "VMAF", "NVENC bitrate at that VMAF", "NVENC / CPU"}, rows)
		v.report.line("NVENC probes (height, CQ → bitrate, VMAF): %s", probeList(gpu.Probes))
		v.report.line("")
	}

	v.report.line("Summary: whole ladder builds (digest, probes, verification) with `--heights %s`; "+
		"`--gpu` also decodes with NVDEC. Expected: NVENC much faster, and 10–40%% more bitrate than the "+
		"CPU encoders at equal VMAF (larger gap for x265/SVT-AV1 than x264).", ladderHeights)
	v.report.table([]string{"codec", "CPU time", "NVENC time", "speed-up", "mean NVENC / CPU bitrate"}, summary)

	return nil
}

// qcLadder runs qc ladder and decodes its JSON report.
func (v *validator) qcLadder(
	ctx context.Context,
	source, codec string,
	extra ...string,
) (*ladder.Result, time.Duration, error) {
	args := append([]string{"ladder", source, "-c", codec, "--heights", ladderHeights, "--metrics=", "-f", "json"}, extra...)

	out, elapsed, err := v.timed(ctx, v.opts.qc, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("qc ladder: %w", err)
	}

	var res ladder.Result
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, 0, fmt.Errorf("qc ladder: %w", err)
	}

	return &res, elapsed, nil
}

// equalQuality lists the CPU rungs next to the NVENC bitrate reaching their
// VMAF, and the mean bitrate ratio over the rungs the NVENC envelope covers.
func equalQuality(
	cpu, gpu *ladder.Result,
) ([][]string, string) {
	rows := make([][]string, 0, len(cpu.Rungs))
	ratios := 0.0
	matched := 0

	for _, r := range cpu.Rungs {
		bitrate, quality := float64(r.Bitrate), r.PredictedVMAF
		if r.Measured != nil {
			bitrate, quality = float64(r.Measured.Bitrate), r.Measured.VMAF
		}

		row := []string{strconv.Itoa(r.Height) + "p", kbps(bitrate), fmt.Sprintf("%.1f", quality), "outside the NVENC envelope", ""}

		if nvenc, ok := bitrateAt(gpu.Hull, quality); ok && bitrate > 0 {
			ratio := nvenc / bitrate
			row[3], row[4] = kbps(nvenc), fmt.Sprintf("%.2f", ratio)
			ratios += ratio
			matched++
		}

		rows = append(rows, row)
	}

	if matched == 0 {
		return rows, "n/a"
	}

	return rows, fmt.Sprintf("%.2f (%d rungs)", ratios/float64(matched), matched)
}

// bitrateAt reads the bitrate reaching quality on an envelope (ascending
// bitrates, non-decreasing VMAF), interpolated in log bitrate. It never
// extrapolates.
func bitrateAt(
	hull []ladder.HullPoint,
	quality float64,
) (float64, bool) {
	for i, p := range hull {
		if p.VMAF < quality {
			continue
		}

		if i == 0 {
			return float64(p.Bitrate), p.VMAF == quality
		}

		prev := hull[i-1]
		t := (quality - prev.VMAF) / (p.VMAF - prev.VMAF)
		logRate := math.Log(float64(prev.Bitrate)) + t*(math.Log(float64(p.Bitrate))-math.Log(float64(prev.Bitrate)))

		return math.Exp(logRate), true
	}

	return 0, false
}

// probeList renders probes compactly.
func probeList(
	probes []ladder.Probe,
) string {
	parts := make([]string, len(probes))
	for i, p := range probes {
		parts[i] = fmt.Sprintf("%dp CQ %g → %s, %.1f", p.Height, p.CRF, kbps(float64(p.Bitrate)), p.VMAF)
	}

	return strings.Join(parts, ", ")
}

// kbps renders a bitrate.
func kbps(
	bitrate float64,
) string {
	return fmt.Sprintf("%.0f kb/s", bitrate/1000)
}
