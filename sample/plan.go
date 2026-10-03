package sample

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/media"
)

// piece is a scene a sample can take: a run of whole GOPs of a video, at
// least Options.Piece long (but for the last of a video).
type piece struct {
	// first is the index of its first frame, a keyframe.
	first, frames int
	start, end    media.Duration
	// si and ti are the mean spatial and temporal information of its
	// frames.
	si, ti float64
}

// length is the duration of the piece.
func (p piece) length() media.Duration {
	return p.end - p.start
}

// score is what the piece costs an encoder, relative to the others: its
// spatial times its temporal information, as the ladder's top digest ranks
// scenes.
func (p piece) score() float64 {
	return p.si * p.ti
}

// pieces cuts the frames of a video into the scenes a sample can take:
// from a keyframe to the first keyframe at least `least` later. Frames
// before the first keyframe are left out; a last piece shorter than least
// joins the one before it.
func pieces(
	frames *analysis.FrameSeries,
	end media.Duration,
	least media.Duration,
) []piece {
	var out []piece

	n := len(frames.PTS)
	open := -1

	closePiece := func(to int) {
		p := piece{first: open, frames: to - open, start: frames.PTS[open], end: end}
		if to < n {
			p.end = frames.PTS[to]
		}

		for i := open; i < to; i++ {
			p.si += frames.SI[i]
			p.ti += frames.TI[i]
		}

		p.si /= float64(p.frames)
		p.ti /= float64(p.frames)
		out = append(out, p)
	}

	for i := range n {
		if !frames.Keyframe[i] {
			continue
		}

		if open >= 0 && frames.PTS[i]-frames.PTS[open] < least {
			continue
		}

		if open >= 0 {
			closePiece(i)
		}

		open = i
	}

	if open < 0 {
		return nil
	}

	closePiece(n)

	// A short tail is not a scene of its own.
	if last := len(out) - 1; last > 0 && out[last].length() < least {
		out[last-1] = join(out[last-1], out[last])
		out = out[:last]
	}

	return out
}

// join merges two consecutive pieces.
func join(
	a, b piece,
) piece {
	frames := a.frames + b.frames

	return piece{
		first: a.first, frames: frames, start: a.start, end: b.end,
		si: (a.si*float64(a.frames) + b.si*float64(b.frames)) / float64(frames),
		ti: (a.ti*float64(a.frames) + b.ti*float64(b.frames)) / float64(frames),
	}
}

// total is the length of pieces.
func total(
	list []piece,
) media.Duration {
	var sum media.Duration
	for _, p := range list {
		sum += p.length()
	}

	return sum
}

// ranked takes pieces in the order given until they are as close to budget
// as they get: a piece is taken when it brings the total closer, so that a
// long scene does not double the sample, and at least one is.
func ranked(
	order []piece,
	budget media.Duration,
) []piece {
	var (
		out []piece
		sum media.Duration
	)

	for _, p := range order {
		if len(out) > 0 && absDuration(budget-sum-p.length()) >= absDuration(budget-sum) {
			continue
		}

		out = append(out, p)
		sum += p.length()
	}

	return out
}

// absDuration is the absolute value of d.
func absDuration(
	d media.Duration,
) media.Duration {
	return max(d, -d)
}

// extreme takes the pieces of pool with the highest scores (or the lowest,
// with easiest set) for about budget.
func extreme(
	pool []piece,
	budget media.Duration,
	easiest bool,
) []piece {
	order := slices.Clone(pool)
	slices.SortStableFunc(order, func(a, b piece) int {
		if easiest {
			return cmp.Compare(a.score(), b.score())
		}

		return cmp.Compare(b.score(), a.score())
	})

	return ranked(order, budget)
}

// representative takes pieces of pool, spread over the video, that
// together have the spatial and temporal information of target for about
// budget. The pool is split into as many runs as pieces are needed; one
// piece is taken in each, first the middle one, then moved, run after run,
// while the frames taken get closer to the target (and their length to
// the budget).
func representative(
	pool []piece,
	budget media.Duration,
	target [2]float64,
) []piece {
	if len(pool) == 0 || budget <= 0 {
		return nil
	}

	mean := total(pool) / media.Duration(len(pool))
	k := min(len(pool), max(1, int(math.Round(float64(budget)/float64(mean)))))

	runs := make([][]piece, k)
	choice := make([]int, k)

	for g := range k {
		runs[g] = pool[g*len(pool)/k : (g+1)*len(pool)/k]
		choice[g] = len(runs[g]) / 2
	}

	spread := spreadOf(pool)
	cost := func() float64 {
		return imbalance(runs, choice, budget, target, spread)
	}

	for range balancePasses {
		moved := false

		for g := range k {
			best, bestCost := choice[g], cost()

			for c := range runs[g] {
				choice[g] = c
				if now := cost(); now < bestCost-costEpsilon {
					best, bestCost, moved = c, now, true
				}
			}

			choice[g] = best
		}

		if !moved {
			break
		}
	}

	out := make([]piece, k)
	for g, c := range choice {
		out[g] = runs[g][c]
	}

	return out
}

// imbalance is how far the scenes chosen in runs are from the target:
// their spatial and temporal information, in spreads, and their length, as
// a share of the budget.
func imbalance(
	runs [][]piece,
	choice []int,
	budget media.Duration,
	target, spread [2]float64,
) float64 {
	var si, ti, frames float64

	var length media.Duration

	for g, c := range choice {
		p := runs[g][c]
		si += p.si * float64(p.frames)
		ti += p.ti * float64(p.frames)
		frames += float64(p.frames)
		length += p.length()
	}

	return math.Abs(si/frames-target[0])/spread[0] + math.Abs(ti/frames-target[1])/spread[1] +
		math.Abs(float64(length-budget))/float64(budget)
}

const (
	// balancePasses bounds the passes over the runs: the search settles in
	// two or three.
	balancePasses = 8
	// costEpsilon ignores gains within rounding.
	costEpsilon = 1e-12
)

// spreadOf is the standard deviation of the spatial and temporal
// information of the pieces of pool, 1 when they do not vary: what a gap
// to the target is measured in.
func spreadOf(
	pool []piece,
) [2]float64 {
	var mean, variance [2]float64

	for _, p := range pool {
		mean[0] += p.si
		mean[1] += p.ti
	}

	n := float64(len(pool))
	mean[0] /= n
	mean[1] /= n

	for _, p := range pool {
		variance[0] += (p.si - mean[0]) * (p.si - mean[0])
		variance[1] += (p.ti - mean[1]) * (p.ti - mean[1])
	}

	out := [2]float64{1, 1}

	for i := range out {
		if sd := math.Sqrt(variance[i] / n); sd > 0 {
			out[i] = sd
		}
	}

	return out
}

// meansOf is the spatial and temporal information of the frames of pieces.
func meansOf(
	list []piece,
) [2]float64 {
	var si, ti, frames float64

	for _, p := range list {
		si += p.si * float64(p.frames)
		ti += p.ti * float64(p.frames)
		frames += float64(p.frames)
	}

	if frames == 0 {
		return [2]float64{}
	}

	return [2]float64{si / frames, ti / frames}
}

// choose takes the scenes of a video for about budget, as opts ask, in the
// order of the video.
func choose(
	pool []piece,
	budget media.Duration,
	opts Options,
) []Segment {
	var out []Segment

	take := func(list []piece, kind Scenes) {
		for _, p := range list {
			out = append(out, Segment{
				Interval: media.Interval{Start: p.start, End: p.end},
				Frames:   p.frames, SI: p.si, TI: p.ti, Kind: kind,
			})
		}
	}

	switch opts.Scenes {
	case ScenesTop:
		take(extreme(pool, budget, false), ScenesTop)
	case ScenesEasy:
		take(extreme(pool, budget, true), ScenesEasy)
	case ScenesAverage:
		take(representative(pool, budget, meansOf(pool)), ScenesAverage)
	case ScenesMixed:
		top := extreme(pool, media.Duration(float64(budget)*opts.TopShare), false)
		take(top, ScenesTop)

		// The rest of the video stands for the whole of it.
		rest := slices.DeleteFunc(slices.Clone(pool), func(p piece) bool {
			return slices.ContainsFunc(top, func(t piece) bool { return t.first == p.first })
		})
		take(representative(rest, budget-total(top), meansOf(pool)), ScenesAverage)
	}

	slices.SortFunc(out, func(a, b Segment) int { return cmp.Compare(a.Start, b.Start) })

	return out
}

// analysisScale is the resolution of the progress of the frame analyses.
const analysisScale = 1000

// plan chooses the scenes of every video: all of a video no longer than
// its share, else those opts ask for, on its frame analysis.
func (e *Engine) plan(
	ctx context.Context,
	videos []source,
	opts Options,
	res *Result,
) error {
	budget := opts.Duration / media.Duration(len(videos))

	for i, v := range videos {
		out := SourceResult{Path: v.path, Duration: v.duration}

		if v.duration <= budget {
			out.Whole = true
			out.Segments = []Segment{{
				Interval: media.Interval{End: v.duration},
				Frames:   v.inspection.Bitstream.PacketCount,
			}}
		} else {
			pool, err := e.analyse(ctx, v, i, len(videos), opts)
			if err != nil {
				return err
			}

			out.Segments = choose(pool, budget, opts)
			video := meansOf(pool)
			out.Complexity = &Complexity{VideoSI: video[0], VideoTI: video[1]}
		}

		out.summarise()
		res.Sources = append(res.Sources, out)
		res.Duration += out.Taken
		res.Frames += out.Frames
	}

	res.order()

	return nil
}

// analyse runs the frame analysis of video i of n and cuts it into the
// scenes a sample can take.
func (e *Engine) analyse(
	ctx context.Context,
	v source,
	i, n int,
	opts Options,
) ([]piece, error) {
	report, err := e.inspector.Analyze(ctx, v.path, analysis.Options{
		Audio: analysis.AudioOptions{Skip: true},
		Video: analysis.VideoOptions{SkipMotion: true},
		Progress: func(p analysis.Progress) {
			if p.Stage == analysis.StageDecode && p.Total > 0 {
				opts.report(Progress{Stage: StageAnalysis, Done: i*analysisScale + p.Done*analysisScale/p.Total, Total: n * analysisScale})
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("sample: analyse %s: %w", v.path, err)
	}

	frames := report.Frames
	if frames == nil || len(frames.PTS) == 0 || len(frames.SI) != len(frames.PTS) ||
		len(frames.TI) != len(frames.PTS) || len(frames.Keyframe) != len(frames.PTS) {
		return nil, fmt.Errorf("sample: analyse %s: %w: no frame analysis", v.path, ErrInvalidSource)
	}

	pool := pieces(frames, videoEnd(frames, v), opts.Piece)
	if len(pool) == 0 {
		return nil, fmt.Errorf("sample: %s: %w", v.path, ErrNoKeyframe)
	}

	return pool, nil
}

// videoEnd is when the last frame of a video ends: the duration of the
// file, or later when its last frames are shown late (a file cut after a
// frame its followers were stored before), so that the scene holding them
// lasts what its timestamps say.
func videoEnd(
	frames *analysis.FrameSeries,
	v source,
) media.Duration {
	last := frames.PTS[len(frames.PTS)-1] + media.Seconds(1/v.video.AvgFrameRate.Float())

	return max(v.duration, last)
}
