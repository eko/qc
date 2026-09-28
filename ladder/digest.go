package ladder

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/eko/qc/analysis"
	"github.com/eko/qc/encode"
	"github.com/eko/qc/media"
)

// maxRawDigestBytes switches the digest to lossless compression above it:
// raw video costs nothing to decode, but a long 4K digest would not fit on
// most temporary disks.
const maxRawDigestBytes = 4 << 30

// samplesPerPixel420 is the number of samples per pixel in 4:2:0 (one luma
// plus two quarter-size chroma planes).
const samplesPerPixel420 = 1.5

// makeDigest extracts the digest of the source into the work directory and
// inspects it once, so that every measurement reuses that inspection.
func (b *build) makeDigest(
	ctx context.Context,
	duration media.Duration,
) (Digest, error) {
	segments := digestSegments(duration, b.opts.SegmentDuration, b.opts.DigestDuration)

	var total media.Duration
	for _, s := range segments {
		total += s.Length()
	}

	depth := digestBitDepth(b.video)
	lossless := rawDigestBytes(b.video, depth, total) > maxRawDigestBytes

	path := filepath.Join(b.workDir, "digest.nut")
	if lossless {
		path = filepath.Join(b.workDir, "digest.mkv")
	}

	spec := encode.DigestSpec{
		Source: b.source, Destination: path, Segments: segments,
		Rate: b.video.AvgFrameRate, BitDepth: depth, Lossless: lossless, Origin: b.origin,
	}

	if err := b.engine.digester.Digest(ctx, spec); err != nil {
		return Digest{}, fmt.Errorf("ladder: %w", err)
	}

	report, err := b.engine.inspector.Analyze(ctx, path, analysis.Options{SkipVideo: true})
	if err != nil {
		return Digest{}, fmt.Errorf("ladder: inspect digest: %w", err)
	}

	report = withSignal(report, b.video)
	b.digest, b.digestReport = path, report
	b.reference, b.referenceReport = path, report

	return Digest{
		Segments: segments,
		Duration: total,
		Share:    total.Seconds() / max(duration.Seconds(), 1e-9),
	}, nil
}

// digestSource is the digest as the source of chunked encodes: its
// timestamps start at 0, at the source's frame rate.
func (b *build) digestSource() encode.ChunkSource {
	return encode.ChunkSource{Path: b.digest, Rate: b.video.AvgFrameRate}
}

// videoOrigin is the presentation time of the first frame of video on the
// container's timeline: the first presentation time of the bitstream, as
// decoding seeks from (decode.Request.Origin), or the stream's start when
// the bitstream was not read.
func videoOrigin(
	report *analysis.Report,
	video media.VideoStream,
) media.Duration {
	if report.Bitstream != nil {
		return report.Bitstream.Start
	}

	return video.StartTime
}

// digestBitDepth keeps the source depth (8 or 10 bits), so that 10-bit
// sources are measured at 10 bits.
func digestBitDepth(
	video media.VideoStream,
) int {
	if video.BitDepth > 8 {
		return 10
	}

	return 8
}

// rawDigestBytes is the size of the digest as raw 4:2:0 video. Samples above
// 8 bits take two bytes.
func rawDigestBytes(
	video media.VideoStream,
	depth int,
	duration media.Duration,
) float64 {
	bytesPerSample := 1.0
	if depth > 8 {
		bytesPerSample = 2
	}

	frames := duration.Seconds() * video.AvgFrameRate.Float()

	return float64(video.Width*video.Height) * samplesPerPixel420 * bytesPerSample * frames
}

// digestSegments spreads segments evenly over the title: systematic sampling
// keeps every part of the title represented. A title no longer than budget
// is used whole.
func digestSegments(
	duration, segment, budget media.Duration,
) []media.Interval {
	if duration <= budget {
		return []media.Interval{{Start: 0, End: duration}}
	}

	count := max(1, int(budget/segment))
	step := duration / media.Duration(count)
	out := make([]media.Interval, count)

	// Each segment is centred in its share of the title.
	for i := range out {
		start := media.Duration(i)*step + (step-segment)/2
		out[i] = media.Interval{Start: start, End: start + segment}
	}

	return out
}
