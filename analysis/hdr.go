package analysis

import (
	"context"
	"slices"

	"github.com/eko/qc/media"
)

// HDRProber is the optional part of a Prober (probe.FFprobe) that reads the
// HDR side data of a video's first frame apart from its streams. The
// Analyzer then reads it while it decodes or measures, instead of before:
// that extra ffprobe call costs about 65 ms, more than the whole inspection
// of a short file.
type HDRProber interface {
	// ProbeStreams reads the container and streams without any frame.
	ProbeStreams(
		ctx context.Context,
		path string,
	) (*media.Info, error)
	// ProbeHDR completes the HDR metadata of info from its first frame.
	ProbeHDR(
		ctx context.Context,
		info *media.Info,
	) error
}

// probeInfo probes path: its streams only when defer is set and the prober
// can complete the HDR metadata later (deferred is then true), everything
// otherwise.
func (a *Analyzer) probeInfo(
	ctx context.Context,
	path string,
	deferHDR bool,
) (info *media.Info, deferred bool, err error) {
	if hp, ok := a.prober.(HDRProber); ok && deferHDR {
		info, err = hp.ProbeStreams(ctx, path)

		return info, err == nil, err
	}

	info, err = a.prober.Probe(ctx, path)

	return info, false, err
}

// completeHDR returns a copy of info with the HDR metadata of its first
// frame: a copy, so that the caller's concurrent readers of info are not
// raced.
func (a *Analyzer) completeHDR(
	ctx context.Context,
	info *media.Info,
) (*media.Info, error) {
	out := *info
	out.Video = slices.Clone(info.Video)

	if err := a.prober.(HDRProber).ProbeHDR(ctx, &out); err != nil {
		return nil, err
	}

	return &out, nil
}
