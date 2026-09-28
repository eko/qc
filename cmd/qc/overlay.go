package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eko/qc/encode"
	"github.com/eko/qc/overlay"
	"github.com/eko/qc/pipeline"
)

// ErrInvalidOverlayHeight is returned for an --overlay-height that is
// neither 0 nor a positive even number (4:2:0 video has even heights).
var ErrInvalidOverlayHeight = errors.New("invalid --overlay-height: want 0 (the source's) or a positive even height")

// ErrInvalidOverlayWorkers is returned for a negative --overlay-workers.
var ErrInvalidOverlayWorkers = errors.New("invalid --overlay-workers: want 0 (automatic) or more")

// ErrOverlayOverwritesSource is returned when --overlay names the video it
// annotates: ffmpeg would overwrite it while reading it.
var ErrOverlayOverwritesSource = errors.New("--overlay would overwrite the video it annotates")

// addOverlayFlags registers the flags of the annotated copy.
func addOverlayFlags(
	cmd *cobra.Command,
) {
	items := make([]string, 0, len(overlay.Items()))
	for _, item := range overlay.Items() {
		items = append(items, string(item))
	}

	flags := cmd.Flags()
	flags.String("overlay", "", "also write a copy of the video with the analysis burnt in as a debug overlay (H.264)")
	flags.StringSlice("overlay-items", nil, "parts of the overlay (default: all): "+strings.Join(items, ", "))
	flags.Int("overlay-height", 0, "height of the annotated copy (0 = the source's; 720 renders faster)")
	flags.String("overlay-encoder", "", "encoder of the annotated copy: auto (default: VideoToolbox on macOS, NVENC with --gpu, x264 elsewhere), "+
		"x264, videotoolbox or nvenc")
	flags.Int("overlay-workers", 0, "segments of the annotated copy rendered at once (0 = the encoder's default, 1 = a single pass)")
}

// validate checks the overlay items, height, encoder and workers.
func (c OverlayConfig) validate() error {
	if _, err := overlay.ParseItems(c.Items); err != nil {
		return fmt.Errorf("invalid --overlay-items: %w", err)
	}

	if c.Height < 0 || c.Height%2 != 0 {
		return fmt.Errorf("%w, got %d", ErrInvalidOverlayHeight, c.Height)
	}

	if _, err := encode.ParseBurnEncoder(c.Encoder); err != nil {
		return fmt.Errorf("invalid --overlay-encoder: %w", err)
	}

	if c.Workers < 0 {
		return fmt.Errorf("%w, got %d", ErrInvalidOverlayWorkers, c.Workers)
	}

	return nil
}

// encoder is the parsed --overlay-encoder (validated already).
func (c OverlayConfig) encoder() encode.BurnEncoder {
	e, _ := encode.ParseBurnEncoder(c.Encoder)

	return e
}

// overlayOptions maps the overlay flags to the pipeline's options for an
// annotated copy of source; nothing is written without --overlay.
func overlayOptions(
	config Config,
	source string,
) (pipeline.OverlayOptions, error) {
	output := config.Output.Overlay
	if output == "" {
		return pipeline.OverlayOptions{}, nil
	}

	if sameFile(output, source) {
		return pipeline.OverlayOptions{}, fmt.Errorf("%w: %s", ErrOverlayOverwritesSource, output)
	}

	// validate already rejected unknown items.
	items, _ := overlay.ParseItems(config.Overlay.Items)

	return pipeline.OverlayOptions{
		Output: output,
		Render: overlay.RenderOptions{
			Options: overlay.Options{Items: items},
			Height:  config.Overlay.Height,
			Encoder: config.Overlay.encoder(),
			Workers: config.Overlay.Workers,
		},
	}, nil
}

// sameFile reports whether paths a and b name the same file: the same
// path, or, when both exist, the same file through links.
func sameFile(
	a, b string,
) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)

	if errA == nil && errB == nil && absA == absB {
		return true
	}

	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)

	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

// burnChecker tells whether ffmpeg can burn the overlay (*encode.FFmpeg).
type burnChecker interface {
	CheckBurn(
		ctx context.Context,
		encoder encode.BurnEncoder,
	) error
}

// checkOverlay verifies, before any work, that ffmpeg can burn the overlay
// when --overlay asks for it, with the hardware encoder asked for: a
// missing libass or encoder must not surface after a long analysis.
func checkOverlay(
	ctx context.Context,
	output OutputConfig,
	config OverlayConfig,
	checker burnChecker,
) error {
	if output.Overlay == "" {
		return nil
	}

	if err := checker.CheckBurn(ctx, config.encoder()); err != nil {
		return fmt.Errorf("--overlay: %w", err)
	}

	return nil
}
