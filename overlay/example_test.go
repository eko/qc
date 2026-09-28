package overlay_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/overlay"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf/libvmaf"
)

// ExampleRenderer_Render writes a copy of an encode with its analysis and
// the VMAF of every frame burnt in: the exact mode scores every frame, a
// sampled measurement only some.
func ExampleRenderer_Render() {
	ctx := context.Background()
	dec := decode.NewFFmpeg("ffmpeg", 0)
	analyzer := analysis.New(nil,
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		dec,
		quality.NewMeter(dec, libvmaf.NewEngine()),
	)

	report, err := analyzer.Analyze(ctx, "encode.mp4", analysis.Options{})
	if err != nil {
		log.Fatal(err)
	}

	cmp, err := analyzer.Compare(ctx, "reference.mov", "encode.mp4", analysis.CompareOptions{
		Quality:   quality.Options{Exact: true},
		Distorted: report,
	})
	if err != nil {
		log.Fatal(err)
	}

	renderer := overlay.NewRenderer(encode.NewFFmpeg("ffmpeg"))

	err = renderer.Render(ctx, "encode.mp4", "annotated.mp4",
		overlay.Input{Report: report, Quality: cmp.VMAF},
		overlay.RenderOptions{
			Options: overlay.Options{Items: []overlay.Item{overlay.ItemTime, overlay.ItemQuality, overlay.ItemTimeline}},
			Height:  720, // a 720p copy encodes faster; libass scales the overlay
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}

// ExampleWrite writes the ASS script of an overlay, here of a two-frame
// inspection, for a player (mpv --sub-file) or another encoder.
func ExampleWrite() {
	report := &analysis.Report{
		Info: &media.Info{Path: "clip.mp4", Video: []media.VideoStream{{Width: 1280, Height: 720}}},
		Bitstream: &bitstream.Report{
			Duration:   media.Seconds(0.08),
			PTS:        []media.Duration{0, media.Seconds(0.04)},
			FrameSizes: []int{12_000, 800},
			KeyFlags:   []bool{true, false},
		},
	}

	var script bytes.Buffer
	if err := overlay.Write(&script, overlay.Input{Report: report}, overlay.Options{Items: []overlay.Item{overlay.ItemTime}}); err != nil {
		log.Fatal(err)
	}

	for line := range strings.Lines(script.String()) {
		if strings.HasPrefix(line, "PlayRes") || strings.Contains(line, "#1") {
			fmt.Print(line)
		}
	}

	// Output:
	// PlayResX: 1920
	// PlayResY: 1080
	// Dialogue: 1,0:00:00.02,0:00:00.08,qc,,0,0,0,,{\an7\pos(42,38)}{\fs36\b1}00:00:00.040{\fs26\b0}{\c&HA8B0B8&}  #1
}
