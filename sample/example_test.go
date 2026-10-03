package sample_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/sample"
)

// exampleEngine wires a sample Engine on the ffmpeg and ffprobe binaries:
// the analyzer inspects and analyses the sources (no quality meter is
// needed), encode.FFmpeg copies the scenes and checks the result.
func exampleEngine() *sample.Engine {
	analyzer := analysis.New(slog.Default(),
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		decode.NewFFmpeg("ffmpeg", 0),
		nil,
	)

	return sample.NewEngine(analyzer, encode.NewFFmpeg("ffmpeg"))
}

// A minute of the most complex scenes of three episodes, copied into one
// file without re-encoding. The sample plays the most complex scene first.
func ExampleEngine_Extract() {
	sources := []string{"episode-01.mov", "episode-02.mov", "episode-03.mov"}

	res, err := exampleEngine().Extract(context.Background(), sources, "sample.mkv", sample.Options{
		Duration: media.Seconds(60),
		Scenes:   sample.ScenesTop,
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	// The scenes as the sample plays them.
	for _, scene := range res.Order {
		fmt.Printf("%s %s-%s SI %.1f TI %.1f\n", res.Sources[scene.Source].Path, scene.Start, scene.End, scene.SI, scene.TI)
	}

	// The file read back: its frame count, its timestamps, a full decode.
	if !res.Check.OK() {
		fmt.Println("do not trust this sample:", res.Check.Note)
	}
}

// A mixed sample: 30% of the most complex scenes, the rest representative
// of each video, with what every video gave.
func ExampleEngine_Extract_mixed() {
	res, err := exampleEngine().Extract(context.Background(), []string{"film.mp4"}, "mix.mp4", sample.Options{
		Duration: media.Seconds(120),
		Scenes:   sample.ScenesMixed,
		TopShare: 0.3,
		Progress: func(p sample.Progress) { fmt.Println(p.Stage, p.Done, p.Total) },
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	for _, video := range res.Sources {
		c := video.Complexity
		fmt.Printf("%s: %s taken in %d scenes, SI %.1f (video %.1f), TI %.1f (video %.1f)\n",
			video.Path, video.Taken, len(video.Segments), c.SI, c.VideoSI, c.TI, c.VideoTI)
	}
}
