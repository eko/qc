package analysis

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
)

var errHDR = errors.New("first frame failed")

// fakeHDRProber can defer the HDR metadata of the first frame: its streams
// say PQ, its first frame HDR10 with a content light level.
type fakeHDRProber struct {
	streamErr, hdrErr error
	// full, streams and hdr count the calls of each method.
	full, streams, hdr *atomic.Int32
}

func newFakeHDRProber() fakeHDRProber {
	return fakeHDRProber{full: &atomic.Int32{}, streams: &atomic.Int32{}, hdr: &atomic.Int32{}}
}

func pqVideo() *media.Info {
	info := fakeVideo()
	info.Video[0].Color.Transfer = media.TransferPQ
	info.Video[0].HDR.DynamicRange = media.DynamicRangePQ

	return info
}

func (p fakeHDRProber) Probe(
	ctx context.Context,
	path string,
) (*media.Info, error) {
	p.full.Add(1)

	info, err := p.ProbeStreams(ctx, path)
	if err != nil {
		return nil, err
	}

	p.streams.Add(-1)

	return info, p.ProbeHDR(ctx, info)
}

func (p fakeHDRProber) ProbeStreams(
	context.Context,
	string,
) (*media.Info, error) {
	p.streams.Add(1)

	return pqVideo(), p.streamErr
}

func (p fakeHDRProber) ProbeHDR(
	_ context.Context,
	info *media.Info,
) error {
	p.hdr.Add(1)

	info.Video[0].HDR.DynamicRange = media.DynamicRangeHDR10
	info.Video[0].HDR.ContentLightLevel = &media.ContentLightLevel{MaxCLL: 1000, MaxFALL: 400}

	return p.hdrErr
}

func hdrAnalyzer(
	p fakeHDRProber,
	meter Meter,
) *Analyzer {
	return New(nil, p, fakePackets{count: fakeFrames}, fakeSource{frames: fakeFrames}, meter)
}

func TestAnalyzeDefersHDRMetadata(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		opts        Options
		streamErr   error
		hdrErr      error
		wantErr     error
		wantRange   media.DynamicRange
		wantCalls   [3]int32
		wantDecoded bool
	}{
		{
			name: "read while decoding", wantRange: media.DynamicRangeHDR10,
			wantCalls: [3]int32{0, 1, 1}, wantDecoded: true,
		},
		{
			name: "inspection reads it first", opts: Options{SkipVideo: true},
			wantRange: media.DynamicRangeHDR10, wantCalls: [3]int32{1, 0, 1},
		},
		{
			name: "inspection leaves it to a later analysis", opts: Options{SkipVideo: true, DeferHDRMetadata: true},
			wantRange: media.DynamicRangePQ, wantCalls: [3]int32{0, 1, 0},
		},
		{name: "streams failure", streamErr: errProbe, wantErr: errProbe},
		{name: "first frame failure", hdrErr: errHDR, wantErr: errHDR},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			p := newFakeHDRProber()
			p.streamErr, p.hdrErr = testCase.streamErr, testCase.hdrErr

			report, err := hdrAnalyzer(p, nil).Analyze(t.Context(), "hdr.mov", testCase.opts)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantRange, report.Info.Video[0].HDR.DynamicRange)
			assert.Equal(t, testCase.wantCalls, [3]int32{p.full.Load(), p.streams.Load(), p.hdr.Load()})
			assert.Equal(t, testCase.wantDecoded, report.Video != nil)
		})
	}
}

func TestCompareDefersHDRMetadata(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		opts      CompareOptions
		hdrErr    error
		wantErr   error
		wantCalls int32
	}{
		{name: "both files", wantCalls: 2},
		{name: "a given reference is complete", opts: CompareOptions{Reference: &Report{Info: pqVideo()}}, wantCalls: 1},
		{name: "first frame failure", hdrErr: errHDR, wantErr: errHDR},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			p := newFakeHDRProber()
			p.hdrErr = testCase.hdrErr
			meter := &fakeMeter{result: &quality.Result{Mean: 90}}

			cmp, err := hdrAnalyzer(p, meter).Compare(t.Context(), "ref.mov", "dist.mp4", testCase.opts)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantCalls, p.hdr.Load())
			assert.Zero(t, p.full.Load(), "inspections defer the first frame")
			assert.Equal(t, media.DynamicRangeHDR10, cmp.Distorted.Info.Video[0].HDR.DynamicRange)
			assert.Equal(t, media.TransferPQ, meter.calls[0][0].Video.Color.Transfer, "the measurement has the colour")
		})
	}
}
