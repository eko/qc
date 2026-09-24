package bitstream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
	"github.com/eko/qc/media"
)

func TestParsePacketLine(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		line    string
		want    media.Packet
		wantErr bool
	}{
		{
			name: "keyframe with negative dts",
			line: "pts_time=0.000000|dts_time=-0.066667|duration_time=0.033333|size=100629|flags=K__",
			want: media.Packet{
				PTS:      0,
				DTS:      -66667 * time.Microsecond,
				Duration: 33333 * time.Microsecond,
				Size:     100629,
				Keyframe: true,
			},
		},
		{
			name: "missing pts falls back to dts",
			line: "pts_time=N/A|dts_time=1.500000|duration_time=N/A|size=42|flags=___",
			want: media.Packet{
				PTS:  1500 * time.Millisecond,
				DTS:  1500 * time.Millisecond,
				Size: 42,
			},
		},
		{
			name: "missing dts falls back to pts and malformed fields are ignored",
			line: "pts_time=2.000000|garbage|dts_time=N/A|size=7|flags=K_",
			want: media.Packet{
				PTS:      2 * time.Second,
				DTS:      2 * time.Second,
				Size:     7,
				Keyframe: true,
			},
		},
		{
			name:    "no timestamp",
			line:    "pts_time=N/A|dts_time=N/A|size=42|flags=___",
			wantErr: true,
		},
		{
			name:    "invalid size",
			line:    "pts_time=0|dts_time=0|size=abc|flags=___",
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParsePacketLine([]byte(testCase.line))
			if testCase.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestFFprobeReaderFake(
	t *testing.T,
) {
	errCallback := errors.New("callback failed")

	testCases := []struct {
		name        string
		lines       []string
		callbackErr error
		want        []media.Packet
		wantErr     string
		wantErrIs   error
	}{
		{
			name:  "blank lines are skipped",
			lines: []string{"pts_time=0|dts_time=0|size=10|flags=K_", "  ", "pts_time=0.04|dts_time=0.04|size=5|flags=__"},
			want: []media.Packet{
				{Size: 10, Keyframe: true},
				{PTS: 40 * time.Millisecond, DTS: 40 * time.Millisecond, Size: 5},
			},
		},
		{
			name:    "parse error",
			lines:   []string{"pts_time=N/A|dts_time=N/A|size=1"},
			wantErr: "read packets in.mp4: packet without timestamp",
		},
		{
			name:        "callback error",
			lines:       []string{"pts_time=0|size=1"},
			callbackErr: errCallback,
			wantErrIs:   errCallback,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			reader := NewFFprobeReader("ffprobe")
			reader.lines = func(_ context.Context, bin string, args []string, fn func([]byte) error) error {
				assert.Equal(t, "ffprobe", bin)
				assert.Equal(t, "in.mp4", args[len(args)-1])

				for _, line := range testCase.lines {
					if err := fn([]byte(line)); err != nil {
						return err
					}
				}

				return nil
			}

			var got []media.Packet

			err := reader.ReadPackets(t.Context(), "in.mp4", func(pkt media.Packet) error {
				got = append(got, pkt)

				return testCase.callbackErr
			})

			switch {
			case testCase.wantErrIs != nil:
				require.ErrorIs(t, err, testCase.wantErrIs)
			case testCase.wantErr != "":
				require.ErrorContains(t, err, testCase.wantErr)
			default:
				require.NoError(t, err)
				assert.Equal(t, testCase.want, got)
			}
		})
	}
}

func TestFFprobeReader(
	t *testing.T,
) {
	path := testutil.Generate(t, testutil.Clip{Seconds: 1, GOP: 10})

	var packets []media.Packet

	err := NewFFprobeReader("ffprobe").ReadPackets(t.Context(), path, func(pkt media.Packet) error {
		packets = append(packets, pkt)

		return nil
	})
	require.NoError(t, err)
	require.Len(t, packets, 25)

	report := Analyze(packets, Options{})
	assert.Equal(t, []media.Duration{0, media.Seconds(0.4), media.Seconds(0.8)}, report.Keyframes)
	assert.True(t, report.GOP.Fixed)
}

func TestFFprobeReaderMissingFile(
	t *testing.T,
) {
	testutil.RequireFFmpeg(t)

	err := NewFFprobeReader("ffprobe").ReadPackets(t.Context(), "does-not-exist.mp4", func(media.Packet) error {
		return nil
	})

	require.ErrorContains(t, err, "read packets does-not-exist.mp4")
}
