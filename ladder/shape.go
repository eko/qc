package ladder

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// ErrInvalidShape is returned when the requested ladder shape cannot be
// built (bad rung count or resolutions, or an empty quality range).
var ErrInvalidShape = errors.New("invalid ladder shape")

// Shape is how the rungs of a ladder are chosen.
type Shape string

// Ladder shapes.
const (
	// ShapeAuto descends one quality step at a time within bitrate ratios.
	ShapeAuto Shape = "auto"
	// ShapeCount places Constraints.Rungs rungs, resolutions automatic.
	ShapeCount Shape = "count"
	// ShapeResolutions places one rung per Constraints.Resolutions entry.
	ShapeResolutions Shape = "resolutions"
)

// Shape reports how the rungs are chosen.
func (c Constraints) Shape() Shape {
	switch {
	case len(c.Resolutions) > 0:
		return ShapeResolutions
	case c.Rungs > 0:
		return ShapeCount
	}

	return ShapeAuto
}

// Validate checks the requested shape against the source height.
func (c Constraints) Validate(
	sourceHeight int,
) error {
	if c.Rungs < 0 {
		return fmt.Errorf("%w: %d rungs", ErrInvalidShape, c.Rungs)
	}

	if len(c.Resolutions) > 0 && c.Rungs > 0 && c.Rungs != len(c.Resolutions) {
		return fmt.Errorf("%w: %d rungs requested but %d resolutions given", ErrInvalidShape, c.Rungs, len(c.Resolutions))
	}

	for _, h := range c.Resolutions {
		if h <= 0 || (sourceHeight > 0 && h > sourceHeight) {
			return fmt.Errorf("%w: resolution %dp (the source is %dp)", ErrInvalidShape, h, sourceHeight)
		}
	}

	return nil
}

// PlanRungs chooses the rungs of the ladder according to its shape:
//
//   - auto: SelectRungs, one quality step at a time;
//   - count and resolutions: quality targets evenly spaced from TopVMAF down
//     to the title's natural bottom (the quality reached at MinBitrate, not
//     below MinVMAF). Count takes the best resolution of the envelope at each
//     target; resolutions reads each target on its imposed resolution's curve.
func PlanRungs(
	hull []HullPoint,
	curves []Curve,
	c Constraints,
) ([]Target, error) {
	c = c.WithDefaults()

	if c.Shape() == ShapeAuto {
		return SelectRungs(hull, c), nil
	}

	if len(hull) == 0 || (c.MaxBitrate > 0 && c.MaxBitrate < hull[0].Bitrate) {
		return nil, nil
	}

	top := topRung(hull, c)
	bottom := max(c.MinVMAF, hullVMAF(hull, float64(c.MinBitrate)))

	count := c.Rungs
	if c.Shape() == ShapeResolutions {
		count = len(c.Resolutions)
	}

	if count > 1 && bottom >= top.VMAF {
		return nil, fmt.Errorf("%w: the quality range is empty (top VMAF %.1f, bottom %.1f)", ErrInvalidShape, top.VMAF, bottom)
	}

	// The envelope grid overshoots the target slightly: aim at it exactly
	// when the title reaches it.
	targets := qualityTargets(math.Min(top.VMAF, c.TopVMAF), bottom, count)

	if c.Shape() == ShapeCount {
		return countRungs(hull, top, targets), nil
	}

	return resolutionRungs(curves, c, targets)
}

// qualityTargets spaces count VMAF targets evenly from top down to bottom.
func qualityTargets(
	top, bottom float64,
	count int,
) []float64 {
	targets := make([]float64, count)
	for i := range targets {
		targets[i] = top
		if count > 1 {
			targets[i] = top - float64(i)*(top-bottom)/float64(count-1)
		}
	}

	return targets
}

// countRungs places one rung per target on the envelope. Resolutions never
// increase as bitrate decreases.
func countRungs(
	hull []HullPoint,
	top Target,
	targets []float64,
) []Target {
	rungs := []Target{top}

	for _, vmaf := range targets[1:] {
		prev := rungs[len(rungs)-1]
		b := math.Min(bitrateFor(hull, vmaf), float64(prev.Bitrate))

		p, _ := pointAt(hull, b)
		rungs = append(rungs, Target{Bitrate: int64(math.Round(b)), Height: min(p.Height, prev.Height), VMAF: vmaf})
	}

	return rungs
}

// resolutionRungs reads each target on the curve of its imposed resolution,
// highest resolution first. The top rung is capped by MaxBitrate.
func resolutionRungs(
	curves []Curve,
	c Constraints,
	targets []float64,
) ([]Target, error) {
	heights := slices.Clone(c.Resolutions)
	slices.SortFunc(heights, func(a, b int) int { return b - a })

	rungs := make([]Target, 0, len(heights))

	for i, h := range heights {
		curve, ok := curveOf(curves, h)
		if !ok {
			return nil, fmt.Errorf("%w: no probe at %dp", ErrInvalidShape, h)
		}

		b, _ := curve.BitrateFor(targets[i])
		if i == 0 && c.MaxBitrate > 0 && b > float64(c.MaxBitrate) {
			b = float64(c.MaxBitrate)
		}

		rungs = append(rungs, Target{
			Bitrate: int64(math.Round(b)), Height: h, VMAF: targets[i],
			Fixed: true, Extrapolated: outsideRange(curve, targets[i]),
		})
	}

	return rungs, nil
}

// extrapolationMargin is how far, in VMAF, a target may lie outside the
// probed range of its resolution before its prediction is flagged: a short
// extension of the curve stays reliable (verification confirms it).
const extrapolationMargin = 2.0

// outsideRange reports whether vmaf lies more than extrapolationMargin
// outside the curve's probed quality range.
func outsideRange(
	c Curve,
	vmaf float64,
) bool {
	lo, hi := c.QualityRange()

	return vmaf < lo-extrapolationMargin || vmaf > hi+extrapolationMargin
}

// curveOf returns the curve of height h.
func curveOf(
	curves []Curve,
	h int,
) (Curve, bool) {
	i := slices.IndexFunc(curves, func(c Curve) bool { return c.Height == h })
	if i < 0 {
		return Curve{}, false
	}

	return curves[i], true
}

// hullVMAF interpolates the envelope quality at bitrate b (in log scale),
// clamped to the envelope ends.
func hullVMAF(
	hull []HullPoint,
	b float64,
) float64 {
	if b <= float64(hull[0].Bitrate) {
		return hull[0].VMAF
	}

	for i := 1; i < len(hull); i++ {
		if p := hull[i]; float64(p.Bitrate) >= b {
			q := hull[i-1]
			t := (math.Log(b) - math.Log(float64(q.Bitrate))) / (math.Log(float64(p.Bitrate)) - math.Log(float64(q.Bitrate)))

			return q.VMAF + t*(p.VMAF-q.VMAF)
		}
	}

	return hull[len(hull)-1].VMAF
}
