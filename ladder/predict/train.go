package predict

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/eko/qc/internal/linalg"
)

// ErrCorpus is returned for a corpus a model cannot be trained on.
var ErrCorpus = errors.New("invalid ladder corpus")

// Title is a title of a corpus: an excerpt of it cut into segments, each
// measured at every point of a grid.
type Title struct {
	Name          string
	Codec, Preset string
	// SourceHeight is the height of the title, at which it was scored.
	SourceHeight int
	Points       []TitlePoint
	// Features are the features of each segment, by name (see package
	// features); nil for a title measured without them.
	Features map[string][]float64
}

// TitlePoint holds the measurements of the segments of a title at one
// point of the grid.
type TitlePoint struct {
	Point
	Width int
	// Bitrate (bits per second) and VMAF of each segment.
	Bitrate []float64
	VMAF    []float64
}

// segments is the number of segments of the title.
func (t Title) segments() int {
	if len(t.Points) == 0 {
		return 0
	}

	return len(t.Points[0].Bitrate)
}

// at returns the measurements of the title at p.
func (t Title) at(
	p Point,
) (TitlePoint, bool) {
	i := slices.IndexFunc(t.Points, func(tp TitlePoint) bool { return tp.Point == p })
	if i < 0 {
		return TitlePoint{}, false
	}

	return t.Points[i], true
}

// input is what a design of anchors and features reads of the clip made
// of the segments segs: its anchors as a digest of those segments would be
// measured, the features of its segments and their means.
func (t Title) input(
	anchors []Point,
	features []string,
	segs []int,
) Input {
	in := Input{Anchors: make([]Measurement, len(anchors)), Features: make(map[string]float64, len(features))}

	for i, a := range anchors {
		in.Anchors[i] = t.clip(a, segs)
	}

	if len(features) > 0 {
		in.Segments = make(map[string][]float64, len(features))
	}

	for _, name := range features {
		in.Segments[name] = make([]float64, len(segs))

		for i, s := range segs {
			in.Segments[name][i] = t.Features[name][s]
			in.Features[name] += t.Features[name][s] / float64(len(segs))
		}
	}

	in.Width = func(height int) int {
		for _, tp := range t.Points {
			if tp.Height == height {
				return tp.Width
			}
		}

		return 0
	}

	return in
}

// clip is the measurement of the clip made of the segments segs at p: its
// bitrate and its VMAF are the means of theirs, as a digest of those
// segments would be measured.
func (t Title) clip(
	p Point,
	segs []int,
) Measurement {
	tp, _ := t.at(p)
	m := Measurement{Point: p, Width: tp.Width}

	for _, s := range segs {
		m.Bitrate += tp.Bitrate[s] / float64(len(segs))
		m.VMAF += tp.VMAF[s] / float64(len(segs))
	}

	return m
}

// Complete splits the titles of a corpus into those measured on the whole
// grid (the grid of the title with the most points) and those still
// missing points: a corpus being measured holds, at any time, one title
// not finished yet, which a model cannot be trained on.
func Complete(
	titles []Title,
) (complete, partial []Title) {
	var grid []Point

	for _, t := range titles {
		if len(t.Points) > len(grid) {
			grid = grid[:0]
			for _, tp := range t.Points {
				grid = append(grid, tp.Point)
			}
		}
	}

	for _, t := range titles {
		whole := len(t.Points) == len(grid) && !slices.ContainsFunc(grid, func(p Point) bool {
			_, ok := t.at(p)

			return !ok
		})

		if whole {
			complete = append(complete, t)
		} else {
			partial = append(partial, t)
		}
	}

	return complete, partial
}

// TrainOptions tunes Train. The zero value is valid.
type TrainOptions struct {
	// Designs are the sets of anchors to train; DefaultDesigns by default.
	Designs [][]Point
	// LowCRF, HighCRF and FarCRF are the CRFs the default designs measure
	// their anchors at (see DefaultDesigns).
	LowCRF, HighCRF, FarCRF float64
	// Penalty is the ridge penalty of the models, relative to the weight
	// of the corpus. Default 0.01.
	Penalty float64
	// FeaturePenalty is the penalty of the designs reading features: they
	// have many more regressors than titles to tell them apart. Default
	// 0.1.
	FeaturePenalty float64
	// NoFeatures leaves the designs reading features out, even when every
	// title has them.
	NoFeatures bool
	// FeatureSets are the sets of features a design without anchor is
	// fitted on, each for what a build can read of a title (everything,
	// or what needs no encode); of each, the features every title has are
	// kept. By default one set: all the features of the corpus. The design
	// of one anchor and features reads the first.
	FeatureSets [][]string
	// Clips is the number of digests drawn from each title left out by the
	// validation, each of ClipSegments segments. Defaults 40 and 20.
	Clips        int
	ClipSegments int
	// FitSegments is the number of segments of the digests the designs
	// of anchors alone are fitted on (see fit). Default 5; 1 fits them on
	// single segments.
	FitSegments int
}

// Defaults of TrainOptions.
const (
	defaultPenalty        = 0.01
	defaultFeaturePenalty = 0.1
	defaultClips          = 40
	defaultClipSegments   = 20
	defaultFitSegments    = 5
)

// Bounds of the VMAF a ladder uses: errors outside them do not move a
// rung (see Validation.MeanVMAFError).
const (
	ladderFloor   = 30.0
	ladderCeiling = 98.0
)

func (o TrainOptions) withDefaults() TrainOptions {
	if o.Penalty <= 0 {
		o.Penalty = defaultPenalty
	}

	if o.FeaturePenalty <= 0 {
		o.FeaturePenalty = defaultFeaturePenalty
	}

	if o.Clips <= 0 {
		o.Clips = defaultClips
	}

	if o.ClipSegments <= 0 {
		o.ClipSegments = defaultClipSegments
	}

	if o.FitSegments <= 0 {
		o.FitSegments = defaultFitSegments
	}

	return o
}

// DefaultDesigns returns the designs Train fits by default on a grid of
// heights (highest first) and CRFs, from the most anchors to the fewest:
//
//   - two anchors per height: its alternating CRF (see below), and the
//     far CRF for the upper half of the heights, the other CRF for the
//     lower ones;
//   - one anchor per height, the CRF alternating from a height to the
//     next (the top height at the low one), plus the top and the middle
//     heights at the far CRF;
//   - the same with the top height alone at the far CRF;
//   - one anchor per height, alternating;
//   - the top height at the low and the far CRFs, the middle one at the
//     high CRF and the bottom one at the low CRF;
//   - one anchor every other height, alternating;
//   - the top height at the low CRF and the bottom one at the high CRF;
//   - the top height alone.
//
// Alternating matters: a title is told from another by the level of its
// curves and by how fast they fall with the CRF, and anchors all at one CRF
// say nothing of the second. On three real titles, five anchors at
// alternating CRFs predicted the grid of a title left out twice as well as
// five anchors at one CRF. The far CRF is for the top resolutions, whose
// curves a ladder reads furthest: content that compresses well keeps them
// down to its lowest rungs.
//
// low, high and far are the CRFs of the grid nearest to those given; zero
// takes the CRFs three tenths, half and four fifths into the grid.
func DefaultDesigns(
	heights []int,
	crfs []float64,
	low, high, far float64,
) [][]Point {
	if len(heights) == 0 || len(crfs) == 0 {
		return nil
	}

	steps := len(crfs) - 1
	pair := [2]float64{nearest(crfs, low, crfs[steps*3/10]), nearest(crfs, high, crfs[steps/2])}
	far = nearest(crfs, far, crfs[steps*4/5])
	last, middle := len(heights)-1, len(heights)/2

	// alternating is the anchor of height i at its alternating CRF, or
	// at the other one.
	alternating := func(i int, other bool) Point {
		side := i % 2
		if other {
			side = 1 - side
		}

		return Point{Height: heights[i], CRF: pair[side]}
	}

	at := func(i int, crf float64) Point {
		return Point{Height: heights[i], CRF: crf}
	}

	var both, every, alternate []Point

	for i := range heights {
		every = append(every, alternating(i, false))

		if i <= middle {
			both = append(both, alternating(i, false), at(i, far))
		} else {
			both = append(both, alternating(i, false), alternating(i, true))
		}

		if i%2 == 0 {
			alternate = append(alternate, at(i, pair[(i/2)%2]))
		}
	}

	candidates := [][]Point{
		both,
		append(slices.Clone(every), at(0, far), at(middle, far)),
		append(slices.Clone(every), at(0, far)),
		every,
		{at(0, pair[0]), at(0, far), at(middle, pair[1]), at(last, pair[0])},
		alternate,
		{at(0, pair[0]), at(last, pair[1])},
		{at(0, pair[0])},
	}

	return distinctSizes(candidates)
}

// distinctSizes returns the candidates without their repeated anchors,
// one design per number of anchors (the first of each), the most anchors
// first: on a small grid several candidates come to the same anchors.
func distinctSizes(
	candidates [][]Point,
) [][]Point {
	var designs [][]Point

	for _, candidate := range candidates {
		var d []Point

		for _, p := range candidate {
			if !slices.Contains(d, p) {
				d = append(d, p)
			}
		}

		same := func(seen []Point) bool { return len(seen) == len(d) }
		if !slices.ContainsFunc(designs, same) {
			designs = append(designs, d)
		}
	}

	slices.SortStableFunc(designs, func(a, b []Point) int { return cmp.Compare(len(b), len(a)) })

	return designs
}

// nearest returns the value of grid nearest to want, or fallback when
// want is zero.
func nearest(
	grid []float64,
	want, fallback float64,
) float64 {
	if want <= 0 {
		return fallback
	}

	return slices.MinFunc(grid, func(a, b float64) int {
		return cmp.Compare(math.Abs(a-want), math.Abs(b-want))
	})
}

// Train fits a model on the titles of a corpus, which must share their
// encoder setting, their source height and their grid, and validates each
// of its designs by leaving every title out in turn (when there are at
// least two).
func Train(
	titles []Title,
	opts TrainOptions,
) (*Model, error) {
	opts = opts.withDefaults()

	m, err := newModel(titles)
	if err != nil {
		return nil, err
	}

	anchorSets := opts.Designs
	if len(anchorSets) == 0 {
		anchorSets = DefaultDesigns(m.Heights, m.CRFs, opts.LowCRF, opts.HighCRF, opts.FarCRF)
	}

	points := m.Points()

	var plans []plan

	for _, anchors := range anchorSets {
		if err := checkAnchors(anchors, points); err != nil {
			return nil, err
		}

		plans = append(plans, plan{anchors: anchors, penalty: opts.Penalty, segments: opts.FitSegments})
	}

	if !opts.NoFeatures {
		// From the analysis alone, and with the one anchor that tells the
		// most: the top resolution, as the smallest set of anchors has it.
		for i, names := range featureSets(commonFeatures(titles), opts.FeatureSets) {
			plans = append(plans, plan{features: names, penalty: opts.FeaturePenalty, segments: 1})

			if i == 0 {
				plans = append(plans, plan{anchors: anchorSets[len(anchorSets)-1][:1], features: names, penalty: opts.FeaturePenalty, segments: 1})
			}
		}
	}

	for _, pl := range plans {
		d := fit(titles, points, pl)
		d.Validation = validate(m, titles, pl, opts)
		m.Designs = append(m.Designs, d)
	}

	slices.SortStableFunc(m.Designs, func(a, b Design) int { return cmp.Compare(len(b.Anchors), len(a.Anchors)) })

	return m, nil
}

// plan is a design to fit: its anchors, the features it reads, the
// penalty of its models and the number of segments of the digests they
// are fitted on.
type plan struct {
	anchors  []Point
	features []string
	penalty  float64
	segments int
}

// featureSets returns the sets of features to fit designs on: of each of
// sets, the features common holds, without the sets left empty nor those
// already there; common itself when no set is asked.
func featureSets(
	common []string,
	sets [][]string,
) [][]string {
	if len(common) == 0 {
		return nil
	}

	if len(sets) == 0 {
		return [][]string{common}
	}

	var out [][]string

	for _, set := range sets {
		kept := make([]string, 0, len(set))

		for _, name := range common {
			if slices.Contains(set, name) {
				kept = append(kept, name)
			}
		}

		if len(kept) > 0 && !slices.ContainsFunc(out, func(o []string) bool { return slices.Equal(o, kept) }) {
			out = append(out, kept)
		}
	}

	return out
}

// commonFeatures returns the names of the features every title has, for
// each of its segments and finite, sorted; none when a title has no
// features.
func commonFeatures(
	titles []Title,
) []string {
	var names []string

	for name := range titles[0].Features {
		usable := func(t Title) bool {
			values := t.Features[name]

			return len(values) == t.segments() && !slices.ContainsFunc(values, func(v float64) bool {
				return math.IsNaN(v) || math.IsInf(v, 0)
			})
		}

		if !slices.ContainsFunc(titles, func(t Title) bool { return !usable(t) }) {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

// newModel describes the corpus: its encoder setting and its grid, which
// every title must share.
func newModel(
	titles []Title,
) (*Model, error) {
	if len(titles) == 0 {
		return nil, fmt.Errorf("%w: no title", ErrCorpus)
	}

	first := titles[0]
	m := &Model{SchemaVersion: SchemaVersion, Codec: first.Codec, Preset: first.Preset, SourceHeight: first.SourceHeight, Titles: len(titles)}

	for _, p := range first.Points {
		if !slices.Contains(m.Heights, p.Height) {
			m.Heights = append(m.Heights, p.Height)
		}

		if !slices.Contains(m.CRFs, p.CRF) {
			m.CRFs = append(m.CRFs, p.CRF)
		}
	}

	slices.SortFunc(m.Heights, func(a, b int) int { return cmp.Compare(b, a) })
	slices.Sort(m.CRFs)

	for _, t := range titles {
		if t.Codec != m.Codec || t.Preset != m.Preset || t.SourceHeight != m.SourceHeight {
			return nil, fmt.Errorf("%w: %s is %s %s from %dp, %s is %s %s from %dp", ErrCorpus,
				t.Name, t.Codec, t.Preset, t.SourceHeight, first.Name, m.Codec, m.Preset, m.SourceHeight)
		}

		if err := checkTitle(t, m.Points()); err != nil {
			return nil, err
		}

		m.Segments += t.segments()
	}

	return m, nil
}

// checkTitle checks that t holds usable measurements of every segment at
// every point of the grid, and no other point.
func checkTitle(
	t Title,
	points []Point,
) error {
	if t.segments() == 0 || len(t.Points) != len(points) {
		return fmt.Errorf("%w: %s has %d points and %d segments for a grid of %d points", ErrCorpus, t.Name, len(t.Points), t.segments(), len(points))
	}

	for _, p := range points {
		tp, ok := t.at(p)
		if !ok || tp.Width <= 0 || len(tp.Bitrate) != t.segments() || len(tp.VMAF) != t.segments() {
			return fmt.Errorf("%w: %s lacks %dp CRF %g, or segments of it", ErrCorpus, t.Name, p.Height, p.CRF)
		}

		if slices.ContainsFunc(tp.Bitrate, func(b float64) bool { return b <= 0 }) {
			return fmt.Errorf("%w: %s has an empty segment at %dp CRF %g", ErrCorpus, t.Name, p.Height, p.CRF)
		}
	}

	return nil
}

func checkAnchors(
	anchors, points []Point,
) error {
	if len(anchors) == 0 {
		return fmt.Errorf("%w: a design of anchors needs an anchor", ErrCorpus)
	}

	for _, a := range anchors {
		if !slices.Contains(points, a) {
			return fmt.Errorf("%w: anchor %dp CRF %g is not on the grid", ErrCorpus, a.Height, a.CRF)
		}
	}

	return nil
}

// fit trains the design pl plans on titles: for every point of the grid,
// the ridge regressions of the bitrate and of the VMAF there on the
// measurements at the anchors and on the features. Every title weighs the
// same, whatever its number of segments.
//
// A design is fitted on what it is applied to. One of anchors alone reads
// the anchors of a digest, measured as one video: it is fitted on digests
// of a few segments (see digests), which on 39 real titles brought the
// error of four anchors from 0.62 to 0.47 VMAF. One reading features is
// fitted on single segments, and predicts every segment of a digest (see
// Input.Segments).
func fit(
	titles []Title,
	points []Point,
	pl plan,
) Design {
	var (
		xs [][]float64
		ws []float64
	)

	d := Design{Anchors: slices.Clone(pl.anchors), Features: slices.Clone(pl.features)}

	groups := make([][][]int, len(titles))

	for i, t := range titles {
		// The same draws at every training: a model must not move with
		// the run.
		rng := rand.New(rand.NewPCG(uint64(t.segments()), uint64(pl.segments))) //nolint:gosec // reproducible draws, nothing secret
		groups[i] = digests(t.segments(), pl.segments, rng)

		for _, segs := range groups[i] {
			x, _ := regressors(d, t.input(pl.anchors, pl.features, segs))
			xs = append(xs, x)
			ws = append(ws, 1/float64(len(groups[i])))
		}
	}

	scale := standardise(xs, ws)
	d.Rate, d.VMAF = make([][]float64, len(points)), make([][]float64, len(points))

	total := 0.0
	for _, w := range ws {
		total += w
	}

	for i, p := range points {
		rates, vmafs := make([]float64, 0, len(xs)), make([]float64, 0, len(xs))

		for j, t := range titles {
			for _, segs := range groups[j] {
				measured := t.clip(p, segs)
				rates = append(rates, math.Log(measured.Bitrate/float64(measured.Width*p.Height)))
				vmafs = append(vmafs, link(measured.VMAF))
			}
		}

		d.Rate[i] = scale.raw(linalg.Ridge(xs, rates, ws, pl.penalty*total))
		d.VMAF[i] = scale.raw(linalg.Ridge(xs, vmafs, ws, pl.penalty*total))
	}

	return d
}

// fitDraws is the number of times the segments of a title are dealt into
// digests for a fit.
const fitDraws = 6

// digests deals the n segments of a title into digests of size segments,
// fitDraws times over: what a design is fitted on. A digest never takes
// more than half of a title, which then gives two different ones at each
// draw; a size of one gives its segments, each alone.
func digests(
	n, size int,
	rng *rand.Rand,
) [][]int {
	size = min(size, n/2)
	if size <= 1 {
		out := make([][]int, n)
		for s := range out {
			out[s] = []int{s}
		}

		return out
	}

	var out [][]int

	for range fitDraws {
		order := rng.Perm(n)
		for start := 0; start+size <= n; start += size {
			group := slices.Clone(order[start : start+size])
			slices.Sort(group)
			out = append(out, group)
		}
	}

	return out
}

// scaling is the standardisation of the regressors of a fit: the penalty
// of a ridge regression only means something on regressors of one scale.
type scaling struct {
	mean, deviation []float64
}

// standardise centres and scales, in place, every column of xs but the
// constant (weights ws) and returns the scaling applied.
func standardise(
	xs [][]float64,
	ws []float64,
) scaling {
	n := len(xs[0])
	s := scaling{mean: make([]float64, n), deviation: make([]float64, n)}

	total := 0.0
	for _, w := range ws {
		total += w
	}

	for j := 1; j < n; j++ {
		for r, x := range xs {
			s.mean[j] += ws[r] * x[j] / total
		}

		for r, x := range xs {
			s.deviation[j] += ws[r] * (x[j] - s.mean[j]) * (x[j] - s.mean[j]) / total
		}

		if s.deviation[j] = math.Sqrt(s.deviation[j]); s.deviation[j] == 0 {
			s.deviation[j] = 1
		}

		for _, x := range xs {
			x[j] = (x[j] - s.mean[j]) / s.deviation[j]
		}
	}

	return s
}

// raw turns coefficients on standardised regressors into coefficients on
// the regressors as measured.
func (s scaling) raw(
	coefficients []float64,
) []float64 {
	out := slices.Clone(coefficients)

	for j := 1; j < len(out); j++ {
		out[j] /= s.deviation[j]
		out[0] -= out[j] * s.mean[j]
	}

	return out
}

// validate leaves each title out in turn: the design pl plans, fitted on
// the others, predicts digests drawn from it (the whole excerpt, then opts.Clips draws
// of opts.ClipSegments segments, the same for every design), each digest
// measured at its anchors as a ladder build would measure it. It returns
// nil for a corpus of one title.
func validate(
	m *Model,
	titles []Title,
	pl plan,
	opts TrainOptions,
) *Validation {
	if len(titles) < 2 {
		return nil
	}

	points := m.Points()
	vmafErrors, rateErrors := make([][]float64, len(points)), make([][]float64, len(points))
	v := &Validation{}

	var inRange, inRangeRate []float64

	for held, t := range titles {
		others := slices.Concat(titles[:held:held], titles[held+1:])
		d := fit(others, points, pl)
		// The same draws at every training: a validation must not move
		// with the run.
		rng := rand.New(rand.NewPCG(uint64(held)+1, uint64(t.segments()))) //nolint:gosec // reproducible draws, nothing secret
		first := len(inRange)

		for _, segs := range clips(t.segments(), opts, rng) {
			predicted, err := m.Predict(d, t.input(pl.anchors, pl.features, segs))
			if err != nil {
				continue
			}

			v.Clips++

			for i, p := range points {
				truth := t.clip(p, segs)
				vmafError := math.Abs(predicted[i].VMAF - truth.VMAF)
				rateError := math.Abs(predicted[i].Bitrate/truth.Bitrate - 1)

				vmafErrors[i], rateErrors[i] = append(vmafErrors[i], vmafError), append(rateErrors[i], rateError)

				if truth.VMAF >= ladderFloor && truth.VMAF <= ladderCeiling {
					inRange, inRangeRate = append(inRange, vmafError), append(inRangeRate, rateError)
				}
			}
		}

		v.Titles = append(v.Titles, TitleValidation{Name: t.Name, VMAFError: mean(inRange[first:]), BitrateError: mean(inRangeRate[first:])})
	}

	for i := range points {
		v.VMAFError = append(v.VMAFError, percentile(vmafErrors[i], validationPercentile))
		v.BitrateError = append(v.BitrateError, percentile(rateErrors[i], validationPercentile))
	}

	v.MeanVMAFError, v.MeanBitrateError = mean(inRange), mean(inRangeRate)
	if len(inRange) > 0 {
		v.WorstVMAFError = slices.Max(inRange)
	}

	return v
}

// validationPercentile is the share of the validation errors a point's
// reported error covers.
const validationPercentile = 0.95

// clips returns the digests the validation draws from a title of n
// segments: the whole excerpt, then draws without replacement.
func clips(
	n int,
	opts TrainOptions,
	rng *rand.Rand,
) [][]int {
	whole := make([]int, n)
	for i := range whole {
		whole[i] = i
	}

	out := [][]int{whole}
	size := min(opts.ClipSegments, n)

	for range opts.Clips {
		draw := rng.Perm(n)[:size]
		slices.Sort(draw)
		out = append(out, draw)
	}

	return out
}

func mean(
	values []float64,
) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// percentile returns the q-th quantile of values (nearest rank).
func percentile(
	values []float64,
	q float64,
) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := slices.Clone(values)
	slices.Sort(sorted)

	return sorted[min(max(int(math.Ceil(q*float64(len(sorted))))-1, 0), len(sorted)-1)]
}
