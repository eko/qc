package tui

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type stageState int

const (
	statePending stageState = iota
	stateRunning
	stateDone
	stateFailed
)

const (
	// throttle bounds progress messages per stage: faster updates would
	// only cost redraws.
	throttle = 50 * time.Millisecond
	// tickInterval refreshes the clocks of the dashboard.
	tickInterval = 100 * time.Millisecond

	// defaultUnit is what stage progress counts unless told otherwise; the
	// dashboard shows a rate only for frames.
	defaultUnit = "frames"

	// Dashboard width bounds: below the minimum, progress lines wrap; above
	// the maximum they are hard to follow.
	defaultDashboardWidth = 100
	minDashboardWidth     = 60
	maxDashboardWidth     = 120

	// labelWidth is the width of the stage label column.
	labelWidth = 20
	// Progress bar width bounds, and the room left for the statistics that
	// follow it.
	minBarWidth   = 10
	maxBarWidth   = 36
	barStatsWidth = 60
	// panelInset is the width taken by the panel's margin, border and
	// padding on each side (1 + 1 + 1).
	panelInset = 3
)

type stage struct {
	label   string
	state   stageState
	summary string
	detail  string
	unit    string
	done    int
	total   int
	started time.Time
	ended   time.Time
	panel   Panel
}

type (
	startMsg struct{ i int }
	doneMsg  struct {
		i       int
		summary string
	}
	progressMsg struct {
		i           int
		done, total int
		detail      string
	}
	unitMsg struct {
		i    int
		unit string
	}
	panelMsg struct {
		i     int
		panel Panel
	}
	finishMsg struct{ err error }
	tickMsg   time.Time
)

// dashboard is the bubbletea model of a running job: one line per stage and
// the live panel of the running stages.
type dashboard struct {
	title     string
	subject   string
	stages    []stage
	spinner   spinner.Model
	width     int
	started   time.Time
	now       time.Time
	finished  bool
	err       error
	cancel    context.CancelFunc
	canceling bool
}

func newDashboard(
	title, subject string,
	labels []string,
	cancel context.CancelFunc,
) *dashboard {
	stages := make([]stage, len(labels))
	for i, l := range labels {
		stages[i] = stage{label: l, unit: defaultUnit}
	}

	// spinner.Dot frames carry a trailing space; these keep columns aligned.
	dots := spinner.Spinner{Frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}, FPS: time.Second / 10}
	sp := spinner.New(spinner.WithSpinner(dots), spinner.WithStyle(Accent))
	now := time.Now()

	return &dashboard{
		title:   title,
		subject: subject,
		stages:  stages,
		spinner: sp,
		width:   defaultDashboardWidth,
		started: now,
		now:     now,
		cancel:  cancel,
	}
}

// Init implements tea.Model.
func (d *dashboard) Init() tea.Cmd {
	return tea.Batch(d.spinner.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// stage returns stage i, or nil when out of range: a wrong index from a
// hook must not bring the dashboard down.
func (d *dashboard) stage(
	i int,
) *stage {
	if i < 0 || i >= len(d.stages) {
		return nil
	}

	return &d.stages[i]
}

// Update implements tea.Model.
func (d *dashboard) Update(
	msg tea.Msg,
) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		d.handleKey(m)
	case tea.WindowSizeMsg:
		d.width = min(max(m.Width, minDashboardWidth), maxDashboardWidth)
	case spinner.TickMsg:
		var cmd tea.Cmd
		d.spinner, cmd = d.spinner.Update(m)

		return d, cmd
	case tickMsg:
		d.now = time.Time(m)
		if !d.finished {
			return d, tick()
		}
	case finishMsg:
		d.finish(m.err)

		return d, tea.Quit
	default:
		d.updateStage(msg)
	}

	return d, nil
}

// handleKey cancels the job on q, esc or ctrl+c. The dashboard keeps
// running until the job returns, to show where it stopped.
func (d *dashboard) handleKey(
	m tea.KeyMsg,
) {
	switch m.String() {
	case "q", "esc", "ctrl+c":
		d.canceling = true
		d.cancel()
	}
}

// updateStage applies a stage event.
func (d *dashboard) updateStage(
	msg tea.Msg,
) {
	switch m := msg.(type) {
	case startMsg:
		if s := d.stage(m.i); s != nil {
			s.state, s.started = stateRunning, time.Now()
		}
	case progressMsg:
		if s := d.stage(m.i); s != nil {
			s.done, s.total = m.done, m.total
			if m.detail != "" {
				s.detail = m.detail
			}
		}
	case unitMsg:
		if s := d.stage(m.i); s != nil {
			s.unit = m.unit
		}
	case panelMsg:
		if s := d.stage(m.i); s != nil {
			s.panel = m.panel
		}
	case doneMsg:
		if s := d.stage(m.i); s != nil {
			s.state, s.summary, s.ended = stateDone, m.summary, time.Now()
		}
	}
}

// finish records the end of the job: stages still running were interrupted.
func (d *dashboard) finish(
	err error,
) {
	d.finished, d.err, d.now = true, err, time.Now()

	for i := range d.stages {
		if s := &d.stages[i]; s.state == stateRunning {
			s.state, s.ended = stateFailed, d.now
		}
	}
}

// View implements tea.Model.
func (d *dashboard) View() string {
	var b strings.Builder

	b.WriteString("\n" + d.header() + "\n\n")

	for i := range d.stages {
		b.WriteString(d.stageLine(&d.stages[i]) + "\n")
	}

	if d.finished {
		return b.String()
	}

	for _, s := range d.stages {
		if s.state == stateRunning && s.panel != nil {
			if panel := d.panelBox(s.panel); panel != "" {
				b.WriteString("\n" + panel + "\n")
			}
		}
	}

	// Key hints as in the wizard: the key, then what it does, dimmer.
	hint := Bold.Render("q") + Subtle.Render(" quit")
	if d.canceling {
		hint = Subtle.Render("cancelling…")
	}

	b.WriteString("\n " + hint + "\n")

	return b.String()
}

// header shows the job and its subject, and the elapsed time aligned with
// the right border of the panels.
func (d *dashboard) header() string {
	head := " " + title.Render(brand) + Subtle.Render("  "+d.title+"  ·  ") + Bold.Render(d.subject)
	clock := Subtle.Render("⏱ " + formatClock(d.now.Sub(d.started)))
	gap := max(1, d.width-1-lipgloss.Width(head)-lipgloss.Width(clock))

	return head + strings.Repeat(" ", gap) + clock
}

// panelBox frames a live panel across the dashboard width.
func (d *dashboard) panelBox(
	p Panel,
) string {
	view := p.View(d.width - 2*panelInset)
	if view == "" {
		return ""
	}

	// lipgloss widths include the padding but not the border and margin.
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(subtle).
		Padding(0, 1).Margin(0, 1).Width(d.width - 4).Render(view)
}

func (d *dashboard) stageLine(
	s *stage,
) string {
	label := lipgloss.NewStyle().Width(labelWidth).Render(s.label)

	switch s.state {
	case statePending:
		return "  " + Subtle.Render("○  "+label+" waiting")
	case stateDone:
		return "  " + Green.Render("✓") + "  " + label + stageDuration(s.ended.Sub(s.started)) + s.summary
	case stateFailed:
		return "  " + Red.Render("✗") + "  " + label + stageDuration(s.ended.Sub(s.started)) + Red.Render("interrupted")
	}

	line := "  " + d.spinner.View() + "  " + label
	running := d.now.Sub(s.started)

	if s.total <= 0 {
		return line + stageDuration(running.Round(tickInterval)) + s.detail
	}

	return line + d.progress(s, running)
}

// progress is the bar and statistics of a stage with a known total.
func (d *dashboard) progress(
	s *stage,
	running time.Duration,
) string {
	ratio := math.Min(1, float64(s.done)/float64(s.total))
	barWidth := max(minBarWidth, min(maxBarWidth, d.width-labelWidth-barStatsWidth))

	eta := ""
	if s.done > 0 && ratio < 1 {
		remaining := time.Duration(float64(running) * (1 - ratio) / ratio)
		eta = " · ETA " + formatClock(remaining)
	}

	// A rate over the first half second would mostly measure start-up.
	rate := ""
	if secs := running.Seconds(); secs > 0.5 && s.unit == defaultUnit {
		rate = fmt.Sprintf(" · %.0f fps", float64(s.done)/secs)
	}

	stats := Subtle.Render(fmt.Sprintf(" %3.0f%%  %d/%d %s%s%s", ratio*100, s.done, s.total, s.unit, rate, eta))
	if s.detail != "" {
		stats += Subtle.Render(" · ") + s.detail
	}

	return GradientBar(ratio, barWidth) + stats
}

func stageDuration(
	d time.Duration,
) string {
	return Subtle.Render(fmt.Sprintf("%7s  ", Elapsed(d)))
}

// GradientBar draws a thin bar filled with the brand's orange gradient: the
// dashboard's progress bars, and the wizard's progress rail.
func GradientBar(
	ratio float64,
	width int,
) string {
	filled := int(math.Round(ratio * float64(width)))

	var b strings.Builder

	for i := range width {
		if i >= filled {
			b.WriteString(Subtle.Render("━"))

			continue
		}

		t := float64(i) / float64(max(width-1, 1))
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(orangeAt(t))).Render("━"))
	}

	return b.String()
}

// mix interpolates linearly between two RGB colours.
func mix(
	from, to [3]float64,
	t float64,
) string {
	var c [3]int
	for i := range c {
		c[i] = int(math.Round(from[i] + (to[i]-from[i])*t))
	}

	return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2])
}

func formatClock(
	d time.Duration,
) string {
	d = d.Round(time.Second)

	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// Emitter forwards events of a running job to the dashboard. The zero value
// (no terminal) discards them. It is safe for concurrent use.
type Emitter struct {
	send func(tea.Msg)
	now  func() time.Time

	mu   sync.Mutex
	last map[int]time.Time
}

func newEmitter(
	send func(tea.Msg),
) *Emitter {
	return &Emitter{send: send, now: time.Now, last: map[int]time.Time{}}
}

// Start marks stage i as running.
func (e *Emitter) Start(
	i int,
) {
	e.emit(startMsg{i: i})
}

// Done marks stage i as done with a one-line summary.
func (e *Emitter) Done(
	i int,
	summary string,
) {
	e.emit(doneMsg{i: i, summary: summary})
}

// Unit sets the unit counted by stage i's progress (default "frames").
func (e *Emitter) Unit(
	i int,
	unit string,
) {
	e.emit(unitMsg{i: i, unit: unit})
}

// Progress updates stage i. Updates are throttled, except the final one so
// that a stage always ends at 100%.
func (e *Emitter) Progress(
	i, done, total int,
	detail string,
) {
	if e.send == nil || !e.due(i, done >= total) {
		return
	}

	e.emit(progressMsg{i: i, done: done, total: total, detail: detail})
}

// due reports whether an update of stage i may be sent now.
func (e *Emitter) due(
	i int,
	final bool,
) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := e.now()
	if !final && now.Sub(e.last[i]) < throttle {
		return false
	}

	e.last[i] = now

	return true
}

// Panel replaces the live panel of stage i.
func (e *Emitter) Panel(
	i int,
	p Panel,
) {
	e.emit(panelMsg{i: i, panel: p})
}

func (e *Emitter) emit(
	msg tea.Msg,
) {
	if e.send != nil {
		e.send(msg)
	}
}

// RunDashboard runs job while drawing a live dashboard of labelled stages on
// out (a terminal). Pressing q cancels the job's context; the dashboard then
// waits for the job to return. When interactive is false the job runs
// without any drawing.
func RunDashboard(
	ctx context.Context,
	out io.Writer,
	interactive bool,
	name, subject string,
	labels []string,
	job func(ctx context.Context, e *Emitter) error,
) error {
	if !interactive {
		return job(ctx, &Emitter{})
	}

	return runDashboard(ctx, name, subject, labels, job, tea.WithOutput(out))
}

// runDashboard runs job under a dashboard program built with opts (tests
// provide their own input).
func runDashboard(
	ctx context.Context,
	name, subject string,
	labels []string,
	job func(ctx context.Context, e *Emitter) error,
	opts ...tea.ProgramOption,
) error {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The program follows the caller's context, not the job's: after q it
	// keeps drawing until the job has actually stopped.
	program := tea.NewProgram(newDashboard(name, subject, labels, cancel), append(opts, tea.WithContext(ctx))...)
	jobDone := make(chan error, 1)

	go func() {
		err := job(jobCtx, newEmitter(program.Send))
		jobDone <- err

		program.Send(finishMsg{err: err})
	}()

	_, runErr := program.Run()

	// The program may also end on its own (terminal error, caller's context):
	// stop the job and wait for it, so that the caller never reads results the
	// job is still writing.
	cancel()

	jobErr := <-jobDone
	if runErr != nil && ctx.Err() == nil {
		return fmt.Errorf("dashboard: %w", runErr)
	}

	return jobErr
}
