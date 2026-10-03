package sample

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

// Result describes an extracted sample.
type Result struct {
	SchemaVersion int       `json:"schemaVersion"`
	GeneratedAt   time.Time `json:"generatedAt"`
	// Path is the sample written.
	Path   string `json:"path"`
	Scenes Scenes `json:"scenes"`
	// TopShare is the share of the most complex scenes asked of a mixed
	// sample.
	TopShare float64 `json:"topShare,omitempty"`
	// Asked is the length asked for, Duration what the whole GOPs taken
	// make.
	Asked    media.Duration `json:"asked"`
	Duration media.Duration `json:"duration"`
	Frames   int            `json:"frames"`
	// Sources are the videos, in the order given, each with its scenes in
	// its own order.
	Sources []SourceResult `json:"sources"`
	// Order lists the scenes as they follow each other in the sample: the
	// most complex first for ScenesTop, the easiest first for ScenesEasy,
	// the complex ones then the representative ones for ScenesMixed, and
	// the order of the videos for ScenesAverage.
	Order []Scene `json:"order"`
	// Check is the sample read back.
	Check   Check          `json:"check"`
	Elapsed media.Duration `json:"elapsed"`
}

// SourceResult is what the sample took of one video.
type SourceResult struct {
	Path     string         `json:"path"`
	Duration media.Duration `json:"duration"`
	// Whole is set for a video no longer than its share, taken as it is
	// and not analysed.
	Whole    bool      `json:"whole,omitempty"`
	Segments []Segment `json:"segments"`
	// Taken and Frames are the length and frame count of the segments.
	Taken  media.Duration `json:"taken"`
	Frames int            `json:"frames"`
	// Complexity compares the frames taken with those of the video, nil
	// for a video taken whole.
	Complexity *Complexity `json:"complexity,omitempty"`
}

// Segment is a scene of a video copied into the sample: whole GOPs, from
// its first frame's time to the next keyframe's.
type Segment struct {
	media.Interval
	Frames int `json:"frames"`
	// SI and TI are the mean spatial and temporal information of its
	// frames (0 for a video taken whole).
	SI float64 `json:"si,omitempty"`
	TI float64 `json:"ti,omitempty"`
	// Kind tells why the scene was taken: ScenesTop, ScenesAverage or
	// ScenesEasy; empty for a video taken whole.
	Kind Scenes `json:"kind,omitempty"`
}

// Scene is a scene of the sample: a segment of one of its sources.
type Scene struct {
	// Source is the index of its video in Result.Sources.
	Source int `json:"source"`
	Segment
}

// Score is what the scene costs an encoder, relative to the others: its
// spatial times its temporal information.
func (s Segment) Score() float64 {
	return s.SI * s.TI
}

// order lists the scenes of every source as the sample plays them. Scenes
// of a video taken whole have no score: they keep the place of their video
// among the representative scenes, after the ranked ones.
func (r *Result) order() {
	r.Order = r.Order[:0]

	for i, s := range r.Sources {
		for _, seg := range s.Segments {
			r.Order = append(r.Order, Scene{Source: i, Segment: seg})
		}
	}

	// rank puts the ranked scenes first: 0 for them, 1 for the others,
	// which stay in the order of the videos.
	rank := func(s Scene) int {
		if s.Kind == ScenesTop || s.Kind == ScenesEasy {
			return 0
		}

		return 1
	}

	slices.SortStableFunc(r.Order, func(a, b Scene) int {
		if d := rank(a) - rank(b); d != 0 || rank(a) == 1 {
			return d
		}

		if a.Kind == ScenesEasy {
			return cmp.Compare(a.Score(), b.Score())
		}

		return cmp.Compare(b.Score(), a.Score())
	})
}

// Complexity is the spatial and temporal information of the frames taken
// of a video, next to the video's.
type Complexity struct {
	SI      float64 `json:"si"`
	TI      float64 `json:"ti"`
	VideoSI float64 `json:"videoSi"`
	VideoTI float64 `json:"videoTi"`
}

// Check is the sample read back once written: its frame count, that no two
// frames share a timestamp, and that every frame decodes.
type Check struct {
	// Frames is the frame count of the file, to compare with
	// Result.Frames: a stream whose GOPs are not closed loses the frames
	// displayed before a keyframe but stored after it.
	Frames int `json:"frames"`
	// Decoded is set when every frame decoded without an error.
	Decoded bool `json:"decoded"`
	// Note says what went wrong, "" when the sample is as planned.
	Note string `json:"note,omitempty"`
}

// OK reports whether the sample is as planned.
func (c Check) OK() bool {
	return c.Note == ""
}

// summarise fills what the segments of a video add up to.
func (s *SourceResult) summarise() {
	var si, ti float64

	for _, seg := range s.Segments {
		s.Taken += seg.Length()
		s.Frames += seg.Frames
		si += seg.SI * float64(seg.Frames)
		ti += seg.TI * float64(seg.Frames)
	}

	if s.Complexity != nil && s.Frames > 0 {
		s.Complexity.SI, s.Complexity.TI = si/float64(s.Frames), ti/float64(s.Frames)
	}
}

// write copies the scenes into the sample and reads it back.
func (e *Engine) write(
	ctx context.Context,
	videos []source,
	opts Options,
	res *Result,
) error {
	spec := encode.CopySpec{
		Destination: res.Path,
		Codec:       videos[0].video.Codec,
		Rate:        videos[0].video.AvgFrameRate,
	}

	// One part per scene, in the order of the sample: a video comes back
	// as often as its scenes are ranked among the others'.
	for _, scene := range res.Order {
		v := videos[scene.Source]
		spec.Parts = append(spec.Parts, encode.CopyPart{
			Source: v.path, Origin: origin(v),
			Segments: []encode.CopySegment{{Start: scene.Start, Frames: scene.Frames, Duration: scene.Length()}},
		})
		spec.Segments++
	}

	spec.Progress = func(done int) {
		opts.report(Progress{Stage: StageExtract, Done: done, Total: spec.Segments})
	}

	if err := e.cutter.Copy(ctx, spec); err != nil {
		return fmt.Errorf("sample: %w", err)
	}

	opts.report(Progress{Stage: StageVerify})

	return e.check(ctx, res)
}

// origin is the presentation time of the first frame of a video on its
// container's timeline: scenes are times of the video, seeked from there.
func origin(
	v source,
) media.Duration {
	if v.inspection.Bitstream != nil {
		return v.inspection.Bitstream.Start
	}

	return v.video.StartTime
}

// sharedTimestamp returns the first presentation time two frames of an
// inspected file share: scenes joined with the wrong durations overlap.
func sharedTimestamp(
	inspection *analysis.Report,
) (media.Duration, bool) {
	if inspection.Bitstream == nil {
		return 0, false
	}

	pts := inspection.Bitstream.PTS
	for i := 1; i < len(pts); i++ {
		if pts[i] <= pts[i-1] {
			return pts[i], true
		}
	}

	return 0, false
}

// check reads the sample back: its frame count, its timestamps, and a
// decode of every frame. What it finds wrong is told in the result, not returned: the file
// is there, and the caller decides.
func (e *Engine) check(
	ctx context.Context,
	res *Result,
) error {
	inspection, err := e.inspector.Analyze(ctx, res.Path, analysis.Options{SkipVideo: true})
	if err != nil {
		return fmt.Errorf("sample: read %s back: %w", res.Path, err)
	}

	if inspection.Bitstream != nil {
		res.Check.Frames = inspection.Bitstream.PacketCount
	}

	if res.Check.Frames != res.Frames {
		res.Check.Note = fmt.Sprintf("the sample holds %d frames where its scenes have %d: the sources may have open GOPs, "+
			"whose frames shown before a keyframe are stored after it", res.Check.Frames, res.Frames)

		return nil
	}

	if at, ok := sharedTimestamp(inspection); ok {
		res.Check.Note = fmt.Sprintf("two frames of the sample are shown at %.3f s: a player drops one of them", at.Seconds())

		return nil
	}

	if err := e.cutter.Decodes(ctx, res.Path); err != nil {
		res.Check.Note = "the sample does not decode cleanly: " + err.Error()

		return nil
	}

	res.Check.Decoded = true

	return nil
}
