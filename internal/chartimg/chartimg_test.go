package chartimg

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	red  = color.RGBA{R: 0xff, A: 0xff}
	ink  = color.RGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xff}
	grid = color.RGBA{R: 0xe0, G: 0xe0, B: 0xe0, A: 0xff}
)

func testChart() Chart {
	return Chart{
		Width: 400, Height: 200, Scale: 2, XLo: 100_000, XHi: 10_000_000, YLo: 0, YHi: 100, XLog: true,
		XTicks: []float64{1e5, 1e6, 1e7}, XLabel: func(float64) string { return "x" },
		YStep: 50, YLabel: func(float64) string { return "y" },
		Lines:   []Line{{Points: [][2]float64{{100_000, 50}, {10_000_000, 50}}, Color: red, Width: 3}},
		Markers: []Marker{{X: 1_000_000, Y: 80, Label: "1", Fill: ink, Text: grid}},
		Legend:  []Key{{Label: "1080p", Color: red}},
		Theme:   Theme{Ink: ink, Muted: ink, Grid: grid},
	}
}

func TestPNG(
	t *testing.T,
) {
	data, err := testChart().PNG()
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)

	assert.Equal(t, 800, img.Bounds().Dx(), "drawn at twice its size")
	assert.Equal(t, 400, img.Bounds().Dy())

	// The line at VMAF 50, halfway up the plot, in the middle of it.
	p := &painter{scale: 2, left: marginLeft * 2, right: (400 - marginRight) * 2, top: marginTop * 2, bottom: (200 - marginBottom) * 2,
		xLo: 0, xHi: 1, yLo: 0, yHi: 100}
	x, y := p.at(0.5, 50)
	r, g, b, a := img.At(int(x), int(y)).RGBA()
	assert.Equal(t, [4]uint32{0xffff, 0, 0, 0xffff}, [4]uint32{r, g, b, a}, "the line is drawn where its points are")

	// The marker disc is drawn at its point: dark around its label.
	mx, my := p.at(0.5, 80)
	_, _, _, ma := img.At(int(mx)-int(markerRadius*2)+3, int(my)).RGBA()
	assert.NotZero(t, ma)

	// Outside the plot, what is not text stays transparent: the terminal's
	// background shows.
	_, _, _, corner := img.At(img.Bounds().Dx()-2, img.Bounds().Dy()-2).RGBA()
	assert.Zero(t, corner)
}

func TestLineOutsideThePlotIsClipped(
	t *testing.T,
) {
	c := testChart()
	c.Lines = []Line{{Points: [][2]float64{{100_000, 150}, {10_000_000, 150}}, Color: red, Width: 3}}
	c.Legend, c.Markers = nil, nil

	data, err := c.PNG()
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)

	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			r, g, b, _ := img.At(x, y).RGBA()
			require.False(t, r == 0xffff && g == 0 && b == 0, "a line above the plot is not drawn (%d, %d)", x, y)
		}
	}
}

func TestDots(
	t *testing.T,
) {
	c := testChart()
	c.Lines = []Line{{Points: [][2]float64{{100_000, 20}, {10_000_000, 20}}, Color: red, Width: 6, Dots: true}}

	data, err := c.PNG()
	require.NoError(t, err)

	img, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)

	p := &painter{left: marginLeft * 2, right: (400 - marginRight) * 2, top: marginTop * 2, bottom: (200 - marginBottom) * 2, xLo: 0, xHi: 1, yLo: 0, yHi: 100}
	x, y := p.at(0.5, 20)
	r, _, _, _ := img.At(int(x), int(y)).RGBA()
	assert.NotEqual(t, uint32(0xffff), r, "dots only: nothing between the points")

	x, y = p.at(0, 20)
	r, _, _, _ = img.At(int(x), int(y)).RGBA()
	assert.Equal(t, uint32(0xffff), r, "a dot at each point")
}
