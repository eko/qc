// Package probe extracts container and stream level information from a media file.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eko/qc/internal/ffexec"
	"github.com/eko/qc/media"
)

// Prober reads stream information from a media file.
type Prober interface {
	Probe(
		ctx context.Context,
		path string,
	) (*media.Info, error)
}

// commandOutput runs a command and returns its stdout. It is ffexec.Output in
// production and a fake in tests.
type commandOutput func(
	ctx context.Context,
	bin string,
	args []string,
) ([]byte, error)

// FFprobe implements Prober using the ffprobe binary. It is safe for
// concurrent use.
type FFprobe struct {
	bin    string
	output commandOutput

	// mu guards firstFrames: the side data of the first frame of the HDR
	// files probed, by file version. A run inspects its source twice (the
	// inspection, then the frame analysis): the second probe reuses it.
	mu          sync.Mutex
	firstFrames map[fileVersion][]ffprobeSideData
}

// fileVersion identifies the content of a file: a path whose size or
// modification time changed is probed again.
type fileVersion struct {
	path    string
	size    int64
	modTime time.Time
}

// NewFFprobe returns a Prober backed by the given ffprobe binary.
func NewFFprobe(
	bin string,
) *FFprobe {
	return &FFprobe{bin: bin, output: ffexec.Output, firstFrames: map[fileVersion][]ffprobeSideData{}}
}

// Probe implements Prober: ProbeStreams, then ProbeHDR.
func (f *FFprobe) Probe(
	ctx context.Context,
	path string,
) (*media.Info, error) {
	info, err := f.ProbeStreams(ctx, path)
	if err != nil {
		return nil, err
	}

	if err := f.ProbeHDR(ctx, info); err != nil {
		return nil, err
	}

	return info, nil
}

// ProbeStreams reads the container and its streams, with the HDR metadata
// the container signals, without reading any frame.
func (f *FFprobe) ProbeStreams(
	ctx context.Context,
	path string,
) (*media.Info, error) {
	out, err := f.output(ctx, f.bin, []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	})
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", path, err)
	}

	info, err := Parse(out)
	if err != nil {
		return nil, fmt.Errorf("probe %s: %w", path, err)
	}

	info.Path = path

	return info, nil
}

// ProbeHDR completes the HDR description of the primary video of info from
// the side data of its first frame. Containers often carry HDR10 metadata
// only in the bitstream (HEVC and AV1 SEI/metadata OBUs in MP4), and HDR10+
// dynamic metadata never is at stream level. Only PQ, HLG and Dolby Vision
// streams pay for this second, one-frame call (about 65 ms, most of it
// ffprobe's start): SDR probing is unchanged. Callers with slower work to
// do (decoding the video) run it meanwhile.
func (f *FFprobe) ProbeHDR(
	ctx context.Context,
	info *media.Info,
) error {
	if len(info.Video) == 0 {
		return nil
	}

	video := &info.Video[0]
	if !video.Color.IsHDR() && video.HDR.DolbyVision == nil {
		return nil
	}

	sideData, err := f.firstFrame(ctx, info.Path, video.Index)
	if err != nil {
		return fmt.Errorf("probe %s: %w", info.Path, err)
	}

	addSideData(&video.HDR, sideData)
	video.HDR.DynamicRange = classify(video.HDR, video.Color.Transfer)

	return nil
}

// firstFrame returns the side data of the first frame of stream index of
// path, from the cache when this version of the file was probed before.
func (f *FFprobe) firstFrame(
	ctx context.Context,
	path string,
	index int,
) ([]ffprobeSideData, error) {
	key, cacheable := versionOf(path)
	if cacheable {
		f.mu.Lock()
		cached, ok := f.firstFrames[key]
		f.mu.Unlock()

		if ok {
			return cached, nil
		}
	}

	out, err := f.output(ctx, f.bin, []string{
		"-v", "error",
		"-print_format", "json",
		"-select_streams", strconv.Itoa(index),
		"-read_intervals", "%+#1",
		"-show_frames",
		path,
	})
	if err != nil {
		return nil, fmt.Errorf("first frame: %w", err)
	}

	var frames ffprobeFrames
	if err := json.Unmarshal(out, &frames); err != nil {
		return nil, fmt.Errorf("decode ffprobe frame json: %w", err)
	}

	var sideData []ffprobeSideData
	for _, fr := range frames.Frames {
		sideData = append(sideData, fr.SideDataList...)
	}

	if cacheable {
		f.mu.Lock()
		f.firstFrames[key] = sideData
		f.mu.Unlock()
	}

	return sideData, nil
}

// versionOf identifies the version of a local file; ok is false for what
// cannot be stat'ed (URLs, missing files), which are not cached.
func versionOf(
	path string,
) (fileVersion, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return fileVersion{}, false
	}

	return fileVersion{path: path, size: st.Size(), modTime: st.ModTime()}, true
}

// Parse converts ffprobe JSON output (-show_format -show_streams) into media.Info.
func Parse(
	data []byte,
) (*media.Info, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode ffprobe json: %w", err)
	}

	info := &media.Info{
		Format:    out.Format.FormatName,
		Duration:  seconds(out.Format.Duration),
		StartTime: seconds(out.Format.StartTime),
		Size:      integer(out.Format.Size),
		BitRate:   integer(out.Format.BitRate),
	}

	for _, s := range out.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition.AttachedPic == 1 {
				continue
			}

			video, err := parseVideo(s)
			if err != nil {
				return nil, fmt.Errorf("stream %d: %w", s.Index, err)
			}

			info.Video = append(info.Video, video)
		case "audio":
			info.Audio = append(info.Audio, parseAudio(s))
		}
	}

	return info, nil
}

// parseVideo converts a video stream; it only fails on malformed frame rates.
func parseVideo(
	s ffprobeStream,
) (media.VideoStream, error) {
	frameRate, err := media.ParseRational(s.RFrameRate)
	if err != nil {
		return media.VideoStream{}, err
	}

	avgFrameRate, err := media.ParseRational(s.AvgFrameRate)
	if err != nil {
		return media.VideoStream{}, err
	}

	// Some containers (NUT, raw streams) only declare the nominal rate.
	if avgFrameRate.Num == 0 {
		avgFrameRate = frameRate
	}

	video := media.VideoStream{
		Index:        s.Index,
		Codec:        s.CodecName,
		Profile:      s.Profile,
		Level:        s.Level,
		Width:        s.Width,
		Height:       s.Height,
		PixelFormat:  s.PixFmt,
		BitDepth:     bitDepth(s),
		FrameRate:    frameRate,
		AvgFrameRate: avgFrameRate,
		FrameCount:   integer(s.NbFrames),
		BitRate:      integer(s.BitRate),
		Duration:     seconds(s.Duration),
		FieldOrder:   s.FieldOrder,
		SampleAspect: s.SampleAspectRatio,
		StartTime:    seconds(s.StartTime),
		Color: media.Color{
			Range:     s.ColorRange,
			Space:     s.ColorSpace,
			Transfer:  s.ColorTransfer,
			Primaries: s.ColorPrimaries,
		},
	}

	video.HDR = parseHDR(s)

	return video, nil
}

// parseHDR reads the HDR side data of a stream and classifies its dynamic
// range.
func parseHDR(
	s ffprobeStream,
) media.HDR {
	var hdr media.HDR

	addSideData(&hdr, s.SideDataList)
	hdr.DynamicRange = classify(hdr, s.ColorTransfer)

	return hdr
}

// hdr10PlusSideData identifies SMPTE ST 2094-40 dynamic metadata in the
// side data type names of ffprobe ("HDR Dynamic Metadata SMPTE2094-40
// (HDR10+)").
const hdr10PlusSideData = "SMPTE2094-40"

// addSideData records the HDR metadata of side data entries, keeping what
// hdr already holds: stream-level values win over the first frame's.
func addSideData(
	hdr *media.HDR,
	list []ffprobeSideData,
) {
	for _, sd := range list {
		switch {
		case sd.SideDataType == "DOVI configuration record" && hdr.DolbyVision == nil:
			hdr.DolbyVision = &media.DolbyVision{
				Profile:         sd.DVProfile,
				Level:           sd.DVLevel,
				RPUPresent:      sd.RPUPresentFlag == 1,
				ELPresent:       sd.ELPresentFlag == 1,
				BLPresent:       sd.BLPresentFlag == 1,
				CompatibilityID: sd.DVBLSignalCompatibilityID,
			}
		case sd.SideDataType == "Mastering display metadata" && hdr.MasteringDisplay == nil:
			hdr.MasteringDisplay = mastering(sd)
		case sd.SideDataType == "Content light level metadata" && hdr.ContentLightLevel == nil:
			hdr.ContentLightLevel = &media.ContentLightLevel{MaxCLL: sd.MaxContent, MaxFALL: sd.MaxAverage}
		case strings.Contains(sd.SideDataType, hdr10PlusSideData):
			hdr.HDR10Plus = true
		}
	}
}

// mastering converts SMPTE ST 2086 side data.
func mastering(
	sd ffprobeSideData,
) *media.MasteringDisplay {
	return &media.MasteringDisplay{
		MinLuminance: fraction(sd.MinLuminance),
		MaxLuminance: fraction(sd.MaxLuminance),
		Red:          media.Chromaticity{X: fraction(sd.RedX), Y: fraction(sd.RedY)},
		Green:        media.Chromaticity{X: fraction(sd.GreenX), Y: fraction(sd.GreenY)},
		Blue:         media.Chromaticity{X: fraction(sd.BlueX), Y: fraction(sd.BlueY)},
		WhitePoint:   media.Chromaticity{X: fraction(sd.WhitePointX), Y: fraction(sd.WhitePointY)},
	}
}

// classify gives the dynamic range of a stream from its HDR metadata and
// transfer characteristic. Dolby Vision wins over the transfer function
// because profile 8 streams also signal a compatible HDR10 or HLG base
// layer; HDR10+ needs PQ; PQ without mastering metadata is reported as PQ
// rather than HDR10, which requires it.
func classify(
	hdr media.HDR,
	transfer string,
) media.DynamicRange {
	switch {
	case hdr.DolbyVision != nil:
		return media.DynamicRangeDolbyVision
	case transfer == media.TransferHLG:
		return media.DynamicRangeHLG
	case transfer == media.TransferPQ && hdr.HDR10Plus:
		return media.DynamicRangeHDR10Plus
	case transfer == media.TransferPQ && hdr.MasteringDisplay != nil:
		return media.DynamicRangeHDR10
	case transfer == media.TransferPQ:
		return media.DynamicRangePQ
	}

	return media.DynamicRangeSDR
}

func parseAudio(
	s ffprobeStream,
) media.AudioStream {
	return media.AudioStream{
		Index:         s.Index,
		Codec:         s.CodecName,
		Profile:       s.Profile,
		SampleRate:    int(integer(s.SampleRate)),
		Channels:      s.Channels,
		ChannelLayout: s.ChannelLayout,
		BitRate:       integer(s.BitRate),
		Language:      s.Tags.Language,
		SampleFormat:  s.SampleFmt,
		BitDepth:      audioBitDepth(s),
		StartTime:     seconds(s.StartTime),
		Duration:      seconds(s.Duration),
		Default:       s.Disposition.Default == 1,
	}
}

// audioBitDepth is the coded depth of a PCM or lossless stream: ffprobe
// gives it as bits_per_raw_sample (FLAC, ALAC, 24-bit PCM in 32-bit
// words) or bits_per_sample (PCM); lossy codecs have neither (0).
func audioBitDepth(
	s ffprobeStream,
) int {
	if depth := integer(s.BitsPerRawSample); depth > 0 {
		return int(depth)
	}

	return s.BitsPerSample
}

// defaultBitDepth is the depth of every pixel format missing from
// highBitDepths: the 8-bit formats (yuv420p, nv12, gray...).
const defaultBitDepth = 8

// highBitDepths maps the high bit depth pixel formats met in practice to their
// sample depth.
var highBitDepths = map[string]int{
	"yuv420p10le": 10, "yuv420p10be": 10, "yuv422p10le": 10, "yuv444p10le": 10, "p010le": 10,
	"yuv420p12le": 12, "yuv422p12le": 12, "yuv444p12le": 12,
}

// bitDepth prefers the coded bit depth and falls back to the pixel format;
// it is 0 when neither is known.
func bitDepth(
	s ffprobeStream,
) int {
	if depth := integer(s.BitsPerRawSample); depth > 0 {
		return int(depth)
	}

	if s.PixFmt == "" {
		return 0
	}

	if depth, ok := highBitDepths[s.PixFmt]; ok {
		return depth
	}

	return defaultBitDepth
}

// seconds, integer and fraction parse ffprobe's string-encoded numbers.
// Missing or "N/A" values are common and simply mean unknown, hence 0.
func seconds(
	s string,
) media.Duration {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	return media.Seconds(f)
}

func integer(
	s string,
) int64 {
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}

	return i
}

func fraction(
	s string,
) float64 {
	r, err := media.ParseRational(s)
	if err != nil {
		return 0
	}

	return r.Float()
}

// ffprobeOutput mirrors the subset of ffprobe's JSON output that is used.
type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		StartTime  string `json:"start_time"`
		Size       string `json:"size"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
}

// ffprobeStream is one entry of ffprobe's "streams" array.
type ffprobeStream struct {
	Index             int    `json:"index"`
	CodecName         string `json:"codec_name"`
	CodecType         string `json:"codec_type"`
	Profile           string `json:"profile"`
	Level             int    `json:"level"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	PixFmt            string `json:"pix_fmt"`
	FieldOrder        string `json:"field_order"`
	SampleAspectRatio string `json:"sample_aspect_ratio"`
	RFrameRate        string `json:"r_frame_rate"`
	AvgFrameRate      string `json:"avg_frame_rate"`
	Duration          string `json:"duration"`
	BitRate           string `json:"bit_rate"`
	NbFrames          string `json:"nb_frames"`
	BitsPerRawSample  string `json:"bits_per_raw_sample"`
	BitsPerSample     int    `json:"bits_per_sample"`
	SampleFmt         string `json:"sample_fmt"`
	StartTime         string `json:"start_time"`
	ColorRange        string `json:"color_range"`
	ColorSpace        string `json:"color_space"`
	ColorTransfer     string `json:"color_transfer"`
	ColorPrimaries    string `json:"color_primaries"`
	SampleRate        string `json:"sample_rate"`
	Channels          int    `json:"channels"`
	ChannelLayout     string `json:"channel_layout"`
	Disposition       struct {
		AttachedPic int `json:"attached_pic"`
		Default     int `json:"default"`
	} `json:"disposition"`
	Tags struct {
		Language string `json:"language"`
	} `json:"tags"`
	SideDataList []ffprobeSideData `json:"side_data_list"`
}

// ffprobeSideData is one entry of a stream's "side_data_list".
type ffprobeSideData struct {
	SideDataType              string `json:"side_data_type"`
	DVProfile                 int    `json:"dv_profile"`
	DVLevel                   int    `json:"dv_level"`
	RPUPresentFlag            int    `json:"rpu_present_flag"`
	ELPresentFlag             int    `json:"el_present_flag"`
	BLPresentFlag             int    `json:"bl_present_flag"`
	DVBLSignalCompatibilityID int    `json:"dv_bl_signal_compatibility_id"`
	RedX                      string `json:"red_x"`
	RedY                      string `json:"red_y"`
	GreenX                    string `json:"green_x"`
	GreenY                    string `json:"green_y"`
	BlueX                     string `json:"blue_x"`
	BlueY                     string `json:"blue_y"`
	WhitePointX               string `json:"white_point_x"`
	WhitePointY               string `json:"white_point_y"`
	MinLuminance              string `json:"min_luminance"`
	MaxLuminance              string `json:"max_luminance"`
	MaxContent                int    `json:"max_content"`
	MaxAverage                int    `json:"max_average"`
}

// ffprobeFrames mirrors ffprobe's -show_frames output: only the side data
// of the frames is used.
type ffprobeFrames struct {
	Frames []struct {
		SideDataList []ffprobeSideData `json:"side_data_list"`
	} `json:"frames"`
}
