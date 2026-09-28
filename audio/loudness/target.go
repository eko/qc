package loudness

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Target is a delivery specification: an integrated loudness with its
// tolerance, and a true-peak ceiling.
type Target struct {
	// Name is the preset's name, or "custom".
	Name string `json:"name"`
	// Integrated is the target loudness (LUFS) and Tolerance the allowed
	// deviation either way (LU).
	Integrated float64 `json:"integrated"`
	Tolerance  float64 `json:"tolerance"`
	// MaxTruePeak is the highest true peak allowed (dBTP).
	MaxTruePeak float64 `json:"maxTruePeak"`
}

// Names of the preset targets.
const (
	// TargetEBU is EBU R 128: -23 LUFS ±0.5 LU where exact normalisation is
	// feasible (file-based delivery), true peak ≤ -1 dBTP.
	TargetEBU = "ebu"
	// TargetEBULive is EBU R 128 for live programmes: ±1 LU.
	TargetEBULive = "ebu-live"
	// TargetATSC is ATSC A/85 (the US CALM Act): -24 LKFS ±2 dB, true
	// peak ≤ -2 dBTP.
	TargetATSC = "atsc"
	// TargetStreaming is the -16 LUFS of most streaming and podcast
	// platforms, ±1 LU, true peak ≤ -1 dBTP.
	TargetStreaming = "streaming"
	// TargetStreamingLoud is the -14 LUFS normalisation level of music
	// streaming services, ±1 LU, true peak ≤ -1 dBTP.
	TargetStreamingLoud = "streaming-14"
	// TargetCustom names a target given as a loudness value.
	TargetCustom = "custom"
)

// Tolerance and ceiling of a custom target: those of the streaming presets.
const (
	customTolerance   = 1.0
	customMaxTruePeak = -1.0
)

// presets are the named targets, in the order of their documentation.
var presets = []Target{
	{Name: TargetEBU, Integrated: -23, Tolerance: 0.5, MaxTruePeak: -1},
	{Name: TargetEBULive, Integrated: -23, Tolerance: 1, MaxTruePeak: -1},
	{Name: TargetATSC, Integrated: -24, Tolerance: 2, MaxTruePeak: -2},
	{Name: TargetStreaming, Integrated: -16, Tolerance: 1, MaxTruePeak: -1},
	{Name: TargetStreamingLoud, Integrated: -14, Tolerance: 1, MaxTruePeak: -1},
}

// ErrInvalidTarget is returned by ParseTarget for an unknown name or an
// implausible loudness.
var ErrInvalidTarget = errors.New("invalid loudness target")

// Bounds of a custom target: quieter than -70 LUFS is below the gate,
// louder than 0 is beyond full scale.
const (
	minTarget = -70.0
	maxTarget = 0.0
)

// DefaultTarget is EBU R 128.
func DefaultTarget() Target {
	return presets[0]
}

// Targets returns the preset targets.
func Targets() []Target {
	return slices.Clone(presets)
}

// ParseTarget reads a preset name (ebu, ebu-live, atsc, streaming,
// streaming-14) or a loudness in LUFS ("-16"), which gets a ±1 LU tolerance
// and a -1 dBTP ceiling. An empty name is DefaultTarget.
func ParseTarget(
	s string,
) (Target, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return DefaultTarget(), nil
	}

	for _, t := range presets {
		if t.Name == s {
			return t, nil
		}
	}

	lufs, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, "lufs")), 64)
	if err != nil || math.IsNaN(lufs) || lufs < minTarget || lufs > maxTarget {
		return Target{}, fmt.Errorf("%w %q: want %s or a loudness in LUFS (-70 to 0)", ErrInvalidTarget, s, names())
	}

	return Target{Name: TargetCustom, Integrated: lufs, Tolerance: customTolerance, MaxTruePeak: customMaxTruePeak}, nil
}

// names lists the presets for error messages.
func names() string {
	list := make([]string, len(presets))
	for i, t := range presets {
		list[i] = t.Name
	}

	return strings.Join(list, ", ")
}

// Compliance is a Result checked against a Target.
type Compliance struct {
	// Deviation is the integrated loudness minus the target (LU):
	// positive when too loud.
	Deviation float64 `json:"deviation"`
	// Loudness is true when the deviation is within the tolerance.
	Loudness bool `json:"loudness"`
	// TruePeak is true when the true peak is at most the ceiling.
	TruePeak bool `json:"truePeak"`
}

// OK reports whether both checks pass.
func (c Compliance) OK() bool {
	return c.Loudness && c.TruePeak
}

// complianceSlack absorbs the rounding of the results to 0.01: a
// measurement exactly on a bound passes.
const complianceSlack = 1e-9

// Check checks r against t. A signal without any block above the gate (the
// integrated loudness is Floor) fails the loudness check.
func (t Target) Check(
	r Result,
) Compliance {
	deviation := Round(r.Integrated - t.Integrated)

	return Compliance{
		Deviation: deviation,
		Loudness:  r.Integrated > Floor && math.Abs(deviation) <= t.Tolerance+complianceSlack,
		TruePeak:  r.TruePeak <= t.MaxTruePeak+complianceSlack,
	}
}
