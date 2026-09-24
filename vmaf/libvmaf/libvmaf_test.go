package libvmaf

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/vmaf"
)

const builtinModel = "vmaf_v0.6.1"

func TestLoadModel(
	t *testing.T,
) {
	dir := t.TempDir()
	invalid := filepath.Join(dir, "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte("{"), 0o600))

	installed, err := vmaf.ResolveModel(builtinModel, 1080, 25, vmaf.DefaultModelDirs())
	require.NoError(t, err)

	testCases := []struct {
		name    string
		spec    vmaf.ModelSpec
		skip    bool
		wantErr bool
	}{
		{name: "built-in", spec: vmaf.ModelSpec{Source: builtinModel}},
		{name: "installed json", spec: installed, skip: !installed.FromPath},
		{name: "unknown built-in", spec: vmaf.ModelSpec{Source: "no_such_model"}, wantErr: true},
		{name: "invalid json", spec: vmaf.ModelSpec{Source: invalid, FromPath: true}, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.skip {
				t.Skip("no installed model")
			}

			model, err := LoadModel(testCase.spec)
			if testCase.wantErr {
				require.ErrorIs(t, err, syscall.EINVAL)
				assert.Nil(t, model)

				return
			}

			require.NoError(t, err)
			model.Close()
			model.Close()
		})
	}
}

func TestVersion(
	t *testing.T,
) {
	assert.Regexp(t, `^\d+\.\d+\.\d+`, Version())
}

func TestLibvmafError(
	t *testing.T,
) {
	testCases := []struct {
		name string
		rc   int
		want string
	}{
		{name: "negated errno", rc: -int(syscall.EINVAL), want: syscall.EINVAL.Error()},
		{name: "other code", rc: 3, want: "error 3"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.EqualError(t, libvmafError(testCase.rc), testCase.want)
		})
	}
}

// texture fills f with a deterministic pattern, contrast-reduced when blur is
// set. 16-bit frames get the 10-bit equivalent.
func texture(
	f *frame.Frame,
	blur bool,
) {
	w, h := f.Luma.Width, f.Luma.Height
	high := f.Luma.BytesPerSample == 2

	for y := range h {
		for x := range w {
			v := (x*37 + y*91 + x*y) % 256
			if blur {
				v = 128 + (v-128)/4
			}

			if high {
				binary.LittleEndian.PutUint16(f.Luma.Pix[y*f.Luma.Stride+2*x:], uint16(v<<2))

				continue
			}

			f.Luma.Pix[y*f.Luma.Stride+x] = byte(v)
		}
	}

	for _, plane := range []frame.Plane{f.Cb, f.Cr} {
		for i := range plane.Pix {
			plane.Pix[i] = 0x80
			if high {
				// 512 = 0x0200, little endian.
				plane.Pix[i] = [2]byte{0x00, 0x02}[i%2]
			}
		}
	}
}

// TestScorer scores a textured frame against itself and against a
// contrast-reduced copy: identical frames must score far above distorted ones,
// at 8 and 10 bits.
func TestScorer(
	t *testing.T,
) {
	model, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer model.Close()

	const w, h = 64, 64

	testCases := []struct {
		name     string
		bitDepth int
	}{
		{name: "8 bits", bitDepth: 8},
		{name: "10 bits", bitDepth: 10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(w, h, frame.PoolOptions{Chroma: true, HighBitDepth: testCase.bitDepth > 8})

			score := func(blur bool) float64 {
				scorer, err := New([]*Model{model}, vmaf.ScorerConfig{Width: w, Height: h, BitDepth: testCase.bitDepth, Threads: 1})
				require.NoError(t, err)
				defer scorer.Close()

				for range 3 {
					ref, dist := pool.Get(), pool.Get()
					texture(ref, false)
					texture(dist, blur)
					require.NoError(t, scorer.Push(ref, dist))
					ref.Release()
					dist.Release()
				}

				scores, err := scorer.Scores()
				require.NoError(t, err)
				require.Len(t, scores, 3)

				return scores[1]
			}

			identical, blurred := score(false), score(true)

			assert.Greater(t, identical, 90.0)
			assert.Less(t, blurred, identical-20)
		})
	}
}

func TestScorerRejectsGeometry(
	t *testing.T,
) {
	model, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer model.Close()

	good := frame.NewPool(64, 64, frame.PoolOptions{Chroma: true}).Get()
	defer good.Release()

	testCases := []struct {
		name     string
		bitDepth int
		pool     frame.PoolOptions
		size     int
		refBad   bool
	}{
		{name: "size", bitDepth: 8, pool: frame.PoolOptions{Chroma: true}, size: 32, refBad: true},
		{name: "no chroma", bitDepth: 8, pool: frame.PoolOptions{}, size: 64, refBad: true},
		{name: "8-bit samples at 10 bits", bitDepth: 10, pool: frame.PoolOptions{Chroma: true}, size: 64, refBad: true},
		{name: "16-bit samples at 8 bits", bitDepth: 8, pool: frame.PoolOptions{Chroma: true, HighBitDepth: true}, size: 64},
		{name: "distorted only", bitDepth: 8, pool: frame.PoolOptions{Chroma: true}, size: 48},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scorer, err := New([]*Model{model}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: testCase.bitDepth, Threads: 1})
			require.NoError(t, err)
			defer scorer.Close()

			bad := frame.NewPool(testCase.size, testCase.size, testCase.pool).Get()
			defer bad.Release()

			ref := good
			if testCase.refBad {
				ref = bad
			}

			require.ErrorIs(t, scorer.Push(ref, bad), ErrGeometry)
		})
	}
}

func TestScorerErrors(
	t *testing.T,
) {
	model, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer model.Close()

	pool := frame.NewPool(64, 64, frame.PoolOptions{Chroma: true})

	push := func(t *testing.T, s *Scorer) {
		t.Helper()

		f := pool.Get()
		defer f.Release()

		texture(f, false)
		require.NoError(t, s.Push(f, f))
	}

	testCases := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{
			name: "model without features",
			run: func(*testing.T) error {
				_, err := New([]*Model{{}}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 8, Threads: 1})

				return err
			},
		},
		{
			name: "unsupported bit depth",
			run: func(t *testing.T) error {
				s, err := New([]*Model{model}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 17, Threads: 1})
				require.NoError(t, err)
				defer s.Close()

				f := frame.NewPool(64, 64, frame.PoolOptions{Chroma: true, HighBitDepth: true}).Get()
				defer f.Release()

				return s.Push(f, f)
			},
		},
		{
			name: "flushed twice",
			run: func(t *testing.T) error {
				s, err := New([]*Model{model}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 8, Threads: 1})
				require.NoError(t, err)
				defer s.Close()

				scores, err := s.Scores()
				require.NoError(t, err)
				assert.Empty(t, scores, "nothing pushed")

				_, err = s.Scores()

				return err
			},
		},
		{
			name: "push after flush",
			run: func(t *testing.T) error {
				s, err := New([]*Model{model}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 8, Threads: 1})
				require.NoError(t, err)
				defer s.Close()

				push(t, s)
				_, err = s.Scores()
				require.NoError(t, err)

				f := pool.Get()
				defer f.Release()

				return s.Push(f, f)
			},
		},
		{
			name: "model missing extracted features",
			run: func(t *testing.T) error {
				spec, err := vmaf.ResolveModel("vmaf_float_v0.6.1", 1080, 25, vmaf.DefaultModelDirs())
				require.NoError(t, err)

				other, err := LoadModel(spec)
				if err != nil {
					t.Skip("float model not installed")
				}
				defer other.Close()

				s, err := New([]*Model{model}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 8, Threads: 1})
				require.NoError(t, err)
				defer s.Close()

				push(t, s)
				s.models[0] = other

				_, err = s.Scores()

				return err
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.ErrorIs(t, testCase.run(t), syscall.EINVAL)
		})
	}
}

// loadDevice loads the VMAF v1 model of a device.
func loadDevice(
	t *testing.T,
	device string,
) *Model {
	t.Helper()

	name, err := vmaf.DeviceModel(device, 25)
	require.NoError(t, err)

	spec, err := vmaf.ResolveModel(name, 1080, 25, vmaf.DefaultModelDirs())
	require.NoError(t, err)

	model, err := LoadModel(spec)
	require.NoError(t, err)
	t.Cleanup(model.Close)

	return model
}

// speckle adds a mild deterministic error to the luma of f.
func speckle(
	f *frame.Frame,
) {
	p := &f.Luma
	for i := 0; i < len(p.Pix); i += p.BytesPerSample * 7 {
		p.Pix[i] ^= 4
	}
}

// TestScorerModelsAndFeatures scores two models and every extractor in one
// context, at 8 and 10 bits: each model keeps its own scores, and each
// extractor output is read back for every frame.
func TestScorerModelsAndFeatures(
	t *testing.T,
) {
	tv := loadDevice(t, vmaf.DeviceTV)

	// The phone model crashes libvmaf on pictures this small (its chroma
	// prescaling): a v0 model is the second one here.
	v0, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer v0.Close()

	// MS-SSIM needs five scales, CAMBI at least 216 samples on one side.
	const w, h = 256, 256

	extractors := []vmaf.Extractor{
		vmaf.ExtractorPSNR, vmaf.ExtractorPSNRHVS, vmaf.ExtractorSSIM, vmaf.ExtractorMSSSIM, vmaf.ExtractorCIEDE2000, vmaf.ExtractorCAMBI,
	}

	testCases := []struct {
		name     string
		bitDepth int
	}{
		{name: "8 bits", bitDepth: 8},
		{name: "10 bits", bitDepth: 10},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := frame.NewPool(w, h, frame.PoolOptions{Chroma: true, HighBitDepth: testCase.bitDepth > 8})

			scorer, err := New([]*Model{tv, v0}, vmaf.ScorerConfig{
				Extractors: extractors,
				Width:      w, Height: h, BitDepth: testCase.bitDepth, Threads: 2,
			})
			require.NoError(t, err)
			defer scorer.Close()

			for range 3 {
				ref, dist := pool.Get(), pool.Get()
				texture(ref, false)
				texture(dist, false)
				speckle(dist)
				require.NoError(t, scorer.Push(ref, dist))
				ref.Release()
				dist.Release()
			}

			out, err := scorer.Collect()
			require.NoError(t, err)

			require.Len(t, out.VMAF, 2)
			assert.Len(t, out.VMAF[0], 3)
			assert.NotEqual(t, out.VMAF[0], out.VMAF[1], "each model has its own scores")

			want := []string{
				vmaf.FeaturePSNRY, vmaf.FeaturePSNRCb, vmaf.FeaturePSNRCr, vmaf.FeaturePSNRHVS, vmaf.FeatureSSIM, vmaf.FeatureMSSSIM, vmaf.FeatureCIEDE2000, vmaf.FeatureCAMBI,
			}
			require.Len(t, out.Features, len(want))

			for _, name := range want {
				assert.Len(t, out.Features[name], 3, name)
			}

			assert.Less(t, out.Features[vmaf.FeaturePSNRY][1], 6*float64(testCase.bitDepth)+12, "distorted luma")
			assert.InDelta(t, 6*testCase.bitDepth+12, out.Features[vmaf.FeaturePSNRCb][1], 1e-9, "identical chroma: capped PSNR")
			assert.Less(t, out.Features[vmaf.FeatureSSIM][1], 1.0)
		})
	}
}

func TestScorerConfigErrors(
	t *testing.T,
) {
	model, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer model.Close()

	testCases := []struct {
		name    string
		setup   func(t *testing.T)
		models  []*Model
		cfg     vmaf.ScorerConfig
		wantErr error
	}{
		{name: "no model", cfg: vmaf.ScorerConfig{Width: 64, Height: 64}, wantErr: ErrNoModel},
		{
			name:    "unknown extractor",
			models:  []*Model{model},
			cfg:     vmaf.ScorerConfig{Extractors: []vmaf.Extractor{"vif"}, Width: 64, Height: 64},
			wantErr: vmaf.ErrUnknownExtractor,
		},
		{
			name: "extractor libvmaf does not have",
			setup: func(t *testing.T) {
				extractorFeatures["no_such_extractor"] = nil
				t.Cleanup(func() { delete(extractorFeatures, "no_such_extractor") })
			},
			models:  []*Model{model},
			cfg:     vmaf.ScorerConfig{Extractors: []vmaf.Extractor{"no_such_extractor"}, Width: 64, Height: 64},
			wantErr: syscall.EINVAL,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.setup != nil {
				testCase.setup(t)
			}

			s, err := New(testCase.models, testCase.cfg)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Nil(t, s)
		})
	}
}

func TestCollectMissingFeature(
	t *testing.T,
) {
	model, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer model.Close()

	s, err := New([]*Model{model}, vmaf.ScorerConfig{Width: 64, Height: 64, BitDepth: 8, Threads: 1})
	require.NoError(t, err)
	defer s.Close()

	f := frame.NewPool(64, 64, frame.PoolOptions{Chroma: true}).Get()
	defer f.Release()

	texture(f, false)
	require.NoError(t, s.Push(f, f))

	s.features = append(s.features, feature{name: "missing", libvmaf: "not_extracted"})

	_, err = s.Collect()
	require.ErrorIs(t, err, syscall.EINVAL)
	assert.Contains(t, err.Error(), "not_extracted")
}
