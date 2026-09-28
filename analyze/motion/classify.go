package motion

import (
	"cmp"
	"math"
	"slices"
)

// maxShakeSpeed (% of the width, or of scale, per second) is the camera
// speed above which a frame's jitter is not counted as shake.
const maxShakeSpeed = 40

// move is the camera move of one frame.
type move struct {
	class     Class
	direction Direction
}

// frameMove labels one frame from its low-passed rates: static below every
// threshold, mixed when two components clearly move, else the strongest
// component with its direction.
func frameMove(
	pan, tilt, zoom float64,
	o Options,
) move {
	ratios := [3]float64{math.Abs(pan) / o.MinSpeed, math.Abs(tilt) / o.MinSpeed, math.Abs(zoom) / o.MinZoom}

	// Components by decreasing ratio (pan first on ties).
	order := [3]int{0, 1, 2}
	slices.SortStableFunc(order[:], func(a, b int) int { return cmp.Compare(ratios[b], ratios[a]) })
	top, second := order[0], order[1]

	switch {
	case ratios[top] < 1:
		return move{class: ClassStatic}
	case ratios[second] >= 1 && ratios[second] >= mixedRatio*ratios[top]:
		return move{class: ClassMixed}
	case top == 0:
		return move{class: ClassPan, direction: pick(pan > 0, DirectionRight, DirectionLeft)}
	case top == 1:
		return move{class: ClassTilt, direction: pick(tilt > 0, DirectionUp, DirectionDown)}
	}

	return move{class: ClassZoom, direction: pick(zoom > 0, DirectionIn, DirectionOut)}
}

func pick(
	cond bool,
	yes, no Direction,
) Direction {
	if cond {
		return yes
	}

	return no
}

// tally counts the moves of the reliable frames of a track.
type tally struct {
	moves  map[move]int
	moving int
	// sums over the reliable frames: rates and confidence.
	pan, tilt, zoom, roll, confidence float64
	// shake and parallax are the values of the reliable (moving) frames.
	shake, parallax []float64
}

func (t track) tally(
	o Options,
) tally {
	out := tally{moves: map[move]int{}}

	for k, ok := range t.valid {
		if !ok {
			continue
		}

		out.pan, out.tilt, out.zoom, out.roll = out.pan+t.pan[k], out.tilt+t.tilt[k], out.zoom+t.zoom[k], out.roll+t.roll[k]
		out.confidence += t.confidence[k]
		// Jitter is only measured around a slow or steady path: a whip pan
		// (or a wipe the cut detector let through) accelerates too fast
		// for any trend to follow, and is not shake.
		if math.Hypot(t.pan[k], t.tilt[k]) < maxShakeSpeed && math.Abs(t.zoom[k]) < maxShakeSpeed {
			out.shake = append(out.shake, t.shake[k])
		}

		m := frameMove(t.pan[k], t.tilt[k], t.zoom[k], o)
		if m.class == ClassStatic {
			continue
		}

		out.moves[m]++
		out.moving++
		out.parallax = append(out.parallax, t.parallax[k])
	}

	return out
}

// classify names the camera work of a track.
func (t track) classify(
	o Options,
) Shot {
	n := len(t.valid)
	if t.reliable < minReliableFrames || float64(t.reliable) < minReliableShare*float64(n) {
		return Shot{Class: ClassUnknown}
	}

	c := t.tally(o)
	r := float64(t.reliable)
	shot := Shot{
		Pan: c.pan / r, Tilt: c.tilt / r, Zoom: c.zoom / r, Roll: c.roll / r,
		Moving: float64(c.moving) / r,
		// The median, not the RMS: handheld jitter lasts, while a single
		// burst (a whip pan, a wipe transition the cut detector does not
		// split) must not make a steady shot shaky.
		Shake: median(c.shake),
	}

	agreement := 1 - shot.Moving

	if shot.Moving >= minMovingShare {
		var share float64

		shot.Class, shot.Direction, share = c.dominant()
		agreement = share * shot.Moving
		shot.Parallax = median(c.parallax)
	}

	if shot.Class == "" {
		shot.Class = ClassStatic
	}

	if shot.Class != ClassStatic && shot.Parallax >= o.MinParallax {
		shot.Class = ClassTracking
	}

	shot.Shaky = shot.Shake >= o.MaxShake
	if shot.Shaky && (shot.Class == ClassStatic || shot.Class == ClassMixed) {
		shot.Class, shot.Direction = ClassHandheld, ""
	}

	shot.Confidence = c.confidence / float64(n) * agreement

	return shot
}

// dominant is the move covering most moving frames and its share: with its
// direction when one direction dominates, without when the class does
// (back and forth), mixed otherwise.
func (c tally) dominant() (Class, Direction, float64) {
	var (
		best    move
		byClass = map[Class]int{}
	)

	for m, count := range c.moves {
		byClass[m.class] += count
		if count > c.moves[best] || count == c.moves[best] && m.less(best) {
			best = m
		}
	}

	moving := float64(c.moving)
	if share := float64(c.moves[best]) / moving; share >= dominantShare && best.class != ClassMixed {
		return best.class, best.direction, share
	}

	for class, count := range byClass {
		if share := float64(count) / moving; share >= dominantShare && class != ClassMixed {
			return class, "", share
		}
	}

	// Mixed is certain when frames move along several components at once,
	// or when no single component dominates.
	top := 0
	for class, count := range byClass {
		if class != ClassMixed {
			top = max(top, count)
		}
	}

	return ClassMixed, "", max(float64(byClass[ClassMixed])/moving, 1-float64(top)/moving)
}

// less orders moves deterministically, for ties.
func (m move) less(
	o move,
) bool {
	if m.class != o.class {
		return m.class < o.class
	}

	return m.direction < o.direction
}
