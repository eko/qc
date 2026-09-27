package decode

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/eko/qc/frame"
	"github.com/eko/qc/internal/ffexec"
)

// sampledArgs builds the ffmpeg command line of a sampling pool (HDR
// analysis): one decode, split in two outputs. Stdout gets the 8-bit luma
// the frame analyzers read, exactly as for any luma pool; the extra pipe
// gets the sample grid, 10-bit 4:4:4 point-sampled with the neighbour
// scaler: point (i, j) is the luma pixel at the centre of its step×step
// cell and the chroma sample covering it, code values untouched. The grid
// is a few hundred kilobytes where whole 10-bit frames would triple the
// bytes piped.
func (d *FFmpeg) sampledArgs(
	req Request,
	mode HWAccel,
) []string {
	args := append(d.inputArgs(req, mode), "-i", req.Path, "-filter_complex", sampledGraph(req, mode))

	output := func(label, target string) []string {
		out := []string{"-map", label, "-fps_mode", "passthrough", "-an", "-sn", "-dn"}
		if req.MaxFrames > 0 {
			out = append(out, "-frames:v", strconv.Itoa(req.MaxFrames))
		}

		return append(out, "-f", "rawvideo", target)
	}

	args = append(args, output("[luma]", "-")...)

	return append(args, output("[grid]", ffexec.ExtraOutput)...)
}

// sampledGraph is the filter graph of sampledArgs: frame selection (and
// the GPU scaling of HWAccelCUDAScale), then the two branches.
func sampledGraph(
	req Request,
	mode HWAccel,
) string {
	pool := req.Pool

	var head []string

	if mode == HWAccelCUDAScale {
		head = append(head,
			fmt.Sprintf("scale_cuda=%d:%d:interp_algo=bicubic:format=yuv420p10le", pool.Width(), pool.Height()),
			"hwdownload", "format=yuv420p10le")
	}

	if len(req.Select) > 0 {
		head = append(head, selectFilter(req.Select))
	}

	grid := pool.GridSize()

	return "[0:v:0]" + strings.Join(append(head, "split=2[l][g]"), ",") + ";" +
		fmt.Sprintf("[l]extractplanes=y,scale=%d:%d:flags=bicubic,format=gray[luma];", pool.Width(), pool.Height()) +
		fmt.Sprintf("[g]scale=%d:%d:flags=neighbor,format=yuv444p10le[grid]", grid[0], grid[1])
}

// sampledReader reads the frames of a sampling pool: the luma from one
// output, the sample grid from the other, in turn (ffmpeg writes a frame
// on each before the next).
func sampledReader(
	luma, grid io.Reader,
	pool *frame.Pool,
) func(*frame.Frame) error {
	return func(f *frame.Frame) error {
		if err := readPlanes(luma, pool.Planes(f)); err != nil {
			return err
		}

		if err := readPlanes(grid, pool.SamplePlanes(f)); err != nil {
			if errors.Is(err, io.EOF) {
				err = fmt.Errorf("sample grid: %w", io.ErrUnexpectedEOF)
			}

			return err
		}

		return nil
	}
}
