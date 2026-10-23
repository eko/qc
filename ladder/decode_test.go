package ladder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/cputime"
	"github.com/eko/qc/media"
)

// errDecode is the failure of fakeDecoder.
var errDecode = errors.New("decode failed")

// fakeDecoder costs seek CPU seconds to reach any start, then perSecond for
// every second it decodes.
type fakeDecoder struct {
	seek, perSecond float64
	fail            bool
	calls           int
}

func (d *fakeDecoder) Decode(
	ctx context.Context,
	_ string,
	_, length media.Duration,
) error {
	d.calls++
	if d.fail {
		return errDecode
	}

	cost := d.seek + d.perSecond*length.Seconds()
	cputime.Add(ctx, time.Duration(cost*float64(time.Second)))

	return nil
}

func TestDecodeCPU(
	t *testing.T,
) {
	segments := func(n int) []media.Interval {
		out := make([]media.Interval, n)
		for i := range out {
			start := media.Seconds(float64(60 * i))
			out[i] = media.Interval{Start: start, End: start + media.Seconds(4)}
		}

		return out
	}

	testCases := []struct {
		name      string
		segments  []media.Interval
		decoder   *fakeDecoder
		want      float64
		wantCalls int
		wantErr   error
	}{
		{
			name:      "seeks left out",
			segments:  segments(3),
			decoder:   &fakeDecoder{seek: 2, perSecond: 0.5},
			want:      0.5 * 12,
			wantCalls: 6,
		},
		{
			name:      "a few segments stand for the digest",
			segments:  segments(20),
			decoder:   &fakeDecoder{seek: 2, perSecond: 0.5},
			want:      0.5 * 80,
			wantCalls: 2 * decodeSegments,
		},
		{
			name:      "decoder failure",
			segments:  segments(3),
			decoder:   &fakeDecoder{fail: true},
			wantCalls: 1,
			wantErr:   errDecode,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			b := &build{
				engine:      &Engine{decoder: testCase.decoder},
				prepared:    &Prepared{},
				digestParts: []encode.DigestPart{{Source: "in.mov", Segments: testCase.segments}},
			}

			cpu, err := b.decodeCPU(t.Context(), Digest{Segments: testCase.segments})
			assert.Equal(t, testCase.wantCalls, testCase.decoder.calls)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.InDelta(t, testCase.want, cpu, 1e-6)
		})
	}
}

func TestDecodeCPUShared(
	t *testing.T,
) {
	segments := []media.Interval{{Start: 0, End: media.Seconds(4)}}
	prepared := &Prepared{}
	prepared.keepDigest(segments, "digest.nut", nil)

	decoder := &fakeDecoder{perSecond: 0.5}
	newBuild := func() *build {
		return &build{
			engine:      &Engine{decoder: decoder},
			prepared:    prepared,
			digestParts: []encode.DigestPart{{Source: "in.mov", Segments: segments}},
		}
	}

	first, err := newBuild().decodeCPU(t.Context(), Digest{Segments: segments})
	require.NoError(t, err)

	second, err := newBuild().decodeCPU(t.Context(), Digest{Segments: segments})
	require.NoError(t, err)

	assert.InDelta(t, 2.0, first, 1e-6)
	assert.InDelta(t, first, second, 1e-9)
	assert.Equal(t, 2, decoder.calls, "the second build reads the first one's measurement")
}

func TestDecodeCPUWithoutDecoder(
	t *testing.T,
) {
	b := &build{engine: &Engine{}, prepared: &Prepared{}}

	cpu, err := b.decodeCPU(t.Context(), Digest{})
	require.NoError(t, err)
	assert.Zero(t, cpu)
}
