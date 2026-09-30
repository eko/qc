package main

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/vmaf"
)

// reviewValues are the rows of a review as "key: value" lines.
func reviewValues(
	sections []reviewSection,
) map[section][]string {
	values := map[section][]string{}

	for _, s := range sections {
		if s.skipped != "" {
			values[s.section] = append(values[s.section], "skipped: "+s.skipped)
		}

		for _, r := range s.rows {
			values[s.section] = append(values[s.section], r.key+": "+r.value)
		}
	}

	return values
}

func TestReviewSections(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		answers wizardAnswers
		want    map[section][]string
	}{
		{
			name: "analysis only",
			answers: wizardAnswers{
				Source: "source.mp4", Actions: []string{actionAnalysis}, HTML: " report.html ",
			},
			want: map[section][]string{
				sectionSource:   {"Video: source.mp4"},
				sectionAnalysis: {"Compute: Technical analysis"},
				sectionQuality:  {"skipped: no VMAF measured"},
				sectionLadder:   {"skipped: no ladder requested"},
				sectionOutputs:  {"Report: terminal, HTML report.html", "Annotated: none", "Hardware: NVIDIA GPU (NVDEC, NVENC, CUDA VMAF)"},
			},
		},
		{
			name: "vmaf with a share budget, devices and an hdr reference",
			answers: wizardAnswers{
				Source: "source.mp4", Reference: "ref.mov", Actions: []string{actionVMAF},
				VMAFMode: vmafShare, Share: "5%", Metrics: []string{"cambi"}, Devices: []string{vmaf.DevicePhone, vmaf.Device4K},
				HDRMetric: "pq", Overlay: true, OverlayPath: "annotated.mp4",
			},
			want: map[section][]string{
				sectionSource:   {"Video: source.mp4"},
				sectionAnalysis: {"Compute: VMAF"},
				sectionQuality: {
					"Reference: ref.mov", "VMAF: 5% of the frames, fixed budget", "Metrics: VMAF + CAMBI",
					"Devices: phone, 4K TV", "HDR: HDR10, scored on the HDR signal",
				},
				sectionLadder:  {"skipped: no ladder requested"},
				sectionOutputs: {"Report: terminal", "Annotated: annotated.mp4", "Hardware: NVIDIA GPU (NVDEC, NVENC, CUDA VMAF)"},
			},
		},
		{
			name: "customised ladder with fixed resolutions",
			answers: wizardAnswers{
				Source: "source.mp4", Actions: []string{actionLadder}, Codecs: []string{"hevc", av1Codec}, Metrics: []string{},
				Advanced: true, Shape: shapeResolutions, Resolutions: "1080, 720p", TopVMAF: "93", MinVMAF: "40",
				MaxBitrate: "8000", Preset: "slow", BitDepth: "10", SkipVerify: true, Probing: "adaptive", FilmGrain: "auto",
			},
			want: map[section][]string{
				sectionSource:   {"Video: source.mp4"},
				sectionAnalysis: {"Compute: Per-title ladder"},
				sectionQuality:  {"Metrics: VMAF only"},
				sectionLadder: {
					"Codecs: HEVC, AV1", "Shape: 1080p, 720p", "Quality: VMAF 40 to 93",
					"Encoding: cap 8000 kb/s, preset slow, 10-bit", "Rungs: not verified, adaptive probes", "Film grain: auto",
				},
				sectionOutputs: {"Report: terminal", "Renditions: none", "Hardware: NVIDIA GPU (NVDEC, NVENC, CUDA VMAF)"},
			},
		},
		{
			name: "per-shot rungs, exact vmaf, rung count",
			answers: wizardAnswers{
				Source: "source.mp4", Reference: "ref.mov", Actions: []string{actionVMAF, actionLadder}, VMAFMode: vmafExact,
				Codecs: []string{"h264"}, Advanced: true, Shape: shapeCount, RungCount: "5", TopVMAF: "95", MinVMAF: "30",
				BitDepth: "8", Probing: "fixed", PerShot: true, HDRMetric: "tonemap",
			},
			want: map[section][]string{
				sectionSource:   {"Video: source.mp4"},
				sectionAnalysis: {"Compute: VMAF, Per-title ladder"},
				sectionQuality: {
					"Reference: ref.mov", "VMAF: exact, every frame", "Metrics: VMAF + XPSNR, CAMBI, PSNR", "HDR: HDR10, scored on an SDR tone mapping",
				},
				sectionLadder: {
					"Codecs: H.264", "Shape: 5 rungs", "Quality: VMAF 30 to 95",
					"Encoding: no bitrate cap, default preset, 8-bit", "Rungs: verified, fixed probes, per-shot",
				},
				sectionOutputs: {"Report: terminal", "Annotated: none", "Renditions: none", "Hardware: NVIDIA GPU (NVDEC, NVENC, CUDA VMAF)"},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			wizardDir(t)

			a := testCase.answers
			a.GPU = true

			assert.Equal(t, testCase.want, reviewValues(a.reviewSections(testWizardContext(t, true))))
		})
	}
}

func TestReviewModeLabels(
	t *testing.T,
) {
	a := wizardAnswers{Precision: " 0.25 ", PerScene: "3", VMAFMode: vmafPerScene}
	assert.Equal(t, "3 clips per scene, fixed budget", a.vmafModeLabel())

	a.VMAFMode = vmafPrecision
	assert.Equal(t, "± 0.25 at 95% confidence, adaptive", a.vmafModeLabel())

	a.Shape = shapeAuto
	assert.Equal(t, "automatic, one quality step at a time", a.shapeLabel())
}

func TestPlainReview(
	t *testing.T,
) {
	wizardDir(t)

	a := wizardAnswers{Source: "source.mp4", Actions: []string{actionAnalysis}}
	ctx := testWizardContext(t, false)
	ctx.videos.inspect("source.mp4")

	review := plainReview(&a, ctx)

	assert.Contains(t, review, "1. Source\n   Video      source.mp4\n              H.264 · 1920×1080")
	assert.Contains(t, review, "3. Quality\n   no VMAF measured")
	assert.Contains(t, review, "Command: qc run source.mp4 --codecs=")
}

func TestWrapCommand(
	t *testing.T,
) {
	args := []string{"qc", "run", "my clip.mp4", "-r", "reference.mov", "--codecs=h264,av1", "--html", "report.html"}

	assert.Equal(t, []string{"qc run 'my clip.mp4' -r reference.mov --codecs=h264,av1 --html report.html"}, wrapCommand(args, 100))
	assert.Equal(t, []string{
		`qc run 'my clip.mp4' \`,
		`  -r reference.mov \`,
		`  --codecs=h264,av1 \`,
		`  --html report.html`,
	}, wrapCommand(args, 28))
}

func TestHandoff(
	t *testing.T,
) {
	s := newWizardStyle(func(string) string { return "" }, termenv.Ascii)

	assert.Equal(t, "  equivalent command · run it again without the wizard\n  $ qc run 'my clip.mp4' --codecs=",
		ansi.Strip(handoff([]string{"my clip.mp4", "--codecs="}, s)))
}

func TestWizardStyle(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		env       map[string]string
		profile   termenv.Profile
		wantASCII bool
		wantPlain bool
	}{
		{name: "colour terminal", env: map[string]string{"LANG": "fr_FR.UTF-8"}, profile: termenv.TrueColor},
		{name: "no locale", profile: termenv.ANSI256},
		{name: "NO_COLOR", env: map[string]string{"NO_COLOR": "1"}, profile: termenv.Ascii, wantASCII: true, wantPlain: true},
		{name: "dumb terminal", env: map[string]string{"TERM": "dumb"}, profile: termenv.Ascii, wantASCII: true, wantPlain: true},
		{name: "C locale", env: map[string]string{"LANG": "en_US.UTF-8", "LC_ALL": "C"}, profile: termenv.ANSI, wantASCII: true},
		{name: "utf8 ctype", env: map[string]string{"LC_CTYPE": "en_US.utf8"}, profile: termenv.ANSI},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			s := newWizardStyle(func(name string) string { return testCase.env[name] }, testCase.profile)

			assert.Equal(t, testCase.wantASCII, s.g == asciiGlyphs)
			assert.Equal(t, testCase.wantPlain, s.plain)

			focused, blurred := buttonStyles(s)
			assert.Equal(t, lipglossWidth(focused.Render("Run")), lipglossWidth(blurred.Render("Run")), "buttons keep their width focused")

			if testCase.wantPlain {
				assert.Equal(t, "[ Run ] ", focused.Render("Run"), "brackets tell the focus without colours")
			}
		})
	}
}

// lipglossWidth is the width of a rendered string.
func lipglossWidth(
	s string,
) int {
	return ansi.StringWidth(s)
}

func TestAccessibleMode(
	t *testing.T,
) {
	assert.True(t, accessibleMode(func(name string) string { return map[string]string{"ACCESSIBLE": "1"}[name] }))
	assert.True(t, accessibleMode(func(name string) string { return map[string]string{"TERM": "dumb"}[name] }))
	assert.False(t, accessibleMode(func(string) string { return "" }))
}

func TestRunAccessible(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		lines     []string
		wantErr   error
		wantHTML  string
		wantShown []string
	}{
		{
			name:      "defaults, run",
			lines:     []string{"source.mp4", "0", "0", "0", "0", "", "", "", "", "1"},
			wantShown: []string{"Review", "Command: qc run source.mp4 --codecs=h264", "Run this command?"},
		},
		{
			name: "edit the outputs, then cancel",
			lines: []string{
				"source.mp4", "0", "0", "0", "0", "", "", "", "",
				"2", "5", "report.html", "", "",
				"3",
			},
			wantErr:   huh.ErrUserAborted,
			wantHTML:  "report.html",
			wantShown: []string{"Edit which section?", "Command: qc run source.mp4 --codecs=h264 --html report.html"},
		},
		{
			name:    "no input",
			wantErr: huh.ErrUserAborted,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			wizardDir(t)

			a := newWizardAnswers()

			var out strings.Builder

			err := runAccessible(testWizardContext(t, false), a, &out, &lineReader{lines: testCase.lines})

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "source.mp4", a.Source)
			}

			assert.Equal(t, testCase.wantHTML, a.HTML)

			for _, want := range testCase.wantShown {
				assert.Contains(t, ansi.Strip(out.String()), want)
			}
		})
	}
}

func TestAskWizardWith(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		env         map[string]string
		input       io.Reader
		wantSource  string
		wantErr     error
		wantDrawing string
	}{
		{
			name:        "accessible",
			env:         map[string]string{"ACCESSIBLE": "1"},
			input:       &lineReader{lines: []string{"source.mp4", "0", "0", "0", "0", "", "", "", "", "1"}},
			wantSource:  "source.mp4",
			wantDrawing: "Command: qc run source.mp4 --codecs=h264",
		},
		{
			name:    "full screen, ctrl+c",
			input:   strings.NewReader("\x03"),
			wantErr: huh.ErrUserAborted,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			wizardDir(t)

			var out strings.Builder

			ctx := testWizardContext(t, false)
			answers, err := askWizardWith(ctx, func(name string) string { return testCase.env[name] }, testCase.input, &out)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, testCase.wantSource, answers.Source)
			assert.Contains(t, ansi.Strip(out.String()), testCase.wantDrawing)
		})
	}
}
