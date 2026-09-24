package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// clip is a test file and the formats its frames are compared in.
type clip struct {
	label string
	path  string
	// format is the planar format frames are hashed in, hwFormat the one
	// NVDEC surfaces are downloaded as.
	format, hwFormat string
}

// content is what the steps work on: a 1080p source, encodes of it in
// every NVDEC codec, and the distorted encode VMAF is measured on.
type content struct {
	source        string
	width, height int
	clips         []clip
	distorted     string
	distorted10   string
}

// segment is a synthetic lavfi source and the filter applied to it.
type segment struct {
	source, filter string
}

// segments are the synthetic sources, each a few seconds of 1080p25:
// sharp motion, the same with temporal grain (NVENC's rate control and
// VMAF both react to it), smooth gradients (banding) and a fractal zoom
// (fine texture).
var segments = []segment{
	{source: "testsrc2=s=1920x1080:r=25"},
	{source: "testsrc2=s=1920x1080:r=25", filter: "noise=alls=14:allf=t+u"},
	{source: "gradients=s=1920x1080:r=25:speed=0.02"},
	{source: "mandelbrot=s=1920x1080:r=25:maxiter=256"},
}

// graph is the lavfi graph of the segment (its length is set by -t: not
// every source has a duration option).
func (s segment) graph() string {
	g := s.source
	if s.filter != "" {
		g += "," + s.filter
	}

	return g
}

// prepare generates the source (or takes -source) and its test encodes.
func (v *validator) prepare(
	ctx context.Context,
) (content, error) {
	c := content{source: v.opts.source}

	if c.source == "" {
		c.source = v.path("source.mp4")
		v.progress("generating %d synthetic segments of %d s", len(segments), v.opts.seconds)

		if _, err := v.run.run(ctx, v.opts.ffmpeg, synthesisArgs(c.source, v.opts.seconds)...); err != nil {
			return content{}, fmt.Errorf("gpuval: generate content: %w", err)
		}
	}

	var err error
	if c.width, c.height, err = v.geometry(ctx, c.source); err != nil {
		return content{}, err
	}

	v.report.section("Content")

	if v.opts.source == "" {
		graphs := make([]string, len(segments))
		for i, seg := range segments {
			graphs[i] = seg.graph()
		}

		v.report.line("Synthetic, generated with ffmpeg lavfi: `%s` (%d s each, x264 CRF 12).", strings.Join(graphs, "`, `"), v.opts.seconds)
	} else {
		v.report.line("Public clip `%s` (%d×%d).", v.opts.source, c.width, c.height)
	}

	encodes := []struct {
		clip
		args []string
	}{
		{clip: clip{label: "H.264 720p (distorted)", path: v.path("dist-h264-720p.mp4"), format: "yuv420p", hwFormat: "nv12"},
			args: []string{"-vf", "scale=-2:720", "-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-g", "50"}},
		{clip: clip{label: "HEVC Main10 1080p", path: v.path("dist-hevc10.mp4"), format: "yuv420p10le", hwFormat: "p010le"},
			args: []string{"-pix_fmt", "yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast", "-crf", "28",
				"-x265-params", "log-level=error", "-g", "50"}},
		{clip: clip{label: "AV1 1080p", path: v.path("dist-av1.mp4"), format: "yuv420p", hwFormat: "nv12"},
			args: []string{"-c:v", "libsvtav1", "-preset", "10", "-crf", "40", "-g", "50"}},
	}

	c.clips = []clip{{label: "H.264 1080p (source)", path: c.source, format: "yuv420p", hwFormat: "nv12"}}

	for _, e := range encodes {
		v.progress("encoding %s", e.label)

		args := append([]string{"-v", "error", "-nostdin", "-y", "-i", c.source, "-an"}, e.args...)
		if _, err := v.run.run(ctx, v.opts.ffmpeg, append(args, e.path)...); err != nil {
			return content{}, fmt.Errorf("gpuval: encode %s: %w", e.label, err)
		}

		c.clips = append(c.clips, e.clip)
	}

	c.distorted, c.distorted10 = c.clips[1].path, c.clips[2].path

	return c, nil
}

// synthesisArgs render the synthetic segments into one x264 file.
func synthesisArgs(
	dst string,
	seconds int,
) []string {
	args := []string{"-v", "error", "-nostdin", "-y"}

	var graph strings.Builder

	for i, s := range segments {
		args = append(args, "-f", "lavfi", "-t", strconv.Itoa(seconds), "-i", s.graph())
		fmt.Fprintf(&graph, "[%d:v]format=yuv420p,setsar=1[s%d];", i, i)
	}

	for i := range segments {
		fmt.Fprintf(&graph, "[s%d]", i)
	}

	fmt.Fprintf(&graph, "concat=n=%d:v=1:a=0[out]", len(segments))

	return append(args, "-filter_complex", graph.String(), "-map", "[out]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "12", "-g", "50", dst)
}

// geometry reads the frame size of path.
func (v *validator) geometry(
	ctx context.Context,
	path string,
) (int, int, error) {
	out, err := v.run.run(ctx, v.opts.ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", path)
	if err != nil {
		return 0, 0, fmt.Errorf("gpuval: probe %s: %w", path, err)
	}

	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d,%d", &w, &h); err != nil {
		return 0, 0, fmt.Errorf("gpuval: probe %s: %q: %w", path, out, err)
	}

	return w, h, nil
}
