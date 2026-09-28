package motion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFrameMove(
	t *testing.T,
) {
	o := Options{}.withDefaults()

	testCases := []struct {
		name            string
		pan, tilt, zoom float64
		wantClass       Class
		wantDirection   Direction
	}{
		{name: "below every threshold", pan: 1, tilt: -1, zoom: 1, wantClass: ClassStatic},
		{name: "pan right", pan: 10, wantClass: ClassPan, wantDirection: DirectionRight},
		{name: "pan left", pan: -10, tilt: 3, wantClass: ClassPan, wantDirection: DirectionLeft},
		{name: "tilt up", tilt: 6, wantClass: ClassTilt, wantDirection: DirectionUp},
		{name: "tilt down", tilt: -6, wantClass: ClassTilt, wantDirection: DirectionDown},
		{name: "zoom in", zoom: 5, wantClass: ClassZoom, wantDirection: DirectionIn},
		{name: "zoom out", zoom: -5, pan: 1, wantClass: ClassZoom, wantDirection: DirectionOut},
		{name: "pan and tilt", pan: 10, tilt: 8, wantClass: ClassMixed},
		{name: "tilt and zoom", tilt: 4, zoom: 3, wantClass: ClassMixed},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := frameMove(testCase.pan, testCase.tilt, testCase.zoom, o)
			assert.Equal(t, move{class: testCase.wantClass, direction: testCase.wantDirection}, got)
		})
	}
}

// constantTrack is a track of n reliable frames (the first one excepted)
// with the given rates, shake and parallax.
func constantTrack(
	n int,
	pan, tilt, zoom, shake, parallax float64,
) track {
	t := track{
		pan: make([]float64, n), tilt: make([]float64, n), zoom: make([]float64, n), roll: make([]float64, n),
		shake: make([]float64, n), confidence: make([]float64, n), parallax: make([]float64, n), valid: make([]bool, n),
	}

	for k := 1; k < n; k++ {
		t.pan[k], t.tilt[k], t.zoom[k], t.shake[k], t.parallax[k] = pan, tilt, zoom, shake, parallax
		t.confidence[k], t.valid[k] = 1, true
		t.reliable++
	}

	return t
}

// backAndForth pans right for the first half and left for the second.
func backAndForth(
	n int,
) track {
	t := constantTrack(n, 10, 0, 0, 0, 0)
	for k := n / 2; k < n; k++ {
		t.pan[k] = -10
	}

	return t
}

// panThenZoom pans on the first half and zooms on the second.
func panThenZoom(
	n int,
) track {
	t := constantTrack(n, 10, 0, 0, 0, 0)
	for k := n / 2; k < n; k++ {
		t.pan[k], t.zoom[k] = 0, 10
	}

	return t
}

func TestClassify(
	t *testing.T,
) {
	o := Options{}.withDefaults()
	unreliable := constantTrack(20, 10, 0, 0, 0, 0)

	for k := 2; k < 20; k++ {
		unreliable.valid[k] = false
	}

	unreliable.reliable = 1

	testCases := []struct {
		name          string
		track         track
		wantClass     Class
		wantDirection Direction
		wantShaky     bool
	}{
		{name: "unreliable", track: unreliable, wantClass: ClassUnknown},
		{name: "static", track: constantTrack(20, 0.5, 0, 0, 0.05, 0), wantClass: ClassStatic},
		{name: "pan", track: constantTrack(20, -8, 0, 0, 0.05, 0.05), wantClass: ClassPan, wantDirection: DirectionLeft},
		{name: "shaky pan", track: constantTrack(20, 8, 0, 0, 0.6, 0.05), wantClass: ClassPan, wantDirection: DirectionRight, wantShaky: true},
		{name: "handheld", track: constantTrack(20, 0, 0, 0, 0.6, 0), wantClass: ClassHandheld, wantShaky: true},
		{name: "whip pan is not shake", track: constantTrack(20, 60, 0, 0, 0.6, 0), wantClass: ClassPan, wantDirection: DirectionRight},
		{name: "tracking", track: constantTrack(20, 8, 0, 0, 0.05, 0.8), wantClass: ClassTracking, wantDirection: DirectionRight},
		{name: "dolly", track: constantTrack(20, 0, 0, 6, 0.05, 0.8), wantClass: ClassTracking, wantDirection: DirectionIn},
		{name: "mixed moves", track: constantTrack(20, 8, 8, 0, 0.05, 0), wantClass: ClassMixed},
		{name: "shaky mixed moves", track: constantTrack(20, 8, 8, 0, 0.6, 0), wantClass: ClassHandheld, wantShaky: true},
		{name: "back and forth", track: backAndForth(20), wantClass: ClassPan},
		{name: "pan then zoom", track: panThenZoom(20), wantClass: ClassMixed},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.track.classify(o)

			assert.Equal(t, testCase.wantClass, got.Class)
			assert.Equal(t, testCase.wantDirection, got.Direction)
			assert.Equal(t, testCase.wantShaky, got.Shaky)
			assert.GreaterOrEqual(t, got.Confidence, 0.0)
			assert.LessOrEqual(t, got.Confidence, 1.0)
		})
	}
}

func TestMoveLess(
	t *testing.T,
) {
	testCases := []struct {
		name string
		a, b move
		want bool
	}{
		{name: "by class", a: move{class: ClassPan}, b: move{class: ClassTilt}, want: true},
		{name: "by direction", a: move{class: ClassPan, direction: DirectionRight}, b: move{class: ClassPan, direction: DirectionLeft}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.a.less(testCase.b))
		})
	}
}
