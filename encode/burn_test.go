package encode

import (
	"cmp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/internal/testutil"
)

func TestBurnArgs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		spec    BurnSpec
		encoder BurnEncoder
		want    []string
		notWant []string
	}{
		{
			name: "defaults, mp4 with aac",
			spec: BurnSpec{Source: "in.mp4", Subtitles: "/tmp/o.ass", Output: "out.mp4", AudioCodec: "aac"},
			want: []string{
				"-v error -nostdin -y -nostats -progress pipe:1 -i in.mp4 -map 0:v:0",
				"-vf scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p,subtitles=filename=\\'/tmp/o.ass\\'",
				"-fps_mode passthrough -c:v libx264 -preset fast -crf 20",
				"-map 0:a:0 -c:a copy -movflags +faststart out.mp4",
			},
		},
		{
			name: "scaled, pcm audio re-encoded for mp4",
			spec: BurnSpec{Source: "in.mov", Subtitles: "o.ass", Output: "out.MP4", AudioCodec: "pcm_s24le", Height: 720, CRF: 18.5, Preset: "veryfast"},
			want: []string{
				"-vf scale=-2:720:flags=bicubic,format=yuv420p,subtitles=",
				"-preset veryfast -crf 18.5",
				"-map 0:a:0 -c:a aac -b:a 192k -movflags +faststart out.MP4",
			},
		},
		{
			name:    "matroska holds any audio",
			spec:    BurnSpec{Source: "in.mov", Subtitles: "o.ass", Output: "out.mkv", AudioCodec: "pcm_s24le", FontsDir: "/fonts"},
			want:    []string{"subtitles=filename=\\'o.ass\\':fontsdir=\\'/fonts\\'", "-c:a copy out.mkv"},
			notWant: []string{"faststart"},
		},
		{
			name:    "no audio",
			spec:    BurnSpec{Source: "in.mov", Subtitles: "o.ass", Output: "out.mov"},
			want:    []string{"-movflags +faststart out.mov"},
			notWant: []string{"0:a", "-c:a"},
		},
		{
			name:    "videotoolbox encodes a single pass decoded on the CPU",
			spec:    BurnSpec{Source: "in.mp4", Subtitles: "o.ass", Output: "out.mp4", CRF: 30},
			encoder: BurnVideoToolbox,
			want:    []string{"-progress pipe:1 -i in.mp4", "-fps_mode passthrough -c:v h264_videotoolbox -q:v 60 -movflags"},
			notWant: []string{"libx264", "-crf", "-hwaccel"},
		},
		{
			name:    "nvenc decodes with nvdec",
			spec:    BurnSpec{Source: "in.mp4", Subtitles: "o.ass", Output: "out.mkv"},
			encoder: BurnNVENC,
			want:    []string{"-hwaccel cuda -i in.mp4", "-c:v h264_nvenc -preset p4 -tune hq -rc vbr -cq 21 -b:v 0 out.mkv"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			args := strings.Join(burnArgs(testCase.spec, cmp.Or(testCase.encoder, BurnX264)), " ")

			for _, w := range testCase.want {
				assert.Contains(t, args, w)
			}

			for _, w := range testCase.notWant {
				assert.NotContains(t, args, w)
			}
		})
	}
}

func TestFilterValue(
	t *testing.T,
) {
	testCases := []struct {
		name string
		path string
		want string
	}{
		{name: "plain", path: "/tmp/a.ass", want: `\'/tmp/a.ass\'`},
		{name: "graph separators", path: "a,b;c[d]:e.ass", want: `\'a\,b\;c\[d\]:e.ass\'`},
		{name: "quote and backslash", path: `it's\x`, want: `\'it\'\\\'\'s\\x\'`},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, filterValue(testCase.path))
		})
	}
}

func TestProgressFrames(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		line   string
		want   int
		wantOK bool
	}{
		{name: "frame count", line: "frame=42\n", want: 42, wantOK: true},
		{name: "other key", line: "fps=12.5", wantOK: false},
		{name: "malformed", line: "frame=N/A", wantOK: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			frames, ok := progressFrames([]byte(testCase.line))
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.want, frames)
		})
	}
}

func TestCheckBurn(
	t *testing.T,
) {
	// withLibass lists the subtitles filter, and fails encodes when fail
	// is set.
	const withLibass = `case "$*" in *-filters*) printf ' T.. scale  V->V  Scale.\n ... subtitles  V->V  Render text subtitles.\n';; ` +
		`*h264_videotoolbox*) [ -z "$FAIL" ] || { echo 'cannot create compression session' >&2; exit 1; };; esac`

	testCases := []struct {
		name    string
		body    string
		encoder BurnEncoder
		wantErr error
		wantMsg string
	}{
		{name: "subtitles filter", body: withLibass},
		{name: "a hardware encoder encoding its test frames", body: withLibass, encoder: BurnVideoToolbox},
		{
			name:    "a hardware encoder failing",
			body:    "FAIL=1\n" + withLibass,
			encoder: BurnVideoToolbox,
			wantMsg: "videotoolbox encoder: ",
		},
		{name: "auto is not tested", body: "FAIL=1\n" + withLibass, encoder: BurnAuto},
		{
			name:    "ffmpeg without libass",
			body:    `printf ' T.. scale  V->V  Scale.\n ... drawbox  V->V  Draw a box.\n'`,
			wantErr: ErrNoSubtitlesFilter,
		},
		{
			name:    "ffmpeg failing",
			body:    `exit 3`,
			wantMsg: "list ffmpeg filters",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := NewFFmpeg(testutil.FakeFFmpeg(t, testCase.body)).CheckBurn(t.Context(), testCase.encoder)

			switch {
			case testCase.wantErr != nil:
				require.ErrorIs(t, err, testCase.wantErr)
			case testCase.wantMsg != "":
				require.ErrorContains(t, err, testCase.wantMsg)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestBurnMissingBinary(
	t *testing.T,
) {
	err := NewFFmpeg("qc-no-such-ffmpeg").Burn(t.Context(), BurnSpec{Source: "in.mp4", Subtitles: "o.ass", Output: "out.mp4"})
	require.ErrorIs(t, err, ffexec.ErrNotFound)
	assert.Contains(t, err.Error(), "burn o.ass into in.mp4")
}

// TestBurn burns a script stored under a path full of characters the
// filter graph would otherwise read (quote, comma, brackets, colon,
// semicolon, backslash), and reports progress.
func TestBurn(
	t *testing.T,
) {
	testutil.RequireFilter(t, "subtitles")

	source := testutil.Generate(t, testutil.Clip{Seconds: 1})

	dir := filepath.Join(t.TempDir(), `it's a [dir], x;y:z\w`)
	require.NoError(t, os.MkdirAll(dir, 0o750))

	script := filepath.Join(dir, "o.ass")
	require.NoError(t, os.WriteFile(script, []byte(testScript), 0o600))

	output := filepath.Join(t.TempDir(), "out.mp4")

	var frames []int

	err := NewFFmpeg("ffmpeg").Burn(t.Context(), BurnSpec{
		Source: source, Subtitles: script, Output: output, Height: 90, Preset: "ultrafast",
		Progress: func(n int) { frames = append(frames, n) },
	})
	require.NoError(t, err)

	require.NotEmpty(t, frames)
	assert.Equal(t, 25, frames[len(frames)-1])

	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=height", "-of", "csv=p=0", output).Output()
	require.NoError(t, err)
	assert.Equal(t, "90", strings.TrimSpace(string(out)))
}

// testScript draws a box for the first second.
const testScript = `[Script Info]
ScriptType: v4.00+
PlayResX: 320
PlayResY: 180

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: qc,Sans,20,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,0,0,7,0,0,0,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:01.00,qc,,0,0,0,,{\pos(0,0)\p1}m 0 0 l 50 0 50 50 0 50{\p0}
`
