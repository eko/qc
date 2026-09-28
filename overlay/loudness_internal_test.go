package overlay

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/audio"
	"github.com/eko/qc/audio/loudness"
	"github.com/eko/qc/media"
)

// withAudio is the inspection with two analysed tracks, the second one
// the default: its short-term loudness rises by 1 LU every 100 ms for
// 300 ms, its momentary one is silent at first.
func withAudio() *analysis.Report {
	report := inspection()
	report.Info.Audio = []media.AudioStream{{Index: 1}, {Index: 2, Default: true}}

	series := loudness.Series{ShortTerm: []float64{-30, -29, -28}, Momentary: []float64{loudness.Floor, -20, -21}}
	report.Audio = &analysis.AudioReport{Tracks: []audio.Track{
		{Stream: 1},
		{Stream: 2, Loudness: loudness.Result{Series: series}},
	}}

	return report
}

func TestLoudnessTrack(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		report func() *analysis.Report
		want   int
	}{
		{name: "no audio", report: inspection, want: -1},
		{name: "the default track", report: withAudio, want: 2},
		{
			name: "the first track when the default one was not analysed",
			report: func() *analysis.Report {
				r := withAudio()
				r.Audio.Tracks = r.Audio.Tracks[:1]

				return r
			},
			want: 1,
		},
		{
			name: "without stream information",
			report: func() *analysis.Report {
				r := withAudio()
				r.Info = nil

				return r
			},
			want: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			track := loudnessTrack(Input{Report: testCase.report()})
			if testCase.want < 0 {
				assert.Nil(t, track)

				return
			}

			require.NotNil(t, track)
			assert.Equal(t, testCase.want, track.Stream)
		})
	}
}

func TestLoudnessRow(
	t *testing.T,
) {
	title, err := newTitle(Input{Report: withAudio()})
	require.NoError(t, err)

	rows := title.leftRows(Options{Items: []Item{ItemLoudness}})
	require.Len(t, rows, 1)

	testCases := []struct {
		name  string
		frame int
		want  string
	}{
		{name: "before the first step", frame: 1, want: "LUFS –"},
		{name: "silent momentary", frame: 3, want: "LUFS S -30.0 M –"},
		{name: "values", frame: 8, want: "LUFS S -28.0 M -21.0"},
		{name: "after the series", frame: 11, want: "LUFS –"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, plain(rows[0].text(testCase.frame)))
		})
	}
}

// plain strips the ASS override blocks of a row and collapses its spaces.
func plain(
	s string,
) string {
	var b strings.Builder

	depth := 0

	for _, r := range s {
		switch {
		case r == '{':
			depth++
		case r == '}':
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}

	return strings.Join(strings.Fields(b.String()), " ")
}
