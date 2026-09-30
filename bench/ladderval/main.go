// Command ladderval checks a fast ladder (qc ladder -f json) against the
// exhaustive optimum of the whole title: it encodes the full title on a dense
// resolution × CRF grid with exact VMAF, builds the true rate-quality
// envelope, then encodes the full title with each rung's settings and reports
// how far every rung lands from the envelope.
//
//	go run ./bench/ladderval [-crf-step 3] [-rungs-only] [-precision 0] [-cache grid.json] ladder.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
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

// ErrNotLadder is returned for a file that is not a ladder result.
var ErrNotLadder = errors.New("not a ladder result (qc ladder -f json)")

// envelopePoints is the resolution of the exhaustive envelope.
const envelopePoints = 400

// options are the command-line settings.
type options struct {
	path      string
	crfStep   float64
	rungsOnly bool
	precision float64
	cache     string
	perShot   bool
	shotRungs int
	optimum   int
	shotSpan  float64
	shotStep  float64
	// shotResolutions extends the per-shot optimum grid to the
	// neighbouring rung resolutions.
	shotResolutions bool
	clean           string
	ffmpeg          string
	ffprobe         string
}

// measureFunc encodes the full title with p and measures the result.
type measureFunc func(p encode.Params) (ladder.Probe, error)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run executes ladderval with args and returns the process exit code.
func run(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return 2
	}

	if err := validate(ctx, opts, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "error:", err)

		return 1
	}

	return 0
}

// parseArgs reads the flags; usage errors are reported on stderr.
func parseArgs(
	args []string,
	stderr io.Writer,
) (options, error) {
	flags := flag.NewFlagSet("ladderval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: ladderval [-crf-step 3] [-rungs-only] [-precision 0] [-cache grid.json] [-per-shot] [-shot-optimum n [-shot-resolutions]] [-grain-reference clean.mp4] ladder.json")
		flags.PrintDefaults()
	}

	var opts options

	flags.Float64Var(&opts.crfStep, "crf-step", 3, "CRF spacing of the exhaustive grid")
	flags.BoolVar(&opts.rungsOnly, "rungs-only", false, "skip the exhaustive grid: only check each rung's prediction on the full title")
	flags.Float64Var(&opts.precision, "precision", 0, "VMAF precision on the full title (0 = exact)")
	flags.StringVar(&opts.cache, "cache", "", "reuse and extend full-title measurements stored in this file (same source, codec, preset and precision)")
	flags.BoolVar(&opts.perShot, "per-shot", false, "compare per-shot and per-title rungs on the full title at equal VMAF (ladder built with --per-shot)")
	flags.IntVar(&opts.shotRungs, "shot-rungs", 0, "with -per-shot, check only the first n per-shot rungs (0 = all)")
	flags.IntVar(&opts.optimum, "shot-optimum", 0, "for the first n per-shot rungs, also compare with the exhaustive per-shot optimum (a CRF grid per shot)")
	flags.Float64Var(&opts.shotSpan, "shot-span", 6, "CRF span either side of the rung of the per-shot grid")
	flags.Float64Var(&opts.shotStep, "shot-step", 1, "CRF step of the per-shot grid")
	flags.BoolVar(&opts.shotResolutions, "shot-resolutions", false, "with -shot-optimum, let the optimum pick each shot's resolution among the rung's and the neighbouring rung resolutions too")
	flags.StringVar(&opts.clean, "grain-reference", "", "compare each rung, film grain included, with this clean version of the source (before grain was added)")
	flags.StringVar(&opts.ffmpeg, "ffmpeg", "ffmpeg", "ffmpeg binary")
	flags.StringVar(&opts.ffprobe, "ffprobe", "ffprobe", "ffprobe binary")

	if err := flags.Parse(args); err != nil {
		return options{}, fmt.Errorf("parse flags: %w", err)
	}

	var err error

	switch {
	case flags.NArg() != 1:
		err = errors.New("expected exactly one ladder.json")
	case opts.crfStep <= 0:
		err = fmt.Errorf("-crf-step must be positive, got %g", opts.crfStep)
	case opts.precision < 0:
		err = fmt.Errorf("-precision must not be negative, got %g", opts.precision)
	case opts.shotStep <= 0 || opts.shotSpan < 0:
		err = fmt.Errorf("-shot-step must be positive and -shot-span not negative, got %g and %g", opts.shotStep, opts.shotSpan)
	}

	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		flags.Usage()

		return options{}, err
	}

	opts.path = flags.Arg(0)

	return opts, nil
}

// validate re-encodes the full title to check the ladder of opts.path.
func validate(
	ctx context.Context,
	opts options,
	stdout, stderr io.Writer,
) error {
	fast, err := readLadder(opts.path)
	if err != nil {
		return err
	}

	codec, err := encode.Lookup(fast.Codec.Name)
	if err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "ladderval-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if opts.clean != "" {
		return checkGrain(ctx, stdout, fast, codec, opts.clean, dir, opts)
	}

	if opts.perShot || opts.optimum > 0 {
		return validatePerShot(stdout, stderr, fast, codec, newFrameMeasure(ctx, fast, codec, filepath.Join(dir, "enc.mp4"), opts), opts)
	}

	measure := newMeasure(ctx, fast, codec, filepath.Join(dir, "enc.mp4"), opts)

	if opts.cache != "" {
		header := cacheFile{Source: fast.Source.Info.Path, Codec: codec.Name, Preset: fast.Preset, Precision: opts.precision}
		if measure, err = cached(opts.cache, header, measure); err != nil {
			return err
		}
	}

	fmt.Fprintln(stdout, encodeCount(fast))

	if opts.rungsOnly {
		return checkRungs(stdout, fast, measure)
	}

	grid := gridSpec{CRFs: gridCRFs(codec, opts.crfStep), Step: opts.crfStep, MinCRF: codec.MinCRF, MaxCRF: codec.MaxCRF}

	hull, err := exhaustiveEnvelope(stderr, fast, grid, measure)
	if err != nil {
		return err
	}

	return checkOptimum(stdout, fast, hull, measure)
}

// readLadder reads a ladder result written by qc ladder -f json.
func readLadder(
	path string,
) (*ladder.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var fast ladder.Result
	if err := json.Unmarshal(data, &fast); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	if fast.Source == nil || fast.Source.Info == nil || len(fast.Rungs) == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrNotLadder)
	}

	if _, ok := fast.Source.Info.PrimaryVideo(); !ok {
		return nil, fmt.Errorf("%s: %w: no video stream", path, ErrNotLadder)
	}

	return &fast, nil
}

// newMeasure returns a function encoding the full source of fast into out
// with the ladder's preset and GOP, then measuring its VMAF.
func newMeasure(
	ctx context.Context,
	fast *ladder.Result,
	codec encode.Codec,
	out string,
	opts options,
) measureFunc {
	dec := decode.NewFFmpeg(opts.ffmpeg, 0)
	analyzer := analysis.New(slog.New(slog.DiscardHandler),
		probe.NewFFprobe(opts.ffprobe), bitstream.NewFFprobeReader(opts.ffprobe), dec, quality.NewMeter(dec, libvmaf.NewEngine()))
	enc := encode.NewFFmpeg(opts.ffmpeg)

	source := fast.Source.Info.Path
	video, _ := fast.Source.Info.PrimaryVideo()
	gop := int(math.Round(2 * video.AvgFrameRate.Float()))

	return func(p encode.Params) (ladder.Probe, error) {
		p.Preset, p.GOP = fast.Preset, gop

		if err := enc.Encode(ctx, codec, source, out, p); err != nil {
			return ladder.Probe{}, fmt.Errorf("encode %dp crf %g: %w", p.Height, p.CRF, err)
		}

		if err := retime(ctx, opts.ffmpeg, codec, out, video.AvgFrameRate); err != nil {
			return ladder.Probe{}, err
		}

		cmp, err := analyzer.Compare(ctx, source, out, analysis.CompareOptions{
			Reference: fast.Source,
			Quality:   quality.Options{Exact: opts.precision == 0, Precision: opts.precision},
		})
		if err != nil {
			return ladder.Probe{}, fmt.Errorf("measure %dp crf %g: %w", p.Height, p.CRF, err)
		}

		return ladder.Probe{
			Width:   p.Width,
			Height:  p.Height,
			CRF:     p.CRF,
			Bitrate: cmp.Distorted.Bitstream.AverageBitrate,
			VMAF:    cmp.VMAF.Mean,
		}, nil
	}
}

// gridCRFs are the CRFs of the exhaustive grid: the codec's probe CRFs
// widened by one step on each side, every step, within the codec's range.
func gridCRFs(
	codec encode.Codec,
	step float64,
) []float64 {
	var crfs []float64

	first, last := codec.ProbeCRFs[0]-step, codec.ProbeCRFs[len(codec.ProbeCRFs)-1]+step
	for crf := first; crf <= last; crf += step {
		if crf >= codec.MinCRF && crf <= codec.MaxCRF {
			crfs = append(crfs, crf)
		}
	}

	return crfs
}

// gridSpec is the CRF grid of the exhaustive envelope: CRFs at every
// resolution, extended by Step within MinCRF..MaxCRF until each resolution
// spans the rungs' bitrates.
type gridSpec struct {
	CRFs                 []float64
	Step, MinCRF, MaxCRF float64
}

// spanMargin is how far past the lowest and highest rung bitrates each
// resolution's grid reaches.
const spanMargin = 1.1

// exhaustiveEnvelope encodes the full title at every probed resolution and
// every CRF of the grid, extended until every resolution spans the rungs'
// bitrates: a resolution missing from the grid at a rung's bitrate cannot
// show that it would have been cheaper there (a 1080p rung below the 1080p
// grid would go unchallenged). It returns the upper envelope of the
// rate-quality curves. Progress is logged on log.
func exhaustiveEnvelope(
	log io.Writer,
	fast *ladder.Result,
	grid gridSpec,
	measure measureFunc,
) ([]ladder.HullPoint, error) {
	var (
		heights []int
		curves  []ladder.Curve
	)

	low, high := rungSpan(fast.Rungs)

	for _, fp := range fast.Probes {
		if slices.Contains(heights, fp.Height) {
			continue
		}

		heights = append(heights, fp.Height)

		probes, err := gridCurve(log, fp, grid, low, high, measure)
		if err != nil {
			return nil, err
		}

		curves = append(curves, ladder.NewCurve(probes))
	}

	hull := ladder.Envelope(curves, envelopePoints)
	if len(hull) == 0 {
		return nil, errors.New("empty envelope: the ladder has no probes")
	}

	return hull, nil
}

// rungSpan is the range of bitrates the grid must span: the rungs', with
// spanMargin on each side (none without rungs).
func rungSpan(
	rungs []ladder.Rung,
) (float64, float64) {
	low, high := math.Inf(1), 0.0

	for _, r := range rungs {
		low, high = min(low, float64(r.Bitrate)), max(high, float64(r.Bitrate))
	}

	return low / spanMargin, high * spanMargin
}

// gridCurve measures the resolution of fp at every CRF of the grid, then
// at higher CRFs while its lowest bitrate stays above low and lower CRFs
// while its highest stays below high.
func gridCurve(
	log io.Writer,
	fp ladder.Probe,
	grid gridSpec,
	low, high float64,
	measure measureFunc,
) ([]ladder.Probe, error) {
	var probes []ladder.Probe

	at := func(crf float64) (ladder.Probe, error) {
		p, err := measure(encode.Params{Width: fp.Width, Height: fp.Height, CRF: crf})
		if err != nil {
			return ladder.Probe{}, err
		}

		fmt.Fprintf(log, "grid %dp crf %.0f: %d b/s VMAF %.2f\n", p.Height, crf, p.Bitrate, p.VMAF)
		probes = append(probes, p)

		return p, nil
	}

	for _, crf := range grid.CRFs {
		if _, err := at(crf); err != nil {
			return nil, err
		}
	}

	if len(grid.CRFs) == 0 || grid.Step <= 0 {
		return probes, nil
	}

	for crf := grid.CRFs[len(grid.CRFs)-1] + grid.Step; crf <= grid.MaxCRF && lowest(probes) > low; crf += grid.Step {
		if _, err := at(crf); err != nil {
			return nil, err
		}
	}

	for crf := grid.CRFs[0] - grid.Step; crf >= grid.MinCRF && highest(probes) < high; crf -= grid.Step {
		if _, err := at(crf); err != nil {
			return nil, err
		}
	}

	return probes, nil
}

// lowest and highest are the extreme bitrates of probes.
func lowest(
	probes []ladder.Probe,
) float64 {
	out := math.Inf(1)
	for _, p := range probes {
		out = min(out, float64(p.Bitrate))
	}

	return out
}

func highest(
	probes []ladder.Probe,
) float64 {
	out := 0.0
	for _, p := range probes {
		out = max(out, float64(p.Bitrate))
	}

	return out
}

// checkOptimum encodes the full title with each rung's settings and reports
// its distance to the exhaustive envelope.
func checkOptimum(
	w io.Writer,
	fast *ladder.Result,
	hull []ladder.HullPoint,
	measure measureFunc,
) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "rung\tres\tpredicted VMAF\tfull-title VMAF\tbitrate\toptimum VMAF\tΔVMAF\tbitrate overhead\toptimal res\t")

	var gaps, overheads []float64

	for i, r := range fast.Rungs {
		got, err := measure(rungParams(r))
		if err != nil {
			return err
		}

		best, bestRes := hullAt(hull, float64(got.Bitrate))
		overhead := float64(got.Bitrate)/bitrateFor(hull, got.VMAF) - 1

		gaps = append(gaps, best-got.VMAF)
		overheads = append(overheads, overhead)

		fmt.Fprintf(tw, "%d\t%dp\t%.2f\t%.2f\t%d\t%.2f\t%.2f\t%.1f%%\t%dp\t\n",
			i+1, r.Height, r.PredictedVMAF, got.VMAF, got.Bitrate, best, best-got.VMAF, overhead*100, bestRes)
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	fmt.Fprintf(w, "\nmean ΔVMAF to the optimum: %.2f   mean bitrate overhead at equal quality: %.1f%%\n",
		mean(gaps), mean(overheads)*100)

	return nil
}

// checkRungs compares each rung's prediction (made on the digest) with its
// quality on the full title: it measures how representative the digest is.
func checkRungs(
	w io.Writer,
	fast *ladder.Result,
	measure measureFunc,
) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "rung\tres\tpredicted VMAF\tdigest VMAF\tfull-title VMAF\tpredicted bitrate\tfull-title bitrate\t")

	var errs []float64

	for i, r := range fast.Rungs {
		got, err := measure(rungParams(r))
		if err != nil {
			return err
		}

		digest := math.NaN()
		if r.Measured != nil {
			digest = r.Measured.VMAF
		}

		errs = append(errs, math.Abs(got.VMAF-r.PredictedVMAF))
		fmt.Fprintf(tw, "%d\t%dp\t%.2f\t%.2f\t%.2f\t%d\t%d\t\n",
			i+1, r.Height, r.PredictedVMAF, digest, got.VMAF, r.Bitrate, got.Bitrate)
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	fmt.Fprintf(w, "\nmean |predicted − full title| VMAF: %.2f\n", mean(errs))

	return nil
}

// encodeCount summarises the encodes the ladder cost: the metric adaptive
// probing is judged on, next to the distance to the optimum.
func encodeCount(
	fast *ladder.Result,
) string {
	verified, calibrated := 0, 0

	for _, r := range fast.Rungs {
		if r.Measured != nil {
			verified++
		}

		if r.Calibrated {
			calibrated++
		}
	}

	return fmt.Sprintf("encodes: %d probes + %d verifications + %d calibrations = %d",
		len(fast.Probes), verified, calibrated, len(fast.Probes)+verified+calibrated)
}

// rungParams are the encoding settings of a rung.
func rungParams(
	r ladder.Rung,
) encode.Params {
	return encode.Params{Width: r.Width, Height: r.Height, CRF: r.CRF, MaxRate: r.MaxRate, BufSize: r.BufSize}
}

// hullAt interpolates the envelope VMAF at bitrate b (log scale), and returns
// the resolution of the envelope there.
func hullAt(
	hull []ladder.HullPoint,
	b float64,
) (float64, int) {
	if b <= float64(hull[0].Bitrate) {
		return hull[0].VMAF, hull[0].Height
	}

	for i := 1; i < len(hull); i++ {
		if float64(hull[i].Bitrate) >= b {
			p, q := hull[i-1], hull[i]
			t := (math.Log(b) - math.Log(float64(p.Bitrate))) / (math.Log(float64(q.Bitrate)) - math.Log(float64(p.Bitrate)))

			return p.VMAF + t*(q.VMAF-p.VMAF), q.Height
		}
	}

	last := hull[len(hull)-1]

	return last.VMAF, last.Height
}

// bitrateFor is the lowest envelope bitrate reaching vmaf (log-interpolated),
// or the top of the envelope when vmaf is out of reach.
func bitrateFor(
	hull []ladder.HullPoint,
	vmaf float64,
) float64 {
	for i, p := range hull {
		if p.VMAF >= vmaf {
			if i == 0 {
				return float64(p.Bitrate)
			}

			q := hull[i-1]
			t := (vmaf - q.VMAF) / (p.VMAF - q.VMAF)

			return math.Exp(math.Log(float64(q.Bitrate)) + t*(math.Log(float64(p.Bitrate))-math.Log(float64(q.Bitrate))))
		}
	}

	return float64(hull[len(hull)-1].Bitrate)
}

// mean is the arithmetic mean of values (NaN when empty).
func mean(
	values []float64,
) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}
