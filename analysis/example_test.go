package analysis_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// exampleAnalyzer wires an Analyzer on the ffmpeg and ffprobe binaries.
func exampleAnalyzer() *analysis.Analyzer {
	dec := decode.NewFFmpeg("ffmpeg", 0)

	return analysis.New(slog.Default(),
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		dec,
		quality.NewMeter(dec, libvmaf.NewEngine()),
	)
}

// The technical analysis of one file: bitrate, GOP structure, then one
// decode for SI/TI, shots, black and frozen segments, crop and levels.
func ExampleAnalyzer_Analyze() {
	report, err := exampleAnalyzer().Analyze(context.Background(), "video.mp4", analysis.Options{})
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Printf("%d shots, SI %.0f, TI %.0f\n", len(report.Video.Shots),
		report.Video.SITI.SISummary.Mean, report.Video.SITI.TISummary.Mean)
}

// VMAF with a 95% confidence interval, plus other metrics and the VMAF of
// viewing devices, measured on the same sampled frames.
func ExampleAnalyzer_Compare() {
	cmp, err := exampleAnalyzer().Compare(context.Background(), "reference.mov", "encode.mp4", analysis.CompareOptions{
		Quality: quality.Options{
			Precision: 0.5, // ± VMAF points; Exact: true scores every frame
			Metrics:   []string{quality.MetricXPSNR, quality.MetricCAMBI, quality.MetricMSSSIM},
			Devices:   []string{vmaf.DevicePhone, vmaf.Device4K},
		},
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	res := cmp.VMAF
	fmt.Printf("VMAF %.2f ± %.2f\n", res.Mean, res.HalfWidth)

	if m, ok := res.Metric(quality.SeriesXPSNRY); ok {
		fmt.Printf("XPSNR Y %.2f dB [%.2f, %.2f]\n", m.Mean, m.Low, m.High)
	}

	if d, ok := res.Device(vmaf.DevicePhone); ok {
		fmt.Printf("phone VMAF %.2f\n", d.Mean)
	}

	if res.Banding != nil {
		for _, s := range res.Banding.Segments {
			fmt.Printf("banding %v–%v, CAMBI peak %.1f\n", s.Start, s.End, s.Peak)
		}
	}
}

// A fixed budget instead of a precision target. Passing the analysed encode
// skips inspecting it again, and its shot cuts become the scenes of a
// per-scene budget (keyframes alone otherwise).
func ExampleAnalyzer_Compare_budget() {
	ctx := context.Background()
	analyzer := exampleAnalyzer()

	encode, err := analyzer.Analyze(ctx, "encode.mp4", analysis.Options{})
	if err != nil {
		fmt.Println(err)

		return
	}

	budget, _ := quality.ParseSample("2/scene") // or "5%"

	cmp, err := analyzer.Compare(ctx, "reference.mov", "encode.mp4", analysis.CompareOptions{
		Quality:   quality.Options{Sample: budget},
		Distorted: encode,
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Printf("VMAF %.2f ± %.2f from %d of %d frames\n",
		cmp.VMAF.Mean, cmp.VMAF.HalfWidth, cmp.VMAF.FramesScored, cmp.VMAF.FramesTotal)
}

// VMAF on an NVIDIA GPU: NVDEC decoding (frames identical to a CPU decode)
// and CUDA feature extraction when the model has CUDA features. VMAF v1,
// the default model, has none: BackendAuto then stays on the CPU and says
// why in BackendNote. CUDA VMAF needs a binary built with -tags cuda
// against a CUDA libvmaf (libvmaf.CUDABuilt).
func ExampleAnalyzer_Compare_gpu() {
	dec := decode.NewFFmpeg("ffmpeg", 0, decode.WithHWAccel(decode.HWAccelCUDA))
	analyzer := analysis.New(slog.Default(),
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		dec,
		quality.NewMeter(dec, libvmaf.NewEngine()),
	)

	cmp, err := analyzer.Compare(context.Background(), "reference.mov", "encode.mp4", analysis.CompareOptions{
		Quality: quality.Options{
			Model:   "vmaf_v0.6.1", // has CUDA features, unlike VMAF v1
			Backend: vmaf.BackendAuto,
		},
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Printf("VMAF %.2f ± %.2f\n", cmp.VMAF.Mean, cmp.VMAF.HalfWidth)
	fmt.Println(cmp.VMAF.GPUSummary()) // e.g. NVDEC decoding (cuda) · VMAF features on CUDA
}

// An HDR (PQ or HLG) video also gets its light levels: the CTA-861.3
// MaxCLL and MaxFALL measured on the decoded frames, next to the values it
// signals, and a comparison against an HDR reference reports the HDR
// metrics (wPSNR, ΔE ITP) and how to read VMAF on it.
func ExampleAnalyzer_Analyze_hdr() {
	analyzer := exampleAnalyzer()

	report, err := analyzer.Analyze(context.Background(), "hdr10.mov", analysis.Options{})
	if err != nil {
		fmt.Println(err)

		return
	}

	video, _ := report.Info.PrimaryVideo()
	fmt.Println(video.HDR.DynamicRange) // HDR10, HLG, HDR10+, DolbyVision...

	if l := report.Video.Light; l != nil {
		fmt.Printf("MaxCLL %.0f (strict %.0f) MaxFALL %.0f cd/m²\n", l.MaxCLLRobust, l.MaxCLL, l.MaxFALL)
	}

	if cll := video.HDR.ContentLightLevel; cll != nil {
		fmt.Printf("signalled %d / %d cd/m²\n", cll.MaxCLL, cll.MaxFALL)
	}

	cmp, err := analyzer.Compare(context.Background(), "hdr10.mov", "encode.mp4", analysis.CompareOptions{
		Quality: quality.Options{HDRMetric: quality.HDRMetricToneMap}, // VMAF on an SDR tone mapping
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	for _, name := range []string{quality.SeriesWPSNRY, quality.SeriesDeltaEITP} {
		if m, ok := cmp.VMAF.Metric(name); ok {
			fmt.Printf("%s %.2f ± %.2f\n", quality.DescribeSeries(name).Label, m.Mean, m.HalfWidth)
		}
	}

	if h := cmp.VMAF.HDR; h != nil {
		fmt.Println(h.Note)
	}
}
