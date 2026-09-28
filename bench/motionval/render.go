package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Texture geometry: a 16:9 still, about 2.9× the clip width, leaving room
// for long pans, tilts and zooms at one texture pixel per output pixel.
const (
	textureWidth  = 5496
	textureHeight = 3091
)

// renderer writes the synthetic clips with ffmpeg.
type renderer struct {
	ffmpeg  string
	texture string
	dir     string
}

// prepareTexture scales and crops src to the texture geometry, in grey.
func (r renderer) prepareTexture(
	ctx context.Context,
	src string,
) (string, error) {
	out := filepath.Join(r.dir, "texture.png")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos,crop=%d:%d", textureWidth, textureHeight, textureWidth, textureHeight)

	return out, r.run(ctx, "-i", src, "-vf", vf, "-frames:v", "1", out)
}

// render writes c (cached by name) and returns its path.
func (r renderer) render(
	ctx context.Context,
	c clip,
) (string, error) {
	out := filepath.Join(r.dir, c.name+".mp4")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}

	err := r.run(ctx, "-i", r.texture, "-filter_complex", graph(c),
		"-map", "[out]", "-frames:v", strconv.Itoa(c.frames),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "18", out)
	if err != nil {
		// A partial clip must not be cached.
		_ = os.Remove(out)
	}

	return out, err
}

func (r renderer) run(
	ctx context.Context,
	args ...string,
) error {
	cmd := exec.CommandContext(ctx, r.ffmpeg, append([]string{"-v", "error", "-y"}, args...)...)

	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(msg)))
	}

	return nil
}

// graph is the filtergraph rendering c from the texture (input 0): a
// zoompan window following the camera path, an optional moving layer, then
// the post filters.
func graph(
	c clip,
) string {
	fps := strconv.Itoa(c.fps)
	t := "(on/" + fps + ")"
	x, y, m := c.path.expressions(t)

	if c.cut > 0 {
		x2, y2, m2 := c.second.expressions(t)
		cond := fmt.Sprintf("lt(%s,%g)", t, c.cut)
		x, y, m = "if("+cond+","+x+","+x2+")", "if("+cond+","+y+","+y2+")", "if("+cond+","+m+","+m2+")"
	}

	base := float64(textureWidth) / clipWidth
	zoompan := fmt.Sprintf("zoompan=z='%g*%s':x='%s-iw/zoom/2':y='%s-ih/zoom/2':d=1:s=%dx%d:fps=%s",
		base, m, x, y, clipWidth, clipHeight, fps)

	source := "[0:v]loop=loop=-1:size=1:start=0,setpts=N/" + fps + "/TB,format=gray"
	parts := []string{source + "[tex]"}

	last := "[bg]"
	if l := c.layer; l.w > 0 {
		parts = []string{source + ",split=2[tex][src]", "[src]" + l.crop() + "[fg]", last + "[fg]" + l.overlay() + "[comp]"}
		last = "[comp]"
	}

	parts = append(parts, "[tex]"+zoompan+"[bg]")

	post := "format=yuv420p"
	if c.post != "" {
		post = c.post + "," + post
	}

	return strings.Join(append(parts, last+post+"[out]"), ";")
}

// crop cuts the layer out of the texture, scrolling its content when it
// is a plane rather than an object.
func (l layer) crop() string {
	if l.scroll {
		return fmt.Sprintf("crop=%g:%g:x='%g+%g*t':y=%g", l.w, l.h, l.sx, l.vx, l.sy)
	}

	return fmt.Sprintf("crop=%g:%g:%g:%g", l.w, l.h, l.sx, l.sy)
}

// overlay places the layer, moving it when it is an object.
func (l layer) overlay() string {
	if l.scroll {
		return fmt.Sprintf("overlay=x=%g:y=%g", l.x, l.y)
	}

	return fmt.Sprintf("overlay=x='%g+%g*t':y=%g", l.x, l.vx, l.y)
}
