package encode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eko/qc/internal/testutil"
)

func TestStatsArgs(
	t *testing.T,
) {
	spec := StatsSpec{
		Source: "digest.nut", Width: 640, Height: 360, Preset: "ultrafast",
		CRFs: []float64{23, 35.5}, GOP: 50, WorkDir: "work", Threads: 4,
	}

	assert.Equal(t, []string{
		"-v", "error", "-nostdin", "-y", "-i", "digest.nut", "-an", "-sn", "-dn",
		"-vf", "scale=640:360:flags=bicubic,format=yuv420p",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "35.5",
		"-g", "50", "-keyint_min", "50", "-sc_threshold", "0",
		"-flags", "+psnr", "-pass", "1", "-fastfirstpass", "0", "-passlogfile", filepath.Join("work", "stats-1"),
		"-vstats_file", filepath.Join("work", "stats-1-frames.log"),
		"-threads", "4", "-f", "null", "-",
	}, statsArgs(spec, 1))
}

func TestReadPassLog(
	t *testing.T,
) {
	const header = "#options: 640x360 fps=25/1 keyint=2 rc=crf crf=29.0\n"

	testCases := []struct {
		name    string
		log     string
		psnr    []float64
		want    []GOPStats
		wantErr bool
	}{
		{
			name: "two GOPs",
			log: header +
				"in:0 out:0 type:I dur:2 cpbdur:2 q:26.09 aq:26.00 tex:1000 mv:100 misc:20 imb:100 pmb:0 smb:0 d:- ref:;\n" +
				"in:1 out:1 type:P dur:2 cpbdur:2 q:33.26 aq:30.00 tex:200 mv:60 misc:20 imb:10 pmb:40 smb:50 d:- ref:0 ;\n" +
				"in:2 out:2 type:I dur:2 cpbdur:2 q:26.00 aq:24.00 tex:800 mv:80 misc:20 imb:100 pmb:0 smb:0 d:- ref:;\n",
			psnr: []float64{40, 36, 42},
			want: []GOPStats{
				{Frames: 2, Bits: 1400, Texture: 1200, Motion: 160, QP: 28, PSNR: 38, Intra: 0.55, Skip: 0.25},
				{Frames: 1, Bits: 900, Texture: 800, Motion: 80, QP: 24, PSNR: 42, Intra: 1, Skip: 0},
			},
		},
		{
			name:    "frames missing from ffmpeg's log",
			log:     header + "in:0 out:0 type:I q:26.09 aq:26.00 tex:1000 mv:100 misc:20 imb:100 pmb:0 smb:0\n",
			wantErr: true,
		},
		{name: "empty log", log: header, wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stats-0.log")
			require.NoError(t, os.WriteFile(path, []byte(testCase.log), 0o600))

			got, err := readPassLog(path, testCase.psnr)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrStats)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestReadPSNR(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "frames.log")
	require.NoError(t, os.WriteFile(path, []byte(
		"out=  0 st=  0 frame=     0 q= 31.0 PSNR=  40.69 f_size=  23765 s_size=        0KiB time= 0.010 br=  4753.0kbits/s avg_br=     0.0kbits/s type= I\n"+
			"out=  0 st=  0 frame=     1 q= 31.0 PSNR=  36.50 f_size=   2239 s_size=       23KiB time= 0.040 br=   447.8kbits/s avg_br=  4753.0kbits/s type= P\n"), 0o600))

	got, err := readPSNR(path)
	require.NoError(t, err)
	assert.Equal(t, []float64{40.69, 36.5}, got)

	_, err = readPSNR(filepath.Join(t.TempDir(), "missing.log"))
	require.ErrorIs(t, err, ErrStats)
}

func TestScaleLossArgs(
	t *testing.T,
) {
	spec := ScaleLossSpec{Source: "digest.nut", Width: 1920, Height: 1080, Heights: []int{720, 270}, Step: 10, WorkDir: "work"}

	assert.Equal(t, []string{
		"-v", "error", "-nostdin", "-y", "-i", "digest.nut", "-filter_complex",
		`[0:v:0]select='not(mod(n\,10))',format=yuv420p,split=4[r0][d0][r1][d1]` +
			`;[d0]scale=1280:720:flags=bicubic,scale=1920:1080:flags=bicubic[u0];[u0][r0]psnr=stats_file=work/scale-0.log[o0]` +
			`;[d1]scale=480:270:flags=bicubic,scale=1920:1080:flags=bicubic[u1];[u1][r1]psnr=stats_file=work/scale-1.log[o1]`,
		"-map", "[o0]", "-fps_mode", "passthrough", "-f", "null", "-",
		"-map", "[o1]", "-fps_mode", "passthrough", "-f", "null", "-",
	}, scaleLossArgs(spec))

	assert.Equal(t, `C\:/work\,dir/it\'s.log`, filterPath(`C:/work,dir/it's.log`))
}

func TestStatsAndScaleLossRejectEmptySpecs(
	t *testing.T,
) {
	enc := NewFFmpeg("ffmpeg")

	_, err := enc.Stats(t.Context(), StatsSpec{Source: "in.nut", GOP: 50})
	require.ErrorIs(t, err, ErrStats)

	_, err = enc.Stats(t.Context(), StatsSpec{Source: "in.nut", CRFs: []float64{29}})
	require.ErrorIs(t, err, ErrStats)

	_, err = enc.ScaleLoss(t.Context(), ScaleLossSpec{Source: "in.nut", Width: 1920, Height: 1080})
	require.ErrorIs(t, err, ErrStats)
}

// TestStatsAndScaleLossOnAClip runs both passes with ffmpeg on a generated
// clip: two GOPs of a second, a busy picture.
func TestStatsAndScaleLossOnAClip(
	t *testing.T,
) {
	clip := testutil.Generate(t, testutil.Clip{GOP: 25, Seconds: 2, Name: "clip.mp4"})
	enc := NewFFmpeg("ffmpeg")
	dir := t.TempDir()

	stats, err := enc.Stats(t.Context(), StatsSpec{
		Source: clip, Width: 160, Height: 90, Preset: "ultrafast", CRFs: []float64{23, 35}, GOP: 25, WorkDir: dir,
	})
	require.NoError(t, err)
	require.Len(t, stats, 2)

	for _, crf := range stats {
		require.Len(t, crf, 2)

		for _, gop := range crf {
			assert.Equal(t, 25, gop.Frames)
			assert.Positive(t, gop.Bits)
			assert.Greater(t, gop.PSNR, 20.0)
			assert.GreaterOrEqual(t, gop.Bits, gop.Texture+gop.Motion)
		}
	}

	assert.Greater(t, stats[0][0].Bits, stats[1][0].Bits, "a lower CRF costs more")
	assert.Greater(t, stats[0][0].PSNR, stats[1][0].PSNR)

	loss, err := enc.ScaleLoss(t.Context(), ScaleLossSpec{Source: clip, Width: 320, Height: 180, Heights: []int{90, 44}, Step: 10, WorkDir: dir})
	require.NoError(t, err)
	require.Len(t, loss, 2)
	require.Len(t, loss[0], 5)
	assert.Greater(t, loss[0][0], loss[1][0], "a smaller resolution loses more")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the logs are removed")
}
