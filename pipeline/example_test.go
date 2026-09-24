package pipeline_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/pipeline"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf/libvmaf"
)

// Everything the CLI's run command does: the technical analysis of an
// encode, its VMAF against the mezzanine (here on a fixed budget of 5% of
// the frames) and one ladder per codec, built from the mezzanine
// (LadderSource: without it, ladders are built from Source), with progress
// hooks. The comparison reuses the analysis of the encode, shot cuts
// included.
func ExampleRunner_Run() {
	dec := decode.NewFFmpeg("ffmpeg", 0)
	analyzer := analysis.New(slog.Default(),
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		dec,
		quality.NewMeter(dec, libvmaf.NewEngine()),
	)
	ffmpeg := encode.NewFFmpeg("ffmpeg")
	runner := pipeline.NewRunner(analyzer, ladder.NewEngine(analyzer, ffmpeg, ffmpeg, ladder.WithGrainLab(ffmpeg)))

	budget, _ := quality.ParseSample("5%")

	rep, err := runner.Run(context.Background(), pipeline.Options{
		Source:       "encode.mp4",
		Reference:    "mezzanine.mov",
		LadderSource: "mezzanine.mov",
		Codecs:       []string{"h264", "av1"},
		Quality:      quality.Options{Sample: budget, Metrics: []string{quality.MetricXPSNR}},
	}, pipeline.Hooks{
		Done: func(i int, r pipeline.StageResult) {
			if r.Ladder != nil {
				fmt.Printf("stage %d: %d %s rungs\n", i, len(r.Ladder.Rungs), r.Ladder.Codec.Name)
			}
		},
		Quality: func(_ int, p quality.Progress) {
			if p.Estimated {
				fmt.Printf("VMAF %.2f ± %.2f so far\n", p.Mean, p.HalfWidth)
			}
		},
		Ladder: func(_ int, p ladder.Progress) {
			if p.Rung != nil {
				fmt.Printf("rung %dp verified\n", p.Rung.Height)
			}
		},
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Printf("VMAF %.2f, %d ladders\n", rep.Comparison.VMAF.Mean, len(rep.Ladders))
}
