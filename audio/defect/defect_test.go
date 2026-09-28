package defect

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/internal/audiotest"
	"github.com/eko/qc/media"
)

const rate = 48000

// detect runs a stereo detector over ch in one chunk.
func detect(
	t *testing.T,
	ch [][]float32,
	opts Options,
) Result {
	t.Helper()

	d := New(rate, len(ch), [][2]int{{0, 1}}, opts)
	require.NoError(t, d.Add(ch))

	return d.Result()
}

// interval is [from, to) seconds.
func interval(
	from, to float64,
) media.Interval {
	return media.Interval{Start: media.Seconds(from), End: media.Seconds(to)}
}

func TestWithDefaults(
	t *testing.T,
) {
	assert.Equal(t, Options{
		SilenceThreshold: -60, SilenceDuration: media.Seconds(2), ClipLevel: -0.01, ClipRun: 3,
		PhaseThreshold: -0.5, PhaseDuration: media.Seconds(1),
	}, Options{}.WithDefaults())

	custom := Options{SilenceThreshold: -50, SilenceDuration: 1, ClipLevel: -1, ClipRun: 5, PhaseThreshold: -0.8, PhaseDuration: 2}
	assert.Equal(t, custom, custom.WithDefaults())
}

func TestAddErrors(
	t *testing.T,
) {
	d := New(rate, 2, nil, Options{})
	require.ErrorIs(t, d.Add([][]float32{{0}}), ErrChannels)

	empty := New(rate, 0, nil, Options{})
	require.NoError(t, empty.Add(nil))

	r := empty.Result()
	assert.Zero(t, r.Duration)
	assert.Empty(t, r.Channels)

	// Channels without a sample: silence, no share.
	unfed := New(rate, 2, [][2]int{{0, 1}}, Options{}).Result()
	require.Len(t, unfed.Channels, 2)
	assert.Equal(t, loudness.Floor, unfed.Channels[0].RMS)
	assert.Zero(t, unfed.Channels[0].Silent)
}

func TestClean(
	t *testing.T,
) {
	r := detect(t, audiotest.Programme(rate, 20, 1), Options{})

	assert.Equal(t, media.Seconds(20), r.Duration)
	assert.Empty(t, r.Silence)
	assert.Zero(t, r.LeadingSilence)
	assert.Zero(t, r.TrailingSilence)
	require.Len(t, r.Channels, 2)
	require.Len(t, r.Levels, 2)
	assert.Len(t, r.Levels[0], 200)

	for _, c := range r.Channels {
		assert.False(t, c.Muted)
		assert.Empty(t, c.Silence)
		assert.Zero(t, c.ClippedSamples)
		assert.Zero(t, c.Silent)
		assert.Less(t, c.Peak, -6.0)
		assert.InDelta(t, -26.5, c.RMS, 1)
		assert.InDelta(t, 0, c.DC, 0.001)
	}

	require.Len(t, r.Pairs, 1)
	p := r.Pairs[0]
	assert.InDelta(t, 0.62, p.Correlation, 0.05)
	assert.False(t, p.Identical)
	assert.False(t, p.Inverted)
	assert.Empty(t, p.OutOfPhase)
	assert.Len(t, p.Series, 200)
}

func TestSilence(
	t *testing.T,
) {
	ch := audiotest.Programme(rate, 30, 2)
	for _, c := range ch {
		audiotest.Mute(c, rate, 0, 2.5)
		audiotest.Mute(c, rate, 10, 13)
		audiotest.Mute(c, rate, 20, 21)
		audiotest.Mute(c, rate, 27, 30)
	}

	r := detect(t, ch, Options{})

	// The 1 s silence is shorter than the default 2 s.
	assert.Equal(t, []media.Interval{interval(0, 2.5), interval(10, 13), interval(27, 30)}, r.Silence)
	assert.Equal(t, media.Seconds(2.5), r.LeadingSilence)
	assert.Equal(t, media.Seconds(3), r.TrailingSilence)
	assert.InDelta(t, 9.5/30, r.Channels[0].Silent, 0.001)
	assert.Empty(t, r.Channels[0].Silence, "a silence of the mix is not a channel's")
}

func TestSilentSignal(
	t *testing.T,
) {
	r := detect(t, [][]float32{make([]float32, 3*rate+10), make([]float32, 3*rate+10)}, Options{})

	length := media.Seconds(float64(3*rate+10) / rate)
	assert.Equal(t, length, r.LeadingSilence)
	assert.Equal(t, length, r.TrailingSilence)
	assert.Equal(t, []media.Interval{{Start: 0, End: length}}, r.Silence)
	assert.False(t, r.Channels[0].Muted)
	assert.Equal(t, 1.0, r.Channels[0].Silent)
	assert.Equal(t, loudness.Floor, r.Channels[0].Peak)
	assert.Equal(t, loudness.Floor, r.Pairs[0].Difference)
	assert.False(t, r.Pairs[0].Identical, "silence is not a mono signal")
}

func TestChannelSilence(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		mute      func(ch [][]float32)
		wantMuted bool
		want      []media.Interval
	}{
		{name: "dropout", mute: func(ch [][]float32) { audiotest.Mute(ch[0], rate, 5, 9) }, want: []media.Interval{interval(5, 9)}},
		{name: "dropout until the end", mute: func(ch [][]float32) { audiotest.Mute(ch[0], rate, 15, 20) }, want: []media.Interval{interval(15, 20)}},
		{name: "muted", mute: func(ch [][]float32) { audiotest.Mute(ch[0], rate, 0, 20) }, wantMuted: true, want: []media.Interval{interval(0, 20)}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ch := audiotest.Programme(rate, 20, 3)
			testCase.mute(ch)

			r := detect(t, ch, Options{})
			assert.Equal(t, testCase.wantMuted, r.Channels[0].Muted)
			assert.Equal(t, testCase.want, r.Channels[0].Silence)
			assert.False(t, r.Channels[1].Muted)
			assert.Empty(t, r.Silence)
		})
	}
}

func TestClipping(
	t *testing.T,
) {
	ch := audiotest.Programme(rate, 10, 4)
	audiotest.Clip(ch[0], rate, 4, 4.4, 8)

	r := detect(t, ch, Options{})

	c := r.Channels[0]
	assert.Positive(t, c.ClippedSamples)
	assert.Positive(t, c.ClipEvents)
	require.Len(t, c.Clipping, 1, "events 0.5 s apart or less merge")
	assert.GreaterOrEqual(t, c.Clipping[0].Start, media.Seconds(4))
	assert.LessOrEqual(t, c.Clipping[0].End, media.Seconds(4.4))
	assert.Zero(t, r.Channels[1].ClippedSamples)
}

func TestClipRuns(
	t *testing.T,
) {
	// Two samples at full scale are not clipping; three are, and a run
	// open at the end counts.
	x := make([]float32, rate)
	x[100], x[101] = 1, -1
	x[1000], x[1001], x[1002] = 1, 1, 1
	x[rate-3], x[rate-2], x[rate-1] = -1, -1, -1

	d := New(rate, 1, nil, Options{})
	require.NoError(t, d.Add([][]float32{x}))

	c := d.Result().Channels[0]
	assert.Equal(t, int64(6), c.ClippedSamples)
	assert.Equal(t, 2, c.ClipEvents)
	assert.Len(t, c.Clipping, 2)
}

func TestClipSegmentsBounded(
	t *testing.T,
) {
	// One burst of clipping per second: never merged, the first 100 kept.
	x := make([]float32, 120*1000)
	for s := range 120 {
		x[s*1000], x[s*1000+1], x[s*1000+2] = 1, 1, 1
	}

	d := New(1000, 1, nil, Options{})
	require.NoError(t, d.Add([][]float32{x}))

	c := d.Result().Channels[0]
	assert.Equal(t, 120, c.ClipEvents)
	assert.Len(t, c.Clipping, maxClipSegments)
}

func TestPhase(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		apply        func(ch [][]float32)
		wantSegments []media.Interval
		inverted     bool
		identical    bool
	}{
		{name: "inverted segment", apply: func(ch [][]float32) { audiotest.Invert(ch[1], rate, 5, 10) }, wantSegments: []media.Interval{interval(5, 10)}},
		{name: "inverted until the end", apply: func(ch [][]float32) { audiotest.Invert(ch[1], rate, 12, 20) }, wantSegments: []media.Interval{interval(12, 20)}},
		{name: "inverted throughout", apply: func(ch [][]float32) { audiotest.Invert(ch[1], rate, 0, 20) }, wantSegments: []media.Interval{interval(0, 20)}, inverted: true},
		{name: "too short", apply: func(ch [][]float32) { audiotest.Invert(ch[1], rate, 5, 5.3) }},
		{name: "identical", apply: func(ch [][]float32) { copy(ch[1], ch[0]) }, identical: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ch := audiotest.Programme(rate, 20, 5)
			testCase.apply(ch)

			p := detect(t, ch, Options{}).Pairs[0]
			assert.Equal(t, testCase.inverted, p.Inverted)
			assert.Equal(t, testCase.identical, p.Identical)
			require.Len(t, p.OutOfPhase, len(testCase.wantSegments))

			for i, want := range testCase.wantSegments {
				assert.InDelta(t, want.Start.Seconds(), p.OutOfPhase[i].Start.Seconds(), 0.4)
				assert.InDelta(t, want.End.Seconds(), p.OutOfPhase[i].End.Seconds(), 0.4)
			}
		})
	}
}

func TestDCOffset(
	t *testing.T,
) {
	ch := audiotest.Programme(rate, 5, 6)
	audiotest.Offset(ch[0], 0.01)

	r := detect(t, ch, Options{})
	assert.InDelta(t, 0.01, r.Channels[0].DC, 0.001)
	assert.InDelta(t, 0, r.Channels[1].DC, 0.001)
}

func TestChunking(
	t *testing.T,
) {
	// Any chunking gives the result of a single chunk, at a rate whose
	// windows are uneven (22.05 kHz: 2 205 samples per step).
	const odd = 22050

	ch := audiotest.Programme(odd, 12, 7)
	audiotest.Mute(ch[0], odd, 3, 6)
	audiotest.Clip(ch[1], odd, 8, 8.2, 8)

	whole := New(odd, 2, [][2]int{{0, 1}}, Options{})
	require.NoError(t, whole.Add(ch))

	pieces := New(odd, 2, [][2]int{{0, 1}}, Options{})
	for from := 0; from < len(ch[0]); from += 777 {
		to := min(len(ch[0]), from+777)
		require.NoError(t, pieces.Add([][]float32{ch[0][from:to], ch[1][from:to]}))
	}

	want, got := whole.Result(), pieces.Result()
	assert.Equal(t, want, got)
	assert.Equal(t, []media.Interval{interval(3, 6)}, got.Channels[0].Silence)
}
