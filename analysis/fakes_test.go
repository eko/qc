package analysis

import (
	"context"
	"errors"
	"time"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var (
	errProbe   = errors.New("probe failed")
	errPackets = errors.New("packets failed")
	errDecode  = errors.New("decode failed")
	errMeasure = errors.New("measure failed")
)

const (
	fakeWidth  = 32
	fakeHeight = 16
	fakeFrames = 50
	fakeRate   = 25
)

// fakeVideo is a 2 s, 25 fps clip: a moving texture for 1 s, then black.
func fakeVideo() *media.Info {
	return &media.Info{
		Duration: media.Seconds(2),
		Video: []media.VideoStream{{
			Codec:        "h264",
			Width:        fakeWidth,
			Height:       fakeHeight,
			AvgFrameRate: media.Rational{Num: fakeRate, Den: 1},
		}},
	}
}

// fakePixel draws frame i: a texture moving one pixel per frame, then black.
func fakePixel(
	i, x, y int,
) byte {
	if i >= fakeFrames/2 {
		return 16
	}

	return byte(40 + ((x+i)*37+y*11)%180)
}

type fakeProber struct {
	info *media.Info
	err  error
}

func (p fakeProber) Probe(
	context.Context,
	string,
) (*media.Info, error) {
	return p.info, p.err
}

type fakePackets struct {
	count int
	err   error
}

func (p fakePackets) ReadPackets(
	_ context.Context,
	_ string,
	fn func(media.Packet) error,
) error {
	for i := range p.count {
		pkt := media.Packet{
			PTS:      time.Duration(i) * time.Second / fakeRate,
			Duration: time.Second / fakeRate,
			Size:     1000,
			Keyframe: i%fakeRate == 0,
		}

		if err := fn(pkt); err != nil {
			return err
		}
	}

	return p.err
}

type fakeSource struct {
	frames int
	err    error
}

func (s fakeSource) Decode(
	_ context.Context,
	req decode.Request,
	fn func(*frame.Frame) error,
) error {
	for i := range s.frames {
		f := req.Pool.Get()
		f.Index = i

		if i < len(req.PTS) {
			f.PTS = req.PTS[i]
		}

		for y := range f.Luma.Height {
			row := f.Luma.Row(y)
			for x := range row {
				row[x] = fakePixel(i, x, y)
			}
		}

		req.Pool.BuildThumb(f)

		if err := fn(f); err != nil {
			return err
		}
	}

	return s.err
}

type fakeMeter struct {
	result *quality.Result
	err    error
	// calls records the inputs of Measure.
	calls [][2]quality.Input
}

func (m *fakeMeter) Measure(
	_ context.Context,
	ref, dist quality.Input,
	_ quality.Options,
) (*quality.Result, error) {
	m.calls = append(m.calls, [2]quality.Input{ref, dist})

	return m.result, m.err
}

// world configures the fakes of an Analyzer.
type world struct {
	info      *media.Info
	probeErr  error
	packetErr error
	decodeErr error
	meter     Meter
}

func (w world) analyzer() *Analyzer {
	return New(
		nil,
		fakeProber{info: w.info, err: w.probeErr},
		fakePackets{count: fakeFrames, err: w.packetErr},
		fakeSource{frames: fakeFrames, err: w.decodeErr},
		w.meter,
	)
}
