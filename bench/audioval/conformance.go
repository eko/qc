package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/audiotest"
)

// mask50 is the channel mask of 5.0 (FL, FR, FC, BL, BR): the surround
// channels of Tech 3341 case 6, which ffmpeg weights 1.41 like qc.
const mask50 = 0x37

// masks are the WAV channel masks by channel count.
var masks = map[int]uint32{1: audiotest.MaskMono, 2: audiotest.MaskStereo, 5: mask50}

// conformance measures the synthetic cases of EBU Tech 3341 and 3342 with
// qc and with ffmpeg's ebur128 filter and prints the table; ok is false
// when a qc reading is out of tolerance.
func conformance(
	ctx context.Context,
	cfg config,
	w io.Writer,
) (bool, error) {
	fmt.Fprintln(w, "## EBU Tech 3341 / 3342 conformance")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| case | signal | reading | expected | qc | ffmpeg ebur128 | |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|")

	ok := true

	for _, c := range append(audiotest.Tech3341(), audiotest.Tech3342()...) {
		if !cfg.selected(c.Name) {
			continue
		}

		signal := c.Signal()

		meter := loudness.NewMeter(audiotest.Rate, c.Weights)
		if err := meter.Add(signal); err != nil {
			return false, fmt.Errorf("%s: %w", c.Name, err)
		}

		qc := meter.Result()

		ref, err := referenceOf(ctx, cfg, c.Name, signal)
		if err != nil {
			return false, err
		}

		for _, check := range c.Checks {
			reading := check.Reading(qc)
			pass := check.Pass(reading)
			ok = ok && pass

			fmt.Fprintf(w, "| %s | %s | %s | %g %s | %.2f | %s | %s |\n",
				c.Name, c.Description, check.Measure, check.Want, check.Tolerance(), reading, referenceReading(ref, check.Measure), verdict(pass))
		}
	}

	fmt.Fprintln(w)

	return ok, nil
}

// referenceOf writes a signal as a WAV file and measures it with ffmpeg.
func referenceOf(
	ctx context.Context,
	cfg config,
	name string,
	signal [][]float32,
) (reference, error) {
	path := filepath.Join(cfg.dir, name+".wav")
	if err := os.WriteFile(path, audiotest.WAV(audiotest.Rate, masks[len(signal)], signal), 0o600); err != nil {
		return reference{}, fmt.Errorf("%s: %w", name, err)
	}

	return ebur128(ctx, cfg.ffmpeg, path, "0:0")
}

// referenceReading is ffmpeg's reading of a measure, a dash for the
// meters its summary does not give.
func referenceReading(
	ref reference,
	m audiotest.Measure,
) string {
	v := math.NaN()

	switch m {
	case audiotest.Integrated:
		v = ref.integrated
	case audiotest.Range:
		v = ref.lra
	case audiotest.TruePeak:
		v = ref.truePeak
	}

	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "–"
	}

	return fmt.Sprintf("%.2f", v)
}

// verdict marks a check.
func verdict(
	pass bool,
) string {
	if pass {
		return "✓"
	}

	return "✗"
}
