package encode

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
)

func TestParseBurnEncoder(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		in      string
		want    BurnEncoder
		wantErr error
	}{
		{name: "empty is auto", in: "", want: BurnAuto},
		{name: "auto", in: " Auto ", want: BurnAuto},
		{name: "x264", in: "x264", want: BurnX264},
		{name: "videotoolbox", in: "VideoToolbox", want: BurnVideoToolbox},
		{name: "nvenc", in: "nvenc", want: BurnNVENC},
		{name: "unknown", in: "qsv", want: BurnAuto, wantErr: ErrUnknownBurnEncoder},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseBurnEncoder(testCase.in)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestBurnEncoderNames(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		encoder      BurnEncoder
		wantString   string
		wantHardware bool
		wantWorkers  int
	}{
		{name: "auto", encoder: BurnAuto, wantString: "auto", wantWorkers: 1},
		{name: "x264", encoder: BurnX264, wantString: "x264", wantWorkers: 1},
		{name: "videotoolbox", encoder: BurnVideoToolbox, wantString: "videotoolbox", wantHardware: true, wantWorkers: vtWorkers},
		{name: "nvenc", encoder: BurnNVENC, wantString: "nvenc", wantHardware: true, wantWorkers: nvencWorkers},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.wantString, testCase.encoder.String())
			assert.Equal(t, testCase.wantHardware, testCase.encoder.Hardware())
			assert.Equal(t, testCase.wantWorkers, defaultWorkers(testCase.encoder))
		})
	}
}

// TestBurnEncoderAuto resolves BurnAuto: VideoToolbox on macOS when it
// encodes its test frames (checked once), x264 with a warning when it
// fails, and x264 elsewhere without trying.
func TestBurnEncoderAuto(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		goos        string
		body        string
		in          BurnEncoder
		want        BurnEncoder
		wantWarning bool
	}{
		{name: "explicit encoders are kept", goos: darwin, body: "exit 1", in: BurnNVENC, want: BurnNVENC},
		{name: "macOS with VideoToolbox", goos: darwin, body: "exit 0", want: BurnVideoToolbox},
		{name: "macOS without VideoToolbox", goos: darwin, body: "exit 1", want: BurnX264, wantWarning: true},
		{name: "elsewhere", goos: "linux", body: "exit 1", want: BurnX264},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var logs bytes.Buffer

			f := NewFFmpeg(testutil.FakeFFmpeg(t, testCase.body), WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
			f.goos = testCase.goos

			assert.Equal(t, testCase.want, f.burnEncoder(t.Context(), testCase.in))
			assert.Equal(t, testCase.want, f.burnEncoder(t.Context(), testCase.in), "the choice is kept")
			assert.Equal(t, testCase.wantWarning, bytes.Contains(logs.Bytes(), []byte("burning the overlay with x264")))
		})
	}
}
