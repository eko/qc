// Package bitstream analyses compressed packets without decoding them: bitrate
// over time, peaks and GOP structure.
package bitstream

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/eko/qc/internal/stats"
	"github.com/eko/qc/media"
)

// defaultWindow is the default bucket and peak window: bitrates are
// conventionally reported per second.
const defaultWindow = time.Second

// Options tunes the bitstream analysis. The zero value is valid.
type Options struct {
	// Interval is the bucket size of the bitrate series (default 1s).
	Interval time.Duration
	// PeakWindow is the sliding window used for the peak bitrate (default 1s).
	PeakWindow time.Duration
}

// Report is the result of a bitstream analysis.
type Report struct {
	PacketCount int            `json:"packetCount"`
	Duration    media.Duration `json:"duration"`
	// Start is the presentation time of the first frame on the container's
	// timeline: ffmpeg shifts it by the container's start time, which
	// timelines built on PTS (relative to the first frame) need to match
	// what ffmpeg filters see.
	Start          media.Duration   `json:"start"`
	TotalBytes     int64            `json:"totalBytes"`
	AverageBitrate int64            `json:"averageBitrate"`
	PeakBitrate    int64            `json:"peakBitrate"`
	PeakAt         media.Duration   `json:"peakAt"`
	PeakWindow     media.Duration   `json:"peakWindow"`
	PeakToAverage  float64          `json:"peakToAverage"`
	FrameSize      SizeStats        `json:"frameSize"`
	GOP            GOPStats         `json:"gop"`
	Interval       media.Duration   `json:"interval"`
	Bitrate        []BitratePoint   `json:"bitrate"`
	Keyframes      []media.Duration `json:"keyframes"`
	// Per-frame columns in presentation order. PTS is relative to the first
	// frame; decoded-frame analyzers use it to timestamp frames.
	PTS        []media.Duration `json:"-"`
	FrameSizes []int            `json:"-"`
	KeyFlags   []bool           `json:"-"`
}

// SizeStats summarises packet sizes in bytes.
type SizeStats struct {
	Min  int     `json:"min"`
	Max  int     `json:"max"`
	Mean float64 `json:"mean"`
	P50  int     `json:"p50"`
	P95  int     `json:"p95"`
}

// GOPStats summarises the distance between consecutive keyframes.
type GOPStats struct {
	KeyframeCount int            `json:"keyframeCount"`
	MinInterval   media.Duration `json:"minInterval"`
	MaxInterval   media.Duration `json:"maxInterval"`
	MeanInterval  media.Duration `json:"meanInterval"`
	// Fixed reports whether every interval is within one and a half mean
	// frame durations of the mean interval.
	Fixed bool `json:"fixed"`
}

// BitratePoint is the bitrate of one bucket of the series.
type BitratePoint struct {
	Start   media.Duration `json:"start"`
	Bitrate int64          `json:"bitrate"`
}

// Analyze computes a Report from packets given in any order.
func Analyze(
	packets []media.Packet,
	opts Options,
) Report {
	opts = opts.withDefaults()

	report := Report{
		PacketCount: len(packets),
		Interval:    media.Duration(opts.Interval),
		PeakWindow:  media.Duration(opts.PeakWindow),
	}
	if len(packets) == 0 {
		return report
	}

	sorted := slices.Clone(packets)
	slices.SortFunc(sorted, func(a, b media.Packet) int {
		return cmp.Compare(a.PTS, b.PTS)
	})

	start := sorted[0].PTS
	report.Start = media.Duration(start)
	last := sorted[len(sorted)-1]
	duration := last.PTS + last.Duration - start
	report.Duration = media.Duration(duration)

	for _, pkt := range sorted {
		report.TotalBytes += int64(pkt.Size)
	}

	report.AverageBitrate = bitrate(report.TotalBytes, duration)
	report.FrameSize = sizeStats(sorted)
	report.Bitrate = bitrateSeries(sorted, start, duration, opts.Interval)

	peak, peakAt := peakBitrate(sorted, opts.PeakWindow)
	report.PeakBitrate, report.PeakAt = peak, media.Duration(peakAt)

	if report.AverageBitrate > 0 {
		report.PeakToAverage = float64(report.PeakBitrate) / float64(report.AverageBitrate)
	}

	keys := keyframes(sorted)
	report.GOP = gopStats(keys, duration/time.Duration(len(sorted)))

	report.Keyframes = make([]media.Duration, len(keys))
	for i, k := range keys {
		report.Keyframes[i] = media.Duration(k)
	}

	report.PTS = make([]media.Duration, len(sorted))
	report.FrameSizes = make([]int, len(sorted))
	report.KeyFlags = make([]bool, len(sorted))

	for i, pkt := range sorted {
		report.PTS[i] = media.Duration(pkt.PTS - start)
		report.FrameSizes[i] = pkt.Size
		report.KeyFlags[i] = pkt.Keyframe
	}

	return report
}

// withDefaults replaces unset or invalid durations with defaultWindow.
func (o Options) withDefaults() Options {
	if o.Interval <= 0 {
		o.Interval = defaultWindow
	}

	if o.PeakWindow <= 0 {
		o.PeakWindow = defaultWindow
	}

	return o
}

// bitrate returns the bits per second of bytes spread over d, or 0 for an
// empty duration.
func bitrate(
	bytes int64,
	d time.Duration,
) int64 {
	if d <= 0 {
		return 0
	}

	return int64(math.Round(float64(bytes*8) / d.Seconds()))
}

// sizeStats summarises the sizes of a non-empty packet list.
func sizeStats(
	packets []media.Packet,
) SizeStats {
	sizes := make([]int, len(packets))

	var total int64

	for i, pkt := range packets {
		sizes[i] = pkt.Size
		total += int64(pkt.Size)
	}

	slices.Sort(sizes)

	return SizeStats{
		Min:  sizes[0],
		Max:  sizes[len(sizes)-1],
		Mean: float64(total) / float64(len(sizes)),
		P50:  stats.Percentile(sizes, 0.50),
		P95:  stats.Percentile(sizes, 0.95),
	}
}

// bitrateSeries buckets packets by presentation time. The last bucket is
// normalised by its real length so a partial second is not under-reported.
func bitrateSeries(
	packets []media.Packet,
	start time.Duration,
	duration time.Duration,
	interval time.Duration,
) []BitratePoint {
	buckets := int((duration + interval - 1) / interval)
	bytesPerBucket := make([]int64, max(buckets, 1))

	for _, pkt := range packets {
		idx := min(int((pkt.PTS-start)/interval), len(bytesPerBucket)-1)
		bytesPerBucket[idx] += int64(pkt.Size)
	}

	series := make([]BitratePoint, len(bytesPerBucket))

	for i, b := range bytesPerBucket {
		bucketStart := time.Duration(i) * interval
		length := min(interval, duration-bucketStart)

		series[i] = BitratePoint{
			Start:   media.Duration(bucketStart),
			Bitrate: bitrate(b, length),
		}
	}

	return series
}

// peakBitrate finds the maximum bitrate over a sliding window anchored on each
// packet, using two pointers over packets sorted by PTS.
func peakBitrate(
	packets []media.Packet,
	window time.Duration,
) (int64, time.Duration) {
	var (
		sum, best int64
		bestAt    time.Duration
		end       int
	)

	for i, pkt := range packets {
		for end < len(packets) && packets[end].PTS < pkt.PTS+window {
			sum += int64(packets[end].Size)
			end++
		}

		if sum > best {
			best = sum
			bestAt = pkt.PTS - packets[0].PTS
		}

		sum -= int64(packets[i].Size)
	}

	return bitrate(best, window), bestAt
}

// keyframes returns the keyframe times relative to the first packet of a
// non-empty list sorted by PTS.
func keyframes(
	packets []media.Packet,
) []time.Duration {
	start := packets[0].PTS

	var out []time.Duration

	for _, pkt := range packets {
		if pkt.Keyframe {
			out = append(out, pkt.PTS-start)
		}
	}

	return out
}

// gopStats measures the intervals between keyframes; frameDuration sets the
// tolerance of the fixed GOP detection.
func gopStats(
	keyframes []time.Duration,
	frameDuration time.Duration,
) GOPStats {
	gop := GOPStats{KeyframeCount: len(keyframes)}
	if len(keyframes) < 2 {
		return gop
	}

	minInterval, maxInterval := time.Duration(math.MaxInt64), time.Duration(0)

	for i := 1; i < len(keyframes); i++ {
		interval := keyframes[i] - keyframes[i-1]
		minInterval = min(minInterval, interval)
		maxInterval = max(maxInterval, interval)
	}

	intervals := len(keyframes) - 1
	meanInterval := (keyframes[len(keyframes)-1] - keyframes[0]) / time.Duration(intervals)

	// One and a half frames absorbs timestamp rounding and the odd frame of
	// jitter without accepting a genuinely irregular GOP.
	tolerance := frameDuration + frameDuration/2
	gop.Fixed = maxInterval-meanInterval <= tolerance && meanInterval-minInterval <= tolerance
	gop.MinInterval = media.Duration(minInterval)
	gop.MaxInterval = media.Duration(maxInterval)
	gop.MeanInterval = media.Duration(meanInterval)

	return gop
}
