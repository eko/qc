// Command ladderreplay replays the ladder engine on an exhaustive
// rate-quality grid instead of encoding: every encode the engine asks for
// is answered from the grid (interpolated in CRF), then the rungs are
// checked against the grid's own optimum. A build takes milliseconds, so
// probing modes and engine changes can be compared on several titles'
// grids, with the sampling error of real measurements added on demand
// (-bias, a level shared by the sampled measurements; -noise, per
// measurement).
//
// The grids are ladderval's measurement caches (-cache), or files of
// "codec height crf bitrate vmaf" lines.
//
//	go run ./bench/ladderreplay [-codec h264] [-probing fixed,adaptive] [-top-vmaf 95]
//		[-bias -0.8] [-noise 0.5] [-runs 20] grid.json...
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var (
	// ErrNoGrid is returned for a grid without any measurement of a height.
	ErrNoGrid = errors.New("no grid measurement")
	// ErrChunks is returned for chunked encodes (per-shot rungs), which a
	// grid of whole encodes cannot answer.
	ErrChunks = errors.New("chunked encodes are not replayed")
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options are the command-line settings.
type options struct {
	codec   string
	probing []ladder.Probing
	topVMAF float64
	bias    float64
	noise   float64
	runs    int
	verbose bool
	paths   []string
}

// run executes ladderreplay with args and returns the process exit code.
func run(
	args []string,
	stdout, stderr io.Writer,
) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return 2
	}

	for _, path := range opts.paths {
		if err := replayFile(stdout, path, opts); err != nil {
			fmt.Fprintln(stderr, "error:", err)

			return 1
		}
	}

	return 0
}

// parseArgs reads the command line.
func parseArgs(
	args []string,
	stderr io.Writer,
) (options, error) {
	flags := flag.NewFlagSet("ladderreplay", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var opts options

	probing := flags.String("probing", "fixed,adaptive", "probing modes to replay")
	flags.StringVar(&opts.codec, "codec", "", "codec of line grids (h264, hevc, av1); caches name theirs")
	flags.Float64Var(&opts.topVMAF, "top-vmaf", 95, "quality of the top rung")
	flags.Float64Var(&opts.bias, "bias", 0, "VMAF added to every sampled measurement (the shared error of the sampled frames)")
	flags.Float64Var(&opts.noise, "noise", 0, "standard deviation of the VMAF error of each sampled measurement")
	flags.IntVar(&opts.runs, "runs", 1, "replays per mode (with -noise, each with its own draws)")
	flags.BoolVar(&opts.verbose, "v", false, "list the rungs of the first replay of each mode")

	if err := flags.Parse(args); err != nil {
		return options{}, err //nolint:wrapcheck // the flag package reports it
	}

	for _, p := range strings.Split(*probing, ",") {
		mode, err := ladder.ParseProbing(strings.TrimSpace(p))
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)

			return options{}, err
		}

		opts.probing = append(opts.probing, mode)
	}

	opts.paths = flags.Args()
	if len(opts.paths) == 0 {
		fmt.Fprintln(stderr, "usage: ladderreplay [flags] grid...")
		flags.PrintDefaults()

		return options{}, flag.ErrHelp
	}

	return opts, nil
}

// replayFile replays every probing mode on the grid at path.
func replayFile(
	w io.Writer,
	path string,
	opts options,
) error {
	g, err := loadGrid(path, opts.codec)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "== %s (%s, top VMAF %.0f, bias %+.2f, noise %.2f)\n", path, g.codec, opts.topVMAF, opts.bias, opts.noise)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "probing\tencodes\tmean overhead\tworst overhead\ttop VMAF\toff-optimum rungs\toutside the grid\t")

	for _, mode := range opts.probing {
		s, err := replayMode(w, g, mode, opts)
		if err != nil {
			return err
		}

		fmt.Fprintf(tw, "%s\t%.1f\t%+.1f%%\t%+.1f%%\t%.2f\t%.1f\t%.1f\t\n", mode, s.encodes, 100*s.meanOverhead, 100*s.worstOverhead, s.topVMAF, s.offOptimum, s.unchecked)
	}

	return tw.Flush() //nolint:wrapcheck // a write to w
}

// summary averages the checks of the replays of one mode.
type summary struct {
	encodes, meanOverhead, worstOverhead, topVMAF, offOptimum, unchecked float64
}

// replayMode builds opts.runs ladders in mode and averages their checks.
func replayMode(
	w io.Writer,
	g *grid,
	mode ladder.Probing,
	opts options,
) (summary, error) {
	var s summary

	for run := range opts.runs {
		lab := newLab(g, opts.bias, opts.noise, uint64(run+1))
		engine := ladder.NewEngine(lab, lab, lab)

		res, err := engine.Build(context.Background(), sourcePath, ladder.Options{
			Codec: g.codec, Probing: mode, Heights: g.heights(),
			Constraints: ladder.Constraints{TopVMAF: opts.topVMAF}, WorkDir: os.TempDir(),
		})
		if err != nil {
			return summary{}, fmt.Errorf("%s: %w", mode, err)
		}

		if opts.verbose && run == 0 {
			g.describe(w, mode, res.Rungs)
		}

		c := g.check(res.Rungs)
		s.encodes += float64(lab.encodes)
		s.meanOverhead += c.meanOverhead
		s.worstOverhead += c.worstOverhead
		s.topVMAF += c.topVMAF
		s.offOptimum += float64(c.offOptimum)
		s.unchecked += float64(c.unchecked)
	}

	n := float64(opts.runs)

	return summary{s.encodes / n, s.meanOverhead / n, s.worstOverhead / n, s.topVMAF / n, s.offOptimum / n, s.unchecked / n}, nil
}

// point is one grid measurement.
type point struct {
	crf, bitrate, vmaf float64
}

// grid holds the measurements of every height, by increasing CRF.
type grid struct {
	codec  string
	width  map[int]int
	points map[int][]point
}

// cacheFile is ladderval's measurement cache.
type cacheFile struct {
	Codec   string                  `json:"codec"`
	Entries map[string]ladder.Probe `json:"entries"`
}

// loadGrid reads a ladderval cache, or a file of "codec height crf bitrate
// vmaf" lines of codec.
func loadGrid(
	path, codec string,
) (*grid, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read grid: %w", err)
	}

	g := &grid{codec: codec, width: map[int]int{}, points: map[int][]point{}}

	var cache cacheFile
	if json.Unmarshal(data, &cache) == nil && len(cache.Entries) > 0 {
		g.codec = cache.Codec

		for key, p := range cache.Entries {
			// Only the uncapped grid: rung checks are VBV-capped.
			if strings.Contains(key, "/max0/") {
				g.add(p.Width, p.Height, p.CRF, float64(p.Bitrate), p.VMAF)
			}
		}
	} else if err := g.readLines(data); err != nil {
		return nil, err
	}

	for h := range g.points {
		slices.SortFunc(g.points[h], func(a, b point) int { return cmpFloat(a.crf, b.crf) })
	}

	if len(g.points) == 0 {
		return nil, fmt.Errorf("%s: %w", path, ErrNoGrid)
	}

	return g, nil
}

// readLines reads "codec height crf bitrate vmaf" lines of g's codec.
func (g *grid) readLines(
	data []byte,
) error {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) != 5 || f[0] != g.codec {
			continue
		}

		h, err1 := strconv.Atoi(f[1])
		crf, err2 := strconv.ParseFloat(f[2], 64)
		bitrate, err3 := strconv.ParseFloat(f[3], 64)
		vmaf, err4 := strconv.ParseFloat(f[4], 64)

		if err := errors.Join(err1, err2, err3, err4); err != nil {
			return fmt.Errorf("grid line %q: %w", scanner.Text(), err)
		}

		// Widths of a 16:9 source, even.
		g.add(int(math.Round(float64(h)*16/9/2))*2, h, crf, bitrate, vmaf)
	}

	return scanner.Err() //nolint:wrapcheck // reading a string
}

func (g *grid) add(
	width, height int,
	crf, bitrate, vmaf float64,
) {
	g.width[height] = width
	g.points[height] = append(g.points[height], point{crf, bitrate, vmaf})
}

// heights are the grid's heights, highest first.
func (g *grid) heights() []int {
	out := make([]int, 0, len(g.points))
	for h := range g.points {
		out = append(out, h)
	}

	slices.Sort(out)
	slices.Reverse(out)

	return out
}

// at is the bitrate and VMAF of an encode at height and crf: linear in CRF
// between grid points (log bitrate), extended along the edge segments.
func (g *grid) at(
	height int,
	crf float64,
) (float64, float64, error) {
	ps := g.points[height]
	if len(ps) < 2 {
		return 0, 0, fmt.Errorf("%dp: %w", height, ErrNoGrid)
	}

	i := 1
	for i < len(ps)-1 && ps[i].crf < crf {
		i++
	}

	a, b := ps[i-1], ps[i]
	t := (crf - a.crf) / (b.crf - a.crf)
	bitrate := math.Exp(math.Log(a.bitrate) + t*(math.Log(b.bitrate)-math.Log(a.bitrate)))

	return bitrate, min(100, a.vmaf+t*(b.vmaf-a.vmaf)), nil
}

// covers reports whether crf lies within the grid of height.
func (g *grid) covers(
	height int,
	crf float64,
) bool {
	ps := g.points[height]

	return len(ps) > 1 && crf >= ps[0].crf && crf <= ps[len(ps)-1].crf
}

// vmafAt is the grid VMAF of height at bitrate, false outside its range.
func (g *grid) vmafAt(
	height int,
	bitrate float64,
) (float64, bool) {
	ps := g.points[height]

	for i := 1; i < len(ps); i++ {
		hi, lo := ps[i-1], ps[i]
		if bitrate <= hi.bitrate && bitrate >= lo.bitrate {
			t := (math.Log(bitrate) - math.Log(lo.bitrate)) / (math.Log(hi.bitrate) - math.Log(lo.bitrate))

			return lo.vmaf + t*(hi.vmaf-lo.vmaf), true
		}
	}

	return 0, false
}

// envelope is the best grid VMAF at bitrate over every height.
func (g *grid) envelope(
	bitrate float64,
) (float64, int) {
	best, height := math.Inf(-1), 0

	for h := range g.points {
		if v, ok := g.vmafAt(h, bitrate); ok && v > best {
			best, height = v, h
		}
	}

	return best, height
}

// bitrateFor is the lowest bitrate at which the envelope reaches vmaf,
// within the grid's bitrates (its highest when the grid never reaches it).
func (g *grid) bitrateFor(
	vmaf float64,
) float64 {
	lowest, highest := math.Inf(1), 0.0

	for _, ps := range g.points {
		for _, p := range ps {
			lowest, highest = min(lowest, p.bitrate), max(highest, p.bitrate)
		}
	}

	lo, hi := math.Log(lowest), math.Log(highest)

	for range 80 {
		mid := (lo + hi) / 2
		if v, _ := g.envelope(math.Exp(mid)); v >= vmaf {
			hi = mid
		} else {
			lo = mid
		}
	}

	return min(math.Exp(hi), highest)
}

// describe lists the rungs of a replay placed on the grid.
func (g *grid) describe(
	w io.Writer,
	mode ladder.Probing,
	rungs []ladder.Rung,
) {
	for _, r := range rungs {
		bitrate, vmaf, _ := g.at(r.Height, r.CRF)
		need := g.bitrateFor(vmaf)
		_, h := g.envelope(need)
		fmt.Fprintf(w, "  %s %4dp crf %4.1f: %6.0f kb/s VMAF %5.2f (predicted %5.2f); optimum %4dp at %6.0f kb/s (%+.1f%%)\n",
			mode, r.Height, r.CRF, bitrate/1000, vmaf, r.PredictedVMAF, h, need/1000, 100*(bitrate/need-1))
	}
}

// checks are the rungs' distances to the grid's optimum.
type checks struct {
	meanOverhead, worstOverhead, topVMAF float64
	// offOptimum counts the rungs whose resolution is not the optimum's at
	// their quality; unchecked those outside the grid, which cannot be
	// placed on it.
	offOptimum, unchecked int
}

// check places every rung on the grid (its resolution and CRF) and measures
// the bitrate it costs over the grid's optimum at the same quality.
func (g *grid) check(
	rungs []ladder.Rung,
) checks {
	var (
		c       checks
		checked int
	)

	for i, r := range rungs {
		bitrate, vmaf, err := g.at(r.Height, r.CRF)
		if err != nil || !g.covers(r.Height, r.CRF) {
			c.unchecked++

			continue
		}

		if i == 0 {
			c.topVMAF = vmaf
		}

		need := g.bitrateFor(vmaf)
		overhead := bitrate/need - 1
		c.meanOverhead += overhead
		c.worstOverhead = max(c.worstOverhead, overhead)
		checked++

		if _, h := g.envelope(need); h != r.Height {
			c.offOptimum++
		}
	}

	c.meanOverhead /= float64(max(checked, 1))

	return c
}

// sourcePath is the replayed title.
const sourcePath = "/title/source.mov"

// replayDuration is the replayed title's duration: a digest of it is the
// title itself (the grid's measurements).
const replayDuration = 40

// lab answers the engine's ports from a grid.
type lab struct {
	grid        *grid
	bias, noise float64

	mu      sync.Mutex
	rng     *rand.Rand
	params  map[string]encode.Params
	encodes int
	digest  *analysis.Report
}

func newLab(
	g *grid,
	bias, noise float64,
	seed uint64,
) *lab {
	return &lab{
		grid: g, bias: bias, noise: noise,
		rng:    rand.New(rand.NewPCG(seed, 0)), //nolint:gosec // simulation draws
		params: map[string]encode.Params{},
		digest: &analysis.Report{Info: &media.Info{Path: "digest", Duration: media.Seconds(replayDuration)}},
	}
}

// Analyze returns the source's inspection, or the digest's.
func (l *lab) Analyze(
	_ context.Context,
	path string,
	_ analysis.Options,
) (*analysis.Report, error) {
	if path != sourcePath {
		return l.digest, nil
	}

	top := l.grid.heights()[0]
	rate := media.Rational{Num: 25, Den: 1}

	return &analysis.Report{Info: &media.Info{
		Path: sourcePath, Duration: media.Seconds(replayDuration),
		Video: []media.VideoStream{{
			Width: l.grid.width[top], Height: top, BitDepth: 8,
			FrameRate: rate, AvgFrameRate: rate, Duration: media.Seconds(replayDuration),
		}},
	}}, nil
}

// Compare answers from the grid, with the bias and noise of a sampled
// measurement unless it is exact.
func (l *lab) Compare(
	_ context.Context,
	_, dist string,
	opts analysis.CompareOptions,
) (*analysis.Comparison, error) {
	l.mu.Lock()
	p := l.params[dist]
	draw := l.rng.NormFloat64()
	l.mu.Unlock()

	bitrate, vmaf, err := l.grid.at(p.Height, p.CRF)
	if err != nil {
		return nil, err
	}

	halfWidth := 0.0
	if !opts.Quality.Exact {
		vmaf += l.bias + l.noise*draw
		halfWidth = 1.96 * l.noise
	}

	return &analysis.Comparison{
		Distorted: &analysis.Report{Bitstream: &bitstream.Report{AverageBitrate: int64(bitrate)}},
		VMAF:      &quality.Result{Mean: vmaf, HalfWidth: halfWidth},
	}, nil
}

// Encode records the settings the comparison of dst answers from.
func (l *lab) Encode(
	_ context.Context,
	_ encode.Codec,
	_, dst string,
	p encode.Params,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.params[dst] = p
	l.encodes++

	return nil
}

// EncodeChunks is not replayed.
func (l *lab) EncodeChunks(
	context.Context,
	encode.Codec,
	encode.ChunkSource,
	string,
	[]encode.Chunk,
	encode.Params,
) error {
	return ErrChunks
}

// Digest does nothing: the grid is the digest.
func (l *lab) Digest(
	context.Context,
	encode.DigestSpec,
) error {
	return nil
}

func cmpFloat(
	a, b float64,
) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}

	return 0
}
