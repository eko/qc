package ladder

// Memory of the ffmpeg processes of a build, per pixel of the source,
// measured on H.264 builds at 1080p in the Linux image (docs/install.md):
// about 500 MiB whatever the parallelism (the digest, its decoders), and
// 620 MiB more per probe run at once (its encode and the decodes of its
// measurement).
const (
	toolBytesPerPixel  = 250
	probeBytesPerPixel = 300
	// pieceBytesPerPixel is what each process cutting a segment of a fast
	// digest holds (encode.DigestSpec.Fast): 258 MiB at 1080p.
	pieceBytesPerPixel = 130
	// toolShare is the share of Options.Memory left to those processes;
	// the rest is the measurements' (quality.WithMemory) and a margin.
	toolShareNum, toolShareDen = 1, 2
)

// fitParallel is the number of probes run at once within memory bytes for
// a source of pixels pixels: at most parallel, at least one. Without a
// memory limit, parallel.
func fitParallel(
	parallel int,
	memory int64,
	pixels int,
) int {
	if memory <= 0 || pixels <= 0 {
		return parallel
	}

	room := memory*toolShareNum/toolShareDen - int64(pixels)*toolBytesPerPixel

	return max(1, min(parallel, int(room/(int64(pixels)*probeBytesPerPixel))))
}

// fitWorkers is the number of segments of a fast digest cut at once
// within memory bytes for a source of pixels pixels: at most workers, at
// least one. Without a memory limit, workers.
func fitWorkers(
	workers int,
	memory int64,
	pixels int,
) int {
	if memory <= 0 || pixels <= 0 {
		return workers
	}

	room := memory * toolShareNum / toolShareDen

	return max(1, min(workers, int(room/(int64(pixels)*pieceBytesPerPixel))))
}
