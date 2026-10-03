package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/charmbracelet/huh"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
)

func TestWizardAnswersRunArgs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		answers wizardAnswers
		want    []string
	}{
		{
			name: "defaults",
			answers: wizardAnswers{
				Source:  "source.mov",
				Actions: []string{actionAnalysis, actionLadder},
				Codecs:  []string{"h264"},
			},
			want: []string{"source.mov", "--codecs=h264"},
		},
		{
			name: "ladder encoded on the whole title",
			answers: wizardAnswers{
				Source:     "source.mov",
				Actions:    []string{actionLadder},
				Codecs:     []string{"h264", "av1"},
				Renditions: true, RenditionsDir: " renditions ",
			},
			want: []string{"source.mov", "--codecs=h264,av1", "--skip-analysis", "--encode-ladder", "renditions"},
		},
		{
			name: "renditions asked, then no ladder",
			answers: wizardAnswers{
				Source:     "source.mov",
				Actions:    []string{actionAnalysis},
				Renditions: true, RenditionsDir: "renditions",
			},
			want: []string{"source.mov", "--codecs="},
		},
		{
			name: "everything",
			answers: wizardAnswers{
				Source:    "encode.mp4",
				Reference: "source.mov",
				Actions:   []string{actionAnalysis, actionVMAF, actionLadder},
				Codecs:    []string{"h264", "av1"},
				VMAFMode:  vmafExact,
				HTML:      "  report.html ",
			},
			want: []string{"encode.mp4", "-r", "source.mov", "--exact", "--codecs=h264,av1", "--html", "report.html"},
		},
		{
			name: "sampled vmaf only",
			answers: wizardAnswers{
				Source:    "encode.mp4",
				Reference: "source.mov",
				Actions:   []string{actionVMAF},
				Codecs:    []string{"hevc"},
			},
			want: []string{"encode.mp4", "-r", "source.mov", "--codecs=", "--skip-analysis"},
		},
		{
			name: "paths starting with a dash",
			answers: wizardAnswers{
				Source:    "-encode.mp4",
				Reference: "-source.mov",
				Actions:   []string{actionAnalysis, actionVMAF},
				HTML:      "-report.html",
			},
			want: []string{"./-encode.mp4", "-r", "./-source.mov", "--codecs=", "--html", "./-report.html"},
		},
		{
			name: "exact ignored without vmaf",
			answers: wizardAnswers{
				Source:   "source.mov",
				Actions:  []string{actionLadder},
				Codecs:   []string{"hevc"},
				VMAFMode: vmafExact,
				HTML:     "   ",
			},
			want: []string{"source.mov", "--codecs=hevc", "--skip-analysis"},
		},
		{
			name: "hdr tone mapping",
			answers: wizardAnswers{
				Source:    "hdr.mov",
				Actions:   []string{actionLadder},
				Codecs:    []string{"hevc"},
				HDRMetric: "tonemap",
			},
			want: []string{"hdr.mov", "--codecs=hevc", "--skip-analysis", "--hdr-metric", "tonemap"},
		},
		{
			name: "annotated copy",
			answers: wizardAnswers{
				Source:      "encode.mp4",
				Reference:   "source.mov",
				Actions:     []string{actionVMAF},
				VMAFMode:    vmafExact,
				Overlay:     true,
				OverlayPath: " -annotated.mp4 ",
			},
			want: []string{"encode.mp4", "-r", "source.mov", "--exact", "--codecs=", "--skip-analysis", "--overlay", "./-annotated.mp4"},
		},
		{
			name: "annotated copy of a ladder-only run is left out",
			answers: wizardAnswers{
				Source:      "source.mov",
				Actions:     []string{actionLadder},
				Codecs:      []string{"h264"},
				Overlay:     true,
				OverlayPath: "annotated.mp4",
			},
			want: []string{"source.mov", "--codecs=h264", "--skip-analysis"},
		},
		{
			name: "hdr default metric and tone mapping without vmaf are left out",
			answers: wizardAnswers{
				Source:    "hdr.mov",
				Actions:   []string{actionAnalysis},
				HDRMetric: "tonemap",
			},
			want: []string{"hdr.mov", "--codecs="},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.answers.runArgs())
		})
	}
}

func TestQuoteArg(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain path",
			input: "videos/source-01_v2.mp4",
			want:  "videos/source-01_v2.mp4",
		},
		{
			name:  "flag",
			input: "--codecs=h264,av1",
			want:  "--codecs=h264,av1",
		},
		{
			name:  "empty",
			input: "",
			want:  "''",
		},
		{
			name:  "space",
			input: "my video.mp4",
			want:  "'my video.mp4'",
		},
		{
			name:  "single quote",
			input: "it's.mp4",
			want:  `'it'\''s.mp4'`,
		},
		{
			name:  "shell metacharacters",
			input: "a&b(1)$x*.mp4",
			want:  "'a&b(1)$x*.mp4'",
		},
		{
			name:  "non-ascii",
			input: "été.mp4",
			want:  "'été.mp4'",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, quoteArg(testCase.input))
		})
	}
}

func TestCommandLine(
	t *testing.T,
) {
	got := commandLine([]string{"qc", "run", "my clip.mp4", "--codecs=", "--html", "r.html"})

	assert.Equal(t, "qc run 'my clip.mp4' --codecs= --html r.html", got)
}

func TestValidators(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		validate func() error
		wantErr  string
	}{
		{
			name:     "path picked",
			validate: func() error { return requirePath("clip.mp4") },
		},
		{
			name:     "no path",
			validate: func() error { return requirePath("") },
			wantErr:  "pick a video file",
		},
		{
			name:     "one value",
			validate: func() error { return requireOne("pick one")([]string{"h264"}) },
		},
		{
			name:     "no value",
			validate: func() error { return requireOne("pick one")(nil) },
			wantErr:  "pick one",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.validate()

			if testCase.wantErr != "" {
				require.EqualError(t, err, testCase.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestRunWizard(
	t *testing.T,
) {
	source := testutil.Generate(t, testutil.Clip{Name: "source.mp4"})
	episode := testutil.Generate(t, testutil.Clip{Name: "episode.mp4"})
	sampleFile := filepath.Join(t.TempDir(), "sample.mkv")
	html := filepath.Join(t.TempDir(), "wizard.html")

	testCases := []struct {
		name       string
		answers    wizardAnswers
		err        error
		wantCode   int
		wantStderr string
		wantStdout string
		wantFile   string
	}{
		{
			name: "runs the equivalent command",
			answers: wizardAnswers{
				Source:  source,
				Actions: []string{actionAnalysis},
				HTML:    html,
			},
			wantStderr: "$ qc run " + source + " --codecs= --html " + html,
			wantFile:   html,
		},
		{
			name: "several videos run qc ladder",
			answers: wizardAnswers{
				Source: source, Program: []string{episode}, Codecs: []string{"h264"},
				Advanced: true, Shape: shapeResolutions, Resolutions: "180", SkipVerify: true,
				HTML: html,
			},
			wantStderr: "$ qc ladder " + source + " " + episode + " -c h264 --rungs 180p --no-verify --html " + html,
			wantStdout: "program     2 videos",
			wantFile:   html,
		},
		{
			name: "a sample runs qc sample",
			answers: wizardAnswers{
				Source: source, Program: []string{episode}, ProgramAction: actionSample,
				SampleDuration: "2", SampleScenes: "easy", SampleTo: sampleFile,
			},
			wantStderr: "$ qc sample " + source + " " + episode + " --to " + sampleFile + " --duration 2 --scenes easy",
			wantStdout: "sample read back",
			wantFile:   sampleFile,
		},
		{
			name: "file named like a flag",
			answers: wizardAnswers{
				Source:  "--missing.mp4",
				Actions: []string{actionAnalysis},
			},
			wantCode:   1,
			wantStderr: "qc run ./--missing.mp4 --codecs=\n",
		},
		{
			name: "aborted",
			err:  huh.ErrUserAborted,
		},
		{
			name:       "form error",
			err:        errors.New("no terminal"),
			wantCode:   1,
			wantStderr: "qc: wizard: no terminal",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			env := environment{
				wizard: true,
				width:  defaultWidth,
				askWizard: func(wizardContext) (wizardAnswers, error) {
					return testCase.answers, testCase.err
				},
			}

			code, stdout, stderr := execute(t, env)

			require.Equal(t, testCase.wantCode, code, stderr)
			assert.Contains(t, stderr, "qc · fast video quality analysis")
			assert.Contains(t, stderr, testCase.wantStderr)
			assert.Contains(t, stdout, testCase.wantStdout)

			if testCase.wantFile != "" {
				_, err := os.Stat(testCase.wantFile)
				require.NoError(t, err)
			}
		})
	}
}

func TestWizardBrand(
	t *testing.T,
) {
	banner := ansi.Strip(wizardBanner())
	assert.Contains(t, banner, "▀▀  ▀▀ ▀▀ ▀▀")
	assert.Contains(t, banner, "qc · fast video quality analysis")
	assert.Contains(t, banner, "technical metrics · VMAF · per-title ladders")

	theme := wizardTheme(newWizardStyle(func(string) string { return "" }, termenv.TrueColor))
	assert.Equal(t, toneText, theme.Focused.Title.GetForeground())
	assert.Equal(t, brandOrange, theme.Group.Title.GetForeground())
	assert.Equal(t, brandOrange, theme.Focused.FocusedButton.GetBackground())
	assert.Equal(t, brandOrange, theme.Focused.Base.GetBorderLeftForeground(), "the focused field has the brand bar")
}

func TestWizardInvalidConfiguration(
	t *testing.T,
) {
	t.Setenv("QC_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))

	env := environment{
		wizard: true,
		width:  defaultWidth,
		askWizard: func(wizardContext) (wizardAnswers, error) {
			return wizardAnswers{}, errors.New("not asked")
		},
	}

	code, _, stderr := execute(t, env)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "missing.yaml")
	assert.NotContains(t, stderr, "not asked")
}
