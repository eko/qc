package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf/libvmaf"
)

// grainRung is one rung of a ladder checked against a clean reference.
type grainRung struct {
	bitrate float64
	// clean is the VMAF of the grain-free decode against the clean
	// reference: how well the picture under the grain survives.
	clean float64
	// shown is the VMAF of the decode as viewers see it (grain synthesised)
	// against the grainy source: what a naive measurement reports.
	shown float64
	// ratio is the noise of the decode as shown over the source's.
	ratio float64
}

// grainEncoder is what the grain check needs of an encoder: encodes, and
// the grain-free decodes and grain measurements of the ladder's GrainLab.
type grainEncoder interface {
	Encode(
		ctx context.Context,
		codec encode.Codec,
		src, dst string,
		p encode.Params,
	) error
	ladder.GrainLab
}

// grainTools are the encoder and analyzer of the grain check.
type grainTools struct {
	enc      grainEncoder
	analyzer *analysis.Analyzer
	ffmpeg   string
}

// checkGrain encodes the full title with each rung's settings (and the
// ladder's film grain level) and compares it with clean, the title before
// grain was added: the fidelity of the underlying picture, what a naive
// VMAF against the grainy source says, and the grain given back.
func checkGrain(
	ctx context.Context,
	w io.Writer,
	fast *ladder.Result,
	codec encode.Codec,
	clean, dir string,
	opts options,
) error {
	dec := decode.NewFFmpeg(opts.ffmpeg, 0)
	tools := grainTools{
		enc:    encode.NewFFmpeg(opts.ffmpeg),
		ffmpeg: opts.ffmpeg,
		analyzer: analysis.New(slog.New(slog.DiscardHandler),
			probe.NewFFprobe(opts.ffprobe), bitstream.NewFFprobeReader(opts.ffprobe), dec, quality.NewMeter(dec, libvmaf.NewEngine())),
	}

	level := 0
	if fast.Grain != nil {
		level = fast.Grain.Level
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintf(tw, "rung\tres\tbitrate\tVMAF vs clean (grain-free)\tVMAF vs source (as shown)\tgrain given back\t\n")

	for i, r := range fast.Rungs {
		got, err := tools.rung(ctx, fast, codec, r, level, clean, dir)
		if err != nil {
			return err
		}

		fmt.Fprintf(tw, "%d\t%dp\t%.0f\t%.2f\t%.2f\t%.0f%%\t\n", i+1, r.Height, got.bitrate, got.clean, got.shown, got.ratio*100)
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	fmt.Fprintf(w, "\nfilm grain level %d\n", level)

	return nil
}

// rung encodes and measures one rung (see checkGrain).
func (g grainTools) rung(
	ctx context.Context,
	fast *ladder.Result,
	codec encode.Codec,
	r ladder.Rung,
	level int,
	clean, dir string,
) (grainRung, error) {
	source := fast.Source.Info.Path
	video, _ := fast.Source.Info.PrimaryVideo()
	out, free := filepath.Join(dir, "grain.mp4"), filepath.Join(dir, "grain-free.nut")

	defer os.Remove(free)

	p := rungParams(r)
	p.Preset, p.GOP, p.FilmGrain = fast.Preset, int(math.Round(2*video.AvgFrameRate.Float())), level

	if err := g.enc.Encode(ctx, codec, source, out, p); err != nil {
		return grainRung{}, fmt.Errorf("encode %dp: %w", r.Height, err)
	}

	if err := retime(ctx, g.ffmpeg, codec, out, video.AvgFrameRate); err != nil {
		return grainRung{}, err
	}

	if err := g.enc.DecodeRaw(ctx, out, free, false); err != nil {
		return grainRung{}, fmt.Errorf("decode %dp: %w", r.Height, err)
	}

	exact := analysis.CompareOptions{Quality: quality.Options{Exact: true}}

	shown, err := g.analyzer.Compare(ctx, source, out, exact)
	if err != nil {
		return grainRung{}, fmt.Errorf("measure %dp: %w", r.Height, err)
	}

	underlying, err := g.analyzer.Compare(ctx, clean, free, exact)
	if err != nil {
		return grainRung{}, fmt.Errorf("measure %dp against the clean reference: %w", r.Height, err)
	}

	step := int(video.AvgFrameRate.Float())

	src, err := g.enc.Noise(ctx, source, r.Width, r.Height, step)
	if err != nil {
		return grainRung{}, fmt.Errorf("noise: %w", err)
	}

	decoded, err := g.enc.Noise(ctx, out, r.Width, r.Height, step)
	if err != nil {
		return grainRung{}, fmt.Errorf("noise: %w", err)
	}

	return grainRung{
		bitrate: float64(shown.Distorted.Bitstream.AverageBitrate),
		clean:   underlying.VMAF.Mean,
		shown:   shown.VMAF.Mean,
		ratio:   decoded.Sigma / math.Max(src.Sigma, 1e-9),
	}, nil
}
