package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strconv"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/probe"
)

// crossCheck measures every audio track of the files with qc and with
// ffmpeg's ebur128 and loudnorm filters, and prints the readings, their
// differences and the time each took. Files are named by their base name.
func crossCheck(
	ctx context.Context,
	cfg config,
	w io.Writer,
) error {
	if len(cfg.files) == 0 {
		return nil
	}

	fmt.Fprintln(w, "## Cross-check with ffmpeg")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| file | stream | qc I / LRA / TP | ebur128 I / LRA / TP | loudnorm I / LRA / TP | max Δ ebur128 | max Δ loudnorm | qc | ebur128 | loudnorm |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|")

	analyzer := analysis.New(nil, probe.NewFFprobe(cfg.ffprobe), bitstream.NewFFprobeReader(cfg.ffprobe), decode.NewFFmpeg(cfg.ffmpeg, 0), nil)

	for _, path := range cfg.files {
		if err := crossCheckFile(ctx, cfg, analyzer, path, w); err != nil {
			return err
		}
	}

	fmt.Fprintln(w)

	return nil
}

// crossCheckFile measures the tracks of one file. qc's time is the whole
// audio analysis of the file, its tracks decoded concurrently.
func crossCheckFile(
	ctx context.Context,
	cfg config,
	analyzer *analysis.Analyzer,
	path string,
	w io.Writer,
) error {
	start := time.Now()

	report, err := analyzer.Analyze(ctx, path, analysis.Options{SkipVideo: true, Audio: analysis.AudioOptions{WithInspection: true}})
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	if report.Audio == nil {
		return nil
	}

	for _, t := range report.Audio.Tracks {
		stream := "0:" + strconv.Itoa(t.Stream)

		ebu, err := ebur128(ctx, cfg.ffmpeg, path, stream)
		if err != nil {
			return err
		}

		norm, err := loudnorm(ctx, cfg.ffmpeg, path, stream)
		if err != nil {
			return err
		}

		l := t.Loudness

		fmt.Fprintf(w, "| %s | %d | %.2f / %.2f / %.2f | %s | %s | %.2f | %.2f | %s | %s | %s |\n",
			filepath.Base(path), t.Stream, l.Integrated, l.Range, l.TruePeak, triple(ebu), triple(norm), delta(l, ebu), delta(l, norm),
			seconds(elapsed), seconds(ebu.elapsed), seconds(norm.elapsed))
	}

	return nil
}

// delta is the largest difference between qc's readings and ffmpeg's.
func delta(
	l loudness.Result,
	r reference,
) float64 {
	integrated := diff(l.Integrated, r.integrated)
	// A silent track: qc reads loudness.Floor, ffmpeg the -70 LUFS gate.
	if l.Integrated <= loudness.Floor && r.integrated <= gate {
		integrated = 0
	}

	return max(integrated, diff(l.Range, r.lra), diff(l.TruePeak, r.truePeak))
}

// gate is the absolute gate of BS.1770, which ffmpeg reports for a
// signal without any block above it.
const gate = -70.0

// diff is |a − b|, 0 when b is not a reading.
func diff(
	a, b float64,
) float64 {
	if math.IsNaN(b) || math.IsInf(b, 0) {
		return 0
	}

	return math.Abs(a - b)
}

// triple formats ffmpeg's I / LRA / TP.
func triple(
	r reference,
) string {
	f := func(v float64) string {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "–"
		}

		return strconv.FormatFloat(v, 'f', 2, 64)
	}

	return f(r.integrated) + " / " + f(r.lra) + " / " + f(r.truePeak)
}

// seconds formats a duration in seconds.
func seconds(
	d time.Duration,
) string {
	return fmt.Sprintf("%.2f s", d.Seconds())
}
