package quality

import (
	"context"
	"slices"

	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/vmaf"
)

// device is a requested viewing condition and the model scoring it.
type device struct {
	name   string
	spec   vmaf.ModelSpec
	series string
}

// configureMetrics validates the metrics and devices of the options and
// plans how they are measured: extractors and XPSNR in the primary pass,
// device models in the primary libvmaf contexts when they share its
// resolution, in one extra pass per other resolution otherwise.
func (r *run) configureMetrics() error {
	metrics, err := ParseMetrics(r.opts.Metrics)
	if err != nil {
		return err
	}

	names, err := ParseDevices(r.opts.Devices)
	if err != nil {
		return err
	}

	hdrMetric, err := ParseHDRMetric(string(r.opts.HDRMetric))
	if err != nil {
		return err
	}

	r.opts.Metrics, r.opts.Devices, r.opts.HDRMetric = metrics, names, hdrMetric
	r.extractors = extractors(metrics)
	r.xpsnr = slices.Contains(metrics, MetricXPSNR)
	r.series = metricSeries(metrics, r.spec.Width, r.spec.Height, r.bitDepth)
	r.modelSeries = []string{""}
	r.configureHDR()

	fps := r.ref.Video.AvgFrameRate.Float()
	passes := map[[2]int]int{}

	for _, name := range names {
		// Device names are valid and their models are names, not paths:
		// neither call can fail (a missing model fails when loaded).
		model, _ := vmaf.DeviceModel(name, fps)
		spec, _ := vmaf.ResolveModel(model, r.ref.Video.Height, fps, r.opts.ModelDirs)

		r.addDevice(device{name: name, spec: spec, series: seriesDevice + name}, passes)
	}

	return nil
}

// configureHDR plans the HDR metrics of a PQ or HLG reference: on the
// scored frames, or in a second pass on the HDR frames when VMAF scores
// tone-mapped ones.
func (r *run) configureHDR() {
	if !r.ref.Video.MeasurableHDR() {
		return
	}

	r.toneMap = r.opts.HDRMetric == HDRMetricToneMap
	if r.opts.SkipHDRMetrics {
		return
	}

	r.hdr = !r.toneMap
	r.hdrPass = r.toneMap

	for _, name := range hdrSeriesOf(r.ref.Video.Color.Transfer) {
		r.series = append(r.series, series{name: name})
	}
}

// addDevice plans how a device is scored: as the primary model itself, in
// the primary contexts at the same resolution, or in the extra pass of its
// resolution (passes indexes r.passes by resolution).
func (r *run) addDevice(
	d device,
	passes map[[2]int]int,
) {
	r.devices = append(r.devices, d)

	switch size := [2]int{d.spec.Width, d.spec.Height}; {
	case d.spec.Name == r.spec.Name:
		r.aliases = append(r.aliases, d.series)
	case size == [2]int{r.spec.Width, r.spec.Height}:
		r.models = append(r.models, d.spec)
		r.modelSeries = append(r.modelSeries, d.series)
	default:
		pass, ok := passes[size]
		if !ok {
			pass = len(r.passes)
			passes[size] = pass
			r.passes = append(r.passes, nil)
		}

		r.passes[pass] = append(r.passes[pass], d)
	}
}

// devicePasses scores the device models evaluated at another resolution on
// the clips of results, one pass per resolution, and adds their series to
// results. Each pass decodes the clips again, scaled to its resolution.
func (r *run) devicePasses(
	ctx context.Context,
	results []clipResult,
	workers, threads int,
) error {
	for _, pass := range r.passes {
		sub := &run{
			meter:    r.meter,
			ref:      r.ref,
			dist:     r.dist,
			spec:     pass[0].spec,
			opts:     r.opts,
			n:        r.n,
			bitDepth: r.bitDepth,
			backend:  r.backend,
			plans:    map[string]int{},
			decoders: r.decoders,
			segments: r.segments,
		}
		sub.opts.Progress = nil

		for _, d := range pass {
			sub.models = append(sub.models, d.spec)
			sub.modelSeries = append(sub.modelSeries, d.series)
		}

		if err := r.mergePass(ctx, sub, results, workers, threads); err != nil {
			return err
		}
	}

	return nil
}

// mergePass scores the clips of results in the extra pass sub and adds its
// series to results, its decoded frames and plans to r's.
func (r *run) mergePass(
	ctx context.Context,
	sub *run,
	results []clipResult,
	workers, threads int,
) error {
	clips := make([]clip, len(results))
	for i, cr := range results {
		clips[i] = cr.clip
	}

	scored, err := sub.score(ctx, clips, workers, threads, 0)
	if err != nil {
		return err
	}

	for i, cr := range scored {
		for name, values := range cr.values {
			results[i].values[name] = values
		}
	}

	r.mu.Lock()
	r.decoded += sub.decoded

	for plan, count := range sub.plans {
		r.plans[plan] += count
	}
	r.mu.Unlock()

	return nil
}

// hdrMetricsPass measures the HDR metrics of the clips of results on the
// HDR frames, in a second decode of the same clips: VMAF scored tone-mapped
// frames (HDRMetricToneMap), and wPSNR and ΔE ITP must see the HDR signal.
func (r *run) hdrMetricsPass(
	ctx context.Context,
	results []clipResult,
	workers, threads int,
) error {
	if !r.hdrPass {
		return nil
	}

	sub := &run{
		meter: r.meter, ref: r.ref, dist: r.dist, spec: r.spec, opts: r.opts,
		n: r.n, bitDepth: r.bitDepth, backend: r.backend, plans: map[string]int{}, hdr: true,
		decoders: r.decoders, segments: r.segments,
	}
	sub.opts.Progress = nil

	return r.mergePass(ctx, sub, results, workers, threads)
}

// sourceBandingPass measures CAMBI on the reference at the banded frames of
// results, in a second pass over those frames alone: it tells the banding
// the encode made from the banding it inherited (Banding.SourceFrames).
// Measuring the reference on every frame would cost CAMBI once more
// everywhere (10% of a sampled measurement, 24% of an exact one); most
// encodes have no banded frame, and this pass then costs nothing.
func (r *run) sourceBandingPass(
	ctx context.Context,
	results []clipResult,
	workers, threads int,
) error {
	clips := bandedClips(results)
	if len(clips) == 0 {
		return nil
	}

	sub := &run{
		meter: r.meter, ref: r.ref, dist: r.dist, spec: r.spec, models: []vmaf.ModelSpec{r.spec}, opts: r.opts,
		n: r.n, bitDepth: r.bitDepth, backend: r.backend, plans: map[string]int{},
		extractors: []vmaf.Extractor{vmaf.ExtractorCAMBISource}, decoders: r.decoders,
	}
	sub.opts.Progress = nil

	scored, err := sub.score(ctx, clips, workers, threads, 0)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sourceCAMBI = map[int]float64{}

	for _, cr := range scored {
		for i, source := range cr.values[SeriesCAMBISource] {
			r.sourceCAMBI[cr.clip.from+i] = source
		}
	}

	// Its frames are decoded again; its plan is not one of the measurement.
	r.decoded += sub.decoded

	return nil
}

// bandedClips returns the runs of consecutive frames of results where CAMBI
// shows visible banding, as clips to score again.
func bandedClips(
	results []clipResult,
) []clip {
	var clips []clip

	for _, cr := range results {
		open := false

		for i, cambi := range cr.values[SeriesCAMBI] {
			if cambi <= BandingThreshold {
				open = false

				continue
			}

			if frame := cr.clip.from + i; open {
				clips[len(clips)-1].to = frame + 1
			} else {
				clips = append(clips, clip{stratum: cr.clip.stratum, from: frame, to: frame + 1})
				open = true
			}
		}
	}

	return clips
}

// addMetrics estimates every metric and device series from the clips of
// results, like VMAF, and lists the banded segments.
func (r *run) addMetrics(
	res *Result,
	strata []*stratum,
	results []clipResult,
) {
	// A budget covering every clip slot is exact, even for a video too
	// short to estimate a variance.
	exact := res.Mode == ModeExact || allSampled(strata)
	estimateOf := estimatorFor(r.opts.Sample)

	for _, s := range r.series {
		res.Metrics = append(res.Metrics, MetricResult{
			Name:     s.name,
			Estimate: pooledEstimate(s, strata, results, exact, estimateOf, r.opts.Confidence),
			Scored:   stats.Summarize(frameValues(res.Frames, s.name)),
		})

		if s.name == SeriesCAMBI {
			res.Banding = bandingOf(res.Frames, frameDuration(r.ref.Video.AvgFrameRate))
		}
	}

	for _, d := range r.devices {
		s := series{name: d.series}
		res.Devices = append(res.Devices, DeviceResult{
			Device:   d.name,
			Model:    d.spec,
			Estimate: pooledEstimate(s, strata, results, exact, estimateOf, r.opts.Confidence),
			Scored:   stats.Summarize(frameValues(res.Frames, s.name)),
		})
	}
}

// frameValues returns the reported values of a series over frames.
func frameValues(
	frames []FrameScore,
	name string,
) []float64 {
	values := make([]float64, 0, len(frames))

	for _, f := range frames {
		if v, ok := f.Metrics[name]; ok {
			values = append(values, v)
		}
	}

	return values
}

// frameMetrics returns the reported values of every series at frame i of a
// clip, or nil when the clip has none.
func (r *run) frameMetrics(
	cr clipResult,
	i int,
) map[string]float64 {
	if len(cr.values) == 0 {
		return nil
	}

	out := make(map[string]float64, len(cr.values))

	for _, s := range r.series {
		out[s.name] = s.report(cr.values[s.name][i])
	}

	for _, d := range r.devices {
		out[d.series] = cr.values[d.series][i]
	}

	if source, ok := r.sourceCAMBI[cr.clip.from+i]; ok {
		out[SeriesCAMBISource] = source
	}

	return out
}
