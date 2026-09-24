package encode

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestParseHardware(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    Hardware
		wantErr error
	}{
		{name: "empty", input: "", want: HardwareCPU},
		{name: "cpu", input: "CPU", want: HardwareCPU},
		{name: "nvenc", input: " nvenc ", want: HardwareNVENC},
		{name: "unknown", input: "qsv", wantErr: ErrUnknownHardware},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseHardware(testCase.input)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Equal(t, testCase.want, got)
		})
	}

	assert.Equal(t, "cpu", HardwareCPU.String())
	assert.Equal(t, "nvenc", HardwareNVENC.String())
}

func TestLookupFor(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		codec       string
		hw          Hardware
		wantEncoder string
		wantStep    float64
		wantOption  string
		wantErr     error
	}{
		{name: "x264", codec: "h264", hw: HardwareCPU, wantEncoder: "libx264", wantStep: 0.5, wantOption: "crf"},
		{name: "svt-av1", codec: "av1", hw: HardwareCPU, wantEncoder: "libsvtav1", wantStep: 1, wantOption: "crf"},
		{name: "h264_nvenc", codec: "H264", hw: HardwareNVENC, wantEncoder: "h264_nvenc", wantStep: 1, wantOption: "cq"},
		{name: "hevc_nvenc", codec: "hevc", hw: HardwareNVENC, wantEncoder: "hevc_nvenc", wantStep: 1, wantOption: "cq"},
		{name: "av1_nvenc", codec: "av1", hw: HardwareNVENC, wantEncoder: "av1_nvenc", wantStep: 1, wantOption: "cq"},
		{name: "unknown codec", codec: "vp9", hw: HardwareNVENC, wantErr: ErrUnknownCodec},
		{name: "unknown hardware", codec: "h264", hw: "amf", wantErr: ErrUnknownHardware},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := LookupFor(testCase.codec, testCase.hw)
			require.ErrorIs(t, err, testCase.wantErr)

			if testCase.wantErr != nil {
				return
			}

			assert.Equal(t, testCase.wantEncoder, codec.Encoder)
			assert.Equal(t, testCase.hw, codec.Hardware)
			assert.InDelta(t, testCase.wantStep, codec.Step(), 1e-12)
			assert.Equal(t, testCase.wantOption, codec.QualityOption())
		})
	}
}

// TestNVENCProbeCQs checks the invariants the ladder relies on: three
// probes, high to low quality, inside the codec range and on its grid.
func TestNVENCProbeCQs(
	t *testing.T,
) {
	for name, codec := range nvencCodecs {
		t.Run(name, func(t *testing.T) {
			require.Len(t, codec.ProbeCRFs, 3)
			assert.IsIncreasing(t, codec.ProbeCRFs)
			assert.GreaterOrEqual(t, codec.ProbeCRFs[0], codec.MinCRF)
			assert.LessOrEqual(t, codec.ProbeCRFs[2], codec.MaxCRF)

			for _, cq := range codec.ProbeCRFs {
				assert.Zero(t, cq-float64(int(cq/codec.Step()))*codec.Step())
			}
		})
	}
}

func TestStepDefault(
	t *testing.T,
) {
	assert.InDelta(t, 0.5, Codec{}.Step(), 1e-12)
}

func TestNVENCArgs(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		codec  string
		params Params
		want   string
	}{
		{
			name:   "h264 fixed gop, capped",
			codec:  "h264",
			params: Params{Width: 1280, Height: 720, CRF: 28, GOP: 50, MaxRate: 3_000_000, BufSize: 6_000_000},
			want: "-an -sn -dn -vf scale=1280:720:flags=bicubic,format=yuv420p -c:v h264_nvenc -preset p5 " +
				"-tune hq -rc vbr -cq 28 -b:v 0 -g 50 -maxrate 3000000 -bufsize 6000000 " +
				"-rc-lookahead 20 -no-scenecut 1 -forced-idr 1",
		},
		{
			name:   "hevc Main10 with hvc1",
			codec:  "hevc",
			params: Params{Width: 1920, Height: 1080, CRF: 30, GOP: 48, BitDepth: 10, Preset: "p7"},
			want: "-an -sn -dn -vf scale=1920:1080:flags=bicubic,format=p010le -c:v hevc_nvenc -preset p7 " +
				"-tune hq -rc vbr -cq 30 -b:v 0 -g 48 -rc-lookahead 20 -no-scenecut 1 -forced-idr 1 -tag:v hvc1",
		},
		{
			name:   "av1 without gop keeps the encoder's",
			codec:  "av1",
			params: Params{Width: 640, Height: 360, CRF: 38, FilmGrain: 10},
			want: "-an -sn -dn -vf scale=640:360:flags=bicubic,format=yuv420p -c:v av1_nvenc -preset p5 " +
				"-tune hq -rc vbr -cq 38 -b:v 0 -rc-lookahead 20",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := LookupFor(testCase.codec, HardwareNVENC)
			require.NoError(t, err)

			assert.Equal(t, testCase.want, strings.Join(codec.Args(testCase.params), " "))
		})
	}
}

func TestNVENCCommandLines(
	t *testing.T,
) {
	codec, err := LookupFor("h264", HardwareNVENC)
	require.NoError(t, err)

	p := Params{Width: 640, Height: 360, CRF: 30, GOP: 50}

	assert.Equal(t,
		"ffmpeg -hwaccel cuda -i 'my movie.mov' -an -sn -dn -vf scale=640:360:flags=bicubic,format=yuv420p "+
			"-c:v h264_nvenc -preset p5 -tune hq -rc vbr -cq 30 -b:v 0 -g 50 "+
			"-rc-lookahead 20 -no-scenecut 1 -forced-idr 1 out.mp4",
		codec.CommandLine("my movie.mov", "out.mp4", p))

	script := codec.ChunkCommandLine("src.mov", "out.mp4", media.Rational{Num: 25, Den: 1},
		[]Chunk{{Start: 0, Frames: 50, CRF: 28}, {Start: 50, Frames: 50, CRF: 31}}, p)

	assert.Equal(t, 2, strings.Count(script, "ffmpeg -hwaccel cuda -ss"))
	assert.Contains(t, script, "-cq 31")

	cpu, err := Lookup("h264")
	require.NoError(t, err)
	assert.Nil(t, cpu.InputArgs())
}

// TestNVENCEncodeCommand runs an NVENC encode through a fake ffmpeg: the
// ladder's own encodes read the raw digest, so they carry no -hwaccel.
func TestNVENCEncodeCommand(
	t *testing.T,
) {
	log := t.TempDir() + "/args"
	bin := testutil.FakeFFmpeg(t, `echo "$*" > '`+log+`'`)

	codec, err := LookupFor("av1", HardwareNVENC)
	require.NoError(t, err)

	require.NoError(t, NewFFmpeg(bin).Encode(t.Context(), codec, "digest.nut", "out.mp4", Params{Width: 640, Height: 360, CRF: 38}))

	args, err := os.ReadFile(log)
	require.NoError(t, err)
	assert.Contains(t, string(args), "-i digest.nut -an")
	assert.Contains(t, string(args), "-c:v av1_nvenc")
	assert.NotContains(t, string(args), "-hwaccel")
}

// TestNVENCArgsParse hands the NVENC arguments to a real ffmpeg built with
// NVENC (the CUDA image): without a GPU the encoder fails to load the
// driver, but only after every option and value was parsed, so an unknown
// option or value fails the test. It skips when ffmpeg has no NVENC.
func TestNVENCArgsParse(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	encoders, err := exec.Command("ffmpeg", "-hide_banner", "-encoders").Output()
	require.NoError(t, err)

	for name, codec := range nvencCodecs {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(string(encoders), " "+codec.Encoder+" ") {
				t.Skipf("ffmpeg has no %s", codec.Encoder)
			}

			for _, depth := range []int{8, 10} {
				args := []string{"-hide_banner", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x180:d=0.2", "-frames:v", "1"}
				args = append(args, codec.Args(Params{Width: 320, Height: 180, CRF: codec.ProbeCRFs[1], GOP: 50,
					MaxRate: 1_000_000, BufSize: 2_000_000, BitDepth: depth})...)
				args = append(args, "-f", "null", "-")

				out, err := exec.Command("ffmpeg", args...).CombinedOutput()
				if err == nil {
					continue // a GPU encoded the frame
				}

				// Without a GPU the encoder fails loading the driver, after
				// the options were parsed.
				text := string(out)
				assert.True(t, strings.Contains(text, "Cannot load libcuda") || strings.Contains(text, "No capable devices found"),
					"%d-bit: %s", depth, text)

				for _, parseError := range []string{"Unrecognized option", "Error setting option", "Unable to parse", "Undefined constant"} {
					assert.NotContains(t, text, parseError, "%d-bit: %s", depth, text)
				}
			}
		})
	}
}
