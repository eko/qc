package siti

import (
	"testing"

	"github.com/eko/qc/frame"
)

func benchPlane() (*frame.Plane, *frame.Plane) {
	a := plane(1920, 1080, func(x, y int) byte { return byte((x*7 + y*13) ^ (x * y)) })
	b := plane(1920, 1080, func(x, y int) byte { return byte((x*5 + y*11) ^ (x * y)) })

	return a, b
}

func BenchmarkSpatialInformation1080p(b *testing.B) {
	p, _ := benchPlane()
	b.SetBytes(int64(len(p.Pix)))

	for b.Loop() {
		SpatialInformation(p)
	}
}

func BenchmarkTemporalInformation1080p(b *testing.B) {
	p, q := benchPlane()
	b.SetBytes(int64(len(p.Pix)))

	for b.Loop() {
		TemporalInformation(p, q)
	}
}
