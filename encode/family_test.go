package encode

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupports(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		codec         string
		hw            Hardware
		wantGrain     bool
		wantChunkJoin bool
	}{
		{name: "x264", codec: "h264", wantChunkJoin: true},
		{name: "x265", codec: "hevc", wantChunkJoin: true},
		{name: "svt-av1", codec: "av1", wantGrain: true, wantChunkJoin: true},
		{name: "h264_nvenc", codec: "h264", hw: HardwareNVENC},
		{name: "hevc_nvenc", codec: "hevc", hw: HardwareNVENC},
		{name: "av1_nvenc", codec: "av1", hw: HardwareNVENC},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			codec, err := LookupFor(testCase.codec, testCase.hw)
			require.NoError(t, err)

			assert.Equal(t, testCase.wantGrain, codec.Supports(FeatureFilmGrain))
			assert.Equal(t, testCase.wantChunkJoin, codec.Supports(FeatureChunkJoin))
			assert.False(t, codec.Supports(Feature(0)), "unknown feature")
		})
	}
}

// TestCodecWithoutFamily checks that codecs built by hand or decoded from
// a JSON report drive their encoder like the codecs of the tables.
func TestCodecWithoutFamily(
	t *testing.T,
) {
	params := Params{Width: 1280, Height: 720, CRF: 30, GOP: 50, BitDepth: 10, FilmGrain: 8}

	t.Run("decoded from JSON", func(t *testing.T) {
		for _, hw := range []Hardware{HardwareCPU, HardwareNVENC} {
			for _, name := range []string{"h264", "hevc", "av1"} {
				known, err := LookupFor(name, hw)
				require.NoError(t, err)

				encoded, err := json.Marshal(known)
				require.NoError(t, err)

				var decoded Codec
				require.NoError(t, json.Unmarshal(encoded, &decoded))

				decoded.DefaultPreset = known.DefaultPreset
				assert.Equal(t, known.Args(params), decoded.Args(params), "%s/%s", hw, name)
				assert.Equal(t, known.QualityOption(), decoded.QualityOption())
				assert.Equal(t, known.InputArgs(), decoded.InputArgs())
				assert.Equal(t, known.Supports(FeatureFilmGrain), decoded.Supports(FeatureFilmGrain))
			}
		}
	})

	testCases := []struct {
		name        string
		codec       Codec
		want        []string
		wantOption  string
		wantInput   []string
		wantChunked bool
	}{
		{
			name:       "unknown CPU encoder: the common options only",
			codec:      Codec{Name: "vp9", Encoder: "libvpx-vp9", DefaultPreset: "good"},
			want:       []string{"-an", "-sn", "-dn", "-vf", "scale=1280:720:flags=bicubic,format=yuv420p10le", "-c:v", "libvpx-vp9", "-preset", "good", "-crf", "30", "-g", "50", "-keyint_min", "50"},
			wantOption: "crf",
		},
		{
			name:       "unknown NVENC encoder",
			codec:      Codec{Name: "vp9", Encoder: "vp9_nvenc", Hardware: HardwareNVENC, DefaultPreset: "p5"},
			want:       []string{"-an", "-sn", "-dn", "-vf", "scale=1280:720:flags=bicubic,format=p010le", "-c:v", "vp9_nvenc", "-preset", "p5", "-tune", "hq", "-rc", "vbr", "-cq", "30", "-b:v", "0", "-g", "50", "-rc-lookahead", "20", "-no-scenecut", "1", "-forced-idr", "1"},
			wantOption: "cq",
			wantInput:  []string{"-hwaccel", "cuda"},
		},
		{
			name:        "known encoder built by hand",
			codec:       Codec{Name: "h264", Encoder: "libx264", DefaultPreset: "fast"},
			want:        []string{"-an", "-sn", "-dn", "-vf", "scale=1280:720:flags=bicubic,format=yuv420p10le", "-c:v", "libx264", "-preset", "fast", "-crf", "30", "-g", "50", "-keyint_min", "50", "-sc_threshold", "0"},
			wantOption:  "crf",
			wantChunked: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.codec.Args(params))
			assert.Equal(t, testCase.wantOption, testCase.codec.QualityOption())
			assert.Equal(t, testCase.wantInput, testCase.codec.InputArgs())
			assert.Equal(t, testCase.wantChunked, testCase.codec.Supports(FeatureChunkJoin))
			assert.False(t, testCase.codec.Supports(FeatureFilmGrain))
		})
	}
}
