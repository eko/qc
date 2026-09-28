package overlay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/eko/qc/media"
)

func TestEscape(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "clip.mp4", want: "clip.mp4"},
		{name: "braces", in: "a{\\b1}c", want: "a\\{\\\u2060b1\\}c"},
		{name: "line break escapes stay literal", in: `C:\new\Nope\h`, want: "C:\\\u2060new\\\u2060Nope\\\u2060h"},
		{name: "line breaks", in: "a\nb\r\nc\rd", want: "a b c d"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, escape(testCase.in))
		})
	}
}

func TestFormats(
	t *testing.T,
) {
	testCases := []struct {
		name string
		got  string
		want string
	}{
		{name: "timecode", got: timecode(media.Duration(3*time.Hour + 2*time.Minute + 1*time.Second + 5*time.Millisecond)), want: "03:02:01.005"},
		{name: "timecode rounds to the millisecond", got: timecode(media.Duration(41_708_333)), want: "00:00:00.042"},
		{name: "bytes", got: byteSize(812), want: "812 B"},
		{name: "kilobytes", got: byteSize(45_210), want: "45.2 kB"},
		{name: "megabytes", got: byteSize(1_234_567), want: "1.23 MB"},
		{name: "kilobits", got: bitRate(845_400), want: "845 kb/s"},
		{name: "megabits", got: bitRate(4_823_000), want: "4.82 Mb/s"},
		{name: "tens of megabits", got: bitRate(12_440_000), want: "12.4 Mb/s"},
		{name: "hundreds of megabits", got: bitRate(412_000_000), want: "412 Mb/s"},
		{name: "integer", got: integer(-0.3), want: "0"},
		{name: "signed positive", got: signed(1.25), want: "+1.3"},
		{name: "signed zero", got: signed(-0.04), want: "+0.0"},
		{name: "signed negative", got: signed(-2.26), want: "-2.3"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.got)
		})
	}
}

func TestNewCanvas(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		width, height int
		want          canvas
	}{
		{name: "16:9", width: 1280, height: 720, want: canvas{width: 1920, height: 1080}},
		{name: "4:3", width: 720, height: 540, want: canvas{width: 1440, height: 1080}},
		{name: "vertical", width: 1080, height: 1920, want: canvas{width: 608, height: 1080}},
		{name: "unknown", want: canvas{width: 1920, height: 1080}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, newCanvas(testCase.width, testCase.height))
		})
	}
}
