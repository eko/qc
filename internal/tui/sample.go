package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/eko/qc/sample"
)

// sceneLabels say in a few words which scenes a sample takes.
var sceneLabels = map[sample.Scenes]string{
	sample.ScenesTop:     "the most complex scenes",
	sample.ScenesMixed:   "the most complex scenes and representative ones",
	sample.ScenesAverage: "representative scenes",
	sample.ScenesEasy:    "the easiest scenes",
}

// kindLabels name why a scene was taken.
var kindLabels = map[sample.Scenes]string{
	sample.ScenesTop:     "complex",
	sample.ScenesAverage: "representative",
	sample.ScenesEasy:    "easy",
}

// Sample table column widths.
const (
	colSampleVideo = 30
	colSampleNum   = 9
)

// SampleSummary sums a sample up in a line, for the dashboard.
func SampleSummary(
	res *sample.Result,
) string {
	return fmt.Sprintf("%s · %d scenes · %d frames", Clock(res.Duration, false), sceneCount(res), res.Frames)
}

// sceneCount is the number of scenes of the sample.
func sceneCount(
	res *sample.Result,
) int {
	n := 0
	for _, s := range res.Sources {
		n += len(s.Segments)
	}

	return n
}

// RenderSample prints what a sample took of every video, and the check of
// the file written.
func RenderSample(
	w io.Writer,
	res *sample.Result,
) error {
	blocks := []string{
		title.Render("◆ qc") + Subtle.Render("  ·  sample"),
		sampleHeader(res),
		sampleTable(res),
		sampleScenes(res),
		section.Render("Findings") + "\n" + sampleFindings(res),
		timingsLine("total " + Elapsed(res.Elapsed.Std())),
	}

	_, err := fmt.Fprintln(w, strings.Join(blocks, "\n\n"))
	if err != nil {
		return fmt.Errorf("write sample report: %w", err)
	}

	return nil
}

// sampleHeader tells the file written and the scenes asked for.
func sampleHeader(
	res *sample.Result,
) string {
	scenes := string(res.Scenes) + ": " + sceneLabels[res.Scenes]
	if res.Scenes == sample.ScenesMixed {
		scenes += fmt.Sprintf(" (%.0f%% complex asked)", res.TopShare*100)
	}

	return key.Render("sample") + Bold.Render(res.Path) +
		Subtle.Render(fmt.Sprintf(" · %s for %s asked · %d scenes · %d frames", Clock(res.Duration, false), Clock(res.Asked, false), sceneCount(res), res.Frames)) + "\n" +
		key.Render("scenes") + Subtle.Render(scenes) + "\n" +
		key.Render("copy") + Subtle.Render("whole GOPs of the sources, not re-encoded · video only")
}

// sampleTable lists what every video gave, and how its scenes compare
// with the video.
func sampleTable(
	res *sample.Result,
) string {
	head := fmt.Sprintf("  %-*s %*s %*s %*s %*s   %s", colSampleVideo, "video", colSampleNum, "duration", colSampleNum, "taken",
		colSampleNum, "scenes", colSampleNum, "SI", "TI  (the video: SI, TI)")
	lines := []string{section.Render("Videos"), Subtle.Render(head)}

	for _, s := range res.Sources {
		line := fmt.Sprintf("  %-*s %*s %*s %*d", colSampleVideo, truncate(filepath.Base(s.Path), colSampleVideo),
			colSampleNum, Clock(s.Duration, true), colSampleNum, Clock(s.Taken, true), colSampleNum, len(s.Segments))

		switch c := s.Complexity; {
		case s.Whole:
			line += Subtle.Render("   taken whole: no longer than its share")
		case c != nil:
			line += fmt.Sprintf(" %*.1f   %.1f  ", colSampleNum, c.SI, c.TI) + Subtle.Render(fmt.Sprintf("(%.1f, %.1f)", c.VideoSI, c.VideoTI))
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// maxSampleScenes is the number of scenes listed.
const maxSampleScenes = 30

// orderLabels say how the scenes of a sample follow each other.
var orderLabels = map[sample.Scenes]string{
	sample.ScenesTop:     "the most complex first",
	sample.ScenesMixed:   "the complex ones first, the most complex leading, then the representative ones",
	sample.ScenesAverage: "in the order of the videos",
	sample.ScenesEasy:    "the easiest first",
}

// sampleScenes lists the scenes in the order the sample plays them.
func sampleScenes(
	res *sample.Result,
) string {
	lines := []string{section.Render("Scenes") + Subtle.Render("  (as the sample plays them: "+orderLabels[res.Scenes]+")")}

	for i, scene := range res.Order {
		if i == maxSampleScenes {
			lines = append(lines, Subtle.Render(fmt.Sprintf("  %d more", len(res.Order)-maxSampleScenes)))

			break
		}

		source := res.Sources[scene.Source]
		line := fmt.Sprintf("  %2d  %-*s %s → %s  ", i+1, colSampleVideo, truncate(filepath.Base(source.Path), colSampleVideo),
			Clock(scene.Start, false), Clock(scene.End, false))

		if source.Whole {
			lines = append(lines, line+Subtle.Render(fmt.Sprintf("the whole video · %d frames", scene.Frames)))

			continue
		}

		lines = append(lines, line+fmt.Sprintf("%-16s", kindLabels[scene.Kind])+
			Subtle.Render(fmt.Sprintf("SI %.1f · TI %.1f · SI×TI %.0f · %d frames", scene.SI, scene.TI, scene.Score(), scene.Frames)))
	}

	return strings.Join(lines, "\n")
}

// sampleFindings tell whether the file is what was planned.
func sampleFindings(
	res *sample.Result,
) string {
	if !res.Check.OK() {
		return warnLine(res.Check.Note)
	}

	return okLine(fmt.Sprintf("sample read back: %d frames, every one decodes; they are the sources' own frames", res.Check.Frames))
}
