package ladder

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf"
)

const sourcePath = "/titles/source.mov"

// progressLog collects Progress reports and detects overlapping calls.
type progressLog struct {
	mu       sync.Mutex
	inFlight atomic.Int32
	overlap  atomic.Bool
	reports  []Progress
}

func (l *progressLog) record(
	p Progress,
) {
	if l.inFlight.Add(1) > 1 {
		l.overlap.Store(true)
	}
	defer l.inFlight.Add(-1)

	l.mu.Lock()
	l.reports = append(l.reports, p)
	l.mu.Unlock()
}

func (l *progressLog) stage(
	stage string,
) []Progress {
	var out []Progress

	for _, p := range l.reports {
		if p.Stage == stage {
			out = append(out, p)
		}
	}

	return out
}

func TestBuild(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		model  rateModel
		source *analysis.Report
		opts   Options
		check  func(t *testing.T, res *Result, lab *fakeLab)
	}{
		{
			name:   "h264 1080p title sampled into a raw digest",
			source: sourceReport(1920, 1080, 8, 25, 600),
			opts:   Options{Codec: "h264"},
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				assert.Equal(t, "h264", res.Codec.Name)
				assert.Equal(t, "fast", res.Preset)
				assert.Equal(t, Constraints{}.WithDefaults(), res.Constraints)

				require.Len(t, lab.digests, 1)
				assert.Equal(t, "digest.nut", filepath.Base(lab.digests[0].Destination))
				assert.False(t, lab.digests[0].Lossless)
				assert.Equal(t, 8, lab.digests[0].BitDepth)
				assert.Len(t, res.Digest.Segments, 20)
				assert.Equal(t, media.Seconds(40), res.Digest.Duration)
				assert.InDelta(t, 40.0/600, res.Digest.Share, 1e-9)

				extras := 0

				for _, p := range res.Probes {
					if p.Extra {
						extras++

						assert.Less(t, p.Height, 1080, "1080p reaches the top quality: only a lower resolution may be extended")
					}
				}

				assert.Len(t, res.Probes, 15+extras, "5 resolutions × 3 CRFs")
				assert.LessOrEqual(t, extras, 1)
				assert.Equal(t, 1920, res.Probes[0].Width)
				assert.Equal(t, 1080, res.Probes[0].Height)
				assert.NotEmpty(t, res.Hull)

				require.GreaterOrEqual(t, len(res.Rungs), 3)
				assert.Equal(t, 1080, res.Rungs[0].Height)

				for i, r := range res.Rungs {
					require.NotNil(t, r.Measured, "rung %d", i)
					assert.Contains(t, r.Command, "-maxrate")
					assert.Contains(t, r.Command, "-g 50")

					if !r.Calibrated {
						assert.Equal(t, 2*r.Bitrate, r.MaxRate)
						assert.Equal(t, 4*r.Bitrate, r.BufSize)
					}
				}

				assert.True(t, strings.HasSuffix(res.Rungs[0].Command, " 01-1080p.mp4"), res.Rungs[0].Command)
				assert.Contains(t, res.Timings, StageDigest)
				assert.Contains(t, res.Timings, StageProbe)
				assert.Contains(t, res.Timings, StageVerify)
				assert.Positive(t, res.Elapsed)

				for _, p := range lab.params {
					assert.Equal(t, 50, p.GOP, "2 s at 25 fps")
					assert.Equal(t, "fast", p.Preset)
				}
			},
		},
		{
			name:   "verified rungs get the extra metrics and devices, probes stay VMAF-only",
			source: sourceReport(1920, 1080, 8, 25, 600),
			opts: Options{
				Codec:   "h264",
				Metrics: []string{quality.MetricXPSNR, quality.MetricCAMBI},
				Devices: []string{vmaf.DevicePhone},
			},
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				withMetrics := 0

				for _, q := range lab.compared {
					if len(q.Metrics) > 0 {
						withMetrics++
						assert.Equal(t, []string{vmaf.DevicePhone}, q.Devices)
					}
				}

				calibrated := 0

				for _, r := range res.Rungs {
					if r.Calibrated {
						calibrated++
					}
				}

				assert.Equal(t, len(res.Rungs)+calibrated, withMetrics, "one comparison with metrics per verification encode")
				assert.Greater(t, len(lab.compared), withMetrics, "probes are measured too")

				for i, r := range res.Rungs {
					require.NotNil(t, r.Measured, "rung %d", i)
					assert.Contains(t, r.Measured.Metrics, quality.SeriesXPSNRY)
					assert.InDelta(t, r.Measured.VMAF+3, r.Measured.Devices[vmaf.DevicePhone], 1e-9)
				}
			},
		},
		{
			name:   "skip verify",
			source: sourceReport(1280, 720, 8, 30, 120),
			opts:   Options{Codec: "hevc", SkipVerify: true, Preset: "ultrafast", Heights: []int{1080, 720, 720, 360}},
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				assert.Equal(t, "ultrafast", res.Preset)
				require.Len(t, res.Probes, 7, "1080 is above the source, 720 is listed twice, plus the extra probe")
				assert.Len(t, lab.params, 7)
				assert.True(t, res.Probes[6].Extra)
				assert.NotContains(t, res.Timings, StageVerify)

				for _, r := range res.Rungs {
					assert.Nil(t, r.Measured)
				}
			},
		},
		{
			name:   "hard content extends the top curve",
			model:  rateModel{knee: 0.8},
			source: sourceReport(1920, 1080, 8, 25, 600),
			opts:   Options{Codec: "h264", SkipVerify: true},
			check: func(t *testing.T, res *Result, _ *fakeLab) {
				var extras []Probe

				for _, p := range res.Probes {
					if p.Extra {
						extras = append(extras, p)
					}
				}

				require.NotEmpty(t, extras)
				assert.Equal(t, 1080, extras[0].Height, "the top curve first")
				assert.InDelta(t, 13.0, extras[0].CRF, 1e-9, "lowest probe CRF − 7")

				for _, p := range extras[1:] {
					assert.Less(t, p.Height, 1080, "then lower resolutions that might reach the top quality cheaper")
				}
			},
		},
		{
			name:   "rungs missing their prediction are calibrated",
			model:  rateModel{cappedBias: -4},
			source: sourceReport(1920, 1080, 8, 25, 600),
			opts:   Options{Codec: "h264"},
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				calibrated := 0

				for _, r := range res.Rungs {
					if !r.Calibrated {
						continue
					}

					calibrated++

					assert.Equal(t, r.Measured.Bitrate, r.Bitrate, "the measured bitrate replaces the plan")
					assert.Contains(t, r.Command, "-crf "+formatCRF(r.CRF)+" ")
				}

				assert.Positive(t, calibrated)
				assert.Len(t, lab.params, len(res.Probes)+len(res.Rungs)+calibrated)
			},
		},
		{
			name:   "10-bit 4K digest is compressed losslessly",
			source: sourceReport(3840, 2160, 10, 60, 600),
			opts:   Options{Codec: "av1", BitDepth: 10, SkipVerify: true},
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				require.Len(t, lab.digests, 1)
				assert.Equal(t, "digest.mkv", filepath.Base(lab.digests[0].Destination))
				assert.True(t, lab.digests[0].Lossless)
				assert.Equal(t, 10, lab.digests[0].BitDepth)

				for _, r := range res.Rungs {
					assert.Equal(t, float64(int(r.CRF)), r.CRF, "SVT-AV1 takes integer CRFs")
					assert.Contains(t, r.Command, "yuv420p10le")
				}
			},
		},
		{
			name:   "short title is used whole",
			source: sourceReport(640, 360, 8, 24, 20),
			opts:   Options{Codec: "h264", SkipVerify: true, WorkDir: "work"},
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				assert.Equal(t, []media.Interval{{Start: 0, End: media.Seconds(20)}}, res.Digest.Segments)
				assert.InDelta(t, 1.0, res.Digest.Share, 1e-9)
				assert.Equal(t, filepath.Join("work", "digest.nut"), lab.digests[0].Destination)
			},
		},
		{
			name: "unknown average frame rate and container duration fall back to the stream",
			source: func() *analysis.Report {
				r := sourceReport(1920, 1080, 8, 25, 600)
				r.Info.Duration = 0
				r.Info.Video[0].AvgFrameRate = media.Rational{}

				return r
			}(),
			opts: Options{Codec: "h264", SkipVerify: true},
			check: func(t *testing.T, res *Result, lab *fakeLab) {
				assert.Len(t, res.Digest.Segments, 20)
				assert.Equal(t, media.Rational{Num: 25000, Den: 1000}, lab.digests[0].Rate)
				assert.Equal(t, 50, lab.params[0].GOP)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lab := newFakeLab(testCase.model, testCase.source)
			progress := &progressLog{}
			opts := testCase.opts
			opts.Progress = progress.record

			res, err := labEngine(lab).Build(t.Context(), sourcePath, opts)
			require.NoError(t, err)

			assert.Same(t, testCase.source, res.Source)
			assert.Equal(t, analysis.SchemaVersion, res.SchemaVersion)
			assert.False(t, progress.overlap.Load(), "progress calls must not overlap")
			assert.Equal(t, []Progress{{Stage: StageDigest}}, progress.stage(StageDigest))
			assertProgress(t, progress.stage(StageProbe), len(res.Probes))

			if !opts.SkipVerify {
				assertProgress(t, progress.stage(StageVerify), len(res.Rungs))
			}

			testCase.check(t, res, lab)
		})
	}
}

// assertProgress checks that each batch of reports counts up to its total
// and that the batches cover want measurements.
func assertProgress(
	t *testing.T,
	reports []Progress,
	want int,
) {
	t.Helper()

	measured := 0

	for i, p := range reports {
		if i == 0 || reports[i-1].Done == reports[i-1].Total {
			assert.Equal(t, 1, p.Done, "report %d starts a batch", i)
		} else {
			assert.Equal(t, reports[i-1].Done+1, p.Done, "report %d", i)
		}

		assert.True(t, p.Probe != nil || p.Rung != nil, "report %d carries its measurement", i)

		if p.Probe != nil {
			measured++
		}
	}

	if len(reports) > 0 && reports[0].Probe != nil {
		assert.Equal(t, want, measured)
	}
}

func TestBuildErrors(
	t *testing.T,
) {
	valid := sourceReport(1920, 1080, 8, 25, 600)

	testCases := []struct {
		name    string
		model   rateModel
		source  *analysis.Report
		codec   string
		probing Probing
		failOn  func(op, target string, seen int) bool
		wantErr error
		wantMsg string
	}{
		{
			name:    "unknown codec",
			codec:   "vp9",
			wantErr: encode.ErrUnknownCodec,
		},
		{
			name:    "unknown probing mode",
			probing: "random",
			wantErr: ErrInvalidProbing,
		},
		{
			name:    "adaptive probe after the initial design",
			probing: ProbingAdaptive,
			failOn:  failing(opEncode, "probe-0.mp4", 1),
			wantErr: errFake,
		},
		{
			name:    "source inspection",
			failOn:  failing(opAnalyze, "source.mov", 0),
			wantErr: errFake,
			wantMsg: "ladder: inspect /titles/source.mov",
		},
		{
			name:    "no inspection info",
			source:  &analysis.Report{},
			wantErr: ErrInvalidSource,
		},
		{
			name:    "no video stream",
			source:  &analysis.Report{Info: &media.Info{Duration: media.Seconds(10)}},
			wantErr: ErrInvalidSource,
		},
		{
			name: "unknown frame rate",
			source: func() *analysis.Report {
				r := sourceReport(1920, 1080, 8, 25, 600)
				r.Info.Video[0].AvgFrameRate = media.Rational{}
				r.Info.Video[0].FrameRate = media.Rational{}

				return r
			}(),
			wantErr: ErrInvalidSource,
			wantMsg: "unknown frame rate",
		},
		{
			name: "unknown duration",
			source: func() *analysis.Report {
				r := sourceReport(1920, 1080, 8, 25, 600)
				r.Info.Duration, r.Info.Video[0].Duration = 0, 0

				return r
			}(),
			wantErr: ErrInvalidSource,
			wantMsg: "unknown duration",
		},
		{
			name:    "digest extraction",
			failOn:  failing(opDigest, "digest.nut", 0),
			wantErr: errFake,
		},
		{
			name:    "digest inspection",
			failOn:  failing(opAnalyze, "digest.nut", 0),
			wantErr: errFake,
			wantMsg: "inspect digest",
		},
		{
			name:    "probe encode",
			failOn:  failing(opEncode, "probe-3.mp4", 0),
			wantErr: errFake,
			wantMsg: "ladder: probe: probe-3",
		},
		{
			name:    "probe measurement",
			failOn:  failing(opCompare, "probe-0.mp4", 0),
			wantErr: errFake,
			wantMsg: "probe-0: measure",
		},
		{
			name:    "extra probe",
			model:   rateModel{knee: 0.8},
			failOn:  failing(opEncode, "probe-0.mp4", 1),
			wantErr: errFake,
		},
		{
			name:    "no rung",
			model:   rateModel{zeroBitrate: true},
			wantErr: ErrNoRungs,
			wantMsg: "ladder: no rung satisfies the constraints",
		},
		{
			name:    "verification",
			failOn:  failing(opEncode, "verify-1.mp4", 0),
			wantErr: errFake,
			wantMsg: "ladder: verify: verify-1",
		},
		{
			name:  "calibrated verification",
			model: rateModel{cappedBias: -4},
			failOn: func(op, target string, seen int) bool {
				return op == opCompare && strings.HasPrefix(target, "verify-") && seen == 1
			},
			wantErr: errFake,
			wantMsg: "ladder: verify",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			source := testCase.source
			if source == nil {
				source = valid
			}

			codec := testCase.codec
			if codec == "" {
				codec = "h264"
			}

			lab := newFakeLab(testCase.model, source)
			lab.failOn = testCase.failOn

			res, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: codec, Probing: testCase.probing})
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Nil(t, res)

			if testCase.wantMsg != "" {
				assert.Contains(t, err.Error(), testCase.wantMsg)
			}
		})
	}
}

func TestBuildWorkDir(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		tmp     string
		wantErr bool
	}{
		{name: "temporary directory is removed", tmp: t.TempDir()},
		{name: "temporary directory cannot be created", tmp: filepath.Join(t.TempDir(), "missing"), wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TMPDIR", testCase.tmp)

			lab := newFakeLab(rateModel{}, sourceReport(640, 360, 8, 25, 10))

			_, err := labEngine(lab).Build(t.Context(), sourcePath, Options{Codec: "h264", SkipVerify: true})
			if testCase.wantErr {
				require.ErrorContains(t, err, "ladder: work dir")

				return
			}

			require.NoError(t, err)
			require.Len(t, lab.digests, 1)
			assert.Equal(t, testCase.tmp, filepath.Dir(filepath.Dir(lab.digests[0].Destination)))
			assert.NoDirExists(t, filepath.Dir(lab.digests[0].Destination))
		})
	}
}

func TestOptionsWithDefaults(
	t *testing.T,
) {
	codec := mustCodec(t, "hevc")

	testCases := []struct {
		name string
		in   Options
		want Options
	}{
		{
			name: "zero value",
			want: Options{
				Preset: "veryfast", Constraints: Constraints{}.WithDefaults(), Heights: DefaultHeights(),
				SegmentDuration: media.Seconds(2), DigestDuration: media.Seconds(40), GOPDuration: media.Seconds(2),
				Precision: 1, Parallel: 2, ProbeClips: 16,
				Probing: ProbingFixed, Tolerance: 0.5, BitrateTolerance: 0.03,
			},
		},
		{
			name: "explicit values are kept",
			in: Options{
				Preset: "slow", Heights: []int{720}, SegmentDuration: media.Seconds(4),
				DigestDuration: media.Seconds(60), GOPDuration: media.Seconds(4),
				Precision: 0.5, Parallel: 4, ProbeClips: 32,
				Probing: ProbingAdaptive, Tolerance: 1, BitrateTolerance: 0.05, MaxProbes: 9,
			},
			want: Options{
				Preset: "slow", Constraints: Constraints{}.WithDefaults(), Heights: []int{720},
				SegmentDuration: media.Seconds(4), DigestDuration: media.Seconds(60), GOPDuration: media.Seconds(4),
				Precision: 0.5, Parallel: 4, ProbeClips: 32,
				Probing: ProbingAdaptive, Tolerance: 1, BitrateTolerance: 0.05, MaxProbes: 9,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.in.withDefaults(codec))
		})
	}
}

func TestStageTimings(
	t *testing.T,
) {
	timings := stageTimings{}
	stop := timings.start(StageProbe)

	assert.Empty(t, timings, "recorded when the stage stops")

	stop()

	assert.Contains(t, timings, StageProbe)
}

func TestWorkDirGiven(
	t *testing.T,
) {
	dir := t.TempDir()

	got, cleanup, err := workDir(dir)
	require.NoError(t, err)

	cleanup()

	assert.Equal(t, dir, got)
	assert.DirExists(t, dir, "a given work dir is left in place")

	_, err = os.Stat(dir)
	require.NoError(t, err)
}

// failing fails op on target at its seen-th call.
func failing(
	op, target string,
	seen int,
) func(string, string, int) bool {
	return func(o, tg string, s int) bool {
		return o == op && tg == target && s == seen
	}
}

func mustCodec(
	t *testing.T,
	name string,
) encode.Codec {
	t.Helper()

	codec, err := encode.Lookup(name)
	require.NoError(t, err)

	return codec
}
