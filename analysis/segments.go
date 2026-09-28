package analysis

import (
	"github.com/eko/qc/bitstream"
	"github.com/eko/qc/decode"
)

const (
	// segmentsPerDecoder splits the video into more segments than
	// concurrent decoders: segments decode at different speeds (contents
	// of varying cost, a decode falling back to the CPU), and smaller last
	// segments shorten the tail where only a few are still running.
	segmentsPerDecoder = 3
	// minSegmentFrames bounds how short a segment gets: each one starts a
	// decoder process and decodes up to one GOP it does not output (it
	// seeks to the keyframe before its first frame).
	minSegmentFrames = 500
)

// segmentBounds returns the first frames of the segments of a video of
// frames frames, split for decoders concurrent decoders at keyframes
// (keyframe[i] tells whether frame i, in presentation order, is one): the
// first bound is 0, every other one a keyframe. A video too short to be
// split, or without keyframes to split at, is one segment.
func segmentBounds(
	keyframe []bool,
	decoders int,
) []int {
	count := min(decoders*segmentsPerDecoder, len(keyframe)/minSegmentFrames)

	return bitstream.SplitAtKeyframes(keyframe, count, minSegmentFrames/2)
}

// segmentRequests returns the decoding requests of the segments starting
// at bounds, from base (the request decoding the whole video): segment k
// seeks to its first frame and outputs one frame more than it holds, the
// first frame of segment k+1 (see analyze.Forker); the last one decodes to
// the end.
func segmentRequests(
	base decode.Request,
	bounds []int,
	bs *bitstream.Report,
	threads int,
) []decode.Request {
	reqs := make([]decode.Request, len(bounds))

	for k, first := range bounds {
		req := base
		req.FirstIndex, req.Threads, req.Segment = first, threads, true

		if first > 0 {
			req.Start = bs.PTS[first]
		}

		if k+1 < len(bounds) {
			req.MaxFrames = bounds[k+1] - first + 1
		}

		reqs[k] = req
	}

	return reqs
}
