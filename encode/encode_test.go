package encode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookup(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		codec       string
		wantEncoder string
		wantErr     bool
	}{
		{name: "h264", codec: "h264", wantEncoder: "libx264"},
		{name: "case insensitive", codec: "HEVC", wantEncoder: "libx265"},
		{name: "av1", codec: "av1", wantEncoder: "libsvtav1"},
		{name: "unknown", codec: "vp9", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := Lookup(testCase.codec)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrUnknownCodec)
				assert.Contains(t, err.Error(), `"vp9"`)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantEncoder, codec.Encoder)
		})
	}
}

func TestArgs(
	t *testing.T,
) {
	capped := Params{Width: 1280, Height: 720, CRF: 27.5, GOP: 50, MaxRate: 3_000_000, BufSize: 4_000_000}

	testCases := []struct {
		name    string
		codec   string
		params  Params
		want    []string
		notWant []string
	}{
		{
			name:   "x264 fixed gop without scene cuts",
			codec:  "h264",
			params: capped,
			want: []string{
				"libx264", "-crf", "27.5", "-g", "50", "-keyint_min", "-sc_threshold", "0",
				"-maxrate", "3000000", "-bufsize", "4000000", "fast",
				"scale=1280:720:flags=bicubic,format=yuv420p",
			},
		},
		{
			name:   "SVT-AV1 rate cap within its 100 Mb/s limit",
			codec:  "av1",
			params: Params{Width: 1920, Height: 1080, CRF: 20, MaxRate: 140_000_000, BufSize: 280_000_000},
			want:   []string{"-maxrate", "100000000", "-bufsize", "200000000"},
		},
		{
			name:    "x264 encoder gop keeps scene cuts",
			codec:   "h264",
			params:  Params{Width: 640, Height: 360, CRF: 30},
			want:    []string{"libx264", "30"},
			notWant: []string{"-g", "-sc_threshold", "-maxrate", "-bufsize"},
		},
		{
			name:   "x265 scene cuts go through x265-params",
			codec:  "hevc",
			params: capped,
			want:   []string{"libx265", "log-level=error:scenecut=0", "-tag:v", "hvc1", "veryfast"},
		},
		{
			name:    "x265 without gop only quietens the log",
			codec:   "hevc",
			params:  Params{Width: 640, Height: 360, CRF: 30},
			want:    []string{"log-level=error"},
			notWant: []string{"log-level=error:scenecut=0"},
		},
		{
			name:    "svt-av1",
			codec:   "av1",
			params:  capped,
			want:    []string{"libsvtav1", "-preset", "8", "-g", "50"},
			notWant: []string{"-sc_threshold", "-x265-params"},
		},
		{
			name:   "10-bit and preset override",
			codec:  "av1",
			params: Params{Width: 640, Height: 360, CRF: 40, Preset: "6", BitDepth: 10},
			want:   []string{"6", "scale=640:360:flags=bicubic,format=yuv420p10le"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := Lookup(testCase.codec)
			require.NoError(t, err)

			args := codec.Args(testCase.params)
			assert.Equal(t, []string{"-an", "-sn", "-dn"}, args[:3])

			for _, w := range testCase.want {
				assert.Contains(t, args, w)
			}

			for _, w := range testCase.notWant {
				assert.NotContains(t, args, w)
			}
		})
	}
}

func TestCommandLine(
	t *testing.T,
) {
	codec, err := Lookup("h264")
	require.NoError(t, err)

	cmd := codec.CommandLine("my movie.mov", "out.mp4", Params{Width: 640, Height: 360, CRF: 30})

	assert.Equal(t,
		"ffmpeg -i 'my movie.mov' -an -sn -dn -vf scale=640:360:flags=bicubic,format=yuv420p "+
			"-c:v libx264 -preset fast -crf 30 out.mp4",
		cmd)
}

func TestQuote(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "out_1080p-5000k.mp4", want: "out_1080p-5000k.mp4"},
		{name: "path and option value", in: "/tmp/a/log-level=error+x@50%", want: "/tmp/a/log-level=error+x@50%"},
		{name: "empty", in: "", want: "''"},
		{name: "space", in: "my movie.mov", want: "'my movie.mov'"},
		{name: "filter graph", in: "scale=1:2:flags=bicubic,format=yuv420p", want: "scale=1:2:flags=bicubic,format=yuv420p"},
		{name: "shell metacharacters", in: "a&b(1).mp4", want: "'a&b(1).mp4'"},
		{name: "dollar", in: "$HOME", want: "'$HOME'"},
		{name: "single quote", in: "it's.mp4", want: `'it'\''s.mp4'`},
		{name: "non ascii", in: "été.mov", want: "'été.mov'"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, quote(testCase.in))
		})
	}
}

func TestLookupReturnsCopies(
	t *testing.T,
) {
	for _, hw := range []Hardware{HardwareCPU, HardwareNVENC} {
		first, err := LookupFor("h264", hw)
		require.NoError(t, err)

		want := append([]float64(nil), first.ProbeCRFs...)
		first.ProbeCRFs[0] = -1
		first.MinCRF = -1

		second, err := LookupFor("h264", hw)
		require.NoError(t, err)
		assert.Equal(t, want, second.ProbeCRFs, "%s: the table is not shared with callers", hw)
		assert.Positive(t, second.MinCRF)
	}
}
