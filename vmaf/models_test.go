package vmaf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// builtinModel is a libvmaf built-in version.
const builtinModel = "vmaf_v0.6.1"

func TestResolveModel(
	t *testing.T,
) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "vmaf_custom")
	require.NoError(t, os.MkdirAll(nested, 0o750))

	custom := filepath.Join(nested, "custom_2160.json")
	require.NoError(t, os.WriteFile(custom, []byte("{}"), 0o600))

	file := filepath.Join(dir, "vmaf_4k_file.json")
	require.NoError(t, os.WriteFile(file, []byte("{}"), 0o600))

	testCases := []struct {
		name         string
		model        string
		height       int
		fps          float64
		dirs         []string
		wantName     string
		wantSource   string
		wantFromPath bool
		wantHeight   int
		wantErr      error
	}{
		{name: "auto hd", model: "auto", height: 1080, fps: 25, wantName: "vmaf_v1.0.16_3d0h", wantHeight: 1080},
		{name: "auto uhd", model: "", height: 2160, fps: 24, wantName: "vmaf_v1.0.16_3d0h_2160", wantHeight: 2160},
		{name: "auto hfr", model: "auto", height: 1080, fps: 50, wantName: "vmaf_v1.0.16_hfr_3d0h", wantHeight: 1080},
		{name: "auto hfr uhd", model: "auto", height: 2160, fps: 60, wantName: "vmaf_v1.0.16_hfr_3d0h_2160", wantHeight: 2160},
		{name: "29.97 fps is not hfr", model: "auto", height: 720, fps: 30000.0 / 1001, wantName: "vmaf_v1.0.16_3d0h", wantHeight: 1080},
		{name: "built-in version", model: builtinModel, height: 720, fps: 25, wantName: builtinModel, wantSource: builtinModel, wantHeight: 1080},
		{
			name:  "found in a nested directory",
			model: "custom_2160", height: 1080, fps: 25,
			dirs:     []string{filepath.Join(dir, "missing"), dir},
			wantName: "custom_2160", wantSource: custom, wantFromPath: true, wantHeight: 2160,
		},
		{
			name:  "not in dirs falls back to built-in",
			model: "other", height: 1080, fps: 25,
			dirs:     []string{dir},
			wantName: "other", wantSource: "other", wantHeight: 1080,
		},
		{
			name:  "json path",
			model: file, height: 720, fps: 25,
			wantName: "vmaf_4k_file", wantSource: file, wantFromPath: true, wantHeight: 2160,
		},
		{name: "missing json path", model: filepath.Join(dir, "nope.json"), height: 720, fps: 25, wantErr: ErrModelNotFound},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			spec, err := ResolveModel(testCase.model, testCase.height, testCase.fps, testCase.dirs)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				require.ErrorIs(t, err, os.ErrNotExist)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantName, spec.Name)
			assert.Equal(t, testCase.wantHeight, spec.Height)
			assert.Equal(t, testCase.wantHeight*16/9, spec.Width)
			assert.Equal(t, testCase.wantFromPath, spec.FromPath)

			if testCase.wantSource != "" {
				assert.Equal(t, testCase.wantSource, spec.Source)
			}
		})
	}
}

func TestDeviceModel(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		device  string
		fps     float64
		want    string
		wantErr error
	}{
		{name: "phone", device: DevicePhone, fps: 25, want: "vmaf_v1.0.16_5d0h"},
		{name: "tv", device: DeviceTV, fps: 30000.0 / 1001, want: "vmaf_v1.0.16_3d0h"},
		{name: "4k", device: Device4K, fps: 24, want: "vmaf_v1.0.16_3d0h_2160"},
		{name: "high frame rate phone", device: DevicePhone, fps: 50, want: "vmaf_v1.0.16_hfr_5d0h"},
		{name: "high frame rate 4k", device: Device4K, fps: 60, want: "vmaf_v1.0.16_hfr_3d0h_2160"},
		{name: "unknown", device: "watch", fps: 25, wantErr: ErrUnknownDevice},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := DeviceModel(testCase.device, testCase.fps)
			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.Contains(t, err.Error(), "phone, tv, 4k")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)

			spec, err := ResolveModel(got, 1080, testCase.fps, DefaultModelDirs())
			require.NoError(t, err)
			assert.True(t, spec.FromPath, "installed with libvmaf")
		})
	}
}

func TestDefaultListsAreClones(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		list  func() []string
		first string
	}{
		{name: "devices", list: Devices, first: DevicePhone},
		{name: "model directories", list: DefaultModelDirs, first: "/opt/homebrew/share/libvmaf/model"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := testCase.list()
			require.NotEmpty(t, got)
			assert.Equal(t, testCase.first, got[0])

			got[0] = "changed"
			assert.Equal(t, testCase.first, testCase.list()[0])
		})
	}
}
