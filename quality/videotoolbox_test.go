package quality

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/decode"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/vmaf/libvmaf"
)

// TestMeasureVideoToolbox measures a clip long enough to be split, with
// VideoToolbox sessions and on the CPU: the exact measurement is scored in
// segments and the sampled one decodes its clips in concurrent runs, and
// both give the CPU's results, to the bit.
func TestMeasureVideoToolbox(
	t *testing.T,
) {
	if runtime.GOOS != "darwin" {
		t.Skip("VideoToolbox is macOS only")
	}

	if exact, err := decode.VideoToolboxExact(t.Context(), "ffmpeg"); err != nil || !exact {
		t.Skipf("VideoToolbox does not decode as the CPU on this machine (exact %v, %v): qc decodes on the CPU", exact, err)
	}

	ref, dist := clips(t, testutil.Clip{Seconds: 42, GOP: 50, Filter: "noise=alls=12:allf=t"})

	testCases := []struct {
		name     string
		opts     Options
		wantPlan string
	}{
		{name: "exact", opts: Options{Exact: true}, wantPlan: planSegments},
		{name: "fixed budget", opts: Options{Sample: Sample{PerScene: 2}}, wantPlan: planSweep},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			opts := testCase.opts
			opts.Metrics = []string{MetricXPSNR, MetricPSNR}

			measure := func(m *Meter) (*Result, *run) {
				r := newTestRun(t, m, ref, dist, opts)

				measure := r.sampled
				if opts.Exact {
					measure = r.exact
				}

				res, err := measure(t.Context())
				require.NoError(t, err)

				return res, r
			}

			cpu, _ := measure(decodedMeter())
			hardware, r := measure(NewMeter(decode.NewFFmpeg("ffmpeg", 0, decode.WithHWAccel(decode.HWAccelAuto)), libvmaf.NewEngine()))

			assert.Equal(t, "videotoolbox", hardware.HWAccel)
			assert.Empty(t, cpu.HWAccel)
			assert.Positive(t, r.plans[testCase.wantPlan], "plans: %v", r.plans)
			assert.Equal(t, cpu.Frames, hardware.Frames)
			assert.Equal(t, cpu.Mean, hardware.Mean)
			assert.Equal(t, cpu.HalfWidth, hardware.HalfWidth)
			assert.Equal(t, cpu.Metrics, hardware.Metrics)
		})
	}
}
