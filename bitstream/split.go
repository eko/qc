package bitstream

// SplitAtKeyframes returns the first frames of up to count runs of
// consecutive frames of about equal length, split at keyframes
// (keyframe[i] tells whether frame i, in presentation order, is one): the
// first bound is 0, every other one a keyframe, at least minGap frames
// after the previous bound and before the end. Work spread over concurrent
// processes (decodes, renders) starts each run at a keyframe, where a
// decoder can start. A video without keyframes to split at is one run.
func SplitAtKeyframes(
	keyframe []bool,
	count, minGap int,
) []int {
	frames := len(keyframe)
	bounds := []int{0}

	for i := 1; i < count; i++ {
		// The first keyframe at or after the ideal bound, if it is still
		// far enough from the previous bound and from the end.
		k := i * frames / count
		for k < frames && !keyframe[k] {
			k++
		}

		if k-bounds[len(bounds)-1] >= minGap && frames-k >= minGap {
			bounds = append(bounds, k)
		}
	}

	return bounds
}
