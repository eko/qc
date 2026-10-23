package tui

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/termimg"
	"github.com/eko/qc/ladder"
)

func TestPlot(
	t *testing.T,
) {
	curve := Series{Points: [][2]float64{{100_000, 30}, {1_000_000, 80}, {10_000_000, 99}}, Style: Cyan, Line: true}
	plot := Plot{
		Series: []Series{curve}, Marks: []Mark{{X: 1_000_000, Y: 80, Glyph: '1', Style: Bold}},
		Width: 70, Height: 8, XLog: true, XLo: 300_000, XHi: 3_000_000, YLo: 50, YHi: 100,
		YStep: 10, YLabel: vmafLabel, XLabel: tickBitrate,
	}

	lines := plot.Lines()
	require.Len(t, lines, plot.Height+2, "the rows, the axis line and its labels")

	for _, w := range widths(lines) {
		assert.Equal(t, plot.Width, w)
	}

	text := plainLines(strings.Join(lines, "\n"))

	var labels []string

	for _, line := range text[:plot.Height] {
		if strings.Contains(line, "┤") {
			labels = append(labels, strings.Fields(line)[0])
		}
	}

	assert.Equal(t, []string{"100", "90", "80", "70", "60", "50"}, labels, "a label every step")
	assert.Contains(t, strings.Join(text, "\n"), "1", "the mark is drawn")
	assert.Equal(t, []string{"500k", "1M", "2M"}, strings.Fields(text[plot.Height+1]))
	assert.Equal(t, 3, strings.Count(text[plot.Height], "┬"), "a tick under each label")
}

func TestPlotDropsWhatLiesOutside(
	t *testing.T,
) {
	inside := Plot{Series: []Series{{Points: [][2]float64{{5, 50}}, Style: Red}}, Width: gutter + 10, Height: 2, XLo: 0, XHi: 10, YLo: 0, YHi: 100, YLabel: vmafLabel}
	outside := inside
	outside.Series = []Series{{Points: [][2]float64{{20, 50}, {5, 150}}, Style: Red}}

	dots := func(p Plot) int {
		n := 0

		for _, line := range plainLines(strings.Join(p.Lines(), "\n")) {
			for _, r := range []rune(line)[gutter:] {
				if r > 0x2800 && r <= 0x28FF {
					n++
				}
			}
		}

		return n
	}

	assert.Positive(t, dots(inside))
	assert.Zero(t, dots(outside))
}

func TestTicks(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		lo, hi float64
		log    bool
		want   []float64
	}{
		{name: "log, a decade", lo: math.Log(150_000), hi: math.Log(1_200_000), log: true, want: []float64{200_000, 500_000, 1_000_000}},
		{name: "log, many decades: the powers only", lo: math.Log(20), hi: math.Log(2e6), log: true, want: []float64{100, 1000, 1e4, 1e5, 1e6}},
		{name: "linear", lo: 0, hi: 100, want: []float64{0, 20, 40, 60, 80, 100}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := ticks(testCase.lo, testCase.hi, testCase.log)
			require.Len(t, got, len(testCase.want))

			for i := range got {
				assert.InDelta(t, testCase.want[i], got[i], testCase.want[i]*1e-9)
			}
		})
	}
}

func TestTickBitrate(
	t *testing.T,
) {
	testCases := []struct {
		bps  float64
		want string
	}{
		{bps: 200_000, want: "200k"},
		{bps: 1_000_000, want: "1M"},
		{bps: 1_500_000, want: "1.5M"},
		{bps: 20_000_000, want: "20M"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.want, func(t *testing.T) {
			assert.Equal(t, testCase.want, tickBitrate(testCase.bps))
		})
	}
}

func TestRungGlyph(
	t *testing.T,
) {
	assert.Equal(t, '1', rungGlyph(0))
	assert.Equal(t, '9', rungGlyph(8))
	assert.Equal(t, 'a', rungGlyph(9))
	assert.Equal(t, 'z', rungGlyph(1000), "past the glyphs, the last")
}

func TestCurveAt(
	t *testing.T,
) {
	curve := [][2]float64{{1, 40}, {2, 60}, {4, 80}}

	testCases := []struct {
		name string
		x    float64
		want float64
		ok   bool
	}{
		{name: "on a point", x: 2, want: 60, ok: true},
		{name: "between two points", x: 3, want: 70, ok: true},
		{name: "at the end", x: 4, want: 80, ok: true},
		{name: "before the curve", x: 0.5},
		{name: "after the curve", x: 5},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := curveAt(curve, testCase.x)
			assert.Equal(t, testCase.ok, ok)
			assert.InDelta(t, testCase.want, got, 1e-9)
		})
	}
}

func TestEnvelopeSeries(
	t *testing.T,
) {
	// 1080p wins above 1 Mb/s, 540p under it: each draws the envelope on
	// its side of the crossing and is faint on the other.
	probes := []ladder.Probe{
		{Height: 1080, Bitrate: 300_000, VMAF: 40, Predicted: true},
		{Height: 1080, Bitrate: 3_000_000, VMAF: 90, Predicted: true},
		{Height: 540, Bitrate: 300_000, VMAF: 55},
		{Height: 540, Bitrate: 3_000_000, VMAF: 75},
	}

	series := envelopeSeries(probes, 0, 0, 200)

	var bright, faint, dotted int

	for _, s := range series {
		switch {
		case !s.Line:
			dotted++
			assert.Len(t, s.Points, 2, "the measured probes of 540p, dotted")
		case s.Style.GetForeground() == Subtle.GetForeground():
			faint++
		default:
			bright++

			for _, p := range s.Points {
				x := math.Log(p[0])
				hd, _ := curveAt([][2]float64{{math.Log(300_000), 40}, {math.Log(3_000_000), 90}}, x)
				sd, _ := curveAt([][2]float64{{math.Log(300_000), 55}, {math.Log(3_000_000), 75}}, x)
				assert.GreaterOrEqual(t, p[1], max(hd, sd)-0.5, "a bright line is the envelope")
			}
		}

		assert.True(t, s.NoDots || !s.Line, "lines are one dot thick")
	}

	assert.Equal(t, 2, bright, "each resolution draws the envelope once")
	assert.Equal(t, 2, faint)
	assert.Equal(t, 1, dotted, "predicted probes are not dotted")
	assert.Nil(t, envelopeSeries(nil, 0, 0, 200))
}

func TestRenderLadderPictures(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		protocol termimg.Protocol
		want     string
	}{
		{name: "kitty", protocol: termimg.Kitty, want: "\x1b_Ga=T,f=100"},
		{name: "iterm", protocol: termimg.ITerm, want: "\x1b]1337;File=inline=1"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out strings.Builder
			require.NoError(t, RenderLadder(&out, sampleLadder(t), ReportOptions{Width: 100, Pictures: testCase.protocol}))

			assert.Equal(t, 1, strings.Count(out.String(), testCase.want), "the chart is a picture")
			assert.Contains(t, plain(out.String()), "Rate / quality")
			assert.NotContains(t, out.String(), "┤", "no chart of characters")
			assert.Contains(t, plain(out.String()), "1920×1080", "the table follows")

			h264, av1 := sampleLadder(t), sampleLadder(t)
			av1.Codec.Name = "av1"

			for i := range av1.Rungs {
				av1.Rungs[i].Measured.Bitrate = av1.Rungs[i].Measured.Bitrate * 7 / 10
			}

			out.Reset()
			require.NoError(t, RenderCodecs(&out, []*ladder.Result{h264, av1}, ReportOptions{Width: 100, Pictures: testCase.protocol}))
			assert.Equal(t, 1, strings.Count(out.String(), testCase.want), "the codecs chart is a picture")
			assert.Contains(t, plain(out.String()), "Rungs of each codec")
		})
	}
}

func TestLadderPanelPicture(
	t *testing.T,
) {
	measured := ladder.Probe{Height: 1080, CRF: 27, Bitrate: 3_000_000, VMAF: 90}
	predicted := []ladder.Probe{
		{Height: 1080, CRF: 13, Bitrate: 30_000_000, VMAF: 99.8, Predicted: true},
		{Height: 1080, CRF: 34, Bitrate: 1_000_000, VMAF: 75, Predicted: true},
		{Height: 270, CRF: 48, Bitrate: 30_000, VMAF: 1, Predicted: true},
	}

	testCases := []struct {
		name   string
		panel  LadderPanel
		ok     bool
		lo, hi float64
	}{
		{name: "analysing", panel: LadderPanel{Stage: ladder.StageAnalysis}},
		{name: "no probe yet", panel: LadderPanel{Stage: ladder.StageProbe}},
		{name: "one probe", panel: LadderPanel{Stage: ladder.StageProbe, Probes: []ladder.Probe{measured}}, ok: true, lo: 3_000_000 / probeMargin, hi: 3_000_000 * probeMargin * probeMargin * probeMargin},
		{
			name:  "a model's grid: framed on the qualities of a ladder",
			panel: LadderPanel{Stage: ladder.StageProbe, Probes: append([]ladder.Probe{measured}, predicted...)}, ok: true,
			lo: 1_000_000 / probeMargin, hi: 3_000_000 * probeMargin,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			head, png, ok := testCase.panel.Picture(100, panelPictureRows, true)
			require.Equal(t, testCase.ok, ok)

			if !ok {
				return
			}

			assert.NotEmpty(t, png)
			assert.Contains(t, plain(head), "probe encodes")

			lo, hi, _ := testCase.panel.frame()
			assert.InDelta(t, testCase.lo, lo, 1)
			assert.InDelta(t, testCase.hi, hi, 1)
		})
	}

	last, ok := lastEncoded(append([]ladder.Probe{measured}, predicted...))
	require.True(t, ok)
	assert.Equal(t, measured, last, "a predicted probe landed nowhere")

	verifying := LadderPanel{Stage: ladder.StageVerify, Probes: []ladder.Probe{measured}, Rungs: []ladder.Rung{{Bitrate: 3_000_000, PredictedVMAF: 90}}}
	head, _, ok := verifying.Picture(100, panelPictureRows, false)
	require.True(t, ok)
	assert.Contains(t, plain(head), "verifying rungs  0 of 1")
	assert.NotEqual(t, verifying.PictureKey(), LadderPanel{Stage: ladder.StageVerify, Probes: verifying.Probes, Rungs: []ladder.Rung{{Bitrate: 3_000_000, PredictedVMAF: 90, Measured: &ladder.Measurement{VMAF: 89}}}}.PictureKey(),
		"a rung verified draws the picture again")
}

func TestEnergyBlock(
	t *testing.T,
) {
	res := sampleLadder(t)
	res.Rungs[0].Encode = &ladder.EncodeCost{CPUHours: 2.5}
	res.Rungs[1].Encode = &ladder.EncodeCost{CPUHours: 0.5, Estimated: true}

	testCases := []struct {
		name    string
		energy  *ladder.Energy
		want    []string
		wantNot []string
	}{
		{name: "none", wantNot: []string{"Energy"}},
		{
			name:   "CPU time alone",
			energy: &ladder.Energy{CPUHours: 3},
			want:   []string{"Energy", "2.5 core-h", "30.0 core-min  estimated from the encodes of its resolution", "all rungs  3.0 core-h", "--core-watts gives the energy"},
		},
		{
			name:   "energy at a power per core",
			energy: &ladder.Energy{CPUHours: 3, CoreWatts: 12, KWh: 0.036},
			want:   []string{"all rungs  3.0 core-h · 36.0 Wh at 12 W per core"},
		},
		{
			name: "a rung lowered by the tolerance",
			energy: &ladder.Energy{CPUHours: 3, Tolerance: 0.5, SavedCPUHours: 1,
				Lowered: []ladder.LoweredRung{{Rung: 1, From: 1080, To: 720, VMAFLost: 0.4}}},
			want: []string{"rung 2 at 720p instead of 1080p: 0.4 VMAF less, same bitrate", "energy tolerance 0.5 VMAF: about 1.0 core-h saved (25% of the ladder"},
		},
		{
			name:   "a tolerance that moves nothing",
			energy: &ladder.Energy{CPUHours: 3, Tolerance: 0.2},
			want:   []string{"energy tolerance 0.2 VMAF: no lower resolution comes that close"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res.Energy = testCase.energy

			var out strings.Builder
			require.NoError(t, RenderLadder(&out, res, ReportOptions{Width: 100}))

			text := plain(out.String())
			for _, want := range testCase.want {
				assert.Contains(t, text, want)
			}

			for _, wantNot := range testCase.wantNot {
				assert.NotContains(t, text, wantNot)
			}
		})
	}
}

func TestEnergyBlockDecode(
	t *testing.T,
) {
	res := sampleLadder(t)
	res.Energy = &ladder.Energy{CPUHours: 3, DecodeCPUHours: 1, CoreWatts: 10, KWh: 0.04}

	var out strings.Builder
	require.NoError(t, RenderLadder(&out, res, ReportOptions{Width: 100}))

	text := plain(out.String())
	assert.Contains(t, text, "decoding the source  1.0 core-h, once if the rungs share one decode")
	assert.Contains(t, text, "all rungs  4.0 core-h · 40.0 Wh at 10 W per core", "the source decoded once")
}
