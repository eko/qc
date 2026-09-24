// Command vmafsim checks the sampled VMAF estimator against ground truth: it
// replays the production sampling loop many times on the per-frame scores of
// an exact measurement (qc vmaf --exact -f json) and reports the error, the
// real coverage of the confidence intervals and the share of frames scored.
// With -samples it replays fixed budgets too (a share of the frames, or clips
// per scene), with -shots the shot cuts of each distorted file as scenes.
// With -series it also checks the other metrics estimated from the clips
// VMAF samples (XPSNR is replayed on its distortion √WSSE, the quantity it
// pools).
//
//	go run ./bench/vmafsim [-runs 500] [-precisions 0.25,0.5,1] [-samples 5%,1/scene]
//		[-shots analysis.json,...] [-series all] exact.json...
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var (
	// ErrNotExact is returned for a measurement that is not an exact one.
	ErrNotExact = errors.New("not an exact VMAF measurement (qc vmaf --exact -f json)")
	// ErrNoSeries is returned when a requested series is not on every frame.
	ErrNoSeries = errors.New("series not measured on every frame")
	// ErrNoShots is returned for a -shots file without detected shots.
	ErrNoShots = errors.New("no shots: want a technical analysis report (qc analyze -o)")
	// ErrNoConfig is returned when neither precisions nor budgets are given.
	ErrNoConfig = errors.New("nothing to simulate: give -precisions or -samples")
	// ErrShotsCount is returned when -shots does not list one file per
	// measurement.
	ErrShotsCount = errors.New("-shots needs one analysis report per measurement")
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes vmafsim with args and returns the process exit code.
func run(
	args []string,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("vmafsim", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: vmafsim [-runs N] [-precisions 0.25,0.5,1] [-samples 5%,1/scene] [-shots analysis.json,...] exact.json...")
		flags.PrintDefaults()
	}

	runs := flags.Int("runs", 500, "simulated measurements per configuration")
	precisionList := flags.String("precisions", "0.25,0.5,1", "comma-separated target half-widths of the 95% interval (empty: none)")
	sampleList := flags.String("samples", "", "comma-separated fixed budgets: shares of frames (5%) or clips per scene (1/scene)")
	shotList := flags.String("shots", "", "comma-separated technical analysis reports (qc analyze -o), one per measurement: their shots are the scenes of the budgets")
	seriesList := flags.String("series", "", "comma-separated metric series to check too (xpsnr_y, psnr_y, vmaf_phone...), or all")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	configs, err := parseConfigs(*precisionList, *sampleList)
	if err == nil && *runs <= 0 {
		err = fmt.Errorf("-runs must be positive, got %d", *runs)
	}

	shots := splitList(*shotList)
	if err == nil && len(shots) > 0 && len(shots) != flags.NArg() {
		err = ErrShotsCount
	}

	if err != nil || flags.NArg() == 0 {
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
		}

		flags.Usage()

		return 2
	}

	if err := simulateAll(stdout, flags.Args(), shots, *runs, configs, splitList(*seriesList)); err != nil {
		fmt.Fprintln(stderr, "error:", err)

		return 1
	}

	return 0
}

// config is one sampling configuration to replay: a precision or a budget.
type config struct {
	label string
	opts  quality.Options
}

// parseConfigs reads the precisions and budgets to replay.
func parseConfigs(
	precisionList, sampleList string,
) ([]config, error) {
	var configs []config

	if strings.TrimSpace(precisionList) != "" {
		precisions, err := parseFloats(precisionList)
		if err != nil {
			return nil, err
		}

		for _, p := range precisions {
			configs = append(configs, config{label: fmt.Sprintf("±%.2f", p), opts: quality.Options{Precision: p}})
		}
	}

	for _, field := range splitList(sampleList) {
		sample, err := quality.ParseSample(field)
		if err != nil {
			return nil, fmt.Errorf("invalid budget: %w", err)
		}

		configs = append(configs, config{label: sample.String(), opts: quality.Options{Sample: sample}})
	}

	if len(configs) == 0 {
		return nil, ErrNoConfig
	}

	return configs, nil
}

// simulateAll prints one table row per file and configuration, then one per
// file, configuration and requested series. shots, when set, holds one
// analysis report per path whose shots cut the scenes of the budgets.
func simulateAll(
	w io.Writer,
	paths, shots []string,
	runs int,
	configs []config,
	seriesNames []string,
) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "file\tmode\tframes\tstrata\ttrue\tRMSE\tmean |err|\tmean ±\tcoverage\tsampled cov.\tshare\tfallbacks\t")

	var rows []string

	for i, path := range paths {
		m, err := readMeasurement(path, seriesNames)
		if err != nil {
			return err
		}

		var cuts []media.Duration
		if len(shots) > 0 {
			if cuts, err = readCuts(shots[i]); err != nil {
				return err
			}
		}

		for _, c := range configs {
			opts := c.opts
			opts.Cuts = cuts

			sim, others := quality.SimulateSeries(m.scores, m.series, m.pts, m.keyframes, opts, runs)

			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%.3f\t%.3f\t%.3f\t%.3f\t%.1f%%\t%.1f%%\t%.1f%%\t%d\t\n",
				shortName(path), c.label, len(m.scores), sim.Strata, sim.True, sim.RMSE, sim.MeanAbsErr, sim.MeanHalf,
				sim.Coverage*100, sim.SampledCoverage*100, sim.MeanShare*100, sim.Fallbacks)

			for _, name := range slices.Sorted(maps.Keys(others)) {
				s := others[name]
				rows = append(rows, fmt.Sprintf("%s\t%s\t%s\t%.4f\t%.4f\t%.4f\t%.1f%%\t%.1f%%\t",
					shortName(path), c.label, name, s.True, s.RMSE, s.MeanHalf, s.Coverage*100, s.SampledCoverage*100))
			}
		}
	}

	if len(rows) > 0 {
		fmt.Fprintln(tw)
		fmt.Fprintln(tw, "file\tmode\tseries\ttrue\tRMSE\tmean ±\tcoverage\tsampled cov.\t")

		for _, row := range rows {
			fmt.Fprintln(tw, row)
		}
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	return nil
}

// measurement is the ground truth read from an exact VMAF report.
type measurement struct {
	scores    []float64
	series    map[string][]float64
	pts       []media.Duration
	keyframes []media.Duration
}

// readMeasurement reads the per-frame scores, the requested series ("all":
// every series of the report) and the distorted keyframes of an exact
// comparison report.
func readMeasurement(
	path string,
	seriesNames []string,
) (measurement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return measurement{}, fmt.Errorf("read %s: %w", path, err)
	}

	var cmp analysis.Comparison
	if err := json.Unmarshal(data, &cmp); err != nil {
		return measurement{}, fmt.Errorf("decode %s: %w", path, err)
	}

	if cmp.VMAF == nil || cmp.VMAF.Mode != quality.ModeExact || len(cmp.VMAF.Frames) == 0 {
		return measurement{}, fmt.Errorf("%s: %w", path, ErrNotExact)
	}

	m := measurement{
		scores: make([]float64, len(cmp.VMAF.Frames)),
		series: map[string][]float64{},
		pts:    make([]media.Duration, len(cmp.VMAF.Frames)),
	}

	for i, f := range cmp.VMAF.Frames {
		m.scores[i], m.pts[i] = f.Score, f.PTS
	}

	if slices.Equal(seriesNames, []string{"all"}) {
		seriesNames = slices.Sorted(maps.Keys(cmp.VMAF.Frames[0].Metrics))
	}

	for _, name := range seriesNames {
		values, ok := quality.RawSeries(cmp.VMAF, name)
		if !ok || len(values) != len(m.scores) {
			return measurement{}, fmt.Errorf("%s: %w %q", path, ErrNoSeries, name)
		}

		m.series[name] = values
	}

	if cmp.Distorted != nil && cmp.Distorted.Bitstream != nil {
		m.keyframes = cmp.Distorted.Bitstream.Keyframes
	}

	return m, nil
}

// readCuts reads the shot cuts of a technical analysis report: the start of
// every shot but the first.
func readCuts(
	path string,
) ([]media.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var report analysis.Report
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	if report.Video == nil || len(report.Video.Shots) == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrNoShots)
	}

	cuts := make([]media.Duration, 0, len(report.Video.Shots)-1)
	for _, shot := range report.Video.Shots[1:] {
		cuts = append(cuts, shot.Start)
	}

	return cuts, nil
}

// splitList splits a comma-separated list, dropping empty fields.
func splitList(
	s string,
) []string {
	var out []string

	for field := range strings.SplitSeq(s, ",") {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}

	return out
}

// shortName keeps the end of long paths so that the table stays narrow.
func shortName(
	path string,
) string {
	if r := []rune(path); len(r) > 28 {
		return "…" + string(r[len(r)-27:])
	}

	return path
}

// parseFloats parses a comma-separated list of positive numbers.
func parseFloats(
	s string,
) ([]float64, error) {
	var out []float64

	for field := range strings.SplitSeq(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("invalid precision %q: want a positive number", field)
		}

		out = append(out, v)
	}

	return out, nil
}
