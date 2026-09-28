package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/vmaf"
	"github.com/eko/qc/vmaf/libvmaf"
)

func TestResolveBuild(
	t *testing.T,
) {
	const revision = "0123456789abcdef0123456789abcdef01234567"

	vcs := func(modified string) []debug.BuildSetting {
		return []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: "2026-09-25T10:00:00Z"},
			{Key: "vcs.modified", Value: modified},
		}
	}

	testCases := []struct {
		name                              string
		ldVersion, ldCommit, ldDate       string
		info                              *debug.BuildInfo
		wantVersion, wantCommit, wantDate string
	}{
		{
			name:        "no build info",
			wantVersion: devVersion,
		},
		{
			name:        "link-time metadata wins",
			ldVersion:   "v0.1.0",
			ldCommit:    "abc1234",
			ldDate:      "2026-09-25",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "v0.0.9"}, Settings: vcs("true")},
			wantVersion: "v0.1.0",
			wantCommit:  "abc1234",
			wantDate:    "2026-09-25",
		},
		{
			name:        "go install of a tagged module",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			wantVersion: "v0.1.0",
		},
		{
			name:        "clean checkout",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("false")},
			wantVersion: devVersion,
			wantCommit:  "0123456789ab",
			wantDate:    "2026-09-25T10:00:00Z",
		},
		{
			name:        "dirty checkout",
			info:        &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("true")},
			wantVersion: devVersion,
			wantCommit:  "0123456789ab-dirty",
			wantDate:    "2026-09-25T10:00:00Z",
		},
		{
			name:        "short revision",
			info:        &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}},
			wantVersion: devVersion,
			wantCommit:  "abc",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			build := resolveBuild(testCase.ldVersion, testCase.ldCommit, testCase.ldDate, testCase.info)

			assert.Equal(t, testCase.wantVersion, build.Version)
			assert.Equal(t, testCase.wantCommit, build.Commit)
			assert.Equal(t, testCase.wantDate, build.Date)
			assert.Equal(t, runtime.Version(), build.Go)
			assert.Equal(t, runtime.GOOS+"/"+runtime.GOARCH, build.Platform)
		})
	}
}

// ffmpegEncoders is an excerpt of ffmpeg -encoders.
const ffmpegEncoders = `Encoders:
 V..... = Video
 A..... = Audio
 ------
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC (codec h264)
 V....D libx265              libx265 H.265 / HEVC (codec hevc)
 V....D libsvtav1            SVT-AV1(Scalable Video Technology for AV1) encoder (codec av1)
 A....D aac                  AAC (Advanced Audio Coding)
`

// fakeTools answers doctor's ffmpeg and ffprobe calls: outputs per binary
// and first argument after -hide_banner, errors per binary.
type fakeTools struct {
	outputs map[string]string
	errs    map[string]error
}

func (f fakeTools) output(
	_ context.Context,
	bin string,
	args []string,
) ([]byte, error) {
	if err := f.errs[bin+" "+args[1]]; err != nil {
		return nil, err
	}

	if err := f.errs[bin]; err != nil {
		return nil, err
	}

	return []byte(f.outputs[bin+" "+args[1]]), nil
}

func TestDoctorCheck(
	t *testing.T,
) {
	errNotFound := errors.New("binary not found")
	healthy := map[string]string{
		"ffmpeg -version":  "ffmpeg version 7.1.1-1 Copyright (c) 2000-2025 the FFmpeg developers\nbuilt with gcc\n",
		"ffprobe -version": "ffprobe version 7.1.1-1 Copyright (c) 2007-2025 the FFmpeg developers\n",
		"ffmpeg -encoders": ffmpegEncoders,
	}
	withNVENC := map[string]string{
		"ffmpeg -version":  healthy["ffmpeg -version"],
		"ffprobe -version": healthy["ffprobe -version"],
		"ffmpeg -encoders": ffmpegEncoders + " V....D h264_nvenc   NVIDIA NVENC H.264 encoder (codec h264)\n" +
			" V....D av1_nvenc    NVIDIA NVENC av1 encoder (codec av1)\n",
	}
	withVT := map[string]string{
		"ffmpeg -version":  healthy["ffmpeg -version"],
		"ffprobe -version": healthy["ffprobe -version"],
		"ffmpeg -encoders": ffmpegEncoders + " V....D h264_videotoolbox VideoToolbox H.264 Encoder (codec h264)\n",
	}
	withLibass := map[string]string{
		"ffmpeg -version":  healthy["ffmpeg -version"],
		"ffprobe -version": healthy["ffprobe -version"],
		"ffmpeg -encoders": ffmpegEncoders,
		"ffmpeg -filters":  " ... scale        V->V       Scale the input video size.\n ... subtitles    V->V       Render text subtitles.\n",
	}
	x264Only := map[string]string{
		"ffmpeg -version":  healthy["ffmpeg -version"],
		"ffprobe -version": "custom build\n",
		"ffmpeg -encoders": " V....D libx264  libx264 H.264\n",
	}

	testCases := []struct {
		name       string
		tools      fakeTools
		modelErr   error
		wantOK     map[string]bool
		wantDetail map[string]string
		wantFailed []string
	}{
		{
			name:  "healthy",
			tools: fakeTools{outputs: healthy},
			wantDetail: map[string]string{
				checkFFmpeg:    "7.1.1-1",
				checkFFprobe:   "7.1.1-1",
				checkNVENC:     "not available (optional)",
				checkVT:        "not available (optional)",
				checkLibass:    "not available (optional: --overlay)",
				checkVMAFModel: "/models/vmaf_v1.0.16_3d0h.json",
			},
		},
		{
			name:       "libass",
			tools:      fakeTools{outputs: withLibass},
			wantOK:     map[string]bool{checkLibass: true},
			wantDetail: map[string]string{checkLibass: "subtitles filter (--overlay)"},
		},
		{
			name:       "filters unreadable",
			tools:      fakeTools{outputs: withLibass, errs: map[string]error{"ffmpeg -filters": errors.New("killed")}},
			wantOK:     map[string]bool{checkLibass: false},
			wantDetail: map[string]string{checkLibass: "not available (optional: --overlay)"},
		},
		{
			name:       "nvenc encoders",
			tools:      fakeTools{outputs: withNVENC},
			wantOK:     map[string]bool{checkNVENC: true},
			wantDetail: map[string]string{checkNVENC: "h264_nvenc, av1_nvenc (built in; needs an NVIDIA GPU and driver)"},
		},
		{
			name:       "videotoolbox encoder",
			tools:      fakeTools{outputs: withVT},
			wantOK:     map[string]bool{checkVT: true, checkNVENC: false},
			wantDetail: map[string]string{checkVT: "h264_videotoolbox (built in; --overlay on macOS)"},
		},
		{
			name:       "missing encoders and unknown version",
			tools:      fakeTools{outputs: x264Only},
			wantDetail: map[string]string{checkFFprobe: "unknown version", "libx265": "not built into ffmpeg"},
			wantFailed: []string{"libx265", "libsvtav1"},
		},
		{
			name:       "no ffmpeg",
			tools:      fakeTools{outputs: healthy, errs: map[string]error{"ffmpeg": errNotFound}},
			wantDetail: map[string]string{checkFFmpeg: "binary not found", "libx264": "ffmpeg unavailable"},
			wantFailed: []string{checkFFmpeg, "libx264", "libx265", "libsvtav1"},
		},
		{
			name:       "encoders unreadable",
			tools:      fakeTools{outputs: healthy, errs: map[string]error{"ffmpeg -encoders": errors.New("killed")}},
			wantDetail: map[string]string{"libsvtav1": "killed"},
			wantFailed: []string{"libx264", "libx265", "libsvtav1"},
		},
		{
			name:       "model not loadable",
			tools:      fakeTools{outputs: healthy},
			modelErr:   vmaf.ErrModelNotFound,
			wantDetail: map[string]string{checkVMAFModel: vmaf.ErrModelNotFound.Error()},
			wantFailed: []string{checkVMAFModel},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotDirs []string

			d := doctor{
				output: testCase.tools.output,
				loadModel: func(dirs []string) (string, error) {
					gotDirs = dirs
					if testCase.modelErr != nil {
						return "", testCase.modelErr
					}

					return "/models/vmaf_v1.0.16_3d0h.json", nil
				},
			}

			config := Config{Tools: ToolsConfig{FFmpeg: "ffmpeg", FFprobe: "ffprobe"}, Quality: QualityConfig{ModelDir: []string{"/models"}}}
			report := versionReport{Checks: d.check(t.Context(), config)}

			names := make([]string, 0, len(report.Checks))
			for _, c := range report.Checks {
				names = append(names, c.Name)

				if want, ok := testCase.wantDetail[c.Name]; ok {
					assert.Equal(t, want, c.Detail, c.Name)
				}

				if want, ok := testCase.wantOK[c.Name]; ok {
					assert.Equal(t, want, c.OK, c.Name)
				}
			}

			wantNames := append([]string{checkFFmpeg, checkFFprobe}, requiredEncoders...)
			assert.Equal(t, append(wantNames, checkNVENC, checkVT, checkLibass, checkVMAFModel), names)
			assert.Equal(t, testCase.wantFailed, report.failed())
			assert.Equal(t, []string{"/models"}, gotDirs)
		})
	}
}

func TestLoadDefaultModel(
	t *testing.T,
) {
	empty := t.TempDir()

	corrupt := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(corrupt, "vmaf_v1.0.16_3d0h.json"), []byte("{}"), 0o600))

	testCases := []struct {
		name      string
		dirs      []string
		wantErrIs error
		wantErr   string
	}{
		{
			name:      "not in the directories",
			dirs:      []string{empty},
			wantErrIs: vmaf.ErrModelNotFound,
		},
		{
			name:    "unloadable model",
			dirs:    []string{corrupt},
			wantErr: "libvmaf ≥ 3.2.1 is required",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := loadDefaultModel(testCase.dirs)
			require.Error(t, err)

			if testCase.wantErrIs != nil {
				require.ErrorIs(t, err, testCase.wantErrIs)
			}

			assert.Contains(t, err.Error(), testCase.wantErr)
		})
	}
}

func TestPrintVersion(
	t *testing.T,
) {
	report := versionReport{
		Build: buildInfo{Version: "v0.1.0", Commit: "abc1234", Go: "go1.27.1", Platform: "linux/amd64", Libvmaf: "3.2.0"},
		Checks: []envCheck{
			{Name: checkFFmpeg, OK: true, Detail: "7.1.1"},
			{Name: "libsvtav1", Detail: "not built into ffmpeg"},
			{Name: checkNVENC, Optional: true, Detail: "not available (optional)"},
		},
	}

	testCases := []struct {
		name   string
		format string
		report versionReport
		want   []string
		absent []string
	}{
		{
			name:   "build only",
			format: formatText,
			report: versionReport{Build: report.Build},
			want:   []string{"qc       v0.1.0\n", "commit   abc1234\n", "go       go1.27.1 linux/amd64\n", "libvmaf  3.2.0\n"},
			absent: []string{"built", "✓"},
		},
		{
			name:   "checks",
			format: formatText,
			report: report,
			want:   []string{"✓ ffmpeg     7.1.1\n", "✗ libsvtav1  not built into ffmpeg\n", "- nvenc      not available (optional)\n"},
		},
		{
			name:   "json",
			format: formatJSON,
			report: report,
			want:   []string{`"version": "v0.1.0"`, `"name": "libsvtav1"`, `"optional": true`},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer

			require.NoError(t, printVersion(&out, testCase.format, testCase.report))

			for _, want := range testCase.want {
				assert.Contains(t, out.String(), want)
			}

			for _, absent := range testCase.absent {
				assert.NotContains(t, out.String(), absent)
			}
		})
	}
}

func TestVersionCommand(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	testCases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr string
		check      func(t *testing.T, stdout string)
	}{
		{
			name:       "build information",
			args:       []string{"version"},
			wantStdout: []string{"qc ", "go ", "libvmaf  " + libvmaf.Version()},
		},
		{
			name:       "environment check",
			args:       []string{"version", "--check"},
			wantStdout: []string{"✓ ffmpeg", "✓ libx264", "✓ vmaf model"},
		},
		{
			name: "environment check as json",
			args: []string{"version", "--check", "-f", "json"},
			check: func(t *testing.T, stdout string) {
				var report versionReport
				require.NoError(t, json.Unmarshal([]byte(stdout), &report))
				assert.Equal(t, libvmaf.Version(), report.Build.Libvmaf)
				assert.Empty(t, report.failed())
			},
		},
		{
			name:       "missing requirements",
			args:       []string{"version", "--check", "--ffmpeg", "no-such-ffmpeg", "--model-dir", t.TempDir()},
			wantCode:   1,
			wantStdout: []string{"✗ ffmpeg", "✗ vmaf model"},
			wantStderr: "environment check failed: ffmpeg, libx264, libx265, libsvtav1, vmaf model",
		},
		{
			name:       "invalid format",
			args:       []string{"version", "-f", "yaml"},
			wantCode:   1,
			wantStderr: "invalid output format",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			code, stdout, stderr := execute(t, testEnv, testCase.args...)

			assert.Equal(t, testCase.wantCode, code, stderr)
			assert.Contains(t, stderr, testCase.wantStderr)

			for _, want := range testCase.wantStdout {
				assert.Contains(t, stdout, want)
			}

			if testCase.check != nil {
				testCase.check(t, stdout)
			}
		})
	}
}
