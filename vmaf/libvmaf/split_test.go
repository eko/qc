package libvmaf

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/vmaf"
)

// moving fills f with a pattern shifted by step pixels: consecutive frames
// have motion. The distorted one is softened and speckled.
func moving(
	f *frame.Frame,
	step int,
	distorted bool,
) {
	w, h := f.Luma.Width, f.Luma.Height

	for y := range h {
		for x := range w {
			v := ((x+step)*37 + y*91 + (x+step)*y/7) % 256
			if distorted {
				v = 128 + (v-128)*3/4
				if (x*13+y*7+step)%11 == 0 {
					v ^= 8
				}
			}

			f.Luma.Pix[y*f.Luma.Stride+x] = byte(v)
		}
	}

	for _, plane := range []frame.Plane{f.Cb, f.Cr} {
		for i := range plane.Pix {
			plane.Pix[i] = byte(112 + (i+step)%32)
		}
	}
}

// TestSplitScoresAsInFull checks that the warm-up pairs of a clip, seen by
// the motion alone, leave the scores of the other pairs as a scorer
// extracting every feature from every pair gets them: the features the
// split extracts from scored pairs only are spatial.
func TestSplitScoresAsInFull(
	t *testing.T,
) {
	// The phone and 4K models crash libvmaf on pictures this small (their
	// chroma prescaling): the TV model is the one checked here.
	const w, h = 256, 256

	testCases := []struct {
		name       string
		extractors []vmaf.Extractor
		warm       []bool
	}{
		{name: "one frame between two warm-up ones", warm: []bool{true, false, true}},
		{name: "a clip of four", warm: []bool{true, true, false, false, false, false, true}},
		{name: "with CAMBI and PSNR", extractors: []vmaf.Extractor{vmaf.ExtractorCAMBI, vmaf.ExtractorPSNR}, warm: []bool{true, false, false, true}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			split, full := loadDevice(t, vmaf.DeviceTV), loadDevice(t, vmaf.DeviceTV)
			require.NotNil(t, split.features, "a v1 model file splits")

			full.features = nil

			cfg := vmaf.ScorerConfig{Extractors: testCase.extractors, Width: w, Height: h, BitDepth: 8, Threads: 2}
			pool := frame.NewPool(w, h, frame.PoolOptions{Chroma: true})

			score := func(m *Model) vmaf.Scores {
				s, err := New([]*Model{m}, cfg)
				require.NoError(t, err)
				defer s.Close()

				for i, warm := range testCase.warm {
					ref, dist := pool.Get(), pool.Get()
					moving(ref, 3*i, false)
					moving(dist, 3*i, true)

					if warm {
						require.NoError(t, s.PushWarm(ref, dist))
					} else {
						require.NoError(t, s.Push(ref, dist))
					}

					ref.Release()
					dist.Release()
				}

				out, err := s.Collect()
				require.NoError(t, err)

				return out
			}

			got, want := score(split), score(full)
			require.Len(t, got.VMAF[0], len(testCase.warm))

			for i, warm := range testCase.warm {
				if warm {
					assert.True(t, math.IsNaN(got.VMAF[0][i]), "a warm-up pair has no score")

					continue
				}

				assert.InDelta(t, want.VMAF[0][i], got.VMAF[0][i], 1e-9, "pair %d", i)

				for name, values := range want.Features {
					assert.InDelta(t, values[i], got.Features[name][i], 1e-9, "%s of pair %d", name, i)
				}
			}

			assert.Greater(t, want.VMAF[0][len(testCase.warm)/2], 0.0)
		})
	}
}

func TestSplitFeatures(
	t *testing.T,
) {
	for _, device := range []string{vmaf.DevicePhone, vmaf.DeviceTV, vmaf.Device4K} {
		m := loadDevice(t, device)
		require.Len(t, m.features, 4, device)

		var motion []string

		for _, f := range m.features {
			if f.collected != "" {
				motion = append(motion, f.collected)
			}
		}

		assert.Equal(t, []string{"integer_motion3_mmxv_18"}, motion, device)
	}

	builtin, err := LoadModel(vmaf.ModelSpec{Source: builtinModel})
	require.NoError(t, err)
	defer builtin.Close()

	assert.Nil(t, builtin.features, "a built-in model is scored in full")
	assert.Nil(t, splitFeatures("/no/such/model.json"))
}
