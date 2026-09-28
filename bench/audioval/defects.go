package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/eko/qc/audio"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/audiotest"
	"github.com/eko/qc/media"
	"github.com/eko/qc/probe"
)

// Synthetic programmes of the defect cases.
const (
	rate            = audiotest.Rate
	programmeLength = 60.0
)

// Tolerances on the times of detected defects (seconds): silence is found
// on 10 ms windows, phase on 400 ms ones; AAC shifts edges by its
// transform (2 048 samples, 43 ms).
const (
	edgeTolerance  = 0.02
	phaseTolerance = 0.45
	aacTolerance   = 0.05
)

// maxDC is the DC offset under which a channel is clean (the finding's
// threshold, -50 dBFS).
const maxDC = 0.00316

// defectCase is a programme with defects inserted at known times, and the
// check of what qc finds in it.
type defectCase struct {
	name     string
	inserted string
	mask     uint32
	signal   func() [][]float32
	// check describes what was detected and whether it is what was
	// inserted, edges within tolerance (seconds).
	check func(t *audio.Track, tolerance float64) (string, bool)
}

// stereo is a 60 s programme with the given defects applied.
func stereo(
	seed uint64,
	apply func(ch [][]float32),
) func() [][]float32 {
	return func() [][]float32 {
		ch := audiotest.Programme(rate, programmeLength, seed)
		apply(ch)

		return ch
	}
}

func defectCases() []defectCase {
	return []defectCase{
		{name: "clean", inserted: "nothing", mask: audiotest.MaskStereo, signal: stereo(1, func([][]float32) {}), check: clean},
		{
			name: "silence", inserted: "silence 20.0–23.0 s", mask: audiotest.MaskStereo,
			signal: stereo(2, func(ch [][]float32) { muteAll(ch, 20, 23) }),
			check:  func(t *audio.Track, tol float64) (string, bool) { return intervals(t.Defects.Silence, 20, 23, tol) },
		},
		{
			name: "edges", inserted: "silence 0–2.5 s and 57–60 s", mask: audiotest.MaskStereo,
			signal: stereo(3, func(ch [][]float32) { muteAll(ch, 0, 2.5); muteAll(ch, 57, 60) }),
			check:  edges,
		},
		{
			name: "muted", inserted: "FR muted", mask: audiotest.MaskStereo,
			signal: stereo(4, func(ch [][]float32) { audiotest.Mute(ch[1], rate, 0, programmeLength) }),
			check:  muted(1),
		},
		{
			name: "dropout", inserted: "FL silent 30.0–34.0 s", mask: audiotest.MaskStereo,
			signal: stereo(5, func(ch [][]float32) { audiotest.Mute(ch[0], rate, 30, 34) }),
			check: func(t *audio.Track, tol float64) (string, bool) {
				return intervals(t.Defects.Channels[0].Silence, 30, 34, tol)
			},
		},
		{
			name: "clipping", inserted: "×8 and clipped 40.0–40.5 s", mask: audiotest.MaskStereo,
			signal: stereo(6, func(ch [][]float32) {
				for _, c := range ch {
					audiotest.Clip(c, rate, 40, 40.5, 8)
				}
			}),
			check: clipped,
		},
		{
			name: "phase", inserted: "FR inverted 10.0–20.0 s", mask: audiotest.MaskStereo,
			signal: stereo(7, func(ch [][]float32) { audiotest.Invert(ch[1], rate, 10, 20) }),
			check: func(t *audio.Track, tol float64) (string, bool) {
				return intervals(t.Defects.Pairs[0].OutOfPhase, 10, 20, max(tol, phaseTolerance))
			},
		},
		{
			name: "polarity", inserted: "FR inverted throughout", mask: audiotest.MaskStereo,
			signal: stereo(8, func(ch [][]float32) { audiotest.Invert(ch[1], rate, 0, programmeLength) }),
			check:  inverted,
		},
		{
			name: "mono", inserted: "FR = FL", mask: audiotest.MaskStereo,
			signal: stereo(9, func(ch [][]float32) { copy(ch[1], ch[0]) }),
			check:  identical,
		},
		{
			name: "dc", inserted: "DC +0.01 on FL", mask: audiotest.MaskStereo,
			signal: stereo(10, func(ch [][]float32) { audiotest.Offset(ch[0], 0.01) }),
			check:  offset,
		},
		{name: "lfe", inserted: "5.1, LFE empty", mask: audiotest.Mask51, signal: surround, check: muted(3)},
	}
}

// muteAll silences every channel over [from, to) seconds.
func muteAll(
	ch [][]float32,
	from, to float64,
) {
	for _, c := range ch {
		audiotest.Mute(c, rate, from, to)
	}
}

// surround is a 5.1 programme (FL FR FC LFE BL BR) without low-frequency
// effects: the front pair, the centre, silence, and the surround pair
// 6 dB down.
func surround() [][]float32 {
	front := audiotest.Programme(rate, programmeLength, 11)
	back := audiotest.Programme(rate, programmeLength, 12)
	centre := make([]float32, len(front[0]))

	for i := range centre {
		centre[i] = (front[0][i] + front[1][i]) / 2
		back[0][i] /= 2
		back[1][i] /= 2
	}

	return [][]float32{front[0], front[1], centre, make([]float32, len(centre)), back[0], back[1]}
}

// defects runs every defect case, on WAV and with -aac on AAC, and prints
// the table; ok is false when a check fails.
func defects(
	ctx context.Context,
	cfg config,
	w io.Writer,
) (bool, error) {
	fmt.Fprintln(w, "## Synthetic defects")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| case | inserted | codec | detected | |")
	fmt.Fprintln(w, "|---|---|---|---|---|")

	ok := true

	for _, c := range defectCases() {
		if !cfg.selected(c.name) {
			continue
		}

		wav := filepath.Join(cfg.dir, "defect-"+c.name+".wav")
		if err := os.WriteFile(wav, audiotest.WAV(rate, c.mask, c.signal()), 0o600); err != nil {
			return false, fmt.Errorf("%s: %w", c.name, err)
		}

		files := []struct{ codec, path string }{{"PCM", wav}}

		if cfg.aac {
			m4a := strings.TrimSuffix(wav, ".wav") + ".m4a"
			if err := encodeAAC(ctx, cfg.ffmpeg, wav, m4a); err != nil {
				return false, err
			}

			files = append(files, struct{ codec, path string }{"AAC 256k", m4a})
		}

		for _, f := range files {
			track, err := analyse(ctx, cfg, f.path)
			if err != nil {
				return false, err
			}

			tolerance := edgeTolerance
			if f.codec != "PCM" {
				tolerance = aacTolerance
			}

			detected, pass := c.check(track, tolerance)
			ok = ok && pass

			fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", c.name, c.inserted, f.codec, detected, verdict(pass))
		}
	}

	fmt.Fprintln(w)

	return ok, nil
}

// analyse decodes the first audio stream of path with ffmpeg and analyses
// it, as qc analyze does.
func analyse(
	ctx context.Context,
	cfg config,
	path string,
) (*audio.Track, error) {
	info, err := probe.NewFFprobe(cfg.ffprobe).ProbeStreams(ctx, path)
	if err != nil {
		return nil, err
	}

	if len(info.Audio) == 0 {
		return nil, fmt.Errorf("%s: no audio stream", path)
	}

	stream := info.Audio[0]
	analyzer := audio.NewAnalyzer(stream, audio.Options{})
	req := decode.AudioRequest{Path: path, Stream: stream.Index, SampleRate: stream.SampleRate, Channels: stream.Channels}

	if err := decode.NewFFmpeg(cfg.ffmpeg, 0).DecodeAudio(ctx, req, analyzer.Add); err != nil {
		return nil, err
	}

	track := analyzer.Result(0)

	return &track, nil
}

// clean passes when nothing is detected.
func clean(
	t *audio.Track,
	_ float64,
) (string, bool) {
	var found []string

	d := t.Defects
	if len(d.Silence) > 0 {
		found = append(found, "silence")
	}

	for i, c := range d.Channels {
		if c.Muted || len(c.Silence) > 0 || c.ClippedSamples > 0 || math.Abs(c.DC) > maxDC {
			found = append(found, t.Channel(i))
		}
	}

	for _, p := range d.Pairs {
		if p.Identical || p.Inverted || len(p.OutOfPhase) > 0 {
			found = append(found, "phase")
		}
	}

	if len(found) > 0 {
		return strings.Join(found, ", "), false
	}

	return fmt.Sprintf("nothing (%.1f LUFS, correlation %.2f)", t.Loudness.Integrated, d.Pairs[0].Correlation), true
}

// intervals passes when list is the single interval [from, to) within
// tolerance.
func intervals(
	list []media.Interval,
	from, to, tolerance float64,
) (string, bool) {
	parts := make([]string, len(list))
	for i, iv := range list {
		parts[i] = fmt.Sprintf("%.3f–%.3f s", iv.Start.Seconds(), iv.End.Seconds())
	}

	if len(list) == 0 {
		return "nothing", false
	}

	pass := len(list) == 1 && math.Abs(list[0].Start.Seconds()-from) <= tolerance && math.Abs(list[0].End.Seconds()-to) <= tolerance

	return strings.Join(parts, ", "), pass
}

// edges passes on a 2.5 s leading and a 3 s trailing silence.
func edges(
	t *audio.Track,
	tolerance float64,
) (string, bool) {
	lead, trail := t.Defects.LeadingSilence.Seconds(), t.Defects.TrailingSilence.Seconds()
	pass := math.Abs(lead-2.5) <= tolerance && math.Abs(trail-3) <= tolerance && len(t.Defects.Silence) == 2

	return fmt.Sprintf("leading %.3f s, trailing %.3f s", lead, trail), pass
}

// muted passes when channel c, and only it, is muted.
func muted(
	c int,
) func(*audio.Track, float64) (string, bool) {
	return func(t *audio.Track, _ float64) (string, bool) {
		var found []string

		for i, ch := range t.Defects.Channels {
			if ch.Muted {
				found = append(found, t.Channel(i)+" muted")
			}
		}

		return strings.Join(found, ", "), len(found) == 1 && t.Defects.Channels[c].Muted
	}
}

// clipped passes when both channels clip within 40.0–40.5 s.
func clipped(
	t *audio.Track,
	tolerance float64,
) (string, bool) {
	var parts []string

	pass := true

	for i, c := range t.Defects.Channels {
		if len(c.Clipping) == 0 {
			parts = append(parts, t.Channel(i)+": nothing")
			pass = false

			continue
		}

		first, last := c.Clipping[0].Start.Seconds(), c.Clipping[len(c.Clipping)-1].End.Seconds()
		parts = append(parts, fmt.Sprintf("%s: %d samples in %d runs, %.3f–%.3f s", t.Channel(i), c.ClippedSamples, c.ClipEvents, first, last))
		pass = pass && first >= 40-tolerance && last <= 40.5+tolerance
	}

	return strings.Join(parts, "; "), pass
}

// inverted passes on an inverted pair.
func inverted(
	t *audio.Track,
	_ float64,
) (string, bool) {
	p := t.Defects.Pairs[0]

	return fmt.Sprintf("correlation %.2f, inverted %v", p.Correlation, p.Inverted), p.Inverted
}

// identical passes on identical channels.
func identical(
	t *audio.Track,
	_ float64,
) (string, bool) {
	p := t.Defects.Pairs[0]

	return fmt.Sprintf("difference %.1f dB, identical %v", p.Difference, p.Identical), p.Identical
}

// offset passes when the left channel alone has a DC offset near 0.01.
func offset(
	t *audio.Track,
	_ float64,
) (string, bool) {
	l, r := t.Defects.Channels[0].DC, t.Defects.Channels[1].DC

	return fmt.Sprintf("FL %.4f, FR %.4f", l, r), math.Abs(l-0.01) < 0.001 && math.Abs(r) < maxDC
}
