// Package media holds the domain types shared by every analysis stage.
package media

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Rational is an exact fraction such as a frame rate (30000/1001).
type Rational struct {
	Num int64 `json:"num"`
	Den int64 `json:"den"`
}

// ParseRational parses "num/den" or a plain integer. An empty or zero-denominator
// input returns the zero Rational.
func ParseRational(
	s string,
) (Rational, error) {
	if s == "" {
		return Rational{}, nil
	}

	numStr, denStr, found := strings.Cut(s, "/")
	if !found {
		denStr = "1"
	}

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return Rational{}, fmt.Errorf("parse rational %q: %w", s, err)
	}

	den, err := strconv.ParseInt(denStr, 10, 64)
	if err != nil {
		return Rational{}, fmt.Errorf("parse rational %q: %w", s, err)
	}

	if den == 0 {
		return Rational{}, nil
	}

	return Rational{Num: num, Den: den}, nil
}

// Float returns the rational as a float64, or 0 when undefined.
func (r Rational) Float() float64 {
	if r.Den == 0 {
		return 0
	}

	return float64(r.Num) / float64(r.Den)
}

// String implements fmt.Stringer.
func (r Rational) String() string {
	return fmt.Sprintf("%d/%d", r.Num, r.Den)
}

// Info describes a media file at container and stream level.
type Info struct {
	Path      string        `json:"path"`
	Format    string        `json:"format"`
	Duration  Duration      `json:"duration"`
	Size      int64         `json:"size"`
	BitRate   int64         `json:"bitRate"`
	Video     []VideoStream `json:"video"`
	Audio     []AudioStream `json:"audio"`
	StartTime Duration      `json:"startTime"`
}

// PrimaryVideo returns the first video stream, if any.
func (i *Info) PrimaryVideo() (VideoStream, bool) {
	if len(i.Video) == 0 {
		return VideoStream{}, false
	}

	return i.Video[0], true
}

// VideoStream describes a video elementary stream.
type VideoStream struct {
	Index        int      `json:"index"`
	Codec        string   `json:"codec"`
	Profile      string   `json:"profile,omitempty"`
	Level        int      `json:"level,omitempty"`
	Width        int      `json:"width"`
	Height       int      `json:"height"`
	PixelFormat  string   `json:"pixelFormat"`
	BitDepth     int      `json:"bitDepth"`
	FrameRate    Rational `json:"frameRate"`
	AvgFrameRate Rational `json:"avgFrameRate"`
	FrameCount   int64    `json:"frameCount,omitempty"`
	BitRate      int64    `json:"bitRate,omitempty"`
	Duration     Duration `json:"duration"`
	FieldOrder   string   `json:"fieldOrder,omitempty"`
	SampleAspect string   `json:"sampleAspectRatio,omitempty"`
	Color        Color    `json:"color"`
	HDR          HDR      `json:"hdr"`
}

// Color holds the signalled colour description of a stream.
type Color struct {
	Range     string `json:"range,omitempty"`
	Space     string `json:"space,omitempty"`
	Transfer  string `json:"transfer,omitempty"`
	Primaries string `json:"primaries,omitempty"`
}

// DynamicRange classifies how a stream is meant to be displayed.
type DynamicRange string

// Dynamic range values.
const (
	DynamicRangeSDR         DynamicRange = "SDR"
	DynamicRangeHDR10       DynamicRange = "HDR10"
	DynamicRangeHLG         DynamicRange = "HLG"
	DynamicRangePQ          DynamicRange = "PQ"
	DynamicRangeDolbyVision DynamicRange = "DolbyVision"
)

// HDR describes high dynamic range signalling found at stream level.
type HDR struct {
	DynamicRange      DynamicRange       `json:"dynamicRange"`
	MasteringDisplay  *MasteringDisplay  `json:"masteringDisplay,omitempty"`
	ContentLightLevel *ContentLightLevel `json:"contentLightLevel,omitempty"`
	DolbyVision       *DolbyVision       `json:"dolbyVision,omitempty"`
}

// MasteringDisplay is the SMPTE ST 2086 mastering display colour volume.
type MasteringDisplay struct {
	MinLuminance float64 `json:"minLuminance"`
	MaxLuminance float64 `json:"maxLuminance"`
}

// ContentLightLevel holds MaxCLL / MaxFALL in cd/m².
type ContentLightLevel struct {
	MaxCLL  int `json:"maxCLL"`
	MaxFALL int `json:"maxFALL"`
}

// DolbyVision holds the Dolby Vision decoder configuration record.
type DolbyVision struct {
	Profile         int  `json:"profile"`
	Level           int  `json:"level"`
	RPUPresent      bool `json:"rpuPresent"`
	ELPresent       bool `json:"elPresent"`
	BLPresent       bool `json:"blPresent"`
	CompatibilityID int  `json:"compatibilityId"`
}

// AudioStream describes an audio elementary stream.
type AudioStream struct {
	Index         int    `json:"index"`
	Codec         string `json:"codec"`
	Profile       string `json:"profile,omitempty"`
	SampleRate    int    `json:"sampleRate"`
	Channels      int    `json:"channels"`
	ChannelLayout string `json:"channelLayout,omitempty"`
	BitRate       int64  `json:"bitRate,omitempty"`
	Language      string `json:"language,omitempty"`
}

// Packet is a compressed access unit as stored in the container.
type Packet struct {
	PTS      time.Duration
	DTS      time.Duration
	Duration time.Duration
	Size     int
	Keyframe bool
}

// Interval is a time range [Start, End).
type Interval struct {
	Start Duration `json:"start"`
	End   Duration `json:"end"`
}

// Length returns End - Start.
func (i Interval) Length() Duration {
	return i.End - i.Start
}

// Levels are the nominal black and white luma code values of an 8-bit signal.
type Levels struct {
	Black int `json:"black"`
	White int `json:"white"`
}

// Nominal 8-bit luma code values of the two quantisation ranges defined by
// ITU-R BT.601/BT.709: full ("pc") and limited ("tv").
const (
	fullRangeBlack    = 0
	fullRangeWhite    = 255
	limitedRangeBlack = 16
	limitedRangeWhite = 235
)

// LevelsFor returns the nominal levels for an ffmpeg color_range value.
// Unknown ranges default to limited ("tv"), the norm for YUV video.
func LevelsFor(
	colorRange string,
) Levels {
	if colorRange == "pc" || colorRange == "jpeg" {
		return Levels{Black: fullRangeBlack, White: fullRangeWhite}
	}

	return Levels{Black: limitedRangeBlack, White: limitedRangeWhite}
}
