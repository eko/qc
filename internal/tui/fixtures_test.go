package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/black"
	"github.com/eko/qc/analyze/crop"
	"github.com/eko/qc/analyze/freeze"
	"github.com/eko/qc/analyze/levels"
	"github.com/eko/qc/analyze/scene"
	"github.com/eko/qc/analyze/siti"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

// TestMain renders in true colour, as in a real terminal, so that tests catch
// layouts broken by escape sequences. Assertions strip them with plain.
func TestMain(
	m *testing.M,
) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	os.Exit(m.Run())
}

// plain strips ANSI escape sequences.
func plain(
	s string,
) string {
	return ansi.Strip(s)
}

// plainLines splits a rendering into lines without escape sequences.
func plainLines(
	s string,
) []string {
	return strings.Split(plain(strings.TrimRight(s, "\n")), "\n")
}

// linesWith returns the lines containing marker.
func linesWith(
	lines []string,
	marker string,
) []string {
	var out []string

	for _, l := range lines {
		if strings.Contains(l, marker) {
			out = append(out, l)
		}
	}

	return out
}

// linesStarting returns the lines starting with prefix once left-trimmed.
func linesStarting(
	lines []string,
	prefixes ...string,
) []string {
	var out []string

	for _, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		for _, p := range prefixes {
			if strings.HasPrefix(trimmed, p) {
				out = append(out, l)

				break
			}
		}
	}

	return out
}

// widths returns the display width of each line.
func widths(
	lines []string,
) []int {
	out := make([]int, len(lines))
	for i, l := range lines {
		out[i] = ansi.StringWidth(l)
	}

	return out
}

func series(
	n int,
	f func(i int) float64,
) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = f(i)
	}

	return out
}

const (
	fixtureFrames   = 1500
	fixtureDuration = 60
	fixtureFPS      = 25
)

func sampleInfo(
	path string,
) *media.Info {
	return &media.Info{
		Path:     path,
		Format:   "mov,mp4,m4a",
		Duration: media.Seconds(fixtureDuration),
		Size:     30 << 20,
		BitRate:  4_000_000,
		Audio: []media.AudioStream{
			{Index: 1, Codec: "aac", SampleRate: 48000, Channels: 2, Language: "eng"},
			{Index: 2, Codec: "ac3", SampleRate: 48000, Channels: 6},
		},
		Video: []media.VideoStream{{
			Codec: "h264", Profile: "High", Width: 1920, Height: 1080,
			AvgFrameRate: media.Rational{Num: fixtureFPS, Den: 1},
			PixelFormat:  "yuv420p", BitDepth: 8, FieldOrder: "progressive",
			Color: media.Color{Range: "tv", Primaries: "bt709", Transfer: "bt709"},
			HDR:   media.HDR{DynamicRange: media.DynamicRangeSDR},
		}},
	}
}

func sampleBitstream() *bitstream.Report {
	points := make([]bitstream.BitratePoint, fixtureDuration)
	for i := range points {
		points[i] = bitstream.BitratePoint{Start: media.Seconds(float64(i)), Bitrate: int64(3_000_000 + (i%7)*400_000)}
	}

	var keyframes []media.Duration
	for s := 0; s < fixtureDuration; s += 6 {
		keyframes = append(keyframes, media.Seconds(float64(s)))
	}

	return &bitstream.Report{
		Duration:       media.Seconds(fixtureDuration),
		AverageBitrate: 4_000_000,
		PeakBitrate:    9_000_000,
		PeakAt:         media.Seconds(30),
		PeakWindow:     media.Seconds(1),
		PeakToAverage:  2.25,
		FrameSize:      bitstream.SizeStats{P50: 12_000, P95: 80_000, Max: 250_000},
		GOP: bitstream.GOPStats{
			KeyframeCount: len(keyframes),
			MinInterval:   media.Seconds(2),
			MaxInterval:   media.Seconds(6),
			MeanInterval:  media.Seconds(5),
		},
		Bitrate:   points,
		Keyframes: keyframes,
	}
}

// sampleShots cuts the title into shots of 6 seconds of varied difficulty.
func sampleShots() []analysis.ShotReport {
	shots := make([]analysis.ShotReport, fixtureDuration/6)
	for i := range shots {
		start := i * 6 * fixtureFPS
		shots[i] = analysis.ShotReport{
			Shot: scene.Shot{
				Interval:   media.Interval{Start: media.Seconds(float64(i * 6)), End: media.Seconds(float64(i*6 + 6))},
				FirstFrame: start,
				LastFrame:  start + 6*fixtureFPS - 1,
			},
			Frames:  6 * fixtureFPS,
			SIMean:  float64(20 + (i*7)%30),
			TIMean:  float64(5 + (i*3)%10),
			Bitrate: int64(2_000_000 + i*100_000),
		}
	}

	return shots
}

func sampleVideo() *analysis.VideoReport {
	return &analysis.VideoReport{
		FramesDecoded: fixtureFrames,
		Levels: levels.Result{
			Levels:            media.Levels{Black: 16, White: 235},
			OutOfRangeSummary: stats.Summary{P95: 0.05},
		},
		SITI: siti.Result{
			SISummary: stats.Summary{Mean: 40.2},
			TISummary: stats.Summary{Mean: 12.4},
		},
		Shots:      sampleShots(),
		Black:      black.Result{Segments: []media.Interval{{Start: 0, End: media.Seconds(1)}}},
		Freeze:     freeze.Result{Segments: []media.Interval{{Start: media.Seconds(30), End: media.Seconds(32)}}},
		Crop:       crop.Result{Content: crop.Rect{X: 0, Y: 140, Width: 1920, Height: 800}, Letterbox: true},
		Complexity: analysis.ComplexityHint{Spatial: "medium", Temporal: "low"},
	}
}

func sampleFrames() *analysis.FrameSeries {
	pts := make([]media.Duration, fixtureFrames)
	sizes := make([]int, fixtureFrames)

	for i := range pts {
		pts[i] = media.Seconds(float64(i) / fixtureFPS)
		sizes[i] = 10_000 + (i%50)*1_000
	}

	return &analysis.FrameSeries{
		PTS:      pts,
		Size:     sizes,
		SI:       series(fixtureFrames, func(i int) float64 { return 30 + float64(i%100)/5 }),
		TI:       series(fixtureFrames, func(i int) float64 { return float64(i%40) / 2 }),
		LumaMean: series(fixtureFrames, func(i int) float64 { return 60 + float64(i%200)/4 }),
	}
}

// sampleReport is a complete technical analysis with every finding.
func sampleReport() *analysis.Report {
	return &analysis.Report{
		Info:      sampleInfo("/videos/clip.mp4"),
		Bitstream: sampleBitstream(),
		Video:     sampleVideo(),
		Frames:    sampleFrames(),
		Timings:   map[string]string{"probe": "52ms", "bitstream": "71ms", "video": "2.1s"},
	}
}

// sampleVMAF is a VMAF result of the fixture title, sampled or exact.
func sampleVMAF(
	mode string,
) *quality.Result {
	res := &quality.Result{
		Model:        vmaf.ModelSpec{Name: "vmaf_v0.6.1", Width: 1920, Height: 1080},
		Mode:         mode,
		Mean:         88.4,
		Low:          87.9,
		High:         88.9,
		HalfWidth:    0.5,
		Confidence:   0.95,
		Scored:       stats.Summary{Min: 61.2, P5: 80.3, P50: 89.1, Max: 97.5},
		FramesTotal:  fixtureFrames,
		FramesScored: 240,
		Rounds:       2,
		Elapsed:      media.Seconds(3.2),
	}

	if mode == quality.ModeExact {
		res.Low, res.High, res.HalfWidth = res.Mean, res.Mean, 0
		res.FramesScored, res.HarmonicMean = fixtureFrames, 87.9
	}

	step := fixtureFrames / res.FramesScored
	for i := range res.FramesScored {
		index := i * step
		res.Frames = append(res.Frames, quality.FrameScore{
			Index: index,
			PTS:   media.Seconds(float64(index) / fixtureFPS),
			Score: 85 + float64(i%10),
		})
	}

	for i := range 6 {
		res.Strata = append(res.Strata, quality.StratumResult{
			Interval: media.Interval{Start: media.Seconds(float64(i * 10)), End: media.Seconds(float64(i*10 + 10))},
			Frames:   10 * fixtureFPS,
			Mean:     80 + float64(i)*2,
		})
	}

	return res
}

func sampleComparison(
	mode string,
) *analysis.Comparison {
	return &analysis.Comparison{
		Reference: &analysis.Report{Info: sampleInfo("/videos/source.mov"), Bitstream: sampleBitstream()},
		Distorted: &analysis.Report{Info: sampleInfo("/encodes/clip.mp4"), Bitstream: sampleBitstream()},
		VMAF:      sampleVMAF(mode),
		Timings:   map[string]string{"inspect": "120ms", "vmaf": "3.2s"},
	}
}

func sampleProbes() []ladder.Probe {
	return []ladder.Probe{
		{Width: 1920, Height: 1080, CRF: 20, Bitrate: 5_600_000, VMAF: 97.1},
		{Width: 1920, Height: 1080, CRF: 27, Bitrate: 2_300_000, VMAF: 91.8},
		{Width: 1920, Height: 1080, CRF: 34, Bitrate: 1_050_000, VMAF: 82.3},
		{Width: 1280, Height: 720, CRF: 27, Bitrate: 1_150_000, VMAF: 86.6},
		{Width: 1280, Height: 720, CRF: 20, Bitrate: 2_700_000, VMAF: 93.0},
		{Width: 1280, Height: 720, CRF: 34, Bitrate: 520_000, VMAF: 74.8},
		{Width: 640, Height: 360, CRF: 27, Bitrate: 360_000, VMAF: 64.1},
		{Width: 640, Height: 360, CRF: 20, Bitrate: 800_000, VMAF: 76.5},
	}
}

func sampleRungs() []ladder.Rung {
	return []ladder.Rung{
		{
			Width: 1920, Height: 1080, Bitrate: 3_100_000, CRF: 24.5, MaxRate: 6_200_000, PredictedVMAF: 95.0,
			Measured: &ladder.Measurement{Bitrate: 2_970_000, VMAF: 95.3, HalfWidth: 0.6},
			Command:  "ffmpeg -i source.mov -c:v libx264 -crf 24.5 1080p.mp4",
		},
		{
			Width: 1280, Height: 720, Bitrate: 1_150_000, CRF: 27, MaxRate: 2_300_000, PredictedVMAF: 86.6,
			Measured:   &ladder.Measurement{Bitrate: 1_100_000, VMAF: 84.1, HalfWidth: 0.7},
			Calibrated: true,
			Command:    "ffmpeg -i source.mov -c:v libx264 -crf 27 720p.mp4",
		},
		{
			Width: 640, Height: 360, Bitrate: 360_000, CRF: 27, MaxRate: 720_000, PredictedVMAF: 64.1,
			Measured: &ladder.Measurement{Bitrate: 350_000, VMAF: 64.5, HalfWidth: 0.9},
			Command:  "ffmpeg -i source.mov -c:v libx264 -crf 27 360p.mp4",
		},
	}
}

func sampleLadder(
	t *testing.T,
) *ladder.Result {
	t.Helper()

	codec, err := encode.Lookup("h264")
	if err != nil {
		t.Fatal(err)
	}

	return &ladder.Result{
		Source:      &analysis.Report{Info: sampleInfo("/videos/source.mov"), Bitstream: sampleBitstream()},
		Codec:       codec,
		Preset:      "fast",
		Constraints: ladder.Constraints{TopVMAF: 95, Step: 6},
		Digest: ladder.Digest{
			Segments: []media.Interval{{Start: 0, End: media.Seconds(2)}, {Start: media.Seconds(30), End: media.Seconds(32)}},
			Duration: media.Seconds(4),
			Share:    4.0 / fixtureDuration,
		},
		Probes:  sampleProbes(),
		Hull:    []ladder.HullPoint{{Bitrate: 360_000, VMAF: 64.1, Height: 360}, {Bitrate: 5_600_000, VMAF: 97.1, Height: 1080}},
		Rungs:   sampleRungs(),
		Timings: map[string]string{"digest": "1.2s", "probe": "40s", "verify": "12s"},
		Elapsed: media.Seconds(53.4),
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write(
	[]byte,
) (int, error) {
	return 0, os.ErrClosed
}
