package analyze_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analyze"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/frame"
)

var (
	errDecode  = errors.New("decode failed")
	errConsume = errors.New("consume failed")
	errClose   = errors.New("close failed")
)

// fakeSource emits frames numbered from 0, then returns err. done, when set,
// is closed once Decode returns.
type fakeSource struct {
	frames int
	err    error
	done   chan struct{}
}

func (s fakeSource) Decode(
	_ context.Context,
	req decode.Request,
	fn func(*frame.Frame) error,
) error {
	if s.done != nil {
		defer close(s.done)
	}

	for i := range s.frames {
		f := req.Pool.Get()
		f.Index = i

		if err := fn(f); err != nil {
			return err
		}
	}

	return s.err
}

type recorder struct {
	indices  []int
	failAt   int
	closeErr error
	// block, when set, makes Consume wait on it.
	block  <-chan struct{}
	closed bool
}

func (r *recorder) Consume(
	f *frame.Frame,
) error {
	if r.block != nil {
		<-r.block
	}

	if r.failAt > 0 && f.Index == r.failAt {
		return errConsume
	}

	r.indices = append(r.indices, f.Index)

	return nil
}

func (r *recorder) Close() error {
	r.closed = true

	return r.closeErr
}

func TestRun(
	t *testing.T,
) {
	const frames = 50

	testCases := []struct {
		name     string
		source   fakeSource
		failAt   int
		closeErr error
		wantErr  error
	}{
		{name: "every analyzer sees every frame in order", source: fakeSource{frames: frames}},
		{name: "a failing analyzer stops the run", source: fakeSource{frames: frames}, failAt: 10, wantErr: errConsume},
		{name: "a close error is reported", source: fakeSource{frames: frames}, closeErr: errClose, wantErr: errClose},
		{name: "a decoder error is reported", source: fakeSource{frames: frames, err: errDecode}, wantErr: errDecode},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			first := &recorder{failAt: testCase.failAt, closeErr: testCase.closeErr}
			second := &recorder{}
			progress := 0

			err := analyze.Run(t.Context(), testCase.source, decode.Request{Pool: frame.NewPool(8, 8, frame.PoolOptions{})},
				[]analyze.Analyzer{first, second}, func(n int) { progress = n })

			assert.True(t, first.closed)
			assert.True(t, second.closed)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, frames, progress)

			for _, r := range []*recorder{first, second} {
				require.Len(t, r.indices, frames)

				for i, idx := range r.indices {
					assert.Equal(t, i, idx)
				}
			}
		})
	}
}

func TestRunWithoutProgress(
	t *testing.T,
) {
	r := &recorder{}

	err := analyze.Run(t.Context(), fakeSource{frames: 3}, decode.Request{Pool: frame.NewPool(4, 4, frame.PoolOptions{})},
		[]analyze.Analyzer{r}, nil)

	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 2}, r.indices)
}

// TestRunCancelledWhileBlocked checks that a decoder stuck on a full queue
// gives up when the context is cancelled.
func TestRunCancelledWhileBlocked(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	decoderDone := make(chan struct{})
	source := fakeSource{frames: 100, done: decoderDone}
	// The analyzer only progresses once the decoder has returned, so the
	// decoder must fill the queue and then block until the cancellation.
	stuck := &recorder{block: decoderDone}

	errs := make(chan error, 1)

	go func() {
		errs <- analyze.Run(ctx, source, decode.Request{Pool: frame.NewPool(4, 4, frame.PoolOptions{})},
			[]analyze.Analyzer{stuck}, nil)
	}()

	cancel()

	require.ErrorIs(t, <-errs, context.Canceled)
	assert.True(t, stuck.closed)
	assert.Less(t, len(stuck.indices), 100)
}

func TestThumbnail(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		opts    frame.PoolOptions
		wantLen int
	}{
		{name: "thumbnail when built", opts: frame.PoolOptions{ThumbMaxWidth: 4}, wantLen: 4 * 2},
		{name: "luma without thumbnails", opts: frame.PoolOptions{}, wantLen: 16 * 8},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f := frame.NewPool(16, 8, testCase.opts).Get()
			defer f.Release()

			assert.Len(t, analyze.Thumbnail(f), testCase.wantLen)
		})
	}
}
