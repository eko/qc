package encode

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/eko/qc/internal/ffexec"
)

// ErrStats is returned when the statistics of an encode cannot be read.
var ErrStats = errors.New("encoder statistics")

// StatsSpec describes a statistics pass: Source scaled to Width×Height and
// encoded by x264 at each of CRFs, without keeping the encodes. It is the
// cheapest look an encoder can take at a video (a second or two a minute
// at a low resolution and the ultrafast preset), and what it spends where
// tells how the video encodes elsewhere.
type StatsSpec struct {
	Source        string
	Width, Height int
	Preset        string
	CRFs          []float64
	// GOP is the keyframe interval in frames: the statistics are returned
	// GOP by GOP.
	GOP int
	// WorkDir receives the encoder's logs, removed afterwards.
	WorkDir string
	// Keep writes the encode of each CRF to Output(i) instead of
	// dropping it: an encode this cheap can also be scored.
	Keep bool
	// Threads caps the threads of each encoder (0: its own choice).
	Threads int
}

// Output is the file the encode of CRF i is kept in (see Keep).
func (s StatsSpec) Output(
	i int,
) string {
	return filepath.Join(s.WorkDir, "stats-"+strconv.Itoa(i)+".mp4")
}

// GOPStats are x264's statistics of one GOP of a statistics pass.
type GOPStats struct {
	// Frames is the number of frames of the GOP.
	Frames int
	// Bits is the size of the GOP; Texture the bits of its residuals and
	// Motion those of its motion vectors.
	Bits    int64
	Texture int64
	Motion  int64
	// QP is the mean quantiser of its frames, adaptive quantisation
	// included, and PSNR their mean PSNR at the encoded resolution.
	QP   float64
	PSNR float64
	// Intra and Skip are the shares of its macroblocks coded intra and
	// skipped.
	Intra float64
	Skip  float64
}

// Stats runs the statistics pass spec describes and returns, for each CRF
// in order, the statistics of every GOP. The CRFs are encoded at once, one
// ffmpeg each: ffmpeg's log of the frames of several outputs misses frames.
func (f *FFmpeg) Stats(
	ctx context.Context,
	spec StatsSpec,
) ([][]GOPStats, error) {
	if len(spec.CRFs) == 0 || spec.GOP <= 0 {
		return nil, fmt.Errorf("stats of %s: %w: no CRF or no GOP", spec.Source, ErrStats)
	}

	if spec.Threads == 0 {
		spec.Threads = f.threads
	}

	out := make([][]GOPStats, len(spec.CRFs))
	group, gctx := errgroup.WithContext(ctx)

	for i := range spec.CRFs {
		group.Go(func() (err error) {
			out[i], err = f.statsAt(gctx, spec, i)

			return err
		})
	}

	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("stats of %s: %w", spec.Source, err)
	}

	return out, nil
}

// statsAt runs the pass of CRF i of spec.
func (f *FFmpeg) statsAt(
	ctx context.Context,
	spec StatsSpec,
	i int,
) ([]GOPStats, error) {
	defer func() {
		_ = os.Remove(spec.vstats(i))
		_ = os.Remove(spec.passLog(i) + passLogSuffix)
		_ = os.Remove(spec.passLog(i) + passLogSuffix + ".mbtree")
	}()

	if err := ffexec.Stream(ctx, f.bin, statsArgs(spec, i), discard); err != nil {
		return nil, err
	}

	psnr, err := readPSNR(spec.vstats(i))
	if err != nil {
		return nil, err
	}

	return readPassLog(spec.passLog(i)+passLogSuffix, psnr)
}

// passLogSuffix is what x264 appends to the name of its first-pass log.
const passLogSuffix = "-0.log"

// passLog is the name given to x264 for the first-pass log of CRF i.
func (s StatsSpec) passLog(
	i int,
) string {
	return filepath.Join(s.WorkDir, "stats-"+strconv.Itoa(i))
}

// vstats is ffmpeg's log of the frames of CRF i.
func (s StatsSpec) vstats(
	i int,
) string {
	return filepath.Join(s.WorkDir, "stats-"+strconv.Itoa(i)+"-frames.log")
}

// statsArgs are the ffmpeg arguments of the pass of CRF i of spec: x264
// writes its first-pass log (with the preset as it is: x264 otherwise cuts
// the analysis of a first pass short) and no output.
func statsArgs(
	spec StatsSpec,
	i int,
) []string {
	gop := strconv.Itoa(spec.GOP)
	args := []string{
		"-v", "error", "-nostdin", "-y", "-i", spec.Source, "-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=bicubic,format=%s", spec.Width, spec.Height, pixelFormat8),
		"-c:v", "libx264", "-preset", spec.Preset, "-crf", strconv.FormatFloat(spec.CRFs[i], 'f', -1, 64),
		"-g", gop, "-keyint_min", gop, "-sc_threshold", "0",
		"-flags", "+psnr", "-pass", "1", "-fastfirstpass", "0", "-passlogfile", spec.passLog(i),
		"-vstats_file", spec.vstats(i),
	}

	if spec.Threads > 0 {
		args = append(args, "-threads", strconv.Itoa(spec.Threads))
	}

	if spec.Keep {
		return append(args, spec.Output(i))
	}

	return append(args, "-f", "null", "-")
}

// readPassLog reads x264's first-pass log at path, one line per frame in
// coded order ("in:0 out:0 type:I q:26.09 aq:26.00 tex:175696 mv:9256
// misc:5176 imb:920 pmb:0 smb:0"), into the statistics of its GOPs, each
// starting at an I frame. psnr is the PSNR of the frames, in the same
// order.
func readPassLog(
	path string,
	psnr []float64,
) ([]GOPStats, error) {
	frames, err := readFields(path, ":")
	if err != nil {
		return nil, err
	}

	if len(frames) == 0 || len(frames) != len(psnr) {
		return nil, fmt.Errorf("%w: %d frames in %s, %d in ffmpeg's log", ErrStats, len(frames), filepath.Base(path), len(psnr))
	}

	var gops []GOPStats

	for i, frame := range frames {
		if frame.text["type"] == "I" || len(gops) == 0 {
			gops = append(gops, GOPStats{})
		}

		g := &gops[len(gops)-1]
		blocks := max(frame.value["imb"]+frame.value["pmb"]+frame.value["smb"], 1)

		g.Frames++
		g.Bits += int64(frame.value["tex"] + frame.value["mv"] + frame.value["misc"])
		g.Texture += int64(frame.value["tex"])
		g.Motion += int64(frame.value["mv"])
		g.QP += frame.value["aq"]
		g.PSNR += psnr[i]
		g.Intra += frame.value["imb"] / blocks
		g.Skip += frame.value["smb"] / blocks
	}

	for i := range gops {
		g := &gops[i]
		n := float64(g.Frames)
		g.QP, g.PSNR, g.Intra, g.Skip = g.QP/n, g.PSNR/n, g.Intra/n, g.Skip/n
	}

	return gops, nil
}

// readPSNR reads ffmpeg's -vstats_file at path ("frame= 1 q= 31.0 PSNR=
// 36.69 f_size= 2239 ...", one line per encoded frame) into the PSNR of
// the frames, in coded order.
func readPSNR(
	path string,
) ([]float64, error) {
	frames, err := readFields(path, "=")
	if err != nil {
		return nil, err
	}

	out := make([]float64, len(frames))
	for i, frame := range frames {
		out[i] = frame.value["PSNR"]
	}

	return out, nil
}

// ScaleLossSpec describes a measure of what scaling loses: frames of
// Source, one every Step, scaled down to each of Heights and back, and
// compared with themselves.
type ScaleLossSpec struct {
	Source string
	// Width and Height are the size of the source; Heights the
	// resolutions tried, their widths keeping its aspect ratio.
	Width, Height int
	Heights       []int
	// Step keeps one frame in Step (1 or less: every frame).
	Step int
	// WorkDir receives ffmpeg's logs, removed afterwards.
	WorkDir string
}

// ScaleLoss returns, for each height of spec in order, the luma PSNR of
// every frame kept after a round trip through that resolution (bicubic
// both ways, as the encodes of a ladder and their scoring scale): what a
// rendition at that resolution can no longer show, however many bits it
// gets. Detailed pictures lose much, soft ones nothing.
func (f *FFmpeg) ScaleLoss(
	ctx context.Context,
	spec ScaleLossSpec,
) ([][]float64, error) {
	if len(spec.Heights) == 0 || spec.Width <= 0 || spec.Height <= 0 {
		return nil, fmt.Errorf("scale loss of %s: %w: no resolution", spec.Source, ErrStats)
	}

	defer func() {
		for i := range spec.Heights {
			_ = os.Remove(spec.log(i))
		}
	}()

	if err := ffexec.Stream(ctx, f.bin, scaleLossArgs(spec), discard); err != nil {
		return nil, fmt.Errorf("scale loss of %s: %w", spec.Source, err)
	}

	out := make([][]float64, len(spec.Heights))

	for i := range spec.Heights {
		frames, err := readFields(spec.log(i), ":")
		if err != nil {
			return nil, fmt.Errorf("scale loss of %s: %w", spec.Source, err)
		}

		for _, frame := range frames {
			out[i] = append(out[i], frame.value["psnr_y"])
		}
	}

	return out, nil
}

// log is the log of the PSNR filter of height i.
func (s ScaleLossSpec) log(
	i int,
) string {
	return filepath.Join(s.WorkDir, "scale-"+strconv.Itoa(i)+".log")
}

// scaleLossArgs are the ffmpeg arguments of a scale loss measure: the
// frames kept are split between the resolutions, each scaled down and up
// and compared with the frames by a psnr filter writing its own log.
func scaleLossArgs(
	spec ScaleLossSpec,
) []string {
	var graph strings.Builder

	n := len(spec.Heights)

	fmt.Fprintf(&graph, "[0:v:0]select='not(mod(n\\,%d))',format=%s,split=%d", max(spec.Step, 1), pixelFormat8, 2*n)

	for i := range n {
		fmt.Fprintf(&graph, "[r%d][d%d]", i, i)
	}

	for i, height := range spec.Heights {
		width := evenWidth(height, spec.Width, spec.Height)
		fmt.Fprintf(&graph, ";[d%d]scale=%d:%d:flags=bicubic,scale=%d:%d:flags=bicubic[u%d];[u%d][r%d]psnr=stats_file=%s[o%d]",
			i, width, height, spec.Width, spec.Height, i, i, i, filterPath(spec.log(i)), i)
	}

	args := []string{"-v", "error", "-nostdin", "-y", "-i", spec.Source, "-filter_complex", graph.String()}

	for i := range n {
		args = append(args, "-map", fmt.Sprintf("[o%d]", i), "-fps_mode", "passthrough", "-f", "null", "-")
	}

	return args
}

// evenWidth is the width of a frame of height keeping the aspect ratio of
// sourceWidth×sourceHeight, rounded to even.
func evenWidth(
	height, sourceWidth, sourceHeight int,
) int {
	return (height*sourceWidth/sourceHeight + 1) / 2 * 2
}

// filterPath escapes a path for an option of a filter graph, where ':',
// '\' and quotes have a meaning.
func filterPath(
	path string,
) string {
	replacer := strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`, `,`, `\,`, `[`, `\[`, `]`, `\]`, `;`, `\;`)

	return replacer.Replace(path)
}

// logFrame is one line of an encoder log: its fields by name, the numeric
// ones as numbers.
type logFrame struct {
	value map[string]float64
	text  map[string]string
}

// readFields reads a log of one line per frame made of "name<sep>value"
// fields, the value possibly after spaces. Lines starting with # are
// comments.
func readFields(
	path, sep string,
) ([]logFrame, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStats, err)
	}
	defer file.Close()

	var frames []logFrame

	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		frames = append(frames, parseFields(line, sep))
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStats, err)
	}

	return frames, nil
}

// parseFields splits a log line into its fields.
func parseFields(
	line, sep string,
) logFrame {
	fields := strings.Fields(strings.ReplaceAll(line, sep, sep+" "))
	frame := logFrame{value: map[string]float64{}, text: map[string]string{}}

	for i := 0; i+1 < len(fields); i++ {
		name, found := strings.CutSuffix(fields[i], sep)
		if !found {
			continue
		}

		frame.text[name] = fields[i+1]

		if value, err := strconv.ParseFloat(fields[i+1], 64); err == nil {
			frame.value[name] = value
		}
	}

	return frame
}
