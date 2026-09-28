package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// reference is what ffmpeg measures of a stream: integrated loudness,
// loudness range and true peak (NaN when not reported), and how long it
// took.
type reference struct {
	integrated, lra, truePeak float64
	elapsed                   time.Duration
}

// ebur128Filter has ffmpeg's ebur128 filter measure with its true peak
// (ffmpeg upsamples to 192 kHz for it) and print its readings of every
// frame to three decimals: the integrated loudness and loudness range so
// far, and the true peak (linear).
const ebur128Filter = "ebur128=peak=true:metadata=1:framelog=quiet," +
	"ametadata=mode=print:key=lavfi.r128.I," +
	"ametadata=mode=print:key=lavfi.r128.LRA," +
	"ametadata=mode=print:key=lavfi.r128.true_peak"

// Readings printed by ebur128Filter.
var (
	readingIntegrated = regexp.MustCompile(`lavfi\.r128\.I=(\S+)`)
	readingRange      = regexp.MustCompile(`lavfi\.r128\.LRA=(\S+)`)
	readingPeak       = regexp.MustCompile(`lavfi\.r128\.true_peak=(\S+)`)
)

// ebur128 measures stream (an ffmpeg stream specifier, "0:1") of path
// with ffmpeg's ebur128 filter: the readings of the last frame, and the
// highest true peak.
func ebur128(
	ctx context.Context,
	bin, path, stream string,
) (reference, error) {
	out, elapsed, err := ffmpegLog(ctx, bin, path, stream, ebur128Filter)
	if err != nil {
		return reference{}, err
	}

	peak := math.NaN()
	for _, m := range readingPeak.FindAllStringSubmatch(out, -1) {
		if v := number(m[1]); !(v <= peak) {
			peak = v
		}
	}

	return reference{
		integrated: last(readingIntegrated, out),
		lra:        last(readingRange, out),
		truePeak:   20 * math.Log10(peak),
		elapsed:    elapsed,
	}, nil
}

// loudnorm measures stream of path with the first pass of ffmpeg's
// loudnorm filter (its own BS.1770 implementation, on the signal
// upsampled to 192 kHz), whose JSON has two decimals.
func loudnorm(
	ctx context.Context,
	bin, path, stream string,
) (reference, error) {
	out, elapsed, err := ffmpegLog(ctx, bin, path, stream, "loudnorm=print_format=json")
	if err != nil {
		return reference{}, err
	}

	from, to := strings.LastIndex(out, "{"), strings.LastIndex(out, "}")
	if from < 0 || to < from {
		return reference{}, fmt.Errorf("loudnorm: no JSON in the output of %s", path)
	}

	var measured struct {
		I   string `json:"input_i"`
		TP  string `json:"input_tp"`
		LRA string `json:"input_lra"`
	}

	if err := json.Unmarshal([]byte(out[from:to+1]), &measured); err != nil {
		return reference{}, fmt.Errorf("loudnorm: %w", err)
	}

	return reference{integrated: number(measured.I), lra: number(measured.LRA), truePeak: number(measured.TP), elapsed: elapsed}, nil
}

// ffmpegLog filters stream of path through filter and returns ffmpeg's
// log (stderr) and how long it ran.
func ffmpegLog(
	ctx context.Context,
	bin, path, stream, filter string,
) (string, time.Duration, error) {
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, "-nostdin", "-hide_banner", "-i", path, "-map", stream, "-af", filter, "-f", "null", "-")
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Run(); err != nil {
		return "", 0, fmt.Errorf("%s %s on %s: %w: %s", bin, filter, path, err, tail(stderr.String()))
	}

	return stderr.String(), time.Since(start), nil
}

// encodeAAC encodes a WAV file to AAC-LC at 256 kb/s, the delivery
// bitrate of stereo programmes.
func encodeAAC(
	ctx context.Context,
	bin, in, out string,
) error {
	cmd := exec.CommandContext(ctx, bin, "-nostdin", "-v", "error", "-y", "-i", in, "-c:a", "aac", "-b:a", "256k", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("encode %s: %w: %s", in, err, tail(string(msg)))
	}

	return nil
}

// last parses the last number captured by re in s, NaN without a match.
func last(
	re *regexp.Regexp,
	s string,
) float64 {
	all := re.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return math.NaN()
	}

	return number(all[len(all)-1][1])
}

// number parses a decimal, NaN when it is not one (-inf).
func number(
	s string,
) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(v, 0) {
		return math.NaN()
	}

	return v
}

// tailLength bounds the error output quoted.
const tailLength = 400

// tail is the end of a tool's output, for error messages.
func tail(
	s string,
) string {
	s = strings.TrimSpace(s)

	return s[max(0, len(s)-tailLength):]
}
