package ladder_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/nvidia"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

// exampleEngine wires a ladder Engine on the ffmpeg and ffprobe binaries.
func exampleEngine(
	decodeOpts ...decode.Option,
) *ladder.Engine {
	dec := decode.NewFFmpeg("ffmpeg", 0, decodeOpts...)
	analyzer := analysis.New(slog.Default(),
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		dec,
		quality.NewMeter(dec, libvmaf.NewEngine()),
	)

	// encode.FFmpeg provides every encoding port: encodes, the digest, and
	// the grain measurements AV1 film grain synthesis needs.
	ffmpeg := encode.NewFFmpeg("ffmpeg")

	return ladder.NewEngine(analyzer, ffmpeg, ffmpeg, ladder.WithGrainLab(ffmpeg), ladder.WithRenditionEncoder(ffmpeg))
}

// The automatic ladder: rungs one quality step apart from VMAF 95 down,
// each verified by a real encode of the digest.
func ExampleEngine_Build() {
	res, err := exampleEngine().Build(context.Background(), "source.mov", ladder.Options{Codec: "h264"})
	if err != nil {
		fmt.Println(err)

		return
	}

	for _, r := range res.Rungs {
		fmt.Printf("%dp %d kb/s CRF %.1f VMAF %.1f\n", r.Height, r.Bitrate/1000, r.CRF, r.PredictedVMAF)
		fmt.Println(r.Command)
	}
}

// An imposed ladder: one rung per resolution, top quality 93; the bitrates
// are computed. Verified rungs also get XPSNR, banding and phone VMAF.
func ExampleEngine_Build_imposed() {
	res, err := exampleEngine().Build(context.Background(), "source.mov", ladder.Options{
		Codec:       "hevc",
		Constraints: ladder.Constraints{Resolutions: []int{1080, 720, 540, 360}, TopVMAF: 93},
		Metrics:     []string{quality.MetricXPSNR, quality.MetricCAMBI},
		Devices:     []string{vmaf.DevicePhone},
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	for _, i := range ladder.BandedRungs(res.Rungs) {
		fmt.Printf("%dp is banding-limited\n", res.Rungs[i].Height)
	}

	for _, c := range ladder.RankConflicts(res.Rungs) {
		fmt.Printf("VMAF and XPSNR disagree on rungs %d and %d\n", c.Higher+1, c.Lower+1)
	}
}

// Main10 AV1 with adaptive probing and film grain synthesis when the
// digest is grainy (film grain excludes per-shot rungs).
func ExampleEngine_Build_av1() {
	res, err := exampleEngine().Build(context.Background(), "source.mov", ladder.Options{
		Codec:     "av1",
		BitDepth:  10,
		Probing:   ladder.ProbingAdaptive,
		FilmGrain: ladder.FilmGrainAuto,
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Printf("%d probe encodes, %d rungs\n", len(res.Probes), len(res.Rungs))

	if res.Grain != nil && res.Grain.Detected {
		fmt.Printf("film grain level %d\n", res.Grain.Level)
	}
}

// A per-shot version of every rung: one CRF per shot at an equal
// rate-quality slope. ShotLadder walks a rung's allocation shot by shot,
// PooledBitrate prices it over the whole title.
func ExampleEngine_Build_perShot() {
	res, err := exampleEngine().Build(context.Background(), "source.mov", ladder.Options{
		Codec:   "hevc",
		PerShot: true,
		// PerShotResolution: true would also let each shot pick its
		// resolution among the neighbouring rungs' (experimental).
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	for i, r := range res.Rungs {
		if r.PerShot == nil {
			continue
		}

		fmt.Printf("%dp: %.0f kb/s over the title, %.0f%% saved on the digest\n",
			r.Height, r.PerShot.PooledBitrate(res.Shots)/1000, 100*r.PerShot.Gain)

		for shot, cell := range res.ShotLadder(i) {
			fmt.Printf("  shot at %v: CRF %.1f, %d kb/s\n", res.ShotInterval(shot).Start, cell.CRF, cell.PredictedBitrate/1000)
		}
	}
}

// The renditions of a ladder: every rung, and its per-shot version, encoded
// on the whole title into a folder, then checked against the source, where
// the ladder only predicted them on the digest.
func ExampleEngine_Encode() {
	ctx := context.Background()
	engine := exampleEngine()

	res, err := engine.Build(ctx, "source.mov", ladder.Options{Codec: "av1", PerShot: true})
	if err != nil {
		fmt.Println(err)

		return
	}

	renditions, err := engine.Encode(ctx, "source.mov", res, ladder.RenditionOptions{
		Dir:   "renditions",
		Check: &quality.Options{Precision: 0.5},
		Progress: func(p ladder.RenditionProgress) {
			fmt.Printf("\r%d/%d frames", p.Done, p.Total)
		},
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	for _, rd := range renditions {
		vmaf, bitrate := res.Prediction(rd)
		fmt.Printf("%s: %d kb/s (predicted %.0f), VMAF %s (predicted %.1f)\n",
			rd.Path, rd.Bitrate/1000, bitrate/1000, rd.Checked.VMAFLabel(), vmaf)
	}
}

// A ladder on an NVIDIA GPU: NVDEC decoding, NVENC encodes and CUDA VMAF
// features when the model allows it. The GPU is checked first: NVDEC falls
// back to the CPU by itself, but NVENC would fail at the first probe
// encode. CUDA VMAF needs a binary built with -tags cuda against a CUDA
// libvmaf; with BackendAuto a VMAF v1 model (the default) stays on the CPU.
func ExampleEngine_Build_nvenc() {
	ctx := context.Background()

	if err := nvidia.Check(ctx, "ffmpeg", nvidia.Requirements{
		HWAccel: decode.HWAccelCUDA,
		Codecs:  []string{"h264"},
	}); err != nil {
		fmt.Println(err)

		return
	}

	res, err := exampleEngine(decode.WithHWAccel(decode.HWAccelCUDA)).Build(ctx, "source.mov", ladder.Options{
		Codec:   "h264",
		Encoder: encode.HardwareNVENC,
		Backend: vmaf.BackendAuto,
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(res.Codec.Encoder, "rungs:", len(res.Rungs))

	for _, r := range res.Rungs {
		fmt.Println(r.Command) // ffmpeg -hwaccel cuda -i source.mov ... -c:v h264_nvenc ...
	}
}

// NVENC has neither per-shot rungs nor film grain synthesis: Build rejects
// them before any work.
func ExampleEngine_Build_nvencLimits() {
	engine := exampleEngine()

	for _, opts := range []ladder.Options{
		{Codec: "hevc", Encoder: encode.HardwareNVENC, PerShot: true},
		{Codec: "av1", Encoder: encode.HardwareNVENC, FilmGrain: ladder.FilmGrainAuto},
	} {
		_, err := engine.Build(context.Background(), "source.mov", opts)
		fmt.Println(errors.Is(err, ladder.ErrHardwareEncoder), err)
	}
	// Output:
	// true ladder: per-shot rungs are not supported with a hardware encoder hevc_nvenc
	// true ladder: film grain synthesis is not supported with a hardware encoder av1_nvenc (use the CPU encoder, SVT-AV1)
}

// The shape of a ladder follows from its constraints.
func ExampleConstraints_Shape() {
	for _, c := range []ladder.Constraints{
		{},
		{Rungs: 5},
		{Resolutions: []int{1080, 720, 360}},
	} {
		fmt.Println(c.Shape())
	}
	// Output:
	// auto
	// count
	// resolutions
}

// The ladder of an HDR10 source: every encode is 10-bit and carries the
// source's colour description and HDR10 metadata (the rendered commands
// too); rungs also get the HDR metrics.
func ExampleEngine_Build_hdr() {
	res, err := exampleEngine().Build(context.Background(), "hdr10.mov", ladder.Options{
		Codec:     "hevc",
		HDRMetric: quality.HDRMetricPQ, // VMAF on the PQ signal: ranks the rungs of this title
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	if h := res.HDR; h != nil {
		fmt.Println(h.Signal.Color.Transfer, h.BitDepthUpgraded)
	}

	for _, r := range res.Rungs {
		if r.Measured != nil {
			fmt.Printf("%dp wPSNR %.2f dB, ΔE ITP %.2f\n", r.Height,
				r.Measured.Metrics[quality.SeriesWPSNRY], r.Measured.Metrics[quality.SeriesDeltaEITP])
		}

		fmt.Println(r.Command)
	}
}
