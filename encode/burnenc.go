package encode

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/eko/qc/internal/ffexec"
)

// BurnEncoder is the H.264 encoder of a burn (BurnSpec.Encoder).
type BurnEncoder string

// Burn encoders. A burn is a debug copy: the hardware encoders write it
// several times faster than x264, at a quality where the overlay's text
// stays crisp (see docs/overlay.md).
const (
	// BurnAuto encodes with VideoToolbox on macOS when it encodes a test
	// frame, with x264 otherwise (the zero value). NVENC is never chosen
	// automatically: it needs the GPU checks of an explicit request.
	BurnAuto BurnEncoder = ""
	// BurnX264 encodes with libx264 (BurnSpec.CRF and Preset).
	BurnX264 BurnEncoder = "x264"
	// BurnVideoToolbox encodes with Apple's hardware encoder
	// (h264_videotoolbox), at constant quality (Apple silicon).
	BurnVideoToolbox BurnEncoder = "videotoolbox"
	// BurnNVENC encodes with NVIDIA's hardware encoder (h264_nvenc), and
	// decodes with NVDEC.
	BurnNVENC BurnEncoder = "nvenc"
)

// burnAutoName is how BurnAuto is written on the command line.
const burnAutoName = "auto"

// ErrUnknownBurnEncoder is returned for an unknown burn encoder.
var ErrUnknownBurnEncoder = errors.New("unknown overlay encoder")

// ParseBurnEncoder reads a burn encoder: auto (or empty), x264,
// videotoolbox or nvenc.
func ParseBurnEncoder(
	s string,
) (BurnEncoder, error) {
	switch e := BurnEncoder(strings.ToLower(strings.TrimSpace(s))); e {
	case BurnAuto, burnAutoName:
		return BurnAuto, nil
	case BurnX264, BurnVideoToolbox, BurnNVENC:
		return e, nil
	}

	return BurnAuto, fmt.Errorf("%w %q (supported: auto, x264, videotoolbox, nvenc)", ErrUnknownBurnEncoder, s)
}

// String returns the encoder name, "auto" for the zero value.
func (e BurnEncoder) String() string {
	if e == BurnAuto {
		return burnAutoName
	}

	return string(e)
}

// Hardware reports whether e encodes on a hardware encoder.
func (e BurnEncoder) Hardware() bool {
	return e == BurnVideoToolbox || e == BurnNVENC
}

// Settings of the hardware burns: a debug copy watched to check an
// analysis, whose text must stay crisp on detailed moving pictures.
const (
	// vtQuality is VideoToolbox's constant quality (-q:v, 1–100, higher is
	// better): 60 writes about the bitrate of x264 at CRF 20 on a 1080p
	// cartoon, with the same crisp text (docs/overlay.md).
	vtQuality = 60
	// nvencBurnPreset and nvencBurnCQ are NVENC's speed preset and
	// constant quality, a CQ close to x264's CRF 20 scale.
	nvencBurnPreset = "p4"
	nvencBurnCQ     = 21
)

// Concurrent segment renders of each encoder when BurnSpec.Workers is 0
// (see docs/overlay.md, "Performance"). One ffmpeg draws the overlay on a
// single thread and cannot feed a hardware encoder; x264 already keeps
// every core busy in a single pass.
const (
	vtWorkers    = 6
	nvencWorkers = 4
)

// defaultWorkers is how many segments of a burn with e render at once: 1
// renders in a single pass.
func defaultWorkers(
	e BurnEncoder,
) int {
	switch e {
	case BurnVideoToolbox:
		return vtWorkers
	case BurnNVENC:
		return nvencWorkers
	}

	return 1
}

// burnInputArgs are the input options of a burn with e, in segments or in
// a single pass: hardware decoding next to a hardware encoder leaves the
// CPU to libass (ffmpeg downloads the frames for the filters, and decodes
// in software what the hardware does not). One VideoToolbox session
// decodes slower than the encoder encodes, and than ffmpeg's CPU decoder:
// only concurrent segments decode on it.
func burnInputArgs(
	e BurnEncoder,
	segments bool,
) []string {
	switch {
	case e == BurnVideoToolbox && segments:
		return []string{"-hwaccel", "videotoolbox"}
	case e == BurnNVENC:
		return []string{"-hwaccel", "cuda"}
	}

	return nil
}

// burnVideoArgs are the encoder options of a burn of spec with e.
func burnVideoArgs(
	e BurnEncoder,
	spec BurnSpec,
) []string {
	switch e {
	case BurnVideoToolbox:
		return []string{"-c:v", "h264_videotoolbox", "-q:v", strconv.Itoa(vtQuality)}
	case BurnNVENC:
		return []string{
			"-c:v", "h264_nvenc", "-preset", nvencBurnPreset, "-tune", nvencTune,
			"-rc", "vbr", "-cq", strconv.Itoa(nvencBurnCQ), "-b:v", "0",
		}
	}

	return []string{
		"-c:v", burnEncoder,
		"-preset", cmp.Or(spec.Preset, burnPreset),
		"-crf", strconv.FormatFloat(cmp.Or(spec.CRF, burnCRF), 'f', -1, 64),
	}
}

// checkFrames is the length of the test encode of CheckBurn, in frames.
const checkFrames = "5"

// checkEncoder encodes a few frames with e: it fails when ffmpeg lacks the
// encoder or the machine its hardware (no GPU, an Intel Mac without
// constant quality).
func (f *FFmpeg) checkEncoder(
	ctx context.Context,
	e BurnEncoder,
) error {
	args := []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "color=size=256x144:rate=25", "-frames:v", checkFrames}
	args = append(append(args, burnVideoArgs(e, BurnSpec{})...), "-f", "null", "-")

	if _, err := ffexec.Output(ctx, f.bin, args); err != nil {
		return fmt.Errorf("%s encoder: %w", e, err)
	}

	return nil
}

// burnEncoder resolves BurnAuto for this machine: VideoToolbox on macOS
// when it encodes a test frame (checked once), x264 otherwise, with a
// warning when VideoToolbox fails.
func (f *FFmpeg) burnEncoder(
	ctx context.Context,
	e BurnEncoder,
) BurnEncoder {
	if e != BurnAuto {
		return e
	}

	if f.goos != darwin {
		return BurnX264
	}

	f.autoOnce.Do(func() {
		f.auto = BurnVideoToolbox
		if err := f.checkEncoder(ctx, BurnVideoToolbox); err != nil {
			f.auto = BurnX264
			f.logger.Warn("hardware encoder unavailable, burning the overlay with x264", "error", err)
		}
	})

	return f.auto
}
