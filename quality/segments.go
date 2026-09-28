package quality

import (
	"runtime"

	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
)

// Segmented decoding. A hardware decoding session (VideoToolbox) decodes
// slower than ffmpeg's multithreaded CPU decoder but uses almost no CPU,
// and concurrent sessions add up: when the decoder says so
// (decode.SegmentDecoders), every pass is decoded by concurrent runs, and
// libvmaf gets the CPU the decoders no longer use. An exact measurement is
// then scored in segments, one libvmaf context each.
const (
	// segmentWorkers is how many segments of an exact measurement are
	// scored at once, each on its own libvmaf context with an equal share
	// of the CPUs as threads. libvmaf scales well with threads: at 1080p,
	// one to four segments at once score alike. Three decode in three
	// sessions per side, which keeps decoding ahead of scoring when a
	// session is slower than libvmaf (a 4K reference scored at 1080p).
	segmentWorkers = 3
	// runsPerWorker splits a pass into more runs than decode at once: runs
	// decode at different speeds (contents of varying cost, a session
	// falling back to the CPU), and shorter last runs shorten the tail
	// where only a few are still running.
	runsPerWorker = 3
	// minRunFrames bounds how short a segment or split sweep gets: each
	// run starts two decoders and decodes up to two GOPs before its first
	// frame (see seekPoints).
	minRunFrames = 500
	// segmentLead is the number of warm-up frames before a segment. Frames
	// scored against their predecessors need one: libvmaf's motion (the
	// next frame comes from the trailing warm-up frame) and XPSNR below
	// 32 fps. Above 32 fps, XPSNR's second-order temporal difference needs
	// two, so that every frame of a segment scores bit-exactly as in a
	// single pass.
	segmentLead = 2
)

// segmentDecoders is how many decodes of the videos are worth running at
// once (decode.SegmentDecoders, 1 when the decoder cannot tell): above 1,
// they are hardware sessions.
func segmentDecoders(
	src decode.Source,
	inputs ...Input,
) int {
	sd, ok := src.(decode.SegmentDecoders)
	if !ok {
		return 1
	}

	decoders := 1
	for _, in := range inputs {
		decoders = max(decoders, sd.SegmentDecoders(in.request()))
	}

	return decoders
}

// exactPlan returns the clips of an exact measurement, how many of them are
// scored at once and with how many libvmaf threads each: the whole video
// on a single context using every CPU, or, with hardware decoding, its
// segments (see segmentClips).
func (r *run) exactPlan(
	st *stratum,
) ([]clip, int, int) {
	cpus := runtime.NumCPU()
	whole := []clip{{stratum: st, from: 0, to: r.n}}

	if r.decoders <= 1 {
		return whole, 1, cpus
	}

	workers := min(segmentWorkers, cpus)

	clips := r.segmentClips(st, workers)
	if len(clips) == 1 {
		return whole, 1, cpus
	}

	r.segments = true

	return clips, workers, max(1, cpus/workers)
}

// segmentClips splits the video into clips of about equal length starting
// at keyframes of the distorted video, where its decoder starts cleanly,
// runsPerWorker per concurrent worker and at least minRunFrames each.
func (r *run) segmentClips(
	st *stratum,
	workers int,
) []clip {
	keyframe := make([]bool, r.n)
	for _, k := range framesAt(r.ref.Bitstream.PTS, r.dist.Bitstream.Keyframes) {
		if k < r.n {
			keyframe[k] = true
		}
	}

	count := min(workers*runsPerWorker, r.n/minRunFrames)
	bounds := append(bitstream.SplitAtKeyframes(keyframe, count, minRunFrames/2), r.n)

	clips := make([]clip, len(bounds)-1)
	for i := range clips {
		clips[i] = clip{stratum: st, from: bounds[i], to: bounds[i+1]}
	}

	return clips
}

// mergeSegments joins the results of consecutive segments, in order, into
// the result of a single clip spanning them.
func mergeSegments(
	results []clipResult,
) clipResult {
	if len(results) == 1 {
		return results[0]
	}

	merged := clipResult{clip: results[0].clip, values: map[string][]float64{}}

	for _, cr := range results {
		merged.clip.to = cr.clip.to
		merged.scores = append(merged.scores, cr.scores...)
		merged.decoded += cr.decoded

		for name, values := range cr.values {
			merged.values[name] = append(merged.values[name], values...)
		}
	}

	return merged
}

// warmUp is the number of warm-up frames before a clip.
func (r *run) warmUp() int {
	if r.segments {
		return segmentLead
	}

	return warmUpFrames
}
