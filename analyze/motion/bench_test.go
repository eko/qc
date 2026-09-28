package motion

import "testing"

// BenchmarkEstimate measures one frame of motion estimation on a 1080p
// thumbnail (240×135) panning and zooming: the per-frame cost the
// analyzer adds to a frame analysis.
func BenchmarkEstimate(b *testing.B) {
	tex := newTexture(600, 1)
	frames := [2][]byte{tex.render(240, 135, 300, 300, 1, 0), tex.render(240, 135, 301.3, 300.4, 1.004, 0)}

	var e estimator

	e.next(frames[0], 240, 135, 240)

	i := 1
	for b.Loop() {
		e.next(frames[i%2], 240, 135, 240)
		i++
	}
}
