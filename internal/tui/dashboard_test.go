package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/ladder"
	"github.com/eko/qc/quality"
)

func newTestDashboard(
	labels ...string,
) (*dashboard, *bool) {
	canceled := false
	d := newDashboard("run", "clip.mp4", labels, func() { canceled = true })

	return d, &canceled
}

func TestDashboardLifecycle(
	t *testing.T,
) {
	d, _ := newTestDashboard("Inspect", "Frame analysis", "Ladder · h264")

	d.Update(startMsg{i: 0})
	d.Update(doneMsg{i: 0, summary: "h264 · 1920×1080"})
	d.Update(startMsg{i: 1})
	d.Update(progressMsg{i: 1, done: 50, total: 100})
	d.Update(panelMsg{i: 1, panel: VMAFPanel{Progress: quality.Progress{Estimated: true, Mean: 88, HalfWidth: 0.4, FramesTotal: 100, FramesScored: 20}}})

	view := plain(d.View())
	assert.Contains(t, view, "✓  Inspect")
	assert.Contains(t, view, "h264 · 1920×1080")
	assert.Contains(t, view, "50/100 frames")
	assert.Contains(t, view, "88.00")
	assert.Contains(t, view, "○  Ladder · h264        waiting", "the ladder has not started")
	assert.Contains(t, view, "q quit")

	_, cmd := d.Update(finishMsg{err: errors.New("boom")})
	require.NotNil(t, cmd, "finishing quits the program")
	assert.IsType(t, tea.QuitMsg{}, cmd())

	view = plain(d.View())
	assert.Contains(t, view, "✗  Frame analysis", "a running stage is marked interrupted")
	assert.Contains(t, view, "interrupted")
	assert.NotContains(t, view, "q quit")
	assert.NotContains(t, view, "88.00", "panels disappear once finished")
	assert.EqualError(t, d.err, "boom")
}

func TestDashboardStageMessages(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		msgs  []tea.Msg
		check func(t *testing.T, s stage)
	}{
		{
			name: "start",
			msgs: []tea.Msg{startMsg{i: 0}},
			check: func(t *testing.T, s stage) {
				assert.Equal(t, stateRunning, s.state)
				assert.False(t, s.started.IsZero())
			},
		},
		{
			name: "progress keeps the last detail",
			msgs: []tea.Msg{progressMsg{i: 0, done: 1, total: 4, detail: "round 1"}, progressMsg{i: 0, done: 2, total: 4}},
			check: func(t *testing.T, s stage) {
				assert.Equal(t, 2, s.done)
				assert.Equal(t, 4, s.total)
				assert.Equal(t, "round 1", s.detail)
			},
		},
		{
			name:  "unit",
			msgs:  []tea.Msg{unitMsg{i: 0, unit: "encodes"}},
			check: func(t *testing.T, s stage) { assert.Equal(t, "encodes", s.unit) },
		},
		{
			name:  "panel",
			msgs:  []tea.Msg{panelMsg{i: 0, panel: LadderPanel{}}},
			check: func(t *testing.T, s stage) { assert.Equal(t, LadderPanel{}, s.panel) },
		},
		{
			name: "done",
			msgs: []tea.Msg{startMsg{i: 0}, doneMsg{i: 0, summary: "ok"}},
			check: func(t *testing.T, s stage) {
				assert.Equal(t, stateDone, s.state)
				assert.Equal(t, "ok", s.summary)
				assert.False(t, s.ended.Before(s.started))
			},
		},
		{
			name: "out of range stages are ignored",
			msgs: []tea.Msg{
				startMsg{i: 1}, progressMsg{i: -1, done: 1, total: 1}, unitMsg{i: 5, unit: "x"},
				panelMsg{i: 5, panel: LadderPanel{}}, doneMsg{i: 2},
			},
			check: func(t *testing.T, s stage) { assert.Equal(t, stage{label: "a", unit: defaultUnit}, s) },
		},
		{
			name:  "unknown messages are ignored",
			msgs:  []tea.Msg{struct{}{}},
			check: func(t *testing.T, s stage) { assert.Equal(t, statePending, s.state) },
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d, _ := newTestDashboard("a")

			for _, msg := range testCase.msgs {
				_, cmd := d.Update(msg)
				assert.Nil(t, cmd)
			}

			testCase.check(t, d.stages[0])
		})
	}
}

func TestDashboardKeys(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		key    tea.KeyMsg
		cancel bool
	}{
		{name: "q", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}, cancel: true},
		{name: "esc", key: tea.KeyMsg{Type: tea.KeyEsc}, cancel: true},
		{name: "ctrl+c", key: tea.KeyMsg{Type: tea.KeyCtrlC}, cancel: true},
		{name: "other keys", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d, canceled := newTestDashboard("a")

			_, cmd := d.Update(testCase.key)
			assert.Nil(t, cmd, "the dashboard waits for the job to return")
			assert.Equal(t, testCase.cancel, *canceled)
			assert.Equal(t, testCase.cancel, d.canceling)
			assert.Equal(t, testCase.cancel, strings.Contains(d.View(), "cancelling…"))
		})
	}
}

func TestDashboardWindowSize(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		width int
		want  int
	}{
		{name: "narrow", width: 30, want: minDashboardWidth},
		{name: "regular", width: 90, want: 90},
		{name: "wide", width: 300, want: maxDashboardWidth},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d, _ := newTestDashboard("a")
			d.Update(tea.WindowSizeMsg{Width: testCase.width, Height: 40})
			assert.Equal(t, testCase.want, d.width)
		})
	}
}

func TestDashboardTicks(
	t *testing.T,
) {
	d, _ := newTestDashboard("a")
	require.NotNil(t, d.Init())

	_, cmd := d.Update(spinner.TickMsg{ID: d.spinner.ID()})
	assert.NotNil(t, cmd, "the spinner keeps turning")

	at := time.Now().Add(time.Minute)
	_, cmd = d.Update(tickMsg(at))
	assert.NotNil(t, cmd, "clocks keep ticking while running")
	assert.Equal(t, at, d.now)

	d.Update(finishMsg{})
	_, cmd = d.Update(tickMsg(at))
	assert.Nil(t, cmd, "ticking stops once finished")
}

// TestDashboardView checks every state of a stage line, and that the header
// and panel boxes line up.
func TestDashboardView(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		setup func(d *dashboard)
		want  []string
	}{
		{
			name:  "pending",
			setup: func(*dashboard) {},
			want:  []string{"○  Frame analysis       waiting"},
		},
		{
			name: "running without a total shows the elapsed time",
			setup: func(d *dashboard) {
				d.stages[0] = stage{label: "Frame analysis", state: stateRunning, started: d.now.Add(-1500 * time.Millisecond), detail: "probing"}
			},
			want: []string{"Frame analysis         1.5s  probing"},
		},
		{
			name: "running with a total shows rate and ETA",
			setup: func(d *dashboard) {
				d.stages[0] = stage{label: "Frame analysis", state: stateRunning, unit: defaultUnit, done: 250, total: 1000, started: d.now.Add(-10 * time.Second)}
			},
			want: []string{" 25%  250/1000 frames · 25 fps · ETA 00:30"},
		},
		{
			name: "rate is only shown for frames, detail follows",
			setup: func(d *dashboard) {
				d.stages[0] = stage{label: "Ladder", state: stateRunning, unit: "encodes", done: 3, total: 12, detail: "probe encodes", started: d.now.Add(-30 * time.Second)}
			},
			want: []string{" 25%  3/12 encodes · ETA 01:30 · probe encodes"},
		},
		{
			name: "no rate while starting, no ETA when complete",
			setup: func(d *dashboard) {
				d.stages[0] = stage{label: "VMAF", state: stateRunning, unit: defaultUnit, done: 10, total: 10, started: d.now}
			},
			want: []string{"100%  10/10 frames\n"},
		},
		{
			name: "done",
			setup: func(d *dashboard) {
				d.stages[0] = stage{label: "Inspect", state: stateDone, summary: "h264", started: d.now, ended: d.now.Add(142 * time.Millisecond)}
			},
			want: []string{"✓  Inspect               142ms  h264"},
		},
		{
			name: "failed",
			setup: func(d *dashboard) {
				d.stages[0] = stage{label: "VMAF", state: stateFailed, started: d.now, ended: d.now.Add(2 * time.Second)}
			},
			want: []string{"✗  VMAF                     2s  interrupted"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d, _ := newTestDashboard("Frame analysis")
			testCase.setup(d)

			view := plain(d.View())
			for _, want := range testCase.want {
				assert.Contains(t, view, want)
			}
		})
	}
}

func TestDashboardHeader(
	t *testing.T,
) {
	d, _ := newTestDashboard("a")
	d.started = d.now.Add(-83 * time.Second)

	for _, width := range []int{minDashboardWidth, defaultDashboardWidth, maxDashboardWidth} {
		d.width = width
		header := plain(d.header())

		assert.True(t, strings.HasPrefix(header, " ◆ qc  run  ·  clip.mp4"), header)
		assert.True(t, strings.HasSuffix(header, "⏱ 01:23"), header)
		assert.Equal(t, width-1, widths([]string{header})[0], "the clock lines up with the panel border")
	}
}

// emptyPanel renders nothing.
type emptyPanel struct{}

func (emptyPanel) View(int) string { return "" }

func TestDashboardPanels(
	t *testing.T,
) {
	probes := LadderPanel{Stage: ladder.StageProbe, Probes: sampleProbes()}
	vmafPanel := VMAFPanel{Progress: quality.Progress{Estimated: true, Mean: 80, HalfWidth: 0.5, Round: 1, FramesScored: 10, FramesTotal: 100}}

	testCases := []struct {
		name   string
		width  int
		panels []Panel
		boxes  int
	}{
		{name: "vmaf", width: defaultDashboardWidth, panels: []Panel{vmafPanel}, boxes: 1},
		{name: "probes, narrow", width: minDashboardWidth, panels: []Panel{probes}, boxes: 1},
		{name: "two running stages", width: maxDashboardWidth, panels: []Panel{vmafPanel, probes}, boxes: 2},
		{name: "empty panels are not framed", width: defaultDashboardWidth, panels: []Panel{emptyPanel{}}, boxes: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			labels := make([]string, len(testCase.panels))
			for i := range labels {
				labels[i] = "stage"
			}

			d, _ := newTestDashboard(labels...)
			d.width = testCase.width

			for i, p := range testCase.panels {
				d.Update(startMsg{i: i})
				d.Update(panelMsg{i: i, panel: p})
			}

			lines := plainLines(d.View())
			assert.Len(t, linesStarting(lines, "╭"), testCase.boxes)

			box := linesStarting(lines, "╭", "│", "╰")
			for _, w := range widths(box) {
				assert.Equal(t, testCase.width, w, "boxes and their margins span the dashboard width")
			}

			for _, l := range box {
				assert.True(t, strings.HasPrefix(l, " "), "boxes have a one-column margin")
			}
		})
	}
}

func TestGradientBar(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		ratio  float64
		width  int
		filled int
	}{
		{name: "empty", ratio: 0, width: 20, filled: 0},
		{name: "half", ratio: 0.5, width: 20, filled: 10},
		{name: "full", ratio: 1, width: 20, filled: 20},
		{name: "single column", ratio: 1, width: 1, filled: 1},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			bar := gradientBar(testCase.ratio, testCase.width)

			assert.Equal(t, strings.Repeat("━", testCase.width), plain(bar))
			assert.Equal(t, testCase.width-testCase.filled, strings.Count(bar, Subtle.Render("━")), "the rest of the bar is subtle")
		})
	}
}

func TestFormatClock(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "zero", in: 0, want: "00:00"},
		{name: "rounds", in: 59_600 * time.Millisecond, want: "01:00"},
		{name: "long", in: 75 * time.Minute, want: "75:00"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, formatClock(testCase.in))
		})
	}
}

// recorder collects the messages of an Emitter, with a controlled clock.
type recorder struct {
	mu   sync.Mutex
	msgs []tea.Msg
	now  time.Time
}

func (r *recorder) emitter() *Emitter {
	e := newEmitter(func(msg tea.Msg) {
		r.mu.Lock()
		defer r.mu.Unlock()

		r.msgs = append(r.msgs, msg)
	})
	e.now = func() time.Time { return r.now }

	return e
}

func TestEmitter(
	t *testing.T,
) {
	r := &recorder{now: time.Now()}
	e := r.emitter()

	e.Start(0)
	e.Unit(0, "encodes")
	e.Panel(0, LadderPanel{})
	e.Done(0, "ok")

	assert.Equal(t, []tea.Msg{
		startMsg{i: 0}, unitMsg{i: 0, unit: "encodes"}, panelMsg{i: 0, panel: LadderPanel{}}, doneMsg{i: 0, summary: "ok"},
	}, r.msgs)
}

func TestEmitterThrottlesProgress(
	t *testing.T,
) {
	r := &recorder{now: time.Now()}
	e := r.emitter()

	e.Progress(0, 1, 10, "")
	e.Progress(0, 2, 10, "") // too soon
	e.Progress(1, 1, 10, "") // stages are throttled independently

	r.now = r.now.Add(throttle)
	e.Progress(0, 3, 10, "")

	r.now = r.now.Add(time.Millisecond)
	e.Progress(0, 10, 10, "") // the final update always goes through

	assert.Equal(t, []tea.Msg{
		progressMsg{i: 0, done: 1, total: 10},
		progressMsg{i: 1, done: 1, total: 10},
		progressMsg{i: 0, done: 3, total: 10},
		progressMsg{i: 0, done: 10, total: 10},
	}, r.msgs)
}

func TestZeroEmitterDiscards(
	t *testing.T,
) {
	var e Emitter

	assert.NotPanics(t, func() {
		e.Start(0)
		e.Unit(0, "encodes")
		e.Progress(0, 1, 2, "")
		e.Panel(0, VMAFPanel{})
		e.Done(0, "ok")
	})
}

func TestRunDashboardWithoutTerminal(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		jobErr  error
		wantErr bool
	}{
		{name: "success"},
		{name: "job error", jobErr: errors.New("boom"), wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			called := false

			err := RunDashboard(context.Background(), nil, false, "run", "x", []string{"a"}, func(_ context.Context, e *Emitter) error {
				e.Start(0)
				e.Progress(0, 1, 2, "")
				e.Done(0, "ok")

				called = true

				return testCase.jobErr
			})

			assert.True(t, called)
			assert.Equal(t, testCase.wantErr, err != nil)
		})
	}
}

// syncBuffer is a goroutine-safe output for the program.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(
	p []byte,
) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestRunDashboardInteractive(
	t *testing.T,
) {
	boom := errors.New("boom")

	testCases := []struct {
		name string
		// input feeds the program; the job gets a function to press keys.
		job     func(ctx context.Context, e *Emitter, press func(string)) error
		input   func(r io.Reader) io.Reader
		ctx     func() (context.Context, context.CancelFunc)
		wantErr error
		want    []string
	}{
		{
			name: "job completes",
			job: func(_ context.Context, e *Emitter, _ func(string)) error {
				e.Start(0)
				e.Progress(0, 10, 10, "")
				e.Done(0, "all good")

				return nil
			},
			want: []string{"all good"},
		},
		{
			name:    "job error",
			job:     func(context.Context, *Emitter, func(string)) error { return boom },
			wantErr: boom,
		},
		{
			name: "q cancels the job and waits for it",
			job: func(ctx context.Context, e *Emitter, press func(string)) error {
				e.Start(0)
				press("q")
				<-ctx.Done()

				return ctx.Err()
			},
			wantErr: context.Canceled,
			want:    []string{"interrupted"},
		},
		{
			name: "caller cancellation",
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				return ctx, cancel
			},
			job: func(ctx context.Context, _ *Emitter, _ func(string)) error {
				<-ctx.Done()

				return ctx.Err()
			},
			wantErr: context.Canceled,
		},
		{
			name:  "terminal failure stops the job",
			input: func(io.Reader) io.Reader { return iotest.ErrReader(boom) },
			job: func(ctx context.Context, _ *Emitter, _ func(string)) error {
				<-ctx.Done()

				return nil
			},
			wantErr: boom,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if testCase.ctx != nil {
				ctx, cancel = testCase.ctx()
			}

			defer cancel()

			keys, keyboard := io.Pipe()
			defer keyboard.Close()

			var input io.Reader = keys
			if testCase.input != nil {
				input = testCase.input(keys)
			}

			press := func(k string) {
				go func() { _, _ = keyboard.Write([]byte(k)) }()
			}

			var out syncBuffer

			err := runDashboard(ctx, "run", "clip.mp4", []string{"Stage"},
				func(ctx context.Context, e *Emitter) error {
					// Closing the keyboard ends the program's read loop at once.
					defer keyboard.Close()

					return testCase.job(ctx, e, press)
				},
				tea.WithInput(input), tea.WithOutput(&out), tea.WithoutSignalHandler())

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
			}

			for _, want := range testCase.want {
				assert.Contains(t, plain(out.String()), want)
			}
		})
	}
}
