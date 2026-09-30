package ladder

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/grain"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var errFake = errors.New("fake failure")

// Operations of the fake lab, as seen by failOn.
const (
	opAnalyze = "analyze"
	opCompare = "compare"
	opDigest  = "digest"
	opEncode  = "encode"
	opDecode  = "decode"
	opNoise   = "noise"
)

// qualityModel predicts the bitrate and VMAF of an encode.
type qualityModel interface {
	bitrate(p encode.Params) int64
	vmaf(p encode.Params) float64
}

// rateModel is a parametric rate-quality model standing in for real
// encodes. The bitrate halves every ~6.3 CRF and grows with the pixel count;
// the quality saturates at a ceiling that drops with resolution, so low
// resolutions win at low bitrates and high ones at high bitrates.
type rateModel struct {
	// knee scales the bitrate needed to approach the ceiling (default 0.4):
	// a larger knee is harder content.
	knee float64
	// cappedBias is added to the VMAF of VBV-capped encodes (verification),
	// to trigger calibration.
	cappedBias float64
	// zeroBitrate makes every encode report no bitrate.
	zeroBitrate bool
}

func (m rateModel) scale(
	height int,
) float64 {
	return 6e6 * math.Pow(float64(height)/1080, 2)
}

func (m rateModel) ceiling(
	height int,
) float64 {
	return math.Min(100, 99-13*math.Log2(1080/float64(height)))
}

func (m rateModel) bitrate(
	p encode.Params,
) int64 {
	if m.zeroBitrate {
		return 0
	}

	bitrate := m.scale(p.Height) * math.Exp(-0.11*(p.CRF-23))
	if p.MaxRate > 0 {
		bitrate = math.Min(bitrate, float64(p.MaxRate))
	}

	return int64(bitrate)
}

func (m rateModel) vmaf(
	p encode.Params,
) float64 {
	knee := m.knee
	if knee == 0 {
		knee = 0.4
	}

	v := m.ceiling(p.Height) * (1 - math.Exp(-float64(m.bitrate(p))/(knee*m.scale(p.Height))))
	if p.MaxRate > 0 {
		v += m.cappedBias
	}

	return v
}

// fakeLab implements every port of the engine (Inspector, Encoder, Digester
// and GrainLab) over a rateModel: encodes record their settings,
// comparisons read them back through the model.
type fakeLab struct {
	model  qualityModel
	source *analysis.Report
	// failOn makes an operation fail. target is the base name of the file
	// the operation works on, seen the number of earlier calls of op on it.
	failOn func(op, target string, seen int) bool

	mu      sync.Mutex
	seen    map[string]int
	encodes map[string]encode.Params
	// chunks holds the chunks of chunked encodes, by output path.
	chunks map[string][]encode.Chunk
	// chunkSources holds the sources of chunked encodes.
	chunkSources []encode.ChunkSource
	// frames is the digest's frame count.
	frames int
	// grain is the noise standard deviation of the source at its height.
	grain float64
	// sampledBias is added to the VMAF of sampled measurements: the error
	// of the sampled frames, shared by every measurement (see Level).
	sampledBias float64
	params      []encode.Params
	digests     []encode.DigestSpec
	digest      *analysis.Report
	// compared records the quality options of every comparison.
	compared []quality.Options
}

func newFakeLab(
	model qualityModel,
	source *analysis.Report,
) *fakeLab {
	return &fakeLab{
		model:   model,
		source:  source,
		seen:    map[string]int{},
		encodes: map[string]encode.Params{},
		chunks:  map[string][]encode.Chunk{},
		digest:  &analysis.Report{Info: &media.Info{Path: "digest"}},
	}
}

// check counts the call and returns errFake when failOn says so.
func (l *fakeLab) check(
	op, path string,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	target := filepath.Base(path)
	key := op + "/" + target
	seen := l.seen[key]
	l.seen[key]++

	if l.failOn != nil && l.failOn(op, target, seen) {
		return errFake
	}

	return nil
}

func (l *fakeLab) Analyze(
	_ context.Context,
	path string,
	_ analysis.Options,
) (*analysis.Report, error) {
	if err := l.check(opAnalyze, path); err != nil {
		return nil, err
	}

	if filepath.Base(path) == "source.mov" {
		return l.source, nil
	}

	l.mu.Lock()
	p, encoded := l.encodes[path]
	l.mu.Unlock()

	if encoded && filepath.Ext(path) == ".mp4" {
		return &analysis.Report{Bitstream: &bitstream.Report{AverageBitrate: l.model.bitrate(p)}}, nil
	}

	return l.digest, nil
}

func (l *fakeLab) Compare(
	_ context.Context,
	_, distPath string,
	opts analysis.CompareOptions,
) (*analysis.Comparison, error) {
	if err := l.check(opCompare, distPath); err != nil {
		return nil, err
	}

	sampled := opts.Quality.Budget && opts.Quality.MaxShare == probeShare
	rendition := opts.Reference == l.source

	if !rendition && (!l.isDigest(opts.Reference) || (!sampled && !opts.Quality.Exact)) {
		return nil, errors.New("fake: measurement not against the inspected digest in budget or exact mode")
	}

	l.mu.Lock()
	p, ok := l.encodes[distPath]
	chunks := l.chunks[distPath]
	l.compared = append(l.compared, opts.Quality)
	l.mu.Unlock()

	if !ok {
		return nil, errors.New("fake: compare before encode")
	}

	// Chunked encodes (per-shot) are scored frame by frame; the others,
	// sampled or exact, through the model.
	if chunks != nil && !rendition {
		return l.perFrame(p, chunks, opts.Quality.Exact), nil
	}

	vmaf := l.model.vmaf(p)
	if !opts.Quality.Exact {
		vmaf += l.sampledBias
	}

	return &analysis.Comparison{
		Distorted: &analysis.Report{Bitstream: &bitstream.Report{AverageBitrate: l.model.bitrate(p)}},
		VMAF:      withExtras(&quality.Result{Mean: vmaf, HalfWidth: opts.Quality.Precision / 2}, opts.Quality),
	}, nil
}

// isDigest reports whether report is the inspection of the digest, or its
// copy carrying the colour of an HDR source (withSignal).
func (l *fakeLab) isDigest(
	report *analysis.Report,
) bool {
	return report == l.digest || (report != nil && report.Info != nil && l.digest.Info != nil && report.Info.Path == l.digest.Info.Path)
}

// withExtras adds the requested metrics and devices to res: XPSNR follows
// VMAF, CAMBI shows banding below VMAF 50, a phone scores 3 points higher.
func withExtras(
	res *quality.Result,
	q quality.Options,
) *quality.Result {
	for _, m := range q.Metrics {
		switch m {
		case quality.MetricXPSNR:
			res.Metrics = append(res.Metrics, quality.MetricResult{Name: quality.SeriesXPSNRY, Estimate: quality.Estimate{Mean: 20 + res.Mean/4}})
		case quality.MetricCAMBI:
			banded := 0
			if res.Mean < 50 {
				banded = 10
			}

			res.FramesScored = 100

			res.Metrics = append(res.Metrics, quality.MetricResult{Name: quality.SeriesCAMBI, Estimate: quality.Estimate{Mean: 1}})
			res.Banding = &quality.Banding{Threshold: quality.BandingThreshold, BandedFrames: banded}
		}
	}

	for _, d := range q.Devices {
		res.Devices = append(res.Devices, quality.DeviceResult{Device: d, Estimate: quality.Estimate{Mean: res.Mean + 3}})
	}

	return res
}

func (l *fakeLab) Encode(
	_ context.Context,
	_ encode.Codec,
	_, dst string,
	p encode.Params,
) error {
	if err := l.check(opEncode, dst); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.encodes[dst] = p
	l.params = append(l.params, p)

	return nil
}

// EncodeRendition records a rendition like Encode and reports every frame
// of the title written.
func (l *fakeLab) EncodeRendition(
	ctx context.Context,
	codec encode.Codec,
	spec encode.RenditionSpec,
) error {
	if err := l.Encode(ctx, codec, spec.Source.Path, spec.Destination, spec.Params); err != nil {
		return err
	}

	if spec.Chunks != nil {
		l.mu.Lock()
		l.chunks[spec.Destination] = spec.Chunks
		l.mu.Unlock()
	}

	if spec.Progress != nil {
		video, _ := l.source.Info.PrimaryVideo()
		spec.Progress(int(l.source.Info.Duration.Seconds() * video.AvgFrameRate.Float()))
	}

	return nil
}

func (l *fakeLab) Digest(
	_ context.Context,
	spec encode.DigestSpec,
) error {
	if err := l.check(opDigest, spec.Destination); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for _, seg := range spec.Segments {
		l.frames += int(math.Round(seg.Length().Seconds() * spec.Rate.Float()))
	}

	l.digests = append(l.digests, spec)

	return nil
}

// labEngine is an engine whose every port is lab.
func labEngine(
	lab *fakeLab,
) *Engine {
	return NewEngine(lab, lab, lab, WithGrainLab(lab), WithRenditionEncoder(lab))
}

// sourceReport is the inspection of a source of the given geometry.
func sourceReport(
	width, height, bitDepth int,
	fps float64,
	seconds float64,
) *analysis.Report {
	rate := media.Rational{Num: int64(math.Round(fps * 1000)), Den: 1000}

	return &analysis.Report{Info: &media.Info{
		Path:     "source.mov",
		Duration: media.Seconds(seconds),
		Video: []media.VideoStream{{
			Width: width, Height: height, BitDepth: bitDepth,
			FrameRate: rate, AvgFrameRate: rate,
			Duration: media.Seconds(seconds),
		}},
	}}
}

// EncodeChunks records a chunked encode: every chunk at its own CRF.
func (l *fakeLab) EncodeChunks(
	ctx context.Context,
	codec encode.Codec,
	src encode.ChunkSource,
	dst string,
	chunks []encode.Chunk,
	p encode.Params,
) error {
	if err := l.Encode(ctx, codec, src.Path, dst, p); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.chunks[dst] = chunks
	l.chunkSources = append(l.chunkSources, src)

	return nil
}

// frameComplexity varies the difficulty of the digest's frames in blocks of
// 30 frames (not aligned with GOPs or shots): harder frames cost more bits
// and gain less quality from them, so equal-slope allocation has something
// to reallocate.
func frameComplexity(
	f int,
) float64 {
	return 0.5 + float64((f/30*7)%10)/10
}

// sampledEvery is the frame interval of the scores a sampled per-frame
// measurement lists.
const sampledEvery = 10

// perFrame measures an encode frame by frame: each frame's CRF comes from
// its chunk (or p), its bitrate and VMAF from the model scaled by the
// frame's complexity. exact lists every frame's score, sampled measurements
// one frame in sampledEvery.
func (l *fakeLab) perFrame(
	p encode.Params,
	chunks []encode.Chunk,
	exact bool,
) *analysis.Comparison {
	sizes := make([]int, l.frames)
	res := &quality.Result{}
	total, sum := 0, 0.0

	for f := range l.frames {
		fp := p
		for _, c := range chunks {
			if f >= c.Start && f < c.Start+c.Frames {
				fp.CRF = c.CRF
				if c.Height > 0 {
					fp.Width, fp.Height = c.Width, c.Height
				}
			}
		}

		complexity := frameComplexity(f)
		rate := float64(l.model.bitrate(fp)) * complexity
		score := math.Min(100, l.model.vmaf(fp)-8*(complexity-1)*fp.CRF/30)
		sizes[f] = int(rate / 25 / 8)
		total += sizes[f]
		sum += score

		if exact || f%sampledEvery == 0 {
			res.Frames = append(res.Frames, quality.FrameScore{Index: f, Score: score})
		}
	}

	n := max(l.frames, 1)
	res.Mean = sum / float64(n)

	return &analysis.Comparison{
		Distorted: &analysis.Report{Bitstream: &bitstream.Report{AverageBitrate: int64(total * 8 * 25 / n), FrameSizes: sizes}},
		VMAF:      res,
	}
}

// DecodeRaw records a decode: the raw file measures as its source encode.
func (l *fakeLab) DecodeRaw(
	_ context.Context,
	src, dst string,
	_ bool,
) error {
	if err := l.check(opDecode, dst); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.encodes[dst] = l.encodes[src]

	return nil
}

// Noise models grain: the source's scales with the height it is measured
// at, an encode's is the source's scaled by how much its film grain level
// puts back (all of it from level 35).
func (l *fakeLab) Noise(
	_ context.Context,
	path string,
	_, height, _ int,
) (grain.Stats, error) {
	if err := l.check(opNoise, path); err != nil {
		return grain.Stats{}, err
	}

	video, _ := l.source.Info.PrimaryVideo()
	sigma := l.grain * float64(height) / float64(video.Height)

	l.mu.Lock()
	p, encoded := l.encodes[path]
	l.mu.Unlock()

	if encoded {
		sigma *= math.Min(float64(p.FilmGrain)/35, 1)
	}

	return grain.Stats{Sigma: sigma, Frames: grain.SampleFrames}, nil
}
