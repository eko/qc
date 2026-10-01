// Command digestsim checks how well the digest of a ladder stands for its
// title. On exact measurements of encodes of the whole title (qc vmaf
// --exact -f json), it reads the VMAF and the bitrate of the frames a digest
// holds and compares them with the whole title's, for four digests: the
// engine's balanced one, its evenly spaced one, evenly spaced segments
// shifted through every phase, which is what uniform sampling gives on
// average (the engine's is one draw of it), and the engine's digest of the
// most complex scenes, which is meant to differ from the title. The digests
// are planned by the engine (ladder.PlanDigest) on the frame analysis of the
// source.
//
//	go run ./bench/digestsim -source analysis.json [-digests 40,80] [-segment 2]
//		[-phases 100] exact.json...
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var (
	// ErrNotExact is returned for a measurement that is not an exact one.
	ErrNotExact = errors.New("not an exact VMAF measurement (qc vmaf --exact -f json)")
	// ErrNoBitrate is returned for a measurement without the bitrate series
	// of its encode.
	ErrNoBitrate = errors.New("no bitrate series of the encode")
	// ErrNoSource is returned without the frame analysis of the source.
	ErrNoSource = errors.New("-source needs the frame analysis of the title (qc analyze -f json)")
	// ErrNotSampled is returned when a digest holds the whole title.
	ErrNotSampled = errors.New("the digest holds the whole title: nothing to compare")
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes digestsim with args and returns the process exit code.
func run(
	args []string,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("digestsim", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: digestsim -source analysis.json [-digests 40,80] [-segment 2] [-phases 100] exact.json...")
		flags.PrintDefaults()
	}

	sourcePath := flags.String("source", "", "frame analysis of the title (qc analyze -f json)")
	digestList := flags.String("digests", "40", "comma-separated digest lengths, in seconds")
	segment := flags.Float64("segment", 2, "length of a digest segment, in seconds")
	phases := flags.Int("phases", 100, "shifts of the evenly spaced segments replayed")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	digests, err := parseSeconds(*digestList)
	if err == nil && (*segment <= 0 || *phases <= 0) {
		err = fmt.Errorf("-segment and -phases must be positive, got %g and %d", *segment, *phases)
	}

	if err == nil && *sourcePath == "" {
		err = ErrNoSource
	}

	if err != nil || flags.NArg() == 0 {
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
		}

		flags.Usage()

		return 2
	}

	if err := simulateAll(stdout, *sourcePath, flags.Args(), digests, media.Seconds(*segment), *phases); err != nil {
		fmt.Fprintln(stderr, "error:", err)

		return 1
	}

	return 0
}

// parseSeconds reads a comma-separated list of positive durations.
func parseSeconds(
	list string,
) ([]media.Duration, error) {
	var out []media.Duration

	for field := range strings.SplitSeq(list, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("invalid digest length %q", field)
		}

		out = append(out, media.Seconds(v))
	}

	return out, nil
}

// simulateAll prints, for every digest length, one row per measurement and
// the root mean square of their errors.
func simulateAll(
	w io.Writer,
	sourcePath string,
	paths []string,
	digests []media.Duration,
	segment media.Duration,
	phases int,
) error {
	source, err := readSource(sourcePath)
	if err != nil {
		return err
	}

	encodes := make([]encode, len(paths))
	for i, path := range paths {
		if encodes[i], err = readEncode(path); err != nil {
			return err
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)

	for _, length := range digests {
		opts := ladder.Options{DigestDuration: length, SegmentDuration: segment}
		if err := simulate(tw, source, paths, encodes, opts, phases); err != nil {
			return err
		}
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	return nil
}

// simulate prints the table of the digests opts describe: the error of
// each on every encode, and their root mean square over the encodes.
func simulate(
	w io.Writer,
	source *analysis.Report,
	paths []string,
	encodes []encode,
	opts ladder.Options,
	phases int,
) error {
	plans, err := planDigests(source, opts)
	if err != nil {
		return err
	}

	c := plans.balanced.Complexity
	fmt.Fprintf(w, "digest %s, %d segments (title SI %.2f TI %.2f; balanced %.2f %.2f; uniform %.2f %.2f; top %.2f %.2f)\n",
		plans.balanced.Duration, len(plans.balanced.Segments), c.TitleSI, c.TitleTI, c.SI, c.TI,
		plans.uniform.Complexity.SI, plans.uniform.Complexity.TI, plans.top.Complexity.SI, plans.top.Complexity.TI)
	fmt.Fprintln(w, "file\tVMAF\tkb/s\tbalanced ΔVMAF\tΔbitrate\tuniform ΔVMAF\tΔbitrate\tevery phase: rms ΔVMAF\trms Δbitrate\ttop ΔVMAF\tΔbitrate\t")

	var total [4]gap

	for i, e := range encodes {
		gaps := [4]gap{
			e.gap(plans.balanced.Segments),
			e.gap(plans.uniform.Segments),
			e.shifted(source.Info.Duration, opts.SegmentDuration, len(plans.uniform.Segments), phases),
			e.gap(plans.top.Segments),
		}

		fmt.Fprintf(w, "%s\t%.2f\t%.0f\t%+.2f\t%+.1f%%\t%+.2f\t%+.1f%%\t%.2f\t%.1f%%\t%+.2f\t%+.1f%%\t\n",
			shortName(paths[i]), e.vmaf, e.bitrate/1000, gaps[0].vmaf, gaps[0].bitrate*100,
			gaps[1].vmaf, gaps[1].bitrate*100, gaps[2].vmaf, gaps[2].bitrate*100, gaps[3].vmaf, gaps[3].bitrate*100)

		for k, g := range gaps {
			total[k].vmaf += g.vmaf * g.vmaf
			total[k].bitrate += g.bitrate * g.bitrate
		}
	}

	for k, t := range total {
		total[k] = gap{vmaf: math.Sqrt(t.vmaf / float64(len(encodes))), bitrate: math.Sqrt(t.bitrate / float64(len(encodes)))}
	}

	fmt.Fprintf(w, "rms\t\t\t%.2f\t%.1f%%\t%.2f\t%.1f%%\t%.2f\t%.1f%%\t%.2f\t%.1f%%\t\n\n",
		total[0].vmaf, total[0].bitrate*100, total[1].vmaf, total[1].bitrate*100, total[2].vmaf, total[2].bitrate*100,
		total[3].vmaf, total[3].bitrate*100)

	return nil
}

// plans are the digests the engine can extract from a title.
type plans struct {
	balanced, uniform, top ladder.Digest
}

// planDigests plans the balanced, uniform and top digests of source.
func planDigests(
	source *analysis.Report,
	opts ladder.Options,
) (plans, error) {
	var out plans

	for sampling, digest := range map[ladder.DigestSampling]*ladder.Digest{
		ladder.DigestBalanced: &out.balanced,
		ladder.DigestUniform:  &out.uniform,
		ladder.DigestTop:      &out.top,
	} {
		opts.DigestSampling = sampling

		planned, err := ladder.PlanDigest(source, opts)
		if err != nil {
			return plans{}, err
		}

		*digest = planned
	}

	switch {
	case out.uniform.Sampling == "":
		return plans{}, ErrNotSampled
	case out.balanced.Sampling != ladder.DigestBalanced:
		return plans{}, ErrNoSource
	}

	return out, nil
}

// gap is the error of a digest on one encode: its VMAF minus the title's,
// and its bitrate over the title's, minus one (as RMS when summed up).
type gap struct {
	vmaf, bitrate float64
}

// encode is an exact measurement of an encode of the whole title.
type encode struct {
	frames []quality.FrameScore
	// buckets is the bitrate of the encode over time, in buckets of
	// interval; the encode ends at end.
	buckets  []bitstream.BitratePoint
	interval media.Duration
	end      media.Duration
	// vmaf and bitrate are the whole title's.
	vmaf, bitrate float64
}

// gap returns the error of the digest made of segments.
func (e encode) gap(
	segments []media.Interval,
) gap {
	var (
		score, bits, seconds float64
		frames               int
	)

	for _, f := range e.frames {
		for _, s := range segments {
			if f.PTS >= s.Start && f.PTS < s.End {
				score += f.Score
				frames++

				break
			}
		}
	}

	for _, s := range segments {
		bits += e.bits(s)
		seconds += (min(s.End, e.end) - s.Start).Seconds()
	}

	return gap{vmaf: score/float64(frames) - e.vmaf, bitrate: bits/seconds/e.bitrate - 1}
}

// bits returns the bits of the encode over the interval: the share of every
// bucket it overlaps.
func (e encode) bits(
	iv media.Interval,
) float64 {
	var bits float64

	for _, b := range e.buckets {
		from, to := max(iv.Start, b.Start), min(iv.End, b.Start+e.interval, e.end)
		if to > from {
			bits += float64(b.Bitrate) * (to - from).Seconds()
		}
	}

	return bits
}

// shifted returns the root mean square error of count evenly spaced
// segments over a title of this duration, shifted through phases positions
// from the start to the end of their share of the title.
func (e encode) shifted(
	duration, segment media.Duration,
	count, phases int,
) gap {
	step := duration / media.Duration(count)

	var total gap

	for p := range phases {
		segments := make([]media.Interval, count)

		for i := range segments {
			start := media.Duration(i)*step + media.Duration(float64(step-segment)*float64(p)/float64(max(phases-1, 1)))
			segments[i] = media.Interval{Start: start, End: start + segment}
		}

		g := e.gap(segments)
		total.vmaf += g.vmaf * g.vmaf
		total.bitrate += g.bitrate * g.bitrate
	}

	return gap{vmaf: math.Sqrt(total.vmaf / float64(phases)), bitrate: math.Sqrt(total.bitrate / float64(phases))}
}

// readSource reads the frame analysis of the title.
func readSource(
	path string,
) (*analysis.Report, error) {
	var report analysis.Report
	if err := readJSON(path, &report); err != nil {
		return nil, err
	}

	if report.Frames == nil || len(report.Frames.SI) == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrNoSource)
	}

	return &report, nil
}

// readEncode reads an exact measurement of an encode of the title.
func readEncode(
	path string,
) (encode, error) {
	var cmp analysis.Comparison
	if err := readJSON(path, &cmp); err != nil {
		return encode{}, err
	}

	if cmp.VMAF == nil || cmp.VMAF.Mode != quality.ModeExact || len(cmp.VMAF.Frames) == 0 {
		return encode{}, fmt.Errorf("%s: %w", path, ErrNotExact)
	}

	if cmp.Distorted == nil || cmp.Distorted.Bitstream == nil || len(cmp.Distorted.Bitstream.Bitrate) == 0 {
		return encode{}, fmt.Errorf("%s: %w", path, ErrNoBitrate)
	}

	bs := cmp.Distorted.Bitstream

	return encode{
		frames:   cmp.VMAF.Frames,
		buckets:  bs.Bitrate,
		interval: bs.Interval,
		end:      bs.Duration,
		vmaf:     cmp.VMAF.Mean,
		bitrate:  float64(bs.AverageBitrate),
	}, nil
}

// readJSON decodes the JSON file at path into v.
func readJSON(
	path string,
	v any,
) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	return nil
}

// shortName is the file name of path without its extension.
func shortName(
	path string,
) string {
	base := filepath.Base(path)

	return strings.TrimSuffix(base, filepath.Ext(base))
}
