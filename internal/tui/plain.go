package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// plainInterval is the least time between two progress lines of a stage:
// a log is read afterwards, not watched.
const plainInterval = 10 * time.Second

// RunPlain runs job while writing what a dashboard would show to out as
// plain lines, one per event: no cursor move, no colour, nothing read from
// the keyboard. It is the dashboard of a container, of a CI job, of
// anything whose output is kept as a log:
//
//	qc ladder · source.mp4
//	[1/2] Inspect: started
//	[1/2] Inspect: done in 0.1s · h264 · 1920×1080
//	[2/2] Ladder · h264: started
//	[2/2] Ladder · h264: 6/15 probes (40%) after 31s
//	[2/2] Ladder · h264: done in 1m42s · 6 rungs
//	qc ladder: done in 1m42s
//
// A stage reports its progress every ten seconds at most, and when it
// ends.
func RunPlain(
	ctx context.Context,
	out io.Writer,
	name, subject string,
	labels []string,
	job func(ctx context.Context, e *Emitter) error,
) error {
	p := &plainPrinter{out: out, labels: labels, now: time.Now, stages: make([]plainStage, len(labels))}
	started := p.now()

	p.line("qc %s · %s", name, subject)

	err := job(ctx, newEmitter(p.handle))

	if err != nil {
		p.line("qc %s: failed after %s: %v", name, plainDuration(p.now().Sub(started)), err)
	} else {
		p.line("qc %s: done in %s", name, plainDuration(p.now().Sub(started)))
	}

	return err
}

// plainStage is what the printer remembers of a stage.
type plainStage struct {
	unit    string
	started time.Time
	printed time.Time
	detail  string
}

// plainPrinter turns the events of a job into lines. It is safe for
// concurrent use: stages may report from several goroutines.
type plainPrinter struct {
	out    io.Writer
	labels []string
	now    func() time.Time

	mu     sync.Mutex
	stages []plainStage
}

// line writes one line, without the colours styled summaries may carry.
func (p *plainPrinter) line(
	format string,
	args ...any,
) {
	fmt.Fprintln(p.out, ansi.Strip(fmt.Sprintf(format, args...)))
}

// stageLine writes one line of stage i.
func (p *plainPrinter) stageLine(
	i int,
	format string,
	args ...any,
) {
	p.line("[%d/%d] %s: %s", i+1, len(p.labels), p.labels[i], fmt.Sprintf(format, args...))
}

// handle writes the line of an event, if it deserves one.
func (p *plainPrinter) handle(
	msg tea.Msg,
) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch m := msg.(type) {
	case startMsg:
		if s := p.stage(m.i); s != nil {
			s.started, s.printed = p.now(), p.now()
			p.stageLine(m.i, "started")
		}
	case unitMsg:
		if s := p.stage(m.i); s != nil {
			s.unit = m.unit
		}
	case progressMsg:
		p.progress(m)
	case doneMsg:
		if s := p.stage(m.i); s != nil {
			p.stageLine(m.i, "done in %s%s", plainDuration(p.now().Sub(s.started)), suffix(m.summary))
		}
	case failMsg:
		if s := p.stage(m.i); s != nil {
			p.stageLine(m.i, "failed after %s%s", plainDuration(p.now().Sub(s.started)), suffix(m.reason))
		}
	}
}

// progress writes the progress of a stage when its last line is old
// enough: its count against its total, or what it says it is doing.
func (p *plainPrinter) progress(
	m progressMsg,
) {
	s := p.stage(m.i)
	if s == nil || p.now().Sub(s.printed) < plainInterval {
		return
	}

	elapsed := plainDuration(p.now().Sub(s.started))

	switch {
	case m.total > 0 && m.done < m.total:
		unit := s.unit
		if unit == "" {
			unit = defaultUnit
		}

		p.stageLine(m.i, "%d/%d %s (%d%%) after %s%s", m.done, m.total, unit, 100*m.done/m.total, elapsed, suffix(m.detail))
	case m.total <= 0 && m.detail != "" && m.detail != s.detail:
		p.stageLine(m.i, "%s after %s", m.detail, elapsed)
	default:
		return
	}

	s.printed, s.detail = p.now(), m.detail
}

// stage returns stage i, or nil when there is none.
func (p *plainPrinter) stage(
	i int,
) *plainStage {
	if i < 0 || i >= len(p.stages) {
		return nil
	}

	return &p.stages[i]
}

// suffix is text appended to a line after a separator, or nothing.
func suffix(
	text string,
) string {
	if text = strings.TrimSpace(text); text == "" {
		return ""
	}

	return " · " + text
}

// plainDuration writes a duration as a log reads it: tenths of a second
// under a minute, whole seconds above.
func plainDuration(
	d time.Duration,
) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	return d.Round(time.Second).String()
}
