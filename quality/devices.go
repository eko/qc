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

	r.opts.Metrics, r.opts.Devices = metrics, names
	r.extractors = extractors(metrics)
	r.xpsnr = slices.Contains(metrics, MetricXPSNR)
	r.series = metricSeries(metrics, r.spec.Width, r.spec.Height, r.bitDepth)
	r.modelSeries = []string{""}

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
	clips := make([]clip, len(results))
	for i, cr := range results {
		clips[i] = cr.clip
	}

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
		}
		sub.opts.Progress = nil

		for _, d := range pass {
			sub.models = append(sub.models, d.spec)
			sub.modelSeries = append(sub.modelSeries, d.series)
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
	}

	return nil
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

	return out
}
