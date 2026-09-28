package analyze_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analyze"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
)

// videoSource decodes a video of frames frames, frame i filled with byte
// i, honouring FirstIndex and MaxFrames as a seeking decoder does. skew
// shifts the frames a segment starts at (a seek landing elsewhere).
type videoSource struct {
	frames int
	skew   map[int]int
	err    error
}

func (s videoSource) Decode(
	_ context.Context,
	req decode.Request,
	fn func(*frame.Frame) error,
) error {
	start := req.FirstIndex + s.skew[req.FirstIndex]

	for k, i := 0, start; i < s.frames && (req.MaxFrames == 0 || k < req.MaxFrames); k, i = k+1, i+1 {
		f := req.Pool.Get()
		f.Index = req.FirstIndex + k

		for p := range f.Luma.Pix {
			f.Luma.Pix[p] = byte(i)
		}

		if err := fn(f); err != nil {
			return err
		}
	}

	return s.err
}

// pair is what a fork measures of a frame: its content and its
// predecessor's, -1 without one.
type pair struct {
	content, prev int
}

// pairRecorder is a Forker measuring pairs.
type pairRecorder struct {
	series  analyze.Series[pair]
	forks   atomic.Int32
	closed  atomic.Int32
	failAt  int
	rootErr error
}

func (r *pairRecorder) Consume(*frame.Frame) error { return nil }

func (r *pairRecorder) Close() error {
	r.closed.Add(1)

	return r.rootErr
}

func (r *pairRecorder) Fork() analyze.Analyzer {
	r.forks.Add(1)

	return &pairRun{parent: r, first: -1, prev: -1}
}

type pairRun struct {
	parent      *pairRecorder
	first, prev int
	pairs       []pair
}

func (p *pairRun) Consume(
	f *frame.Frame,
) error {
	if p.parent.failAt > 0 && f.Index == p.parent.failAt {
		return errConsume
	}

	if p.first < 0 {
		p.first = f.Index
	}

	content := int(f.Luma.Pix[0])
	p.pairs = append(p.pairs, pair{content: content, prev: p.prev})
	p.prev = content

	return nil
}

func (p *pairRun) Close() error {
	p.parent.closed.Add(1)
	p.parent.series.Add(p.first, p.pairs)

	return nil
}

// segmentRequests splits frames at bounds as the analysis does: each
// segment but the last also outputs the next one's first frame.
func segmentRequests(
	pool *frame.Pool,
	bounds ...int,
) []decode.Request {
	starts := append([]int{0}, bounds...)
	reqs := make([]decode.Request, len(starts))

	for k, first := range starts {
		reqs[k] = decode.Request{Pool: pool, FirstIndex: first}
		if k+1 < len(starts) {
			reqs[k].MaxFrames = starts[k+1] - first + 1
		}
	}

	return reqs
}

func TestRunSegments(
	t *testing.T,
) {
	const frames = 40

	pool := frame.NewPool(4, 4, frame.PoolOptions{})

	testCases := []struct {
		name    string
		source  videoSource
		reqs    []decode.Request
		failAt  int
		rootErr error
		wantErr error
	}{
		{
			name:   "segments add up to one sequential pass",
			source: videoSource{frames: frames},
			reqs:   segmentRequests(pool, 9, 20, 31),
		},
		{
			name:    "a seek landing elsewhere is detected",
			source:  videoSource{frames: frames, skew: map[int]int{20: 1}},
			reqs:    segmentRequests(pool, 9, 20, 31),
			wantErr: analyze.ErrSegment,
		},
		{
			name:    "a segment ending early is detected",
			source:  videoSource{frames: 25},
			reqs:    segmentRequests(pool, 9, 20, 31),
			wantErr: analyze.ErrSegment,
		},
		{
			name:    "an analyzer failure stops the run",
			source:  videoSource{frames: frames},
			reqs:    segmentRequests(pool, 9, 20, 31),
			failAt:  25,
			wantErr: errConsume,
		},
		{
			name:    "a decoder failure stops the run",
			source:  videoSource{frames: frames, err: errDecode},
			reqs:    segmentRequests(pool, 20),
			wantErr: errDecode,
		},
		{
			name:    "a close error is reported",
			source:  videoSource{frames: frames},
			reqs:    segmentRequests(pool, 20),
			rootErr: errClose,
			wantErr: errClose,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rec := &pairRecorder{failAt: testCase.failAt, rootErr: testCase.rootErr}

			var (
				mu   sync.Mutex
				seen []int
			)

			err := analyze.RunSegments(t.Context(), testCase.source, testCase.reqs, 2, []analyze.Analyzer{rec}, func(n int) {
				mu.Lock()
				seen = append(seen, n)
				mu.Unlock()
			})

			assert.Equal(t, rec.forks.Load()+1, rec.closed.Load(), "every fork and the analyzer are closed")

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)

			pairs := rec.series.Merge()
			require.Len(t, pairs, frames)

			for i, p := range pairs {
				assert.Equal(t, pair{content: i, prev: i - 1}, p, "frame %d", i)
			}

			require.Len(t, seen, frames)
			assert.Equal(t, frames, seen[len(seen)-1])
		})
	}
}

func TestRunSegmentsCancelled(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	pool := frame.NewPool(4, 4, frame.PoolOptions{})
	rec := &pairRecorder{}

	err := analyze.RunSegments(ctx, videoSource{frames: 30}, segmentRequests(pool, 10, 20), 1, []analyze.Analyzer{rec}, nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), rec.closed.Load()-rec.forks.Load())
}

func TestSplittable(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		analyzers []analyze.Analyzer
		want      bool
	}{
		{name: "none", want: true},
		{name: "forkers", analyzers: []analyze.Analyzer{&pairRecorder{}, &pairRecorder{}}, want: true},
		{name: "a sequential analyzer", analyzers: []analyze.Analyzer{&pairRecorder{}, &recorder{}}, want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, analyze.Splittable(testCase.analyzers))
		})
	}
}

func TestSeriesMerge(
	t *testing.T,
) {
	testCases := []struct {
		name string
		runs map[int][]string
		want []string
	}{
		{name: "empty"},
		{name: "one run", runs: map[int][]string{3: {"a", "b"}}, want: []string{"a", "b"}},
		{
			name: "overlaps keep the earlier run",
			runs: map[int][]string{0: {"a", "b", "c"}, 2: {"x", "d"}, 3: {"y", "e"}},
			want: []string{"a", "b", "c", "d", "e"},
		},
		{
			name: "a run inside another adds nothing",
			runs: map[int][]string{0: {"a", "b", "c", "d"}, 1: {"x", "y"}},
			want: []string{"a", "b", "c", "d"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var s analyze.Series[string]

			// Runs are added from the last: Merge orders them.
			for first := 10; first >= 0; first-- {
				s.Add(first, testCase.runs[first])
			}

			assert.Equal(t, testCase.want, s.Merge())
		})
	}
}
