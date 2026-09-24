package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

// retime rewrites the timestamps of an AV1 encode at path to a constant
// rate. SVT-AV1 keeps the source's timestamps, and a source with a gap in
// them (one real title has a 3-frame gap near its end) yields an average
// frame rate the VMAF engine rightly refuses to pair with the source's. AV1
// packets are in presentation order (one temporal unit per shown frame), so
// numbering them is exact; codecs with reordered frames keep theirs.
func retime(
	ctx context.Context,
	ffmpeg string,
	codec encode.Codec,
	path string,
	rate media.Rational,
) error {
	if codec.Name != "av1" {
		return nil
	}

	tmp := path + ".retimed.mp4"

	out, err := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-nostdin", "-y", "-i", path, "-c", "copy",
		"-bsf:v", fmt.Sprintf("setts=ts=N/(%s*TB)", rate), tmp).CombinedOutput()
	if err != nil {
		return fmt.Errorf("retime %s: %w: %s", path, err, out)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("retime %s: %w", path, err)
	}

	return nil
}
