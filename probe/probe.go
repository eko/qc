// Package probe extracts container and stream level information from a media file.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

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

// FFprobe implements Prober using the ffprobe binary.
type FFprobe struct {
	bin    string
	output commandOutput
}

// NewFFprobe returns a Prober backed by the given ffprobe binary.
func NewFFprobe(
	bin string,
) *FFprobe {
	return &FFprobe{bin: bin, output: ffexec.Output}
}

// Probe implements Prober.
func (f *FFprobe) Probe(
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

// parseHDR classifies the dynamic range from the transfer characteristics and
// the side data. Dolby Vision wins over the transfer function because profile
// 8 streams also signal a compatible HDR10 or HLG base layer; PQ without
// mastering metadata is reported as PQ rather than HDR10, which requires it.
func parseHDR(
	s ffprobeStream,
) media.HDR {
	hdr := media.HDR{DynamicRange: media.DynamicRangeSDR}

	for _, sd := range s.SideDataList {
		switch sd.SideDataType {
		case "DOVI configuration record":
			hdr.DolbyVision = &media.DolbyVision{
				Profile:         sd.DVProfile,
				Level:           sd.DVLevel,
				RPUPresent:      sd.RPUPresentFlag == 1,
				ELPresent:       sd.ELPresentFlag == 1,
				BLPresent:       sd.BLPresentFlag == 1,
				CompatibilityID: sd.DVBLSignalCompatibilityID,
			}
		case "Mastering display metadata":
			hdr.MasteringDisplay = &media.MasteringDisplay{
				MinLuminance: fraction(sd.MinLuminance),
				MaxLuminance: fraction(sd.MaxLuminance),
			}
		case "Content light level metadata":
			hdr.ContentLightLevel = &media.ContentLightLevel{
				MaxCLL:  sd.MaxContent,
				MaxFALL: sd.MaxAverage,
			}
		}
	}

	switch {
	case hdr.DolbyVision != nil:
		hdr.DynamicRange = media.DynamicRangeDolbyVision
	case s.ColorTransfer == "arib-std-b67":
		hdr.DynamicRange = media.DynamicRangeHLG
	case s.ColorTransfer == "smpte2084" && hdr.MasteringDisplay != nil:
		hdr.DynamicRange = media.DynamicRangeHDR10
	case s.ColorTransfer == "smpte2084":
		hdr.DynamicRange = media.DynamicRangePQ
	}

	return hdr
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
	}
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
	ColorRange        string `json:"color_range"`
	ColorSpace        string `json:"color_space"`
	ColorTransfer     string `json:"color_transfer"`
	ColorPrimaries    string `json:"color_primaries"`
	SampleRate        string `json:"sample_rate"`
	Channels          int    `json:"channels"`
	ChannelLayout     string `json:"channel_layout"`
	Disposition       struct {
		AttachedPic int `json:"attached_pic"`
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
	MinLuminance              string `json:"min_luminance"`
	MaxLuminance              string `json:"max_luminance"`
	MaxContent                int    `json:"max_content"`
	MaxAverage                int    `json:"max_average"`
}
