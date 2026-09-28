package defect

// mixState tracks the silence of the whole mix: the windows where every
// channel is silent.
type mixState struct {
	run      run
	silences []span
	// leading is the silence at the start, set once the first sound comes
	// (or at the end of a silent signal); started tells it came.
	leading       int64
	started       bool
	activeWindows int64
}

// closeSilenceWindow ends a 10 ms window for the silence detection: a
// channel is silent in it when its peak stays at or below the threshold,
// the mix when every channel is.
func (d *Detector) closeSilenceWindow() {
	from, to := d.windowStart, d.pos
	d.windowStart = d.pos
	d.windowCount++

	mixSilent := true
	for c := range d.channels {
		mixSilent = mixSilent && d.channels[c].windowPeak <= d.silence
	}

	minLength := d.samples(d.opts.SilenceDuration.Seconds())
	d.mix.update(mixSilent, from, to, minLength)

	for c := range d.channels {
		ch := &d.channels[c]
		silent := ch.windowPeak <= d.silence
		ch.peak = max(ch.peak, ch.windowPeak)
		ch.windowPeak = 0

		if silent {
			ch.silentWindows++
		}

		// A channel's own silence only counts while the mix plays.
		if silent && !mixSilent {
			ch.silentActive++
			ch.silence.extend(from, to)

			continue
		}

		if s, ok := ch.silence.close(minLength); ok {
			ch.silences = append(ch.silences, s)
		}
	}
}

// update adds a window [from, to) to the mix's silence.
func (m *mixState) update(
	silent bool,
	from, to int64,
	minLength int64,
) {
	if silent {
		m.run.extend(from, to)

		return
	}

	m.activeWindows++

	if !m.started {
		m.started = true
		m.leading = from
	}

	if s, ok := m.run.close(minLength); ok {
		m.silences = append(m.silences, s)
	}
}

// samples converts seconds into a number of samples.
func (d *Detector) samples(
	seconds float64,
) int64 {
	return int64(seconds * float64(d.rate))
}
