package quality

import (
	"math"
	"slices"

	"github.com/eko/qc/quality/hdr"
	"github.com/eko/qc/quality/xpsnr"
)

// goMeters are the pure-Go metrics of a worker, measured pair by pair next
// to libvmaf: XPSNR and the HDR metrics, each when requested. They keep
// the raw values of the clip being scored.
type goMeters struct {
	xpsnr *xpsnr.Meter
	hdr   *hdr.Meter

	distortions []xpsnr.Distortion
	hdrValues   []hdr.Values
	// hdrNames are the HDR series measured (see hdrSeriesOf).
	hdrNames []string
}

// newGoMeters returns the pure-Go meters of the run, with threads
// goroutines each.
func (r *run) newGoMeters(
	threads int,
) *goMeters {
	m := &goMeters{}

	if r.xpsnr {
		m.xpsnr = xpsnr.New(r.spec.Width, r.spec.Height, r.bitDepth, r.ref.Video.AvgFrameRate.Float(), threads)
	}

	if r.hdr {
		m.hdr = hdr.New(r.spec.Width, r.spec.Height, r.ref.Video.Color, r.bitDepth, threads)
		m.hdrNames = hdrSeriesOf(r.ref.Video.Color.Transfer)
	}

	return m
}

// reset starts a new clip.
func (m *goMeters) reset() {
	if m.xpsnr != nil {
		m.xpsnr.Reset()
	}

	m.distortions, m.hdrValues = m.distortions[:0], m.hdrValues[:0]
}

// measure measures a pair of the clip. primed is set for the first pair of
// a clip starting mid-video: its warm-up frame becomes XPSNR's history
// instead of black.
func (m *goMeters) measure(
	p pair,
	primed bool,
) {
	if m.xpsnr != nil {
		if primed {
			m.xpsnr.Prime(p.ref)
		}

		m.distortions = append(m.distortions, m.xpsnr.Measure(p.ref, p.dist))
	}

	if m.hdr != nil {
		m.hdrValues = append(m.hdrValues, m.hdr.Measure(p.ref, p.dist))
	}
}

// values adds the raw per-pair values of the measured series to values.
func (m *goMeters) values(
	values map[string][]float64,
) {
	if m.xpsnr != nil {
		for plane, name := range []string{SeriesXPSNRY, SeriesXPSNRU, SeriesXPSNRV} {
			v := make([]float64, len(m.distortions))
			for i, d := range m.distortions {
				v[i] = d[plane]
			}

			values[name] = v
		}
	}

	if m.hdr != nil {
		hdrSeriesValues(m.hdrValues, m.hdrNames, values)
	}
}

// hdrSeriesValues adds the per-frame values of the HDR series named: wPSNR
// per plane in dB (capped at maxDecibels for identical planes), ΔE ITP mean
// and 99th percentile.
func hdrSeriesValues(
	frames []hdr.Values,
	names []string,
	values map[string][]float64,
) {
	columns := make([][]float64, len(hdrSeries))

	for i := range columns {
		columns[i] = make([]float64, len(frames))
	}

	for i, v := range frames {
		for plane := range v.WPSNR {
			columns[plane][i] = math.Min(v.WPSNR[plane], maxDecibels)
		}

		columns[3][i], columns[4][i] = v.DeltaE, v.DeltaEP99
	}

	for i, name := range hdrSeries {
		if slices.Contains(names, name) {
			values[name] = columns[i]
		}
	}
}
