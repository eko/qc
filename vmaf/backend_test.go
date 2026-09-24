package vmaf

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBackend(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		input   string
		want    Backend
		wantErr error
	}{
		{name: "empty is cpu", input: "", want: BackendCPU},
		{name: "cpu", input: "cpu", want: BackendCPU},
		{name: "cuda, any case", input: " CUDA ", want: BackendCUDA},
		{name: "auto", input: "auto", want: BackendAuto},
		{name: "unknown", input: "vulkan", wantErr: ErrBackend},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseBackend(testCase.input)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestBackendString(
	t *testing.T,
) {
	assert.Equal(t, "cpu", BackendCPU.String())
	assert.Equal(t, "cuda", BackendCUDA.String())
	assert.Equal(t, "auto", BackendAuto.String())
}

// writeModel writes a JSON model naming features to a temporary file.
func writeModel(
	t *testing.T,
	name, body string,
) ModelSpec {
	t.Helper()

	path := filepath.Join(t.TempDir(), name+".json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return ModelSpec{Name: name, Source: path, FromPath: true}
}

func TestCUDAUnsupported(
	t *testing.T,
) {
	v0 := writeModel(t, "v0", `{"model_dict": {"feature_names": [
		"VMAF_integer_feature_adm2_score", "VMAF_integer_feature_motion2_score",
		"VMAF_integer_feature_vif_scale0_score", "VMAF_integer_feature_vif_scale3_score"]}}`)
	v1 := writeModel(t, "v1", `{"model_dict": {"feature_names": [
		"Cambi_feature_cambi_score", "VMAF_integer_feature_adm3_score", "VMAF_integer_feature_motion2_score"]}}`)
	invalid := writeModel(t, "invalid", "{")

	testCases := []struct {
		name        string
		spec        ModelSpec
		wantMissing []string
		wantErr     bool
	}{
		{name: "built-in v0.6.1", spec: ModelSpec{Source: "vmaf_v0.6.1"}},
		{name: "built-in 4K NEG", spec: ModelSpec{Source: "vmaf_4k_v0.6.1neg"}},
		{
			name:        "built-in v1",
			spec:        ModelSpec{Source: "vmaf_v1.0.16_3d0h"},
			wantMissing: []string{"built-in model vmaf_v1.0.16_3d0h"},
		},
		{name: "integer v0 features", spec: v0},
		{
			name:        "v1 features",
			spec:        v1,
			wantMissing: []string{"Cambi_feature_cambi_score", "VMAF_integer_feature_adm3_score"},
		},
		{name: "invalid json", spec: invalid, wantErr: true},
		{name: "missing file", spec: ModelSpec{Source: filepath.Join(t.TempDir(), "nope.json"), FromPath: true}, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			missing, err := CUDAUnsupported(testCase.spec)
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.wantMissing, missing)
		})
	}
}

// TestInstalledV1ModelsNeedCPU checks the finding behind the CUDA backend
// on the installed VMAF v1 models: none of their four features has a CUDA
// extractor in libvmaf 3.2.1, so they cannot run on the GPU.
func TestInstalledV1ModelsNeedCPU(
	t *testing.T,
) {
	spec, err := ResolveModel("auto", 1080, 25, DefaultModelDirs())
	require.NoError(t, err)

	if !spec.FromPath {
		t.Skip("VMAF v1 models not installed")
	}

	missing, err := CUDAUnsupported(spec)
	require.NoError(t, err)
	assert.Len(t, missing, 4)
}

func TestResolveBackend(
	t *testing.T,
) {
	v1 := ModelSpec{Name: "vmaf_v1.0.16_3d0h", Source: "vmaf_v1.0.16_3d0h"}
	v0 := ModelSpec{Name: "vmaf_v0.6.1", Source: "vmaf_v0.6.1"}
	broken := ModelSpec{Name: "broken", Source: filepath.Join(t.TempDir(), "nope.json"), FromPath: true}
	errNoDevice := errors.New("no device")

	testCases := []struct {
		name       string
		requested  Backend
		specs      []ModelSpec
		initErr    error
		wantInit   bool
		want       Backend
		wantReason string
		wantErr    error
	}{
		{name: "cpu", requested: BackendCPU, specs: []ModelSpec{v1}, want: BackendCPU},
		{name: "unknown", requested: "opencl", wantErr: ErrBackend},
		{name: "cuda with a v1 model", requested: BackendCUDA, specs: []ModelSpec{v0, v1}, wantErr: ErrCUDAModel},
		{
			name: "auto with a v1 model", requested: BackendAuto, specs: []ModelSpec{v1}, want: BackendCPU,
			wantReason: "vmaf: model features have no CUDA extractor: vmaf_v1.0.16_3d0h needs built-in model vmaf_v1.0.16_3d0h",
		},
		{name: "auto with an unreadable model", requested: BackendAuto, specs: []ModelSpec{broken}, want: BackendCPU, wantReason: "vmaf: read model"},
		{name: "cuda on a ready device", requested: BackendCUDA, specs: []ModelSpec{v0}, wantInit: true, want: BackendCUDA},
		{name: "cuda without a device", requested: BackendCUDA, specs: []ModelSpec{v0}, initErr: errNoDevice, wantInit: true, wantErr: errNoDevice},
		{
			name: "auto without a device", requested: BackendAuto, specs: []ModelSpec{v0}, initErr: errNoDevice, wantInit: true,
			want: BackendCPU, wantReason: "vmaf: no device",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			initialised := false
			initCUDA := func() error {
				initialised = true

				return testCase.initErr
			}

			choice, err := ResolveBackend(testCase.requested, testCase.specs, initCUDA)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Equal(t, testCase.want, choice.Backend)
			assert.Equal(t, testCase.wantInit, initialised, "the device is only initialised for CUDA-capable models")

			if testCase.wantReason == "" {
				assert.Empty(t, choice.Reason)
			} else {
				assert.Contains(t, choice.Reason, testCase.wantReason)
			}
		})
	}
}
