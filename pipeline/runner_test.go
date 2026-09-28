package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/analyze/light"
	"github.com/eko/qc/analyze/siti"
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/ladder"
	"github.com/eko/qc/media"
	"github.com/eko/qc/overlay"
	"github.com/eko/qc/probe"
	"github.com/eko/qc/quality"
	"github.com/eko/qc/vmaf/libvmaf"
)

var (
	errInspect  = errors.New("inspect failed")
	errAnalysis = errors.New("analysis failed")
	errCompare  = errors.New("compare failed")
	errLadder   = errors.New("ladder failed")
)

// fakeAnalyzer answers the inspection (SkipVideo), the frame analysis and
// the comparison, recording the options it receives.
type fakeAnalyzer struct {
	inspectErr  error
	analysisErr error
	compareErr  error
	vmaf        quality.Result
	// light is the light analysis of an HDR source, nil for SDR.
	light *light.Result

	analyzed []analysis.Options
	compared []analysis.CompareOptions
}

func (a *fakeAnalyzer) Analyze(
	_ context.Context,
	_ string,
	opts analysis.Options,
) (*analysis.Report, error) {
	a.analyzed = append(a.analyzed, opts)

	report := &analysis.Report{Info: &media.Info{
		Duration: media.Seconds(61.4),
		Video: []media.VideoStream{{
			Codec: "h264", Width: 1920, Height: 1080, AvgFrameRate: media.Rational{Num: 25, Den: 1},
		}},
	}}

	if opts.SkipVideo {
		return report, a.inspectErr
	}

	if opts.Progress != nil {
		opts.Progress(analysis.Progress{Stage: analysis.StageDecode, Done: 1, Total: 2})
	}

	report.Video = &analysis.VideoReport{
		Shots: []analysis.ShotReport{shotAt(0), shotAt(2), shotAt(5)},
		SITI:  siti.Result{SISummary: stats.Summary{Mean: 42.4}, TISummary: stats.Summary{Mean: 7.6}},
		Light: a.light,
	}

	return report, a.analysisErr
}

// shotAt is a shot starting at the given second.
func shotAt(
	seconds float64,
) analysis.ShotReport {
	var shot analysis.ShotReport
	shot.Start = media.Seconds(seconds)

	return shot
}

func (a *fakeAnalyzer) Compare(
	_ context.Context,
	_, _ string,
	opts analysis.CompareOptions,
) (*analysis.Comparison, error) {
	a.compared = append(a.compared, opts)

	if opts.Quality.Progress != nil {
		opts.Quality.Progress(quality.Progress{FramesScored: 1})
	}

	if a.compareErr != nil {
		return nil, a.compareErr
	}

	return &analysis.Comparison{VMAF: &a.vmaf}, nil
}

type fakeLadders struct {
	err     error
	rungs   []ladder.Rung
	codec   encode.Codec
	built   []ladder.Options
	sources []string
}

func (l *fakeLadders) Build(
	_ context.Context,
	source string,
	opts ladder.Options,
) (*ladder.Result, error) {
	l.built = append(l.built, opts)
	l.sources = append(l.sources, source)

	if opts.Progress != nil {
		opts.Progress(ladder.Progress{Done: 1})
	}

	if l.err != nil {
		return nil, l.err
	}

	return &ladder.Result{Rungs: l.rungs, Codec: l.codec}, nil
}

var errOverlay = errors.New("overlay failed")

// fakeOverlayer records the annotated copies it is asked for.
type fakeOverlayer struct {
	err     error
	outputs []string
	inputs  []overlay.Input
	opts    []overlay.RenderOptions
}

func (o *fakeOverlayer) Render(
	_ context.Context,
	source, output string,
	in overlay.Input,
	opts overlay.RenderOptions,
) error {
	o.outputs = append(o.outputs, source+" → "+output)
	o.inputs = append(o.inputs, in)
	o.opts = append(o.opts, opts)

	if opts.Progress != nil {
		opts.Progress(overlay.Progress{Done: 1, Total: 2})
	}

	return o.err
}

// events records the hooks of a run.
type events struct {
	started  []int
	done     []string
	results  []StageResult
	progress []string
}

func (e *events) hooks() Hooks {
	return Hooks{
		Start:    func(i int) { e.started = append(e.started, i) },
		Done:     func(_ int, r StageResult) { e.done = append(e.done, r.Stage.Kind); e.results = append(e.results, r) },
		Analysis: func(int, analysis.Progress) { e.progress = append(e.progress, KindAnalysis) },
		Quality:  func(int, quality.Progress) { e.progress = append(e.progress, KindVMAF) },
		Ladder:   func(int, ladder.Progress) { e.progress = append(e.progress, KindLadder) },
		Overlay:  func(int, overlay.Progress) { e.progress = append(e.progress, KindOverlay) },
	}
}

func TestRun(
	t *testing.T,
) {
	rungs := []ladder.Rung{{Height: 1080, Bitrate: 4_500_000}, {Height: 720, Bitrate: 2_000_000}}
	everything := Options{Source: "a.mp4", Reference: "r.mp4", Codecs: []string{"h264", "av1"}}

	nvencH264, err := encode.LookupFor("h264", encode.HardwareNVENC)
	require.NoError(t, err)

	testCases := []struct {
		name         string
		opts         Options
		analyzer     *fakeAnalyzer
		ladders      *fakeLadders
		wantDone     []string
		wantProgress []string
		wantErr      error
		wantErrText  string
	}{
		{
			name:         "analysis only",
			opts:         Options{Source: "a.mp4"},
			analyzer:     &fakeAnalyzer{},
			ladders:      &fakeLadders{},
			wantDone:     []string{KindInspect, KindAnalysis},
			wantProgress: []string{KindAnalysis},
		},
		{
			name:         "everything",
			opts:         everything,
			analyzer:     &fakeAnalyzer{vmaf: quality.Result{Mean: 93.456, HalfWidth: 0.41}},
			ladders:      &fakeLadders{rungs: rungs},
			wantDone:     []string{KindInspect, KindAnalysis, KindVMAF, KindLadder, KindLadder},
			wantProgress: []string{KindAnalysis, KindVMAF, KindLadder, KindLadder},
		},
		{
			name:         "without the frame analysis, on the gpu",
			opts:         Options{Source: "a.mp4", Reference: "r.mp4", SkipAnalysis: true, Codecs: []string{"h264"}},
			analyzer:     &fakeAnalyzer{vmaf: quality.Result{Mean: 91, Backend: "cuda"}},
			ladders:      &fakeLadders{rungs: rungs, codec: nvencH264},
			wantDone:     []string{KindInspect, KindVMAF, KindLadder},
			wantProgress: []string{KindVMAF, KindLadder},
		},
		{
			name:         "exact vmaf",
			opts:         Options{Source: "a.mp4", Reference: "r.mp4", SkipAnalysis: true},
			analyzer:     &fakeAnalyzer{vmaf: quality.Result{Mean: 91}},
			ladders:      &fakeLadders{},
			wantDone:     []string{KindInspect, KindVMAF},
			wantProgress: []string{KindVMAF},
		},
		{
			name:     "nothing to do",
			opts:     Options{Source: "a.mp4", SkipAnalysis: true},
			analyzer: &fakeAnalyzer{},
			ladders:  &fakeLadders{},
			wantErr:  ErrNothingToDo,
		},
		{
			name:        "inspection failure",
			opts:        everything,
			analyzer:    &fakeAnalyzer{inspectErr: errInspect},
			ladders:     &fakeLadders{},
			wantErr:     errInspect,
			wantErrText: "Inspect: ",
		},
		{
			name:         "analysis failure",
			opts:         everything,
			analyzer:     &fakeAnalyzer{analysisErr: errAnalysis},
			ladders:      &fakeLadders{},
			wantDone:     []string{KindInspect},
			wantProgress: []string{KindAnalysis},
			wantErr:      errAnalysis,
			wantErrText:  "Frame analysis: ",
		},
		{
			name:         "vmaf failure",
			opts:         everything,
			analyzer:     &fakeAnalyzer{compareErr: errCompare},
			ladders:      &fakeLadders{},
			wantDone:     []string{KindInspect, KindAnalysis},
			wantProgress: []string{KindAnalysis, KindVMAF},
			wantErr:      errCompare,
			wantErrText:  "VMAF: ",
		},
		{
			name:         "ladder failure",
			opts:         Options{Source: "a.mp4", SkipAnalysis: true, Codecs: []string{"hevc"}},
			analyzer:     &fakeAnalyzer{},
			ladders:      &fakeLadders{err: errLadder},
			wantDone:     []string{KindInspect},
			wantProgress: []string{KindLadder},
			wantErr:      errLadder,
			wantErrText:  "Ladder · hevc: ",
		},
		{
			name:         "ladder without rungs",
			opts:         Options{Source: "a.mp4", SkipAnalysis: true, Codecs: []string{"hevc"}},
			analyzer:     &fakeAnalyzer{},
			ladders:      &fakeLadders{},
			wantDone:     []string{KindInspect},
			wantProgress: []string{KindLadder},
			wantErr:      errNoRungs,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var ev events

			rep, err := NewRunner(testCase.analyzer, testCase.ladders).Run(t.Context(), testCase.opts, ev.hooks())

			assert.Equal(t, testCase.wantDone, ev.done)
			assert.Equal(t, testCase.wantProgress, ev.progress)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.ErrorContains(t, err, testCase.wantErrText)

				return
			}

			require.NoError(t, err)
			assert.Len(t, ev.started, len(Stages(testCase.opts)))
			assert.Equal(t, analysis.SchemaVersion, rep.SchemaVersion)
			assert.NotEmpty(t, rep.Elapsed)
			assert.NotNil(t, rep.Analysis)
			assert.Equal(t, testCase.opts.Reference != "", rep.Comparison != nil)
			assert.Len(t, rep.Ladders, len(testCase.opts.Codecs))
		})
	}
}

func TestRunOverlay(
	t *testing.T,
) {
	render := overlay.RenderOptions{Height: 720}
	withVMAF := Options{Source: "a.mp4", Reference: "r.mp4", Overlay: OverlayOptions{Output: "o.mp4", Render: render}}

	testCases := []struct {
		name         string
		opts         Options
		overlayer    *fakeOverlayer
		wantDone     []string
		wantProgress []string
		wantQuality  bool
		wantErr      error
	}{
		{
			name:         "analysis and vmaf burnt in",
			opts:         withVMAF,
			overlayer:    &fakeOverlayer{},
			wantDone:     []string{KindInspect, KindAnalysis, KindVMAF, KindOverlay},
			wantProgress: []string{KindAnalysis, KindVMAF, KindOverlay},
			wantQuality:  true,
		},
		{
			name:         "inspection only",
			opts:         Options{Source: "a.mp4", SkipAnalysis: true, Overlay: OverlayOptions{Output: "o.mp4", Render: render}},
			overlayer:    &fakeOverlayer{},
			wantDone:     []string{KindInspect, KindOverlay},
			wantProgress: []string{KindOverlay},
		},
		{
			name:         "overlay failure",
			opts:         withVMAF,
			overlayer:    &fakeOverlayer{err: errOverlay},
			wantDone:     []string{KindInspect, KindAnalysis, KindVMAF},
			wantProgress: []string{KindAnalysis, KindVMAF, KindOverlay},
			wantErr:      errOverlay,
		},
		{
			name:         "no overlayer",
			opts:         withVMAF,
			wantDone:     []string{KindInspect, KindAnalysis, KindVMAF},
			wantProgress: []string{KindAnalysis, KindVMAF},
			wantErr:      ErrNoOverlayer,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				ev      events
				options []RunnerOption
			)

			if testCase.overlayer != nil {
				options = append(options, WithOverlayer(testCase.overlayer))
			}

			rep, err := NewRunner(&fakeAnalyzer{}, &fakeLadders{}, options...).Run(t.Context(), testCase.opts, ev.hooks())

			assert.Equal(t, testCase.wantDone, ev.done)
			assert.Equal(t, testCase.wantProgress, ev.progress)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.ErrorContains(t, err, "Overlay: ")

				return
			}

			require.NoError(t, err)

			o := testCase.overlayer
			require.Len(t, o.outputs, 1)
			assert.Equal(t, "a.mp4 → o.mp4", o.outputs[0])
			assert.Same(t, rep.Analysis, o.inputs[0].Report)
			assert.Equal(t, testCase.wantQuality, o.inputs[0].Quality != nil)
			assert.Equal(t, 720, o.opts[0].Height)
			assert.Equal(t, "o.mp4", ev.results[len(ev.results)-1].Overlay)
		})
	}
}

func TestRunPassesOptions(
	t *testing.T,
) {
	analyzer, ladders := &fakeAnalyzer{}, &fakeLadders{rungs: []ladder.Rung{{Height: 360}}}
	bitstreamOpts := bitstream.Options{Interval: 2 * time.Second}

	_, err := NewRunner(analyzer, ladders).Run(t.Context(), Options{
		Source:    "a.mp4",
		Reference: "r.mp4",
		Codecs:    []string{"av1"},
		// SkipVideo is the business of SkipAnalysis: the frame analysis
		// stage must decode anyway.
		Analysis: analysis.Options{Bitstream: bitstreamOpts, SkipVideo: true},
		Quality:  quality.Options{Precision: 1},
		Ladder:   ladder.Options{Codec: "h264", Preset: "fast"},
	}, Hooks{})
	require.NoError(t, err)

	require.Len(t, analyzer.analyzed, 2)
	assert.True(t, analyzer.analyzed[0].SkipVideo, "the inspection never decodes")
	assert.False(t, analyzer.analyzed[1].SkipVideo)
	assert.Equal(t, bitstreamOpts, analyzer.analyzed[1].Bitstream)

	require.Len(t, analyzer.compared, 1)
	assert.Equal(t, bitstreamOpts, analyzer.compared[0].Bitstream)
	assert.InDelta(t, 1.0, analyzer.compared[0].Quality.Precision, 1e-9)

	require.Len(t, ladders.built, 1)
	assert.Equal(t, "av1", ladders.built[0].Codec, "the stage codec wins")
	assert.Equal(t, "fast", ladders.built[0].Preset)
}

func TestRunPassesAnalysedSource(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		skipAnalysis bool
		wantVideo    bool
	}{
		{name: "with the frame analysis and its shots", wantVideo: true},
		{name: "inspection only", skipAnalysis: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := &fakeAnalyzer{}
			budget := quality.Options{Sample: quality.Sample{PerScene: 1}}

			rep, err := NewRunner(analyzer, &fakeLadders{}).Run(t.Context(), Options{
				Source:       "a.mp4",
				Reference:    "r.mp4",
				SkipAnalysis: testCase.skipAnalysis,
				Quality:      budget,
			}, Hooks{})
			require.NoError(t, err)

			// The comparison reuses the source report instead of inspecting
			// it again, and takes its shot cuts from it.
			require.Len(t, analyzer.compared, 1)
			compared := analyzer.compared[0]
			assert.Same(t, rep.Analysis, compared.Distorted)
			assert.Equal(t, testCase.wantVideo, compared.Distorted.Video != nil)
			assert.Equal(t, budget.Sample, compared.Quality.Sample)
			assert.Nil(t, compared.Quality.Cuts, "cuts are derived by Compare")
		})
	}
}

func TestRunStageResults(
	t *testing.T,
) {
	var ev events

	opts := Options{Source: "a.mp4", Reference: "r.mp4", Codecs: []string{"h264"}}
	ladders := &fakeLadders{rungs: []ladder.Rung{{Height: 720}}}

	rep, err := NewRunner(&fakeAnalyzer{}, ladders).Run(t.Context(), opts, ev.hooks())
	require.NoError(t, err)

	require.Len(t, ev.results, 4)
	assert.Equal(t, Stages(opts), []Stage{ev.results[0].Stage, ev.results[1].Stage, ev.results[2].Stage, ev.results[3].Stage})

	inspection, frames, vmaf, built := ev.results[0], ev.results[1], ev.results[2], ev.results[3]
	assert.Nil(t, inspection.Analysis.Video, "the inspection, replaced by the frame analysis")
	assert.Same(t, rep.Analysis, frames.Analysis)
	assert.Same(t, rep.Comparison, vmaf.Comparison)
	assert.Same(t, rep.Ladders[0], built.Ladder)

	for _, r := range ev.results {
		set := 0

		for _, isSet := range []bool{r.Analysis != nil, r.Comparison != nil, r.Ladder != nil} {
			if isSet {
				set++
			}
		}

		assert.Equal(t, 1, set, "one result per stage: %s", r.Stage.Kind)
	}
}

func TestRunLadderSource(
	t *testing.T,
) {
	testCases := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "the source by default",
			opts: Options{Source: "encode.mp4", Reference: "mezzanine.mov", Codecs: []string{"h264", "av1"}},
			want: []string{"encode.mp4", "encode.mp4"},
		},
		{
			name: "the ladder source when set",
			opts: Options{Source: "encode.mp4", Reference: "mezzanine.mov", LadderSource: "mezzanine.mov", Codecs: []string{"h264"}},
			want: []string{"mezzanine.mov"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ladders := &fakeLadders{rungs: []ladder.Rung{{Height: 720}}}

			_, err := NewRunner(&fakeAnalyzer{}, ladders).Run(t.Context(), testCase.opts, Hooks{})
			require.NoError(t, err)

			assert.Equal(t, testCase.want, ladders.sources)
		})
	}
}

func TestRunStageUnknownKind(
	t *testing.T,
) {
	r := NewRunner(&fakeAnalyzer{}, &fakeLadders{})

	_, err := r.runStage(t.Context(), 0, Stage{Kind: "teleport"}, Options{}, Hooks{}, &Report{})

	require.ErrorContains(t, err, `unknown stage "teleport"`)
}

// TestRunIntegration runs the inspection, frame analysis and VMAF stages on
// a real clip; ladders are covered by the ladder package (too slow here).
func TestRunIntegration(
	t *testing.T,
) {
	source := testutil.Generate(t, testutil.Clip{Width: 64, Height: 36, Seconds: 0.4, GOP: 5})

	dec := decode.NewFFmpeg("ffmpeg", 0)
	analyzer := analysis.New(
		slog.New(slog.DiscardHandler),
		probe.NewFFprobe("ffprobe"),
		bitstream.NewFFprobeReader("ffprobe"),
		dec,
		quality.NewMeter(dec, libvmaf.NewEngine()),
	)

	var ev events

	rep, err := NewRunner(analyzer, &fakeLadders{}).Run(t.Context(), Options{
		Source:    source,
		Reference: source,
		Quality:   quality.Options{Exact: true},
	}, ev.hooks())
	require.NoError(t, err)

	assert.Equal(t, []int{0, 1, 2}, ev.started)
	assert.Equal(t, []string{KindInspect, KindAnalysis, KindVMAF}, ev.done)
	require.Len(t, ev.results, 3)
	assert.Nil(t, ev.results[0].Analysis.Video, "the inspection decodes nothing")
	assert.Same(t, rep.Comparison, ev.results[2].Comparison)
	assert.Equal(t, quality.ModeExact, ev.results[2].Comparison.VMAF.Mode)

	require.NotNil(t, rep.Analysis.Video)
	assert.Equal(t, 10, rep.Analysis.Video.FramesDecoded)
	assert.Equal(t, 10, rep.Comparison.VMAF.FramesScored)
}

func TestRunPassesMeasuredLight(
	t *testing.T,
) {
	measured := &light.Result{MaxCLL: 4000, MaxCLLRobust: 1521.6, MaxFALL: 972.8}
	signalled := &media.ContentLightLevel{MaxCLL: 1000, MaxFALL: 400}

	testCases := []struct {
		name  string
		light *light.Result
		opts  Options
		want  *media.ContentLightLevel
	}{
		{
			name: "robust MaxCLL and MaxFALL of the analysed source", light: measured,
			opts: Options{Source: "hdr.mov", Codecs: []string{"hevc"}},
			want: &media.ContentLightLevel{MaxCLL: 1522, MaxFALL: 973},
		},
		{
			name: "sdr: nothing", opts: Options{Source: "sdr.mov", Codecs: []string{"hevc"}},
		},
		{
			name: "another ladder source was not analysed", light: measured,
			opts: Options{Source: "encode.mp4", LadderSource: "mezzanine.mov", Codecs: []string{"hevc"}},
		},
		{
			name: "the caller's level wins", light: measured,
			opts: Options{Source: "hdr.mov", Codecs: []string{"hevc"}, Ladder: ladder.Options{ContentLight: signalled}},
			want: signalled,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			ladders := &fakeLadders{rungs: []ladder.Rung{{Height: 720}}}

			_, err := NewRunner(&fakeAnalyzer{light: testCase.light}, ladders).Run(t.Context(), testCase.opts, Hooks{})
			require.NoError(t, err)

			require.Len(t, ladders.built, 1)
			assert.Equal(t, testCase.want, ladders.built[0].ContentLight)
		})
	}
}

func TestRunInspectionDefersHDRMetadata(
	t *testing.T,
) {
	testCases := []struct {
		name string
		opts Options
		want bool
	}{
		{name: "a frame analysis follows", opts: Options{Source: "hdr.mov"}, want: true},
		{name: "inspection only", opts: Options{Source: "hdr.mov", SkipAnalysis: true, Reference: "ref.mov"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := &fakeAnalyzer{}

			_, err := NewRunner(analyzer, &fakeLadders{}).Run(t.Context(), testCase.opts, Hooks{})
			require.NoError(t, err)

			require.NotEmpty(t, analyzer.analyzed)
			assert.True(t, analyzer.analyzed[0].SkipVideo)
			assert.Equal(t, testCase.want, analyzer.analyzed[0].DeferHDRMetadata)
		})
	}
}

func TestRunInspectionAudio(
	t *testing.T,
) {
	// The audio asked with an inspection runs once: in the frame analysis
	// when one follows, in the inspection otherwise.
	audio := analysis.Options{Audio: analysis.AudioOptions{WithInspection: true}}

	testCases := []struct {
		name string
		opts Options
		want bool
	}{
		{name: "a frame analysis follows", opts: Options{Source: "a.mov", Analysis: audio}},
		{name: "inspection only", opts: Options{Source: "a.mov", SkipAnalysis: true, Reference: "ref.mov", Analysis: audio}, want: true},
		{name: "not asked", opts: Options{Source: "a.mov", SkipAnalysis: true, Reference: "ref.mov"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			analyzer := &fakeAnalyzer{}

			_, err := NewRunner(analyzer, &fakeLadders{}).Run(t.Context(), testCase.opts, Hooks{})
			require.NoError(t, err)

			require.NotEmpty(t, analyzer.analyzed)
			assert.Equal(t, testCase.want, analyzer.analyzed[0].Audio.WithInspection)
		})
	}
}
